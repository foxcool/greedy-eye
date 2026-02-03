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
	"github.com/foxcool/greedy-eye/internal/api/v1/apiv1connect"
	"github.com/foxcool/greedy-eye/internal/service/marketdata"
	"github.com/foxcool/greedy-eye/internal/service/portfolio"
	"github.com/foxcool/greedy-eye/internal/store/postgres"
	"github.com/getsentry/sentry-go"
	"github.com/jackc/pgx/v5/pgxpool"
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

	pool, err := pgxpool.New(context.Background(), config.DB.URL)
	if err != nil {
		return fmt.Errorf("connect to database: %w", err)
	}

	defer func() {
		log.Info("Closing DB connection, flushing Sentry events...")
		pool.Close()
		sentry.Flush(2 * time.Second)
		log.Info("Bye")
	}()

	// Create stores
	marketDataStore := postgres.NewMarketDataStore(pool)
	portfolioStore := postgres.NewPortfolioStore(pool)

	// Create handlers
	marketDataHandler := marketdata.NewHandler(marketDataStore, log)
	portfolioHandler := portfolio.NewHandler(portfolioStore, log)

	// Setup HTTP mux
	mux := http.NewServeMux()

	// Health endpoint
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		if _, err := w.Write([]byte(`{"status":"ok","service":"greedy-eye"}`)); err != nil {
			log.Error("Failed to write health response", slog.Any("error", err))
		}
	})

	// Register Connect handlers
	path, handler := apiv1connect.NewMarketDataServiceHandler(
		marketDataHandler,
		connect.WithInterceptors(loggingInterceptor(log)),
	)
	mux.Handle(path, handler)

	path, handler = apiv1connect.NewPortfolioServiceHandler(
		portfolioHandler,
		connect.WithInterceptors(loggingInterceptor(log)),
	)
	mux.Handle(path, handler)

	// Create server with h2c (HTTP/2 cleartext) support for Connect
	server := &http.Server{
		Addr:    fmt.Sprintf(":%d", config.Server.Port),
		Handler: h2c.NewHandler(mux, &http2.Server{}),
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
			log.Info("request",
				slog.String("procedure", req.Spec().Procedure),
				slog.Duration("duration", time.Since(start)),
				slog.Bool("error", err != nil),
			)
			return resp, err
		}
	}
}
