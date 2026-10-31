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
