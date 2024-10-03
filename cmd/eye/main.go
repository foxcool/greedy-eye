package main

import (
	"fmt"
	"net"
	"time"

	"github.com/foxcool/greedy-eye/pkg/services/asset"
	"github.com/foxcool/greedy-eye/pkg/services/portfolio"
	"github.com/foxcool/greedy-eye/pkg/services/price"
	"github.com/foxcool/greedy-eye/pkg/services/user"
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
	defer sentry.Flush(2 * time.Second)

	// Connect to DB

	// Start subservices and gRPC server
	server := grpc.NewServer()

	userService := user.NewUserService()
	userService.Register(server)
	assetService := asset.NewAssetService()
	assetService.Register(server)
	portfolioService := portfolio.NewPortfolioService()
	portfolioService.Register(server)
	pricingService := price.NewPricingService(assetService, nil)
	pricingService.Register(server)

	listener, err := net.Listen("tcp", fmt.Sprintf(":%d", config.GRPC.Port))
	if err != nil {
		log.Fatal("failed to listen gRPC", zap.Error(err))
	}
	log.Info("gRPC server started", zap.String("address", listener.Addr().String()))
	if err := server.Serve(listener); err != nil {
		log.Fatal("failed to serve gRPC", zap.Error(err))
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
