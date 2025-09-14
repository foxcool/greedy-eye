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
		if err := log.Sync(); err != nil {
			log.Error("Failed to sync logger", zap.Error(err))
		}
	}()

	// Create context for server lifecycle
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Register services and create servers
	grpcServer, httpServer := registerServicesAndCreateServers(ctx, config, client, log)
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

	// Start HTTP server with gRPC-Gateway (if configured)
	if httpServer != nil {
		wg.Add(1)
		go func() {
			defer wg.Done()
			log.Info("HTTP server starting", zap.Int("port", config.HTTP.Port))
			if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
				log.Fatal("Failed to serve HTTP", zap.Error(err))
			}
		}()
	}

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

// ServiceDefinition defines a service and its registration functions
type ServiceDefinition struct {
	Name            string
	Type            string
	Dependencies    []string
	GRPCRegister    func(*grpc.Server, *ent.Client, *zap.Logger) error
	GatewayRegister func(context.Context, *runtime.ServeMux, string, []grpc.DialOption) error
}

// getAvailableServices returns all available service definitions as a map
func getAvailableServices() map[string]ServiceDefinition {
	return map[string]ServiceDefinition{
		"StorageService": {
			Name:         "StorageService",
			Type:         "storage",
			Dependencies: []string{},
			GRPCRegister: func(server *grpc.Server, client *ent.Client, log *zap.Logger) error {
				storageService := storage.NewService(client, log)
				services.RegisterStorageServiceServer(server, storageService)
				return nil
			},
			GatewayRegister: services.RegisterStorageServiceHandlerFromEndpoint,
		},
		"UserService": {
			Name:         "UserService",
			Type:         "user",
			Dependencies: []string{"StorageService"},
			GRPCRegister: func(server *grpc.Server, client *ent.Client, log *zap.Logger) error {
				services.RegisterUserServiceServer(server, user.NewService(log))
				return nil
			},
			GatewayRegister: services.RegisterUserServiceHandlerFromEndpoint,
		},
		"AssetService": {
			Name:         "AssetService",
			Type:         "asset",
			Dependencies: []string{"StorageService"},
			GRPCRegister: func(server *grpc.Server, client *ent.Client, log *zap.Logger) error {
				services.RegisterAssetServiceServer(server, asset.NewService(log))
				return nil
			},
			GatewayRegister: services.RegisterAssetServiceHandlerFromEndpoint,
		},
		"PortfolioService": {
			Name:         "PortfolioService",
			Type:         "portfolio",
			Dependencies: []string{"StorageService", "AssetService"},
			GRPCRegister: func(server *grpc.Server, client *ent.Client, log *zap.Logger) error {
				services.RegisterPortfolioServiceServer(server, portfolio.NewService(log))
				return nil
			},
			GatewayRegister: services.RegisterPortfolioServiceHandlerFromEndpoint,
		},
		"PriceService": {
			Name:         "PriceService",
			Type:         "price",
			Dependencies: []string{"StorageService", "AssetService"},
			GRPCRegister: func(server *grpc.Server, client *ent.Client, log *zap.Logger) error {
				services.RegisterPriceServiceServer(server, price.NewService(log))
				return nil
			},
			GatewayRegister: services.RegisterPriceServiceHandlerFromEndpoint,
		},
	}
}

// registerServicesAndCreateServers registers services and creates both gRPC and HTTP servers in one pass
func registerServicesAndCreateServers(ctx context.Context, config *Config, client *ent.Client, log *zap.Logger) (*grpc.Server, *http.Server) {
	grpcServer := grpc.NewServer()
	availableServices := getAvailableServices()

	// Create service type to name mapping for faster lookup
	typeToName := make(map[string]string)
	for name, svc := range availableServices {
		typeToName[svc.Type] = name
	}

	// Determine which services to enable
	var servicesToEnable []string
	if len(config.Services) == 0 {
		// Monolithic mode - enable all implemented services
		log.Info("Running in monolithic mode - enabling all services")
		for name := range availableServices {
			servicesToEnable = append(servicesToEnable, name)
		}
	} else {
		// Microservice mode - enable only configured services
		log.Info("Running in microservice mode", zap.Int("configured_services", len(config.Services)))
		for _, svcConfig := range config.Services {
			if name, exists := typeToName[svcConfig.Type]; exists {
				servicesToEnable = append(servicesToEnable, name)
			} else {
				log.Warn("Unknown service type in config", zap.String("type", svcConfig.Type))
			}
		}
	}

	// Prepare HTTP server components if needed
	var mux *runtime.ServeMux
	var grpcEndpoint string
	var opts []grpc.DialOption
	if config.HTTP.Port > 0 {
		mux = runtime.NewServeMux()
		grpcEndpoint = fmt.Sprintf("localhost:%d", config.GRPC.Port)
		opts = []grpc.DialOption{grpc.WithTransportCredentials(insecure.NewCredentials())}
	}

	// Register services with dependency resolution
	registered := make(map[string]bool)

	for len(servicesToEnable) > 0 {
		progress := false
		for i := 0; i < len(servicesToEnable); i++ {
			serviceName := servicesToEnable[i]
			svc, exists := availableServices[serviceName]
			if !exists {
				log.Fatal("Unknown service", zap.String("service", serviceName))
			}

			// Check if all dependencies are satisfied
			depsOk := true
			for _, dep := range svc.Dependencies {
				if !registered[dep] {
					depsOk = false
					break
				}
			}

			if depsOk {
				// Register gRPC service
				if err := svc.GRPCRegister(grpcServer, client, log); err != nil {
					log.Fatal("Failed to register gRPC service", zap.String("service", serviceName), zap.Error(err))
				}
				log.Info("Registered gRPC service", zap.String("service", serviceName))

				// Register gateway handler immediately if HTTP is enabled
				if mux != nil {
					if err := svc.GatewayRegister(ctx, mux, grpcEndpoint, opts); err != nil {
						log.Warn("Failed to register gateway handler", zap.String("service", svc.Name), zap.Error(err))
					} else {
						log.Info("Registered gateway handler", zap.String("service", svc.Name))
					}
				}

				registered[serviceName] = true

				// Remove from pending list
				servicesToEnable = append(servicesToEnable[:i], servicesToEnable[i+1:]...)
				i--
				progress = true
			}
		}

		if !progress {
			log.Fatal("Circular dependency or missing dependency detected", zap.Strings("pending_services", servicesToEnable))
		}
	}

	// Create HTTP server if HTTP port is configured
	var httpServer *http.Server
	if config.HTTP.Port > 0 {
		// Add health check endpoint
		http.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
			if _, err := w.Write([]byte(`{"status":"ok","service":"greedy-eye"}`)); err != nil {
				log.Error("Failed to write health response", zap.Error(err))
			}
		})

		httpServer = &http.Server{
			Addr:    fmt.Sprintf(":%d", config.HTTP.Port),
			Handler: mux,
		}
	}
	return grpcServer, httpServer
}
