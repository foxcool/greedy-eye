package main

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/foxcool/greedy-eye/internal/api/services"
	"github.com/foxcool/greedy-eye/internal/services/asset"
	"github.com/foxcool/greedy-eye/internal/services/portfolio"
	"github.com/foxcool/greedy-eye/internal/services/price"
	"github.com/foxcool/greedy-eye/internal/services/storage"
	"github.com/foxcool/greedy-eye/internal/services/storage/ent"
	"github.com/foxcool/greedy-eye/internal/services/user"
	"github.com/getsentry/sentry-go"
	"github.com/grpc-ecosystem/grpc-gateway/v2/runtime"
	_ "github.com/lib/pq"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

const ServiceName = "EYE"

func main() {
	config, err := getConfig()
	if err != nil {
		panic(err)
	}
	log, err := createLogger(config.Logger.Level)
	if err != nil {
		panic(err)
	}
	defer func() {
		err := log.Sync()
		if err != nil {
			panic(err)
		}
	}()

	// Init sentry
	if config.Sentry.DSN != "" {
		err := sentry.Init(sentry.ClientOptions{
			Dsn:              config.Sentry.DSN,
			TracesSampleRate: config.Sentry.TracesSampleRate,
		})
		if err != nil {
			panic(err)
		}
	}

	// Initialize database client
	if config.DB.URL == "" {
		log.Fatal("Database URL cannot be empty")
	}
	client, err := ent.Open("postgres", config.DB.URL)
	if err != nil {
		log.Fatal("Failed opening connection to postgres", zap.Error(err))
	}

	defer func() {
		log.Info("Closing DB connection, flushing Sentry events, syncing logger...")
		if err := client.Close(); err != nil {
			log.Error("Failed closing ent client", zap.Error(err))
		} else {
			log.Info("Ent client closed successfully")
		}
		sentry.Flush(2 * time.Second)
		log.Info("Bye")
		_ = log.Sync()
	}()

	// Create context for server lifecycle
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Start gRPC server
	grpcServer := registerServices(config.Services, client, log)
	var wg sync.WaitGroup

	// Start gRPC server
	wg.Add(1)
	go func() {
		defer wg.Done()
		listener, err := net.Listen("tcp", fmt.Sprintf(":%d", config.GRPC.Port))
		if err != nil {
			log.Fatal("Failed to listen gRPC", zap.Error(err))
		}
		log.Info("gRPC server started", zap.String("address", listener.Addr().String()), zap.Int("port", config.GRPC.Port))
		if err := grpcServer.Serve(listener); err != nil && err != grpc.ErrServerStopped {
			log.Fatal("Failed to serve gRPC", zap.Error(err))
		}
	}()

	// Start HTTP server with gRPC-Gateway
	wg.Add(1)
	go func() {
		defer wg.Done()
		httpServer, err := createHTTPServer(ctx, config.GRPC.Port, config.HTTP.Port, log)
		if err != nil {
			log.Fatal("Failed to create HTTP server", zap.Error(err))
		}

		log.Info("HTTP server starting", zap.Int("port", config.HTTP.Port))
		if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatal("Failed to serve HTTP", zap.Error(err))
		}
	}()

	// Wait for shutdown signal
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	sig := <-quit
	log.Info("Received shutdown signal", zap.String("signal", sig.String()))

	// Cancel context to stop HTTP server
	cancel()

	// Create a timer for graceful shutdown
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer shutdownCancel()

	// Shutdown gRPC server gracefully
	stopped := make(chan struct{})
	go func() {
		grpcServer.GracefulStop()
		close(stopped)
	}()

	// Wait for servers graceful shutdown
	select {
	case <-shutdownCtx.Done():
		log.Error("gRPC server graceful shutdown timed out", zap.Error(shutdownCtx.Err()))
		grpcServer.Stop()
	case <-stopped:
		log.Info("gRPC server stopped gracefully")
	}

	// Wait for all goroutines to finish
	wg.Wait()
	log.Info("All servers stopped")
}

