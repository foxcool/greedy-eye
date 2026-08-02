package main

import (
	"fmt"
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
	Logger struct {
		Level string `koanf:"level"`
	} `koanf:"logger"`
	Sentry struct {
		DSN              string  `koanf:"dsn"`
		TracesSampleRate float64 `koanf:"tracesSampleRate"`
	} `koanf:"sentry"`
	DB struct {
		URL string `koanf:"url"`
	} `koanf:"db"`
	Server struct {
		Port int `koanf:"port"`
	} `koanf:"server"`
	Services  []ServiceConfig `koanf:"services"`
	CoinGecko struct {
		APIKey string `koanf:"apiKey"`
		Pro    bool   `koanf:"pro"`
	} `koanf:"coingecko"`
	Moralis struct {
		APIKey string `koanf:"apiKey"`
	} `koanf:"moralis"`
	Binance struct {
		APIKey    string `koanf:"apiKey"`
		APISecret string `koanf:"apiSecret"`
		Sandbox   bool   `koanf:"sandbox"`
	} `koanf:"binance"`
	Security struct {
		// MasterKey is a base64-encoded 32-byte key for accounts.data
		// encryption at rest (ADR-005). Empty = plaintext mode.
		MasterKey string `koanf:"masterKey"`
		// PreviousMasterKey is the key the instance is rotating away from,
		// base64 as above. Reads fall back to it so a rotation does not have to
		// be simultaneous with re-encrypting every row; writes never use it.
		// Set it, restart, run `eye rewrap-secrets`, then remove it.
		PreviousMasterKey string `koanf:"previousMasterKey"`
	} `koanf:"security"`
	// RateLimit lets an operator override the built-in per-provider request
	// budget, keyed by provider slug (subscan, coingecko, ...). Useful on a
	// paid plan, or to dial a provider down after an enforcement notice
	// without waiting for a release. Omitted providers use the defaults in
	// internal/adapter/ratelimit.
	RateLimit map[string]RateLimitConfig `koanf:"ratelimit"`
	Scheduler struct {
		Enabled bool `koanf:"enabled"`
		// PriceFetchCron is the cron spec for periodic external price
		// fetching. Empty disables the price job.
		// Key is lowercase on purpose: env vars produce lowercase koanf
		// keys, and a camelCase default key would shadow the env override.
		PriceFetchCron string `koanf:"pricefetchcron"`
		// RescoreCron is the cron spec for the catalogue identity-rescore job
		// (scam-filtering). Empty disables it. Lowercase key for the same
		// env-override reason as pricefetchcron.
		RescoreCron string `koanf:"rescorecron"`
	} `koanf:"scheduler"`
}

// RateLimitConfig is one provider's request budget.
type RateLimitConfig struct {
	// RPS is the sustained rate. Fractional values are meaningful.
	RPS float64 `koanf:"rps"`
	// Burst is how many requests may go out back-to-back. Zero means one:
	// providers that meter per second are tripped by bursts, not by rate.
	Burst int `koanf:"burst"`
}

// ServiceConfig is a config for a service
type ServiceConfig struct {
	Type       string            `koanf:"type"`
	Parameters map[string]string `koanf:"parameters"`
}

const (
	ServiceConfigTypeMarketData = "marketdata"
	ServiceConfigTypePortfolio  = "portfolio"
	ServiceConfigTypeAutomation = "automation"
	ServiceConfigTypeAnalytics  = "analytics"
)

func getConfig() (*Config, error) {
	var err error
	k := koanf.New(".")

	// Default values

	defaults := map[string]any{
		"sentry.tracesSampleRate": 1.0,
		"server.port":             8080,
		"scheduler.enabled":       true,
		// Hourly. The sweep no longer re-prices the whole catalogue: it takes
		// what is due, oldest first, within the share of the provider's plan
		// this interval affords. Fifteen minutes was what burned CoinGecko's
		// monthly free quota in eight days.
		"scheduler.pricefetchcron": "0 * * * *",
		// Daily at 03:00; catalogue identity rescore is cheap and idempotent.
		"scheduler.rescorecron": "0 3 * * *",
		"services": []any{
			map[string]any{"type": ServiceConfigTypeMarketData},
			map[string]any{"type": ServiceConfigTypePortfolio},
			map[string]any{"type": ServiceConfigTypeAutomation},
			map[string]any{"type": ServiceConfigTypeAnalytics},
		},
	}
	err = k.Load(confmap.Provider(defaults, "."), nil)
	if err != nil {
		return nil, fmt.Errorf("can't load default config parameters: %w", err)
	}

	// Load command line and configs

	f := pflag.NewFlagSet("config", pflag.ContinueOnError)
	f.Usage = func() {
		fmt.Println(f.FlagUsages())
		os.Exit(0)
	}
	f.String("c", "", "Path to config file")
	err = f.Parse(os.Args[1:])
	if err != nil {
		return nil, fmt.Errorf("can't parse command line arguments: %w", err)
	}

	// Load the config files provided in the commandline.
	cFile, _ := f.GetString("c")
	switch {
	case strings.HasSuffix(cFile, "toml"):
		if err := k.Load(file.Provider(cFile), toml.Parser()); err != nil {
			return nil, fmt.Errorf("error loading file: %w", err)
		}
	case strings.HasSuffix(cFile, "yaml"):
		if err := k.Load(file.Provider(cFile), yaml.Parser()); err != nil {
			return nil, fmt.Errorf("error loading file: %w", err)
		}
	case strings.HasSuffix(cFile, "json"):
		if err := k.Load(file.Provider(cFile), json.Parser()); err != nil {
			return nil, fmt.Errorf("error loading file: %w", err)
		}
	}

	// Load ENV

	err = k.Load(env.Provider(ServiceName+"_", ".", func(s string) string {
		return strings.ReplaceAll(strings.ToLower(
			strings.TrimPrefix(s, ServiceName+"_")), "_", ".")
	}), nil)
	if err != nil {
		return nil, fmt.Errorf("can't load env variables: %w", err)
	}

	// Unmarshal configs to struct
	var config Config
	err = k.Unmarshal("", &config)
	if err != nil {
		return nil, fmt.Errorf("can't unmarshal config: %w", err)
	}

	return &config, nil
}
