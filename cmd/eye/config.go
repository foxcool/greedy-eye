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
	"go.uber.org/zap"
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
}

func getConfig() (*Config, error) {
	var err error
	k := koanf.New(".")

	// Default values

	defaults := map[string]interface{}{
		"sentry.tracesSampleRate": 1.0,
	}
	err = k.Load(confmap.Provider(defaults, "."), nil)
	if err != nil {
		return nil, fmt.Errorf("can't load default config parameters", zap.Error(err))
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
		return nil, fmt.Errorf("can't parse command line arguments", zap.Error(err))
	}

	// Load the config files provided in the commandline.
	cFile, _ := f.GetString("c")
	switch {
	case strings.HasSuffix(cFile, "toml"):
		if err := k.Load(file.Provider(cFile), toml.Parser()); err != nil {
			return nil, fmt.Errorf("error loading file", zap.Error(err))
		}
	case strings.HasSuffix(cFile, "yaml"):
		if err := k.Load(file.Provider(cFile), yaml.Parser()); err != nil {
			return nil, fmt.Errorf("error loading file", zap.Error(err))
		}
	case strings.HasSuffix(cFile, "json"):
		if err := k.Load(file.Provider(cFile), json.Parser()); err != nil {
			return nil, fmt.Errorf("error loading file", zap.Error(err))
		}
	}

	// Load ENV

	err = k.Load(env.Provider(ServiceName+"_", ".", func(s string) string {
		return strings.Replace(strings.ToLower(
			strings.TrimPrefix(s, ServiceName+"_")), "_", ".", -1)
	}), nil)
	if err != nil {
		return nil, fmt.Errorf("can't load env variables", zap.Error(err))
	}

	// Unmarshal configs to struct
	var config Config
	err = k.Unmarshal("", &config)
	if err != nil {
		return nil, fmt.Errorf("can't unmarshal config", zap.Error(err))
	}

	return &config, nil
}
