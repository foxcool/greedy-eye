package main

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/foxcool/greedy-eye/internal/api/services"
	"github.com/foxcool/greedy-eye/internal/services/asset"
	"github.com/foxcool/greedy-eye/internal/services/portfolio"
	"github.com/foxcool/greedy-eye/internal/services/price"
	"github.com/foxcool/greedy-eye/internal/services/rule"
	"github.com/foxcool/greedy-eye/internal/services/storage"
	"github.com/foxcool/greedy-eye/internal/services/storage/ent"
	"github.com/foxcool/greedy-eye/internal/services/user"
	"github.com/getsentry/sentry-go"
	"github.com/grpc-ecosystem/grpc-gateway/v2/runtime"
	_ "github.com/lib/pq"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

const ServiceName = "EYE"

func main() {
	if err := run(); err != nil {
		// Use basic stderr logging since structured logger may not be initialized
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

	// Initialize database client
	if config.DB.URL == "" {
		return fmt.Errorf("database URL cannot be empty")
	}

	client, err := ent.Open("postgres", config.DB.URL)
	if err != nil {
		return fmt.Errorf("connect to database: %w", err)
	}

	defer func() {
		log.Info("Closing DB connection, flushing Sentry events...")
		if err := client.Close(); err != nil {
			log.Error("Failed closing ent client", slog.Any("error", err))
		} else {
			log.Info("Ent client closed successfully")
		}
		sentry.Flush(2 * time.Second)
		log.Info("Bye")
	}()

	// Create context for server lifecycle
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Register services and create servers
	grpcServer, httpServer, err := registerServicesAndCreateServers(ctx, config, client, log)
	if err != nil {
		return fmt.Errorf("register services: %w", err)
	}

	// Channel to collect errors from goroutines
	errCh := make(chan error, 2)
	var wg sync.WaitGroup

	// Start gRPC server
	wg.Add(1)
	go func() {
		defer wg.Done()
		listener, err := net.Listen("tcp", fmt.Sprintf(":%d", config.GRPC.Port))
		if err != nil {
			errCh <- fmt.Errorf("listen gRPC: %w", err)
			return
		}
		log.Info("gRPC server started", slog.String("address", listener.Addr().String()), slog.Int("port", config.GRPC.Port))
		if err := grpcServer.Serve(listener); err != nil && err != grpc.ErrServerStopped {
			errCh <- fmt.Errorf("serve gRPC: %w", err)
		}
	}()

	// Start HTTP server with gRPC-Gateway (if configured)
	if httpServer != nil {
		wg.Add(1)
		go func() {
			defer wg.Done()
			log.Info("HTTP server starting", slog.Int("port", config.HTTP.Port))
			if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
				errCh <- fmt.Errorf("serve HTTP: %w", err)
			}
		}()
	}

	// Wait for shutdown signal or error
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

	select {
	case sig := <-quit:
		log.Info("Received shutdown signal", slog.String("signal", sig.String()))
	case err := <-errCh:
		log.Error("Server error, initiating shutdown", slog.Any("error", err))
	}

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
		log.Error("gRPC server graceful shutdown timed out", slog.Any("error", shutdownCtx.Err()))
		grpcServer.Stop()
	case <-stopped:
		log.Info("gRPC server stopped gracefully")
	}

	// Wait for all goroutines to finish
	wg.Wait()
	log.Info("All servers stopped")

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

// ServiceDefinition defines a service and its registration functions
type ServiceDefinition struct {
	Name            string
	Type            string
	Dependencies    []string
	GRPCRegister    func(*grpc.Server, *ent.Client, *slog.Logger) error
	GatewayRegister func(context.Context, *runtime.ServeMux, string, []grpc.DialOption) error
}

