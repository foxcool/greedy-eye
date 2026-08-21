package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"maps"
	"net/http"
	"os"
	"os/signal"
	"slices"
	"strings"
	"syscall"
	"time"

	"connectrpc.com/connect"
	"github.com/foxcool/greedy-eye/api/v1/apiv1connect"
	"github.com/foxcool/greedy-eye/internal/adapter/ratelimit"
	"github.com/foxcool/greedy-eye/internal/middleware"
	"github.com/foxcool/greedy-eye/internal/provider"
	"github.com/foxcool/greedy-eye/internal/scheduler"
	"github.com/foxcool/greedy-eye/internal/service/analytics"
	"github.com/foxcool/greedy-eye/internal/service/automation"
	"github.com/foxcool/greedy-eye/internal/service/credentials"
	"github.com/foxcool/greedy-eye/internal/service/marketdata"
	"github.com/foxcool/greedy-eye/internal/service/portfolio"
	"github.com/foxcool/greedy-eye/internal/service/settings"
	"github.com/foxcool/greedy-eye/internal/store/postgres"
	"github.com/getsentry/sentry-go"
	"github.com/robfig/cron/v3"
)

const ServiceName = "EYE"

// version is the build this binary was cut from, stamped at link time by
// GoReleaser (see .goreleaser.yaml). It defaults to "dev" so an unstamped
// binary says what it is instead of claiming a release it is not: a wrong
// version is worse than an absent one, because it is believed.
//
// This exists because the Release Policy asks for the deployed claim to be
// verified against production, and five deploys running could not be, for want
// of any way to ask a live instance what it was. Behaviour was the only
// evidence available, so "the pin says 0.8.1" and "the process serving traffic
// is 0.8.1" stayed the same sentence — while prod went ten days without
// refreshing a crypto price and the balance sweep kept it looking alive
// (personal-yqwj, discovered from personal-cvdk).
var version = "dev"

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "fatal error: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	config, err := getConfig()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	log := createLogger(config.Logger.Level)

	// Init sentry
	if config.Sentry.DSN != "" {
		err := sentry.Init(sentry.ClientOptions{
			Dsn:              config.Sentry.DSN,
			TracesSampleRate: config.Sentry.TracesSampleRate,
		})
		if err != nil {
			return fmt.Errorf("init sentry: %w", err)
		}
	}

	// Initialize database pool
	if config.DB.URL == "" {
		return fmt.Errorf("database URL cannot be empty")
	}

	pool, err := postgres.NewPool(context.Background(), config.DB.URL)
	if err != nil {
		return fmt.Errorf("connect to database: %w", err)
	}

	defer func() {
		log.Info("Closing DB connection, flushing Sentry events...")
		pool.Close()
		sentry.Flush(2 * time.Second)
		log.Info("Bye")
	}()

	// Setup HTTP mux
	mux := http.NewServeMux()

	healthBody, err := healthPayload(version)
	if err != nil {
		return fmt.Errorf("encode health response: %w", err)
	}
	mux.HandleFunc("GET /eye/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		if _, err := w.Write(healthBody); err != nil {
			log.Error("Failed to write health response", slog.Any("error", err))
		}
	})

	mdStore := postgres.NewMarketDataStore(pool)

	// One request budget per provider credential, shared by every client built
	// from it. Clients are short-lived — the credentials resolver makes a new
	// one per account, per sync — so the budget cannot live inside them: a
	// sweep over three accounts would otherwise triple the rate the provider
	// sees, which is exactly how the Subscan plan limit was tripped.
	// Spend is persisted because plan allowances are monthly: a deploy would
	// otherwise hand the process a fresh quota while the provider keeps
	// counting, and the real limit would arrive as a surprise.
	// No operator overrides from configuration: a custom budget belongs to the
	// account that owns the key (see accountLimit). Built-in plans live in
	// internal/adapter/ratelimit.
	rateLimits := ratelimit.NewRegistry(nil,
		ratelimit.WithUsageStore(postgres.NewProviderUsageStore(pool)))
	if err := rateLimits.Start(context.Background()); err != nil {
		return fmt.Errorf("restore provider usage: %w", err)
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := rateLimits.Stop(ctx); err != nil {
			log.Error("Failed to persist provider usage", slog.Any("error", err))
		}
	}()

	// Initialize accounts.data encryption at rest (ADR-005)
	encryptor, err := buildEncryptor(config, log)
	if err != nil {
		return err
	}
	var portfolioStoreOpts []postgres.PortfolioStoreOption
	if encryptor != nil {
		portfolioStoreOpts = append(portfolioStoreOpts, postgres.WithEncryptor(encryptor))
	}

	portfolioStore := postgres.NewPortfolioStore(pool, portfolioStoreOpts...)

	// Converge rows sealed under a key being rotated out. No-op with one key.
	startRekey(context.Background(), pool, encryptor, log)

	// Every adapter this build can construct lives in internal/provider, so the
	// service that composes them does not also have to know them: nothing here
	// imports an adapter package any more, and the same registry answers "which
	// providers exist" to the account form (personal-s05.1).
	providers := provider.New(rateLimits)

	credResolver := credentials.NewResolver(credentials.Config{
		Source:          portfolioStore,
		WalletSyncers:   providers.WalletSyncers(),
		ExchangeSyncers: providers.ExchangeSyncers(),
		PriceProviders:  providers.PriceProviders(),
		// Readers that need no credential. Without these a fresh instance syncs
		// nothing at all: a syncer is chosen from accounts carrying
		// onchain_lookup, and the wallet account holds only an address.
		KeylessWalletSyncers:  providers.KeylessWalletSyncers(),
		KeylessPriceProviders: providers.KeylessPriceProviders(),
		Log:                   log,
	})

	// Which build, then which sources. The two are asked in one breath and for
	// the same reason: when the numbers stop moving, "is the fix even running"
	// has to be answerable before "did it work".
	log.Info("greedy-eye starting", slog.String("version", version))

	logProviderInventory(context.Background(), credResolver, log)

	// Register Connect handlers
	userStore := postgres.NewUserStore(pool)
	interceptor := connect.WithInterceptors(
		middleware.UserProvisioningInterceptor(userStore, log),
		loggingInterceptor(log),
	)
	mdHandler := marketdata.NewHandler(mdStore, log).
		WithProviderSource(credResolver).
		// The sweep interval is what a provider divides its remaining plan
		// allowance by, so it comes from the cron spec rather than a second
		// config key that could disagree with it.
		WithRefreshWindow(sweepWindow(config.Scheduler.PriceFetchCron))
	automationStore := postgres.NewAutomationStore(pool)
	automationHandler := automation.NewHandler(automationStore, log)
	// Built before the loop because two other services read from it: valuation
	// rules are a stored setting, and a total and a heatmap have to answer under
	// the same ones. Registering its route stays inside the loop, so a
	// deployment that does not serve settings still lets the local handler
	// answer in-process — the same shape as mdHandler above.
	settingsHandler := settings.NewHandler(postgres.NewSettingsStore(pool), log)
	// The balance sweep runs against the portfolio service in this process.
	// A deployment without one has no balances of its own to refresh, so the
	// job stays unregistered rather than reaching across the network for them.
	var balanceSweeper scheduler.BalanceSweeper
	for _, svc := range config.Services {
		switch svc.Type {
		case ServiceConfigTypeMarketData:
			path, handler := apiv1connect.NewMarketDataServiceHandler(mdHandler, interceptor)
			mux.Handle(path, handler)
		case ServiceConfigTypePortfolio:
			pHandler := portfolio.NewHandler(portfolioStore, log).
				WithMarketDataClient(mdHandler).
				WithSettingsClient(settingsHandler).
				WithWalletSyncerSource(credResolver).
				WithExchangeSyncerSource(credResolver).
				// The same registry that builds the clients describes them, so
				// the account form offers the slugs, chains and plans this build
				// actually uses rather than a copy of them (personal-7bn).
				WithProviderCatalog(providers)
			path, handler := apiv1connect.NewPortfolioServiceHandler(pHandler, interceptor)
			mux.Handle(path, handler)
			balanceSweeper = pHandler
		case ServiceConfigTypeAutomation:
			path, handler := apiv1connect.NewAutomationServiceHandler(automationHandler, interceptor)
			mux.Handle(path, handler)
		case ServiceConfigTypeAnalytics:
			aHandler := analytics.NewHandler(portfolioStore, log).
				WithMarketDataClient(mdHandler).
				WithSettingsClient(settingsHandler)
			path, handler := apiv1connect.NewAnalyticsServiceHandler(aHandler, interceptor)
			mux.Handle(path, handler)
		case ServiceConfigTypeSettings:
			path, handler := apiv1connect.NewSettingsServiceHandler(settingsHandler, interceptor)
			mux.Handle(path, handler)
		default:
			log.Warn("unknown service type, skipping", slog.String("type", svc.Type))
			continue
		}
		log.Info("registered service", slog.String("type", svc.Type))
	}

	// Start background scheduler (periodic rules + price fetch)
	var sched *scheduler.Scheduler
	if config.Scheduler.Enabled {
		sched, err = scheduler.New(scheduler.Config{
			PriceFetchCron:  config.Scheduler.PriceFetchCron,
			RescoreCron:     config.Scheduler.RescoreCron,
			BalanceSyncCron: config.Scheduler.BalanceSyncCron,
		}, automationStore, automationHandler, mdHandler, mdHandler, log)
		if err != nil {
			return fmt.Errorf("init scheduler: %w", err)
		}
		sched = sched.WithUsageReporter(rateLimits)
		if balanceSweeper != nil {
			sched = sched.WithBalanceSweeper(balanceSweeper, portfolio.SweepOpts{
				MaxAge: config.Scheduler.BalanceMaxAge,
				Limit:  config.Scheduler.BalanceAccountsPerSweep,
			})
		}
		if err := sched.Start(); err != nil {
			return fmt.Errorf("start scheduler: %w", err)
		}
		log.Info("scheduler started",
			slog.String("price_fetch_cron", config.Scheduler.PriceFetchCron),
			slog.String("rescore_cron", config.Scheduler.RescoreCron),
			slog.String("balance_sync_cron", config.Scheduler.BalanceSyncCron),
			slog.Bool("balance_sweep_wired", balanceSweeper != nil))
	}

	// Serve HTTP/1.1 and cleartext HTTP/2 (h2c) for Connect. Since Go 1.24 this
	// is native via http.Server.Protocols — no golang.org/x/net/http2/h2c
	// wrapper (deprecated) needed; the browser reaches Connect without a
	// grpc-web proxy behind the TLS-terminating edge.
	protocols := new(http.Protocols)
	protocols.SetHTTP1(true)
	protocols.SetUnencryptedHTTP2(true)
	server := &http.Server{
		Addr:              fmt.Sprintf(":%d", config.Server.Port),
		Handler:           mux,
		Protocols:         protocols,
		ReadHeaderTimeout: 10 * time.Second, // mitigate Slowloris (gosec G112)
	}

	// Start server in background
	errCh := make(chan error, 1)
	go func() {
		log.Info("HTTP server starting", slog.Int("port", config.Server.Port))
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			errCh <- fmt.Errorf("serve HTTP: %w", err)
		}
	}()

	// Wait for shutdown signal or error
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

	select {
	case sig := <-quit:
		log.Info("Received shutdown signal", slog.String("signal", sig.String()))
	case err := <-errCh:
		return err
	}

	// Graceful shutdown
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Stop the scheduler first so no new in-process calls start while
	// the HTTP server drains.
	if sched != nil {
		sched.Stop(ctx)
	}

	if err := server.Shutdown(ctx); err != nil {
		log.Error("HTTP server shutdown error", slog.Any("error", err))
		return err
	}

	log.Info("Server stopped gracefully")
	return nil
}

