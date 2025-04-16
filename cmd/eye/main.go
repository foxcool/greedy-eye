package main

import (
	"context"
	"fmt"
	"log"
	"net"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/foxcool/greedy-eye/internal/api/services"
	"github.com/foxcool/greedy-eye/internal/services/asset"
	"github.com/foxcool/greedy-eye/internal/services/portfolio"
	"github.com/foxcool/greedy-eye/internal/services/price"
	"github.com/foxcool/greedy-eye/internal/services/storage/ent"
	"github.com/foxcool/greedy-eye/internal/services/storage/ent/migrate"
	"github.com/foxcool/greedy-eye/internal/services/user"
	"github.com/getsentry/sentry-go"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"google.golang.org/grpc"
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

	// ToDo: Only for dev stage of project -> use Atlas migration tool
	log.Info("Running auto migration...")
	if err := client.Schema.Create(
		context.Background(),
		migrate.WithDropIndex(true),
		migrate.WithDropColumn(true),
	); err != nil {
		log.Fatal("Failed creating schema resources", zap.Error(err))
	}
	log.Info("Schema migration completed.")

	log.Info("Ensuring prices table is a hypertable...")
	_, err = client.DB().ExecContext(context.Background(), "SELECT create_hypertable('prices', 'timestamp', if_not_exists => TRUE);")
	if err != nil {
		log.Error("Failed to ensure prices table is a hypertable", zap.Error(err))
	} else {
		log.Info("Prices table is now a hypertable (or already was).")
	}

	// Start subservices and gRPC server
	server := registerServices(config.Services)
	go func() {
		listener, err := net.Listen("tcp", fmt.Sprintf(":%d", config.GRPC.Port))
		if err != nil {
			log.Fatal("Failed to listen gRPC", zap.Error(err))
		}
		log.Info("gRPC server started", zap.String("address", listener.Addr().String()), zap.Int("port", config.GRPC.Port))
		if err := server.Serve(listener); err != nil && err != grpc.ErrServerStopped {
			log.Fatal("Failed to serve gRPC", zap.Error(err))
		}
	}()

	// Wait for shutdown signal
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	sig := <-quit
	log.Info("Received shutdown signal", zap.String("signal", sig.String()))
	// Call GracefulStop on the server
	stopped := make(chan struct{})
	go func() {
		server.GracefulStop()
		close(stopped)
	}()
	// Create a timer for graceful shutdown
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Wait for server graceful shutdown
	select {
	case <-shutdownCtx.Done():
		log.Error("gRPC server graceful shutdown timed out", zap.Error(shutdownCtx.Err()))
		server.Stop()
	case <-stopped:
		log.Info("gRPC server stopped gracefully")
	}
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

func registerServices(serviceConfigs []ServiceConfig) *grpc.Server {
	server := grpc.NewServer()

	if len(serviceConfigs) == 0 {
		// Register all services with default implementations
		userService := user.NewService()
		services.RegisterUserServiceServer(server, userService)
		assetService := asset.NewService()
		services.RegisterAssetServiceServer(server, assetService)
		portfolioService := portfolio.NewService()
		services.RegisterPortfolioServiceServer(server, portfolioService)
		pricingService := price.NewService()
		services.RegisterPriceServiceServer(server, pricingService)
	} else {
		for _, service := range serviceConfigs {
			switch service.Type {
			case ServiceConfigTypeUser:
				userService := user.NewService()
				services.RegisterUserServiceServer(server, userService)
			case ServiceConfigTypeAsset:
				assetService := asset.NewService()
				services.RegisterAssetServiceServer(server, assetService)
			case ServiceConfigTypePortfolio:
				portfolioService := portfolio.NewService()
				services.RegisterPortfolioServiceServer(server, portfolioService)
			case ServiceConfigTypePrice:
				pricingService := price.NewService()
				services.RegisterPriceServiceServer(server, pricingService)
			default:
				log.Fatal("Unknown service type", zap.String("type", service.Type))
			}
		}
	}
	return server
}
