package main

import (
	"fmt"
	"strings"

	"github.com/foxcool/greedy-eye/pkg/adapters/telegram"
	"github.com/foxcool/greedy-eye/pkg/entities"
	"github.com/foxcool/greedy-eye/pkg/services/app_router"
	"github.com/foxcool/greedy-eye/pkg/services/control_panel"
	"github.com/foxcool/greedy-eye/pkg/services/sora"
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
	indexPriceStorage, err := memory.NewIndexPriceStorage()
	if err != nil {
		panic(err)
	}

	// Start message router
	appRouter, err := app_router.NewService(jobChan, opportunityChan, sendMessageChan, errorChan)
	if err != nil {
		panic(err)
	}
	go appRouter.Work()

	// Start control panel service if telegram credentials exists
	if config.Telegram.Token != "" && config.Telegram.ChatIDs != nil {
		bot, err := telegram.NewClient(config.Telegram.Token)
		if err != nil {
			panic(err)
		}

		cp, err := control_panel.NewService(sendMessageChan,
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
				Storage:         indexPriceStorage,
				JobChan:         jobChan,
				OpportunityChan: opportunityChan,
				ErrorChan:       errorChan,
			}
			go soraClient.WaitJobs()
			go soraClient.WaitResponses()
		}
	}
}