func createLogger(level string) *slog.Logger {
	var logLevel slog.Level
	switch strings.ToLower(level) {
	case "debug":
		logLevel = slog.LevelDebug
	case "warn":
		logLevel = slog.LevelWarn
	case "error":
		logLevel = slog.LevelError
	default:
		logLevel = slog.LevelInfo
	}
	return slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: logLevel}))
}

func loggingInterceptor(log *slog.Logger) connect.UnaryInterceptorFunc {
	return func(next connect.UnaryFunc) connect.UnaryFunc {
		return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
			start := time.Now()
			resp, err := next(ctx, req)
			attrs := []any{
				slog.String("procedure", req.Spec().Procedure),
				slog.Duration("duration", time.Since(start)),
				slog.Bool("error", err != nil),
			}
			if err != nil {
				attrs = append(attrs, slog.String("error_msg", err.Error()))
			}
			log.Info("request", attrs...)
			return resp, err
		}
	}
}

// healthPayload is the body of GET /eye/health. It carries the build so that
// "which version is serving this traffic" has an answer that does not depend on
// inferring it from behaviour — the question five deploys running could not
// settle, because the only evidence available was whether the numbers moved,
// and a live process with a stale image moves some of them (personal-yqwj).
//
// status and service keep their names and values: something may already be
// matching on them, and this is a health check.
//
// Encoded rather than concatenated, because ver arrives from a link-time flag:
// a stray quote would otherwise serve a 200 with a broken body, which is worse
// than an honest error at startup.
func healthPayload(ver string) ([]byte, error) {
	return json.Marshal(map[string]string{
		"status":  "ok",
		"service": "greedy-eye",
		"version": ver,
	})
}

