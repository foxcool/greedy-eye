package main

import (
	"log/slog"
	"maps"
	"slices"

	storecrypto "github.com/foxcool/greedy-eye/internal/store/crypto"
)

// featureSnapshot names, in one startup line, every feature this process turns
// on or off by an empty setting or a nil dependency rather than by a flag, so
// what is running can be read off the log instead of off the config. Only
// presence is reported, never a value: several of these are secrets.
func featureSnapshot(config *Config, encryptor *storecrypto.Encryptor) []slog.Attr {
	// Empty rather than nil, so the line reads [] instead of null.
	services := make([]string, 0, len(serviceTypes))
	for _, t := range serviceTypes {
		if slices.ContainsFunc(config.Services, func(s ServiceConfig) bool { return s.Type == t }) {
			services = append(services, t)
		}
	}
	remotes := slices.AppendSeq(make([]string, 0, len(config.Remotes)), maps.Keys(config.Remotes))
	slices.Sort(remotes)

	mode := "split"
	if len(services) == len(serviceTypes) && len(remotes) == 0 {
		mode = "monolith"
	}

	portfolioHere := slices.Contains(services, ServiceConfigTypePortfolio)
	sched := config.Scheduler

	return []slog.Attr{
		slog.String("mode", mode),
		slog.Any("services", services),
		slog.Any("remotes", remotes),
		slog.Bool("loopback", config.Loopback),
		// Account sync is an RPC of the portfolio service; its syncer sources
		// are wired unconditionally, so serving it is the only gate.
		slog.Bool("wallet_sync", portfolioHere),
		slog.Bool("encryption", encryptor != nil),
		slog.Bool("key_rotation", encryptor != nil && encryptor.StaleKeys() > 0),
		// Without it every sibling service call is refused.
		slog.Bool("service_token", config.Security.ServiceToken != ""),
		slog.Bool("sentry", config.Sentry.DSN != ""),
		slog.Bool("scheduler", sched.Enabled),
		slog.Bool("price_sweep", sched.Enabled && sched.PriceFetchCron != ""),
		slog.Bool("rescore", sched.Enabled && sched.RescoreCron != ""),
		// The balance sweep needs a local portfolio service to sweep.
		slog.Bool("balance_sweep", sched.Enabled && sched.BalanceSyncCron != "" && portfolioHere),
	}
}
