package main

import (
	"fmt"
	"log"
	"os"
	"strings"

	"github.com/knadh/koanf"
	"github.com/knadh/koanf/parsers/json"
	"github.com/knadh/koanf/parsers/toml"
	"github.com/knadh/koanf/parsers/yaml"
	"github.com/knadh/koanf/providers/confmap"
	"github.com/knadh/koanf/providers/env"
	"github.com/knadh/koanf/providers/file"
	"github.com/spf13/pflag"
)

// Config is application config struct
type Config struct {
	Telegram struct {
		Token   string `koanf:"token"`
		ChatIDs []int  `koanf:"chatIDs"`
	} `koanf:"telegram"`
	EtherscanToken string `koanf:"etherscanToken"`
	Sora           struct {
		URL string `koanf:"URL"`
	} `koanf:"sora"`
	Airtable struct {
		Key        string `koanf:"key"`
		DatabaseID string `koanf:"databaseID"`
	} `koanf:"airtable"`
	Sentry struct {
		DSN              string  `koanf:"dsn"`
		TracesSampleRate float64 `koanf:"tracesSampleRate"`
	} `koanf:"sentry"`
}

func getConfig() Config {
	var err error
	k := koanf.New(".")

	// Default values

	defaults := map[string]interface{}{
		"sentry.tracesSampleRate": 1.0,
	}
	err = k.Load(confmap.Provider(defaults, "."), nil)
	if err != nil {
		log.Fatalf("error loading default config parameters: %v", err)
	}

	// Load command line and configs

	f := pflag.NewFlagSet("config", pflag.ContinueOnError)
	f.Usage = func() {
		fmt.Println(f.FlagUsages())
		os.Exit(0)
	}
	f.Bool("version", false, "Show version")
	f.String("c", "", "Path to config file")
	err = f.Parse(os.Args[1:])
	if err != nil {
		log.Fatalf("error loading command line parameters: %v", err)
	}

	// Show version and die if needed
	showVersion, _ := f.GetBool("version")
	if showVersion {
		fmt.Printf("Version: %s\n", version)
		os.Exit(0)
	}

	// Load the config files provided in the commandline.
	cFile, _ := f.GetString("c")
	switch {
	case strings.HasSuffix(cFile, "toml"):
		if err := k.Load(file.Provider(cFile), toml.Parser()); err != nil {
			log.Fatalf("error loading file: %v", err)
		}
	case strings.HasSuffix(cFile, "yaml"):
		if err := k.Load(file.Provider(cFile), yaml.Parser()); err != nil {
			log.Fatalf("error loading file: %v", err)
		}
	case strings.HasSuffix(cFile, "json"):
		if err := k.Load(file.Provider(cFile), json.Parser()); err != nil {
			log.Fatalf("error loading file: %v", err)
		}
	}

	// Load ENV

	err = k.Load(env.Provider(ServiceName+"_", ".", func(s string) string {
		return strings.Replace(strings.ToLower(
			strings.TrimPrefix(s, ServiceName+"_")), "_", ".", -1)
	}), nil)
	if err != nil {
		log.Fatalf("error loading ENV parameters: %v", err)
	}

	// Unmarshal configs to struct
	var config Config
	err = k.Unmarshal("", &config)
	if err != nil {
		log.Fatalf("error unmarshaling config parameters to struct: %v", err)
	}

	return config
}
