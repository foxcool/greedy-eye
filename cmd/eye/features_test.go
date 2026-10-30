package main

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"

	storecrypto "github.com/foxcool/greedy-eye/internal/store/crypto"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func snapshotMap(attrs []slog.Attr) map[string]any {
	m := make(map[string]any, len(attrs))
	for _, a := range attrs {
		m[a.Key] = a.Value.Any()
	}
	return m
}

func monolithConfig() *Config {
	c := &Config{}
	for _, t := range serviceTypes {
		c.Services = append(c.Services, ServiceConfig{Type: t})
	}
	c.Scheduler.Enabled = true
	c.Scheduler.PriceFetchCron = "0 * * * *"
	c.Scheduler.RescoreCron = "0 3 * * *"
	c.Scheduler.BalanceSyncCron = "30 * * * *"
	return c
}

func TestFeatureSnapshotMonolithDefaults(t *testing.T) {
	got := snapshotMap(featureSnapshot(monolithConfig(), nil))

	assert.Equal(t, map[string]any{
		"mode":          "monolith",
		"services":      serviceTypes,
		"remotes":       []string{},
		"loopback":      false,
		"wallet_sync":   true,
		"encryption":    false,
		"key_rotation":  false,
		"service_token": false,
		"sentry":        false,
		"scheduler":     true,
		"price_sweep":   true,
		"rescore":       true,
		"balance_sweep": true,
	}, got)
}

// A marketdata-only process: no sync, no balance sweep even with its cron set,
// and every gate driven by presence alone.
func TestFeatureSnapshotSplitProcess(t *testing.T) {
	c := &Config{Services: []ServiceConfig{{Type: ServiceConfigTypeMarketData}, {Type: "bogus"}}}
	c.Remotes = map[string]string{
		ServiceConfigTypeSettings:  "http://settings:8080",
		ServiceConfigTypePortfolio: "http://portfolio:8080",
	}
	c.Security.ServiceToken = "s3cret-token"
	c.Sentry.DSN = "https://key@sentry.example/1"
	c.Scheduler.Enabled = true
	c.Scheduler.BalanceSyncCron = "30 * * * *"

	key := bytes.Repeat([]byte{1}, 32)
	old := bytes.Repeat([]byte{2}, 32)
	enc, err := storecrypto.NewEncryptor(key, old)
	require.NoError(t, err)

	got := snapshotMap(featureSnapshot(c, enc))

	assert.Equal(t, "split", got["mode"])
	assert.Equal(t, []string{ServiceConfigTypeMarketData}, got["services"], "unknown types are not served")
	assert.Equal(t, []string{ServiceConfigTypePortfolio, ServiceConfigTypeSettings}, got["remotes"])
	assert.Equal(t, false, got["wallet_sync"])
	assert.Equal(t, true, got["encryption"])
	assert.Equal(t, true, got["key_rotation"])
	assert.Equal(t, true, got["service_token"])
	assert.Equal(t, true, got["sentry"])
	assert.Equal(t, false, got["price_sweep"])
	assert.Equal(t, false, got["rescore"])
	assert.Equal(t, false, got["balance_sweep"])
}

func TestFeatureSnapshotSchedulerOffDisablesJobs(t *testing.T) {
	c := monolithConfig()
	c.Scheduler.Enabled = false

	got := snapshotMap(featureSnapshot(c, nil))

	for _, k := range []string{"scheduler", "price_sweep", "rescore", "balance_sweep"} {
		assert.Equal(t, false, got[k], k)
	}
}

// The line reports presence only: no secret may reach it.
func TestFeatureSnapshotNeverLogsSecrets(t *testing.T) {
	c := monolithConfig()
	c.Security.ServiceToken = "s3cret-token"
	c.Security.MasterKey = "bWFzdGVyLWtleS1tYXN0ZXIta2V5LW1hc3Rlci1rZXk="
	c.Sentry.DSN = "https://dsnkey@sentry.example/1"
	c.DB.URL = "postgres://user:dbpass@db/eye"

	var buf bytes.Buffer
	slog.New(slog.NewJSONHandler(&buf, nil)).
		LogAttrs(t.Context(), slog.LevelInfo, "active features", featureSnapshot(c, nil)...)

	line := buf.String()
	for _, secret := range []string{"s3cret-token", "bWFzdGVy", "dsnkey", "dbpass"} {
		assert.False(t, strings.Contains(line, secret), "line leaks %q: %s", secret, line)
	}
}
