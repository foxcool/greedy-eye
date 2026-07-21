// Package scheduler runs background cron jobs inside the eye binary:
// periodic automation rules (by RuleSchedule.CronExpression) and external
// price fetching. Single-instance by design — on multi-instance deployments
// enable the scheduler on exactly one node via config.
package scheduler

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"connectrpc.com/connect"
	"github.com/robfig/cron/v3"

	apiv1 "github.com/foxcool/greedy-eye/api/v1"
	"github.com/foxcool/greedy-eye/internal/entity"
	"github.com/foxcool/greedy-eye/internal/service/automation"
	"github.com/foxcool/greedy-eye/internal/service/marketdata"
)

const (
	// ruleReloadSpec controls how often active rules are re-read from the
	// store. Cheap full reload instead of CRUD hooks: rule mutations take
	// effect within a minute.
	ruleReloadSpec = "@every 1m"
	// jobTimeout bounds a single job fire (rule execution or price fetch).
	jobTimeout = 5 * time.Minute
	// rulePageSize is the page size used when listing active rules.
	rulePageSize = 200
)

// RuleStore lists rules for scheduling. Satisfied by automation.Store.
type RuleStore interface {
	ListRules(ctx context.Context, opts automation.ListRulesOpts) ([]*entity.Rule, string, error)
}

// RuleExecutor executes a rule. Satisfied by *automation.Handler.
type RuleExecutor interface {
	ExecuteRule(ctx context.Context, req *connect.Request[apiv1.ExecuteRuleRequest]) (*connect.Response[apiv1.ExecuteRuleResponse], error)
}

// PriceFetcher fetches prices from external providers. Satisfied by *marketdata.Handler.
type PriceFetcher interface {
	FetchExternalPrices(ctx context.Context, req *connect.Request[apiv1.FetchExternalPricesRequest]) (*connect.Response[apiv1.FetchExternalPricesResponse], error)
}

// AssetRescorer rescores the catalogue for scam-filtering identity verdicts.
// Satisfied by *marketdata.Handler; the concrete report is logged by the
// rescorer itself, so the scheduler only needs the error.
type AssetRescorer interface {
	RescoreAssets(ctx context.Context) (marketdata.RescoreReport, error)
}

// Config holds scheduler settings.
type Config struct {
	// PriceFetchCron is the cron spec for the external price fetch job.
	// Empty disables the job.
	PriceFetchCron string
	// RescoreCron is the cron spec for the catalogue identity-rescore job.
	// Empty disables the job.
	RescoreCron string
}

// ruleEntry tracks a scheduled rule so reloads can diff instead of rebuild.
type ruleEntry struct {
	id   cron.EntryID
	spec string
}

// Scheduler owns the cron instance and its jobs.
type Scheduler struct {
	cfg    Config
	cron   *cron.Cron
	rules    RuleStore
	exec     RuleExecutor
	prices   PriceFetcher
	rescorer AssetRescorer
	log      *slog.Logger

	mu      sync.Mutex
	entries map[string]ruleEntry // rule ID -> scheduled entry
}

// New validates the config and builds a stopped scheduler.
func New(cfg Config, rules RuleStore, exec RuleExecutor, prices PriceFetcher, rescorer AssetRescorer, log *slog.Logger) (*Scheduler, error) {
	if cfg.PriceFetchCron != "" {
		if _, err := cron.ParseStandard(cfg.PriceFetchCron); err != nil {
			return nil, fmt.Errorf("invalid priceFetchCron %q: %w", cfg.PriceFetchCron, err)
		}
	}
	if cfg.RescoreCron != "" {
		if _, err := cron.ParseStandard(cfg.RescoreCron); err != nil {
			return nil, fmt.Errorf("invalid rescoreCron %q: %w", cfg.RescoreCron, err)
		}
	}
	cl := &cronLogger{log: log}
	return &Scheduler{
		cfg:      cfg,
		cron:     cron.New(cron.WithChain(cron.Recover(cl), cron.SkipIfStillRunning(cl))),
		rules:    rules,
		exec:     exec,
		prices:   prices,
		rescorer: rescorer,
		log:      log,
		entries:  make(map[string]ruleEntry),
	}, nil
}

// Start registers the static jobs, performs the initial rule load and starts
// the cron loop. Missed fires during downtime are skipped, never caught up:
// a stale trade plan is worse than no trade.
func (s *Scheduler) Start() error {
	if s.cfg.PriceFetchCron != "" {
		if _, err := s.cron.AddFunc(s.cfg.PriceFetchCron, s.fetchPrices); err != nil {
			return fmt.Errorf("register price fetch job: %w", err)
		}
	}
	if s.cfg.RescoreCron != "" && s.rescorer != nil {
		if _, err := s.cron.AddFunc(s.cfg.RescoreCron, s.rescoreAssets); err != nil {
			return fmt.Errorf("register asset rescore job: %w", err)
		}
	}
	if _, err := s.cron.AddFunc(ruleReloadSpec, func() { s.reloadRules(context.Background()) }); err != nil {
		return fmt.Errorf("register rule reload job: %w", err)
	}
	s.reloadRules(context.Background())
	s.cron.Start()
	return nil
}

// Stop halts new fires and waits for running jobs until ctx expires.
func (s *Scheduler) Stop(ctx context.Context) {
	stopped := s.cron.Stop()
	select {
	case <-stopped.Done():
	case <-ctx.Done():
		s.log.Warn("scheduler stopped with jobs still running")
	}
}
