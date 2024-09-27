package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/foxcool/greedy-eye/pkg/entities"
	"github.com/foxcool/greedy-eye/pkg/services/storage/badger"
	"github.com/getsentry/sentry-go"
	"go.uber.org/zap"
)

const ServiceName = "EYE"

var (
	version = "No Version Provided"
)

func main() {
	log, _ := zap.NewProduction()
	config := getConfig(log)
	defer log.Sync()

	sendMessageChan := make(chan interface{}, 100)
	errorChan := make(chan error, 100)
	opportunityChan := make(chan entities.TradingOpportunity, 100)
	priceChan := make(chan entities.Price, 100)
	memoryPriceChan := make(chan entities.Price, 100)

	// Init sentry
	if config.Sentry.DSN != "" {
		err := sentry.Init(sentry.ClientOptions{
			Dsn:              config.Sentry.DSN,
			TracesSampleRate: config.Sentry.TracesSampleRate,
			Release:          version,
		})
		if err != nil {
			panic(err)
		}
	}
	defer sentry.Flush(2 * time.Second)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Start in memory prices storage for local services
	badgerPriceStorage, err := badger.NewPriceStorage("/tmp/prices")
	if err != nil {
		panic(err)
	}
	go badgerPriceStorage.Work(ctx, memoryPriceChan, errorChan)

	// Connect to DB

	// Start configured subservices

	// Start message router
	sigc := make(chan os.Signal, 1)
	signal.Notify(sigc, os.Interrupt, syscall.SIGTERM)
	for {
		select {
		case opportunity := <-opportunityChan:
			sendMessageChan <- opportunity
		case price := <-priceChan:
			memoryPriceChan <- price
		case err := <-errorChan:
			sendMessageChan <- err
		case <-sigc:
			return
		}
	}
}
