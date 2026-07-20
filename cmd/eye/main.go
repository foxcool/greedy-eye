package main

import (
	"context"
	"encoding/base64"
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
	"github.com/foxcool/greedy-eye/internal/adapter/coingecko"
	cosmosadapter "github.com/foxcool/greedy-eye/internal/adapter/cosmos"
	esploraadapter "github.com/foxcool/greedy-eye/internal/adapter/esplora"
	moralisadapter "github.com/foxcool/greedy-eye/internal/adapter/moralis"
	solanaadapter "github.com/foxcool/greedy-eye/internal/adapter/solana"
	subscanadapter "github.com/foxcool/greedy-eye/internal/adapter/subscan"
	tonapiadapter "github.com/foxcool/greedy-eye/internal/adapter/tonapi"
	"github.com/foxcool/greedy-eye/internal/entity"
	"github.com/foxcool/greedy-eye/internal/middleware"
	"github.com/foxcool/greedy-eye/internal/scheduler"
	"github.com/foxcool/greedy-eye/internal/service/analytics"
	"github.com/foxcool/greedy-eye/internal/service/automation"
	"github.com/foxcool/greedy-eye/internal/service/credentials"
	"github.com/foxcool/greedy-eye/internal/service/marketdata"
	"github.com/foxcool/greedy-eye/internal/service/portfolio"
	storecrypto "github.com/foxcool/greedy-eye/internal/store/crypto"
	"github.com/foxcool/greedy-eye/internal/store/postgres"
	"github.com/getsentry/sentry-go"
	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"
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

	// Initialize price providers.
	cgClient := coingecko.NewClient(coingecko.Config{APIKey: config.CoinGecko.APIKey, Pro: config.CoinGecko.Pro})

	// Initialize optional Moralis wallet syncer
	var walletSyncer entity.WalletSyncer
	if apiKey := config.Moralis.APIKey; apiKey != "" {
		moralisClient := moralisadapter.NewClient(moralisadapter.Config{APIKey: apiKey})
		walletSyncer = moralisadapter.NewWalletSyncer(moralisClient)
	}

	// Initialize accounts.data encryption at rest (ADR-005)
	var portfolioStoreOpts []postgres.PortfolioStoreOption
	if config.Security.MasterKey != "" {
		masterKey, err := base64.StdEncoding.DecodeString(config.Security.MasterKey)
		if err != nil {
			return fmt.Errorf("decode security master key: %w", err)
		}
		encryptor, err := storecrypto.NewEncryptor(masterKey)
		if err != nil {
			return fmt.Errorf("init encryptor: %w", err)
		}
		portfolioStoreOpts = append(portfolioStoreOpts, postgres.WithEncryptor(encryptor))
	} else {
		log.Warn("SECURITY_MASTERKEY is not set: accounts.data is stored in plaintext")
	}

	portfolioStore := postgres.NewPortfolioStore(pool, portfolioStoreOpts...)

	// Env-configured price providers stay the fallback registry; the resolver
	// overlays them with credentials stored in accounts (system → user).
	envPriceProviders := map[string]marketdata.PriceProvider{
		coingecko.ProviderName: coingecko.NewProvider(cgClient),
		binanceadapter.ProviderName: binanceadapter.NewProvider(
			binanceadapter.NewClient(binanceadapter.Config{
				APIKey:    config.Binance.APIKey,
				APISecret: config.Binance.APISecret,
				Sandbox:   config.Binance.Sandbox,
			}),
		),
	}
	credResolver := credentials.NewResolver(credentials.Config{
		Source: portfolioStore,
		// Wallet providers are routed by chain: every non-EVM ecosystem
		// registers its own adapter here alongside Moralis (personal-feb).
		WalletSyncers: map[string]credentials.WalletProvider{
			moralisadapter.ProviderName: {
				Factory: func(a *entity.Account) (entity.WalletSyncer, error) {
					return moralisadapter.NewWalletSyncer(
						moralisadapter.NewClient(moralisadapter.Config{APIKey: a.Data["api_key"]}),
					), nil
				},
				Chains:         moralisadapter.SupportedChains(),
				HandlesAddress: moralisadapter.HandlesAddress,
			},
			subscanadapter.ProviderName: {
				Factory: func(a *entity.Account) (entity.WalletSyncer, error) {
					return subscanadapter.NewWalletSyncer(
						subscanadapter.NewClient(subscanadapter.Config{APIKey: a.Data["api_key"]}),
					), nil
				},
				Chains:         subscanadapter.SupportedChains(),
				HandlesAddress: subscanadapter.HandlesAddress,
			},
			tonapiadapter.ProviderName: {
				Factory: func(a *entity.Account) (entity.WalletSyncer, error) {
					return tonapiadapter.NewWalletSyncer(
						tonapiadapter.NewClient(tonapiadapter.Config{APIKey: a.Data["api_key"]}),
					), nil
				},
				Chains:         tonapiadapter.SupportedChains(),
				HandlesAddress: tonapiadapter.HandlesAddress,
			},
			solanaadapter.ProviderName: {
				Factory: func(a *entity.Account) (entity.WalletSyncer, error) {
					return solanaadapter.NewWalletSyncer(
						solanaadapter.NewClient(solanaadapter.Config{APIKey: a.Data["api_key"]}),
					), nil
				},
				Chains:         solanaadapter.SupportedChains(),
				HandlesAddress: solanaadapter.HandlesAddress,
			},
			// Esplora needs no credentials; the account exists only so the
			// registry has something to route to. base_url picks the instance
			// (mempool.space by default, Blockstream serves the same API).
			esploraadapter.ProviderName: {
				Factory: func(a *entity.Account) (entity.WalletSyncer, error) {
					return esploraadapter.NewWalletSyncer(
						esploraadapter.NewClient(esploraadapter.Config{BaseURL: a.Data["base_url"]}),
					), nil
				},
				Chains:         esploraadapter.SupportedChains(),
				HandlesAddress: esploraadapter.HandlesAddress,
			},
			cosmosadapter.ProviderName: {
				Factory: func(a *entity.Account) (entity.WalletSyncer, error) {
					return cosmosadapter.NewWalletSyncer(
						cosmosadapter.NewClient(cosmosadapter.Config{BaseURL: a.Data["base_url"]}),
					), nil
				},
				Chains:         cosmosadapter.SupportedChains(),
				HandlesAddress: cosmosadapter.HandlesAddress,
			},
		},
		ExchangeSyncers: map[string]credentials.ExchangeSyncerFactory{
			binanceadapter.ProviderName: func(a *entity.Account) (entity.ExchangeSyncer, error) {
				return binanceadapter.NewExchangeSyncer(binanceadapter.NewClient(binanceadapter.Config{
					APIKey:    a.Data["api_key"],
					APISecret: a.Data["api_secret"],
				})), nil
			},
		},
		PriceProviders: map[string]credentials.PriceProviderFactory{
			coingecko.ProviderName: func(a *entity.Account) (marketdata.PriceProvider, error) {
				return coingecko.NewProvider(coingecko.NewClient(coingecko.Config{
					APIKey: a.Data["api_key"],
					Pro:    a.Data["pro"] == "true",
				})), nil
			},
			binanceadapter.ProviderName: func(a *entity.Account) (marketdata.PriceProvider, error) {
				return binanceadapter.NewProvider(binanceadapter.NewClient(binanceadapter.Config{
					APIKey:    a.Data["api_key"],
					APISecret: a.Data["api_secret"],
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
		WithProviderSource(credResolver)
	automationStore := postgres.NewAutomationStore(pool)
	automationHandler := automation.NewHandler(automationStore, log)
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
		sched, err = scheduler.New(scheduler.Config{PriceFetchCron: config.Scheduler.PriceFetchCron},
			automationStore, automationHandler, mdHandler, log)
		if err != nil {
			return fmt.Errorf("init scheduler: %w", err)
		}
		if err := sched.Start(); err != nil {
			return fmt.Errorf("start scheduler: %w", err)
		}
		log.Info("scheduler started", slog.String("price_fetch_cron", config.Scheduler.PriceFetchCron))
	}

	// Create server with h2c (HTTP/2 cleartext) support for Connect
	server := &http.Server{
		Addr:              fmt.Sprintf(":%d", config.Server.Port),
		Handler:           h2c.NewHandler(mux, &http2.Server{}),
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
