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
	}
	GRPC struct {
		Port int `koanf:"port"`
	}
	Services []ServiceConfig `koanf:"services"`
}

// ServiceConfig is a config for a service
type ServiceConfig struct {
	Type       string            `koanf:"type"`
	Parameters map[string]string `koanf:"parameters"`
}

const (
	ServiceConfigTypeAsset     = "asset"
	ServiceConfigTypeUser      = "user"
	ServiceConfigTypePrice     = "price"
	ServiceConfigTypePortfolio = "portfolio"
	ServiceConfigTypeTrade     = "trade"
)

func getConfig() (*Config, error) {
	var err error
	k := koanf.New(".")

	// Default values

	defaults := map[string]interface{}{
		"sentry.tracesSampleRate": 1.0,
		"grpc.port":               50051,
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
