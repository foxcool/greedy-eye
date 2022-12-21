package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/foxcool/greedy-eye/pkg/adapters/telegram"
	"github.com/foxcool/greedy-eye/pkg/entities"
	"github.com/foxcool/greedy-eye/pkg/services/control_panel"
	"github.com/foxcool/greedy-eye/pkg/services/sora"
	"github.com/foxcool/greedy-eye/pkg/services/storage/airtable"
	"github.com/foxcool/greedy-eye/pkg/services/storage/memory"
)

const ServiceName = "EYE"

var (
	version = "No Version Provided"
)

func main() {
	config := getConfig()
	sendMessageChan := make(chan interface{}, 100)
	errorChan := make(chan error, 100)
	jobChan := make(chan entities.ExplorationJob, 100)
	opportunityChan := make(chan entities.TradingOpportunity, 100)
	priceChan := make(chan entities.Price, 100)
	memoryPriceChan := make(chan entities.Price, 100)
	airtablePriceChan := make(chan entities.Price, 100)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Start in memory prices storage for local services
	memoryPriceStorage, err := memory.NewPriceStorage()
	if err != nil {
		panic(err)
	}
	go memoryPriceStorage.Work(ctx, memoryPriceChan, errorChan)

	// Start airtable prices storage if DB key and ID if presented
	if config.Airtable.DatabaseID != "" && config.Airtable.Key != "" {
		airtablePriceStorage := airtable.PriceStorage{
			DatabaseID: config.Airtable.DatabaseID,
			APIKey:     config.Airtable.Key,
		}
		if err != nil {
			panic(err)
		}
		go airtablePriceStorage.Work(ctx, airtablePriceChan, errorChan)
	}

	// Start control panel service if telegram credentials exists
	if config.Telegram.Token != "" && config.Telegram.ChatIDs != nil {
		bot, err := telegram.NewClient(config.Telegram.Token)
		if err != nil {
			panic(err)
		}

		cp, err := control_panel.NewService(
			sendMessageChan,
			errorChan,
			bot,
			fmt.Sprintf("%d", config.Telegram.ChatIDs[0]),
		)
		if err != nil {
			panic(err)
		}

		go cp.Run()
	}

	// Start sora price crawlers if urls exist
	if config.Sora.URL != "" {
		for _, url := range strings.Split(config.Sora.URL, ",") {
			soraClient := sora.Service{
				URL:             url,
				Storage:         memoryPriceStorage,
				JobChan:         jobChan,
				OpportunityChan: opportunityChan,
				ErrorChan:       errorChan,
			}
			go soraClient.WaitJobs()
			go soraClient.WaitResponses()
		}
	}

	// Start message router
	sigc := make(chan os.Signal, 1)
	signal.Notify(sigc, os.Interrupt, syscall.SIGTERM)
	for {
		select {
		case opportunity := <-opportunityChan:
			sendMessageChan <- opportunity
		case price := <-priceChan:
			memoryPriceChan <- price
			if config.Airtable.DatabaseID != "" && config.Airtable.Key != "" {
				airtablePriceChan <- price
			}
		case err := <-errorChan:
			sendMessageChan <- err
		case <-sigc:
			return
		}
	}
}