func createHTTPServer(ctx context.Context, grpcPort int, httpPort int, log *zap.Logger) (*http.Server, error) {
	// Create gRPC-Gateway mux
	mux := runtime.NewServeMux()

	// gRPC endpoint (connecting to local gRPC server)
	grpcEndpoint := fmt.Sprintf("localhost:%d", grpcPort)
	opts := []grpc.DialOption{grpc.WithTransportCredentials(insecure.NewCredentials())}

	// Register all services with gRPC-Gateway
	if err := services.RegisterStorageServiceHandlerFromEndpoint(ctx, mux, grpcEndpoint, opts); err != nil {
		return nil, fmt.Errorf("failed to register StorageService: %w", err)
	}

	if err := services.RegisterUserServiceHandlerFromEndpoint(ctx, mux, grpcEndpoint, opts); err != nil {
		return nil, fmt.Errorf("failed to register UserService: %w", err)
	}

	if err := services.RegisterAssetServiceHandlerFromEndpoint(ctx, mux, grpcEndpoint, opts); err != nil {
		return nil, fmt.Errorf("failed to register AssetService: %w", err)
	}

	if err := services.RegisterPortfolioServiceHandlerFromEndpoint(ctx, mux, grpcEndpoint, opts); err != nil {
		return nil, fmt.Errorf("failed to register PortfolioService: %w", err)
	}

	if err := services.RegisterPriceServiceHandlerFromEndpoint(ctx, mux, grpcEndpoint, opts); err != nil {
		return nil, fmt.Errorf("failed to register PriceService: %w", err)
	}

	// Register new services (they will be added later)
	if err := services.RegisterAuthServiceHandlerFromEndpoint(ctx, mux, grpcEndpoint, opts); err != nil {
		log.Warn("Failed to register AuthService (not implemented yet)", zap.Error(err))
	}

	if err := services.RegisterRuleServiceHandlerFromEndpoint(ctx, mux, grpcEndpoint, opts); err != nil {
		log.Warn("Failed to register RuleService (not implemented yet)", zap.Error(err))
	}

	// Add health check endpoint
	http.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		if _, err := w.Write([]byte(`{"status":"ok","service":"greedy-eye"}`)); err != nil {
			log.Error("Failed to write health response", zap.Error(err))
		}
	})

	// Create HTTP server
	return &http.Server{
		Addr:    fmt.Sprintf(":%d", httpPort),
		Handler: mux,
	}, nil
}

func createLogger(level string) (*zap.Logger, error) {
	cfg := zap.NewProductionConfig()
	if level != "" {
		lvl, err := zapcore.ParseLevel(level)
		if err != nil {
			return nil, err
		}
		cfg.Level.SetLevel(lvl)
	}
	return cfg.Build()
}

func registerServices(serviceConfigs []ServiceConfig, client *ent.Client, log *zap.Logger) *grpc.Server {
	server := grpc.NewServer()

	// Always register StorageService
	storageService := storage.NewService(client, log)
	services.RegisterStorageServiceServer(server, storageService)

	// Helper function to register a service by type
	registerServiceByType := func(serviceType string) {
		switch serviceType {
		case ServiceConfigTypeUser:
			services.RegisterUserServiceServer(server, user.NewService())
		case ServiceConfigTypeAsset:
			services.RegisterAssetServiceServer(server, asset.NewService())
		case ServiceConfigTypePortfolio:
			services.RegisterPortfolioServiceServer(server, portfolio.NewService())
		case ServiceConfigTypePrice:
			services.RegisterPriceServiceServer(server, price.NewService())
		default:
			log.Fatal("Unknown service type", zap.String("type", serviceType))
		}
	}

	if len(serviceConfigs) == 0 {
		// Register all services with default implementations
		registerServiceByType(ServiceConfigTypeUser)
		registerServiceByType(ServiceConfigTypeAsset)
		registerServiceByType(ServiceConfigTypePortfolio)
		registerServiceByType(ServiceConfigTypePrice)
	} else {
		// Register only configured services
		for _, service := range serviceConfigs {
			registerServiceByType(service.Type)
		}
	}

	return server
}