// logProviderInventory names the price sources unattended work can reach right
// now, once, at startup.
//
// Credentials no longer come from configuration, so a missing one stops being a
// boot-time error and becomes silence hours later inside a sweep. One line here
// is the difference between "this instance prices nothing until you add a key"
// and an operator wondering why the numbers stopped moving.
//
// Resolved with no user in context — deliberately the scheduler's own view,
// because that is the one that has been wrong before.
//
// It names what it passed over as well as what it reached. The version that
// reported only the reachable providers printed a correct, complete and
// reassuring line on an instance whose one live crypto source sat unused for six
// days (personal-cvdk): a list of what worked cannot show an absence.
func logProviderInventory(ctx context.Context, resolver *credentials.Resolver, log *slog.Logger) {
	inventory, err := resolver.PriceInventoryFor(ctx, "")
	if err != nil {
		log.Error("could not take stock of price providers", slog.Any("error", err))
		return
	}

	for _, s := range inventory.Skipped {
		log.Warn("an account carrying market data is not used by unattended work",
			slog.String("provider", s.Provider),
			slog.String("account_id", s.AccountID),
			slog.String("reason", s.Reason))
	}

	slugs := slices.Sorted(maps.Keys(inventory.Providers))
	if len(slugs) == 0 {
		log.Warn("no price provider is reachable: unattended pricing will do nothing until an account carries a credential")
		return
	}
	log.Info("price providers reachable without a user", slog.Any("providers", slugs))
}

// sweepWindow is how long the price job waits between runs, read off its own
// cron spec by asking when it would fire twice. An unparsable or disabled spec
// yields zero, which leaves sweeps unbudgeted rather than guessing.
func sweepWindow(spec string) time.Duration {
	if spec == "" {
		return 0
	}
	sched, err := cron.ParseStandard(spec)
	if err != nil {
		return 0
	}
	first := sched.Next(time.Now())
	return sched.Next(first).Sub(first)
}
