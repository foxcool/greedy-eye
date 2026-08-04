package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"connectrpc.com/connect"
	"github.com/foxcool/greedy-eye/api/v1/apiv1connect"
	binanceadapter "github.com/foxcool/greedy-eye/internal/adapter/binance"
	blockchairadapter "github.com/foxcool/greedy-eye/internal/adapter/blockchair"
	cbradapter "github.com/foxcool/greedy-eye/internal/adapter/cbr"
	"github.com/foxcool/greedy-eye/internal/adapter/coingecko"
	cosmosadapter "github.com/foxcool/greedy-eye/internal/adapter/cosmos"
	esploraadapter "github.com/foxcool/greedy-eye/internal/adapter/esplora"
	moralisadapter "github.com/foxcool/greedy-eye/internal/adapter/moralis"
	"github.com/foxcool/greedy-eye/internal/adapter/ratelimit"
	solanaadapter "github.com/foxcool/greedy-eye/internal/adapter/solana"
	subscanadapter "github.com/foxcool/greedy-eye/internal/adapter/subscan"
	tonapiadapter "github.com/foxcool/greedy-eye/internal/adapter/tonapi"
	tzktadapter "github.com/foxcool/greedy-eye/internal/adapter/tzkt"
	"github.com/foxcool/greedy-eye/internal/entity"
	"github.com/foxcool/greedy-eye/internal/middleware"
	"github.com/foxcool/greedy-eye/internal/scheduler"
	"github.com/foxcool/greedy-eye/internal/service/analytics"
	"github.com/foxcool/greedy-eye/internal/service/automation"
	"github.com/foxcool/greedy-eye/internal/service/credentials"
	"github.com/foxcool/greedy-eye/internal/service/marketdata"
	"github.com/foxcool/greedy-eye/internal/service/portfolio"
	"github.com/foxcool/greedy-eye/internal/store/postgres"
	"github.com/getsentry/sentry-go"
	"github.com/robfig/cron/v3"
)