// getAvailableServices returns all available service definitions as a map
func getAvailableServices(storageClient services.StorageServiceClient) map[string]ServiceDefinition {
	return map[string]ServiceDefinition{
		"StorageService": {
			Name:         "StorageService",
			Type:         "storage",
			Dependencies: []string{},
			GRPCRegister: func(server *grpc.Server, client *ent.Client, log *slog.Logger) error {
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
			GRPCRegister: func(server *grpc.Server, client *ent.Client, log *slog.Logger) error {
				services.RegisterUserServiceServer(server, user.NewService(log, storageClient))
				return nil
			},
			GatewayRegister: services.RegisterUserServiceHandlerFromEndpoint,
		},
		"AssetService": {
			Name:         "AssetService",
			Type:         "asset",
			Dependencies: []string{"StorageService"},
			GRPCRegister: func(server *grpc.Server, client *ent.Client, log *slog.Logger) error {
				services.RegisterAssetServiceServer(server, asset.NewService(log, storageClient))
				return nil
			},
			GatewayRegister: services.RegisterAssetServiceHandlerFromEndpoint,
		},
		"PortfolioService": {
			Name:         "PortfolioService",
			Type:         "portfolio",
			Dependencies: []string{"StorageService", "AssetService"},
			GRPCRegister: func(server *grpc.Server, client *ent.Client, log *slog.Logger) error {
				services.RegisterPortfolioServiceServer(server, portfolio.NewService(log))
				return nil
			},
			GatewayRegister: services.RegisterPortfolioServiceHandlerFromEndpoint,
		},
		"PriceService": {
			Name:         "PriceService",
			Type:         "price",
			Dependencies: []string{"StorageService", "AssetService"},
			GRPCRegister: func(server *grpc.Server, client *ent.Client, log *slog.Logger) error {
				// TODO: Create AssetService client adapter if needed
				services.RegisterPriceServiceServer(server, price.NewService(log, storageClient, nil))
				return nil
			},
			GatewayRegister: services.RegisterPriceServiceHandlerFromEndpoint,
		},
		"RuleService": {
			Name:         "RuleService",
			Type:         "rule",
			Dependencies: []string{"StorageService", "UserService", "PortfolioService", "AssetService", "PriceService"},
			GRPCRegister: func(server *grpc.Server, client *ent.Client, log *slog.Logger) error {
				services.RegisterRuleServiceServer(server, rule.NewService(log))
				return nil
			},
			GatewayRegister: services.RegisterRuleServiceHandlerFromEndpoint,
		},
	}
}

// registerServicesAndCreateServers registers services and creates both gRPC and HTTP servers in one pass
func registerServicesAndCreateServers(ctx context.Context, config *Config, client *ent.Client, log *slog.Logger) (*grpc.Server, *http.Server, error) {
	grpcServer := grpc.NewServer()

	// Create storage service first as it's needed by other services
	storageService := storage.NewService(client, log)
	storageClient := storage.NewLocalClient(storageService)

	availableServices := getAvailableServices(storageClient)

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
		log.Info("Running in microservice mode", slog.Int("configured_services", len(config.Services)))
		for _, svcConfig := range config.Services {
			if name, exists := typeToName[svcConfig.Type]; exists {
				servicesToEnable = append(servicesToEnable, name)
			} else {
				log.Warn("Unknown service type in config", slog.String("type", svcConfig.Type))
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
				return nil, nil, fmt.Errorf("unknown service: %s", serviceName)
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
					return nil, nil, fmt.Errorf("register gRPC service %s: %w", serviceName, err)
				}
				log.Info("Registered gRPC service", slog.String("service", serviceName))

				// Register gateway handler immediately if HTTP is enabled
				if mux != nil {
					if err := svc.GatewayRegister(ctx, mux, grpcEndpoint, opts); err != nil {
						log.Warn("Failed to register gateway handler", slog.String("service", svc.Name), slog.Any("error",err))
					} else {
						log.Info("Registered gateway handler", slog.String("service", svc.Name))
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
			return nil, nil, fmt.Errorf("circular dependency or missing dependency detected: pending services %v", servicesToEnable)
		}
	}

	// Create HTTP server if HTTP port is configured
	var httpServer *http.Server
	if config.HTTP.Port > 0 {
		// Add health check endpoint
		http.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
			if _, err := w.Write([]byte(`{"status":"ok","service":"greedy-eye"}`)); err != nil {
				log.Error("Failed to write health response", slog.Any("error",err))
			}
		})

		httpServer = &http.Server{
			Addr:    fmt.Sprintf(":%d", config.HTTP.Port),
			Handler: mux,
		}
	}
	return grpcServer, httpServer, nil
}
