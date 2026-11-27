package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// services is a list and cannot come from env, so a split process can only be
// configured through -c.
func TestGetConfigFileFlag(t *testing.T) {
	path := filepath.Join(t.TempDir(), "eye.yaml")
	require.NoError(t, os.WriteFile(path, []byte("services:\n  - type: portfolio\n"), 0o600))

	for _, args := range [][]string{{"-c", path}, {"--c", path}} {
		t.Run(args[0], func(t *testing.T) {
			orig := os.Args
			t.Cleanup(func() { os.Args = orig })
			os.Args = append([]string{"eye"}, args...)

			cfg, err := getConfig()
			require.NoError(t, err)
			require.Len(t, cfg.Services, 1)
			assert.Equal(t, ServiceConfigTypePortfolio, cfg.Services[0].Type)
		})
	}
}

// A chain's confirmations come from env as a per-chain key.
func TestGetConfigLedgerConfirmationsFromEnv(t *testing.T) {
	orig := os.Args
	t.Cleanup(func() { os.Args = orig })
	os.Args = []string{"eye"}
	t.Setenv("EYE_LEDGER_CONFIRMATIONS_POLYGON", "256")

	cfg, err := getConfig()
	require.NoError(t, err)
	assert.Equal(t, map[string]uint64{"polygon": 256}, cfg.Ledger.Confirmations)
}
