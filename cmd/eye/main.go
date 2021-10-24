package main

import (
	"fmt"

	"github.com/foxcool/greedy-eye/pkg/adapters/telegram"
	"github.com/foxcool/greedy-eye/pkg/services/control_panel"
)

const ServiceName = "EYE"

var (
	version = "No Version Provided"
)

func main() {
	config := getConfig()
	sendChan := make(chan interface{}, 100)
	errorChan := make(chan interface{}, 100)

	// Start control panel service if telegram credentials exists
	if config.Telegram.Token != "" && config.Telegram.ChatIDs != nil {
		bot, err := telegram.NewClient(config.Telegram.Token)
		if err != nil {
			panic(err)
		}

		cp, err := control_panel.NewService(sendChan,
			errorChan,
			bot,
			fmt.Sprintf("%d", config.Telegram.ChatIDs[0]),
		)
		if err != nil {
			panic(err)
		}

		go cp.Run()
	}
}