const ServiceName = "EYE"

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

	// Health endpoint
	mux.HandleFunc("GET /eye/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		if _, err := w.Write([]byte(`{"status":"ok","service":"greedy-eye"}`)); err != nil {
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
	rateLimits := ratelimit.NewRegistry(rateLimitOverrides(config),
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

	// Initialize price providers.
	cgCred := ratelimit.Credential{
		Provider: coingecko.ProviderName,
		APIKey:   config.CoinGecko.APIKey,
		Tier:     coingeckoTier(config.CoinGecko.Pro),
	}
	cgClient := coingecko.NewClient(coingecko.Config{
		APIKey: config.CoinGecko.APIKey,
		// One tier, resolved once: it picks the host, the auth header and the
		// plan allowance, and those three disagreeing is a 429 on every call.
		Tier:      cgCred.Tier,
		Transport: rateLimits.Transport(cgCred, nil),
		Budget:    rateLimits.Budget(cgCred),
	})

	// Initialize optional Moralis wallet syncer
	var walletSyncer entity.WalletSyncer
	if apiKey := config.Moralis.APIKey; apiKey != "" {
		moralisClient := moralisadapter.NewClient(moralisadapter.Config{
			APIKey: apiKey,
			Transport: rateLimits.Transport(ratelimit.Credential{
				Provider: moralisadapter.ProviderName, APIKey: apiKey,
			}, nil),
		})
		walletSyncer = moralisadapter.NewWalletSyncer(moralisClient)
	}

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

	// Env-configured price providers stay the fallback registry; the resolver
	// overlays them with credentials stored in accounts (system → user).
	envPriceProviders := map[string]marketdata.PriceProvider{
		coingecko.ProviderName: coingecko.NewProvider(cgClient),
		binanceadapter.ProviderName: binanceadapter.NewProvider(
			binanceadapter.NewClient(binanceadapter.Config{
				APIKey:    config.Binance.APIKey,
				APISecret: config.Binance.APISecret,
				Sandbox:   config.Binance.Sandbox,
				Transport: rateLimits.Transport(ratelimit.Credential{
					Provider: binanceadapter.ProviderName, APIKey: config.Binance.APIKey,
				}, nil),
			}),
		),
		// Keyless and free, so it is registered unconditionally: without a
		// RUB/USD rate a rouble-quoted instrument has a price and still
		// contributes nothing to a USD total.
		cbradapter.ProviderName: cbradapter.NewProvider(cbradapter.NewClient(cbradapter.Config{
			Transport: rateLimits.Transport(ratelimit.Credential{Provider: cbradapter.ProviderName}, nil),
		})),
	}
	credResolver := credentials.NewResolver(credentials.Config{
		Source: portfolioStore,
		// Wallet providers are routed by chain: every non-EVM ecosystem
		// registers its own adapter here alongside Moralis (personal-feb).
		WalletSyncers: map[string]credentials.WalletProvider{
			moralisadapter.ProviderName: {
				Factory: func(a *entity.Account) (entity.WalletSyncer, error) {
					return moralisadapter.NewWalletSyncer(
						moralisadapter.NewClient(moralisadapter.Config{
							APIKey:    a.Data["api_key"],
							Transport: rateLimits.Transport(accountCred(moralisadapter.ProviderName, a), nil),
						}),
					), nil
				},
				Chains:         moralisadapter.SupportedChains(),
				HandlesAddress: moralisadapter.HandlesAddress,
			},
			subscanadapter.ProviderName: {
				Factory: func(a *entity.Account) (entity.WalletSyncer, error) {
					return subscanadapter.NewWalletSyncer(
						subscanadapter.NewClient(subscanadapter.Config{
							APIKey:    a.Data["api_key"],
							Transport: rateLimits.Transport(accountCred(subscanadapter.ProviderName, a), nil),
						}),
					), nil
				},
				Chains:         subscanadapter.SupportedChains(),
				HandlesAddress: subscanadapter.HandlesAddress,
			},
			tonapiadapter.ProviderName: {
				Factory: func(a *entity.Account) (entity.WalletSyncer, error) {
					return tonapiadapter.NewWalletSyncer(
						tonapiadapter.NewClient(tonapiadapter.Config{
							APIKey:    a.Data["api_key"],
							Transport: rateLimits.Transport(accountCred(tonapiadapter.ProviderName, a), nil),
						}),
					), nil
				},
				Chains:         tonapiadapter.SupportedChains(),
				HandlesAddress: tonapiadapter.HandlesAddress,
			},
			solanaadapter.ProviderName: {
				Factory: func(a *entity.Account) (entity.WalletSyncer, error) {
					return solanaadapter.NewWalletSyncer(
						solanaadapter.NewClient(solanaadapter.Config{
							APIKey:    a.Data["api_key"],
							Transport: rateLimits.Transport(accountCred(solanaadapter.ProviderName, a), nil),
						}),
					), nil
				},
				Chains:         solanaadapter.SupportedChains(),
				HandlesAddress: solanaadapter.HandlesAddress,
			},
			// Esplora needs no credentials; the account exists only so the
			// registry has something to route to.
			//
			// The endpoint is not configurable per account on purpose.
			// accounts.data is user-supplied, and a client whose base URL comes
			// from it would make the server issue requests to any address its
			// caller names — cloud metadata, loopback, anything inside the
			// trust boundary — with the response surfacing through sync errors.
			// If a self-hosted instance is ever needed it belongs in operator
			// config, not in user data.
			esploraadapter.ProviderName: {
				Factory: func(_ *entity.Account) (entity.WalletSyncer, error) {
					return esploraadapter.NewWalletSyncer(
						esploraadapter.NewClient(esploraadapter.Config{
							Transport: rateLimits.Transport(ratelimit.Credential{Provider: esploraadapter.ProviderName}, nil),
						}),
					), nil
				},
				Chains:         esploraadapter.SupportedChains(),
				HandlesAddress: esploraadapter.HandlesAddress,
			},
			cosmosadapter.ProviderName: {
				Factory: func(_ *entity.Account) (entity.WalletSyncer, error) {
					return cosmosadapter.NewWalletSyncer(
						cosmosadapter.NewClient(cosmosadapter.Config{
							Transport: rateLimits.Transport(ratelimit.Credential{Provider: cosmosadapter.ProviderName}, nil),
						}),
					), nil
				},
				Chains:         cosmosadapter.SupportedChains(),
				HandlesAddress: cosmosadapter.HandlesAddress,
			},
			tzktadapter.ProviderName: {
				Factory: func(_ *entity.Account) (entity.WalletSyncer, error) {
					return tzktadapter.NewWalletSyncer(
						tzktadapter.NewClient(tzktadapter.Config{
							Transport: rateLimits.Transport(ratelimit.Credential{Provider: tzktadapter.ProviderName}, nil),
						}),
					), nil
				},
				Chains:         tzktadapter.SupportedChains(),
				HandlesAddress: tzktadapter.HandlesAddress,
			},
			blockchairadapter.ProviderName: {
				Factory: func(a *entity.Account) (entity.WalletSyncer, error) {
					return blockchairadapter.NewWalletSyncer(
						blockchairadapter.NewClient(blockchairadapter.Config{
							APIKey:    a.Data["api_key"],
							Transport: rateLimits.Transport(accountCred(blockchairadapter.ProviderName, a), nil),
						}),
					), nil
				},
				Chains:         blockchairadapter.SupportedChains(),
				HandlesAddress: blockchairadapter.HandlesAddress,
			},
		},
		ExchangeSyncers: map[string]credentials.ExchangeSyncerFactory{
			binanceadapter.ProviderName: func(a *entity.Account) (entity.ExchangeSyncer, error) {
				return binanceadapter.NewExchangeSyncer(binanceadapter.NewClient(binanceadapter.Config{
					APIKey:    a.Data["api_key"],
					APISecret: a.Data["api_secret"],
					Transport: rateLimits.Transport(accountCred(binanceadapter.ProviderName, a), nil),
				})), nil
			},
		},
		PriceProviders: map[string]credentials.PriceProviderFactory{
			coingecko.ProviderName: func(a *entity.Account) (marketdata.PriceProvider, error) {
				cred := accountCred(coingecko.ProviderName, a)
				return coingecko.NewProvider(coingecko.NewClient(coingecko.Config{
					APIKey: a.Data["api_key"],
					// accountCred already folded the legacy "pro" flag into the
					// tier; reading data["pro"] separately here is what let the
					// host and the plan disagree.
					Tier:      cred.Tier,
					Transport: rateLimits.Transport(cred, nil),
					Budget:    rateLimits.Budget(cred),
				})), nil
			},
			binanceadapter.ProviderName: func(a *entity.Account) (marketdata.PriceProvider, error) {
				return binanceadapter.NewProvider(binanceadapter.NewClient(binanceadapter.Config{
					APIKey:    a.Data["api_key"],
					APISecret: a.Data["api_secret"],
					Transport: rateLimits.Transport(accountCred(binanceadapter.ProviderName, a), nil),
				})), nil
			},
			// The feed needs no credential, but an account is still how a
			// provider becomes visible in the registry (docs/providers.md).
			// Registering the factory means seeding one later (personal-s05.2)
			// silences the env-fallback warning instead of adding a provider.
			cbradapter.ProviderName: func(a *entity.Account) (marketdata.PriceProvider, error) {
				return cbradapter.NewProvider(cbradapter.NewClient(cbradapter.Config{
					Transport: rateLimits.Transport(accountCred(cbradapter.ProviderName, a), nil),
				})), nil
			},
		},
		// The env syncer is Moralis too (deprecated path, g27), so it carries
		// the same chain coverage — without it a Substrate account would fall
		// back onto an EVM-only syncer.
		EnvWalletSyncer:            walletSyncer,
		EnvWalletSyncerChains:      moralisadapter.SupportedChains(),
		EnvWalletSyncerAddressFunc: moralisadapter.HandlesAddress,
		EnvPriceProviders:          envPriceProviders,
		Log:                        log,
	})

	// Register Connect handlers
	userStore := postgres.NewUserStore(pool)
	interceptor := connect.WithInterceptors(
		middleware.UserProvisioningInterceptor(userStore, log),
		loggingInterceptor(log),
	)
	mdHandler := marketdata.NewHandler(mdStore, log).
		WithProvider(coingecko.ProviderName, envPriceProviders[coingecko.ProviderName]).
		WithProvider(binanceadapter.ProviderName, envPriceProviders[binanceadapter.ProviderName]).
		WithProvider(cbradapter.ProviderName, envPriceProviders[cbradapter.ProviderName]).
		WithProviderSource(credResolver).
		// The sweep interval is what a provider divides its remaining plan
		// allowance by, so it comes from the cron spec rather than a second
		// config key that could disagree with it.
		WithRefreshWindow(sweepWindow(config.Scheduler.PriceFetchCron))
	automationStore := postgres.NewAutomationStore(pool)
	automationHandler := automation.NewHandler(automationStore, log)
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
				WithWalletSyncer(walletSyncer).
				WithWalletSyncerSource(credResolver).
				WithExchangeSyncerSource(credResolver)
			path, handler := apiv1connect.NewPortfolioServiceHandler(pHandler, interceptor)
			mux.Handle(path, handler)
			balanceSweeper = pHandler
		case ServiceConfigTypeAutomation:
			path, handler := apiv1connect.NewAutomationServiceHandler(automationHandler, interceptor)
			mux.Handle(path, handler)
		case ServiceConfigTypeAnalytics:
			aHandler := analytics.NewHandler(portfolioStore, log).
				WithMarketDataClient(mdHandler)
			path, handler := apiv1connect.NewAnalyticsServiceHandler(aHandler, interceptor)
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

// rateLimitOverrides converts the operator-facing config section into the
// limiter's own type. A provider absent here keeps its built-in default, so
// the section is only ever a correction.
func rateLimitOverrides(config *Config) map[string]ratelimit.Limit {
	if len(config.RateLimit) == 0 {
		return nil
	}
	out := make(map[string]ratelimit.Limit, len(config.RateLimit))
	for provider, l := range config.RateLimit {
		out[provider] = ratelimit.Limit{RPS: l.RPS, Burst: l.Burst}
	}
	return out
}

// accountCred is the rate-limit credential behind an account-backed client.
// The plan tier sits next to the key in accounts.data, so moving a provider to
// a paid plan is a settings edit rather than a release. CoinGecko's older
// "pro" flag is still honoured: it named the same thing before tiers existed.
func accountCred(provider string, a *entity.Account) ratelimit.Credential {
	tier := a.Data["tier"]
	if tier == "" && a.Data["pro"] == "true" {
		tier = "pro"
	}
	return ratelimit.Credential{Provider: provider, APIKey: a.Data["api_key"], Tier: tier}
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

// coingeckoTier maps the env-configured pro flag onto a plan tier. Empty means
// the free keyed plan, whose 10k monthly allowance is the built-in default.
func coingeckoTier(pro bool) string {
	if pro {
		return coingecko.TierPro
	}
	return ""
}
