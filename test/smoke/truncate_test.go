//go:build smoke

package smoke_test

import (
	"context"
	"testing"
)

// resetDB truncates all tables to give each test a clean state.
// Uses a direct DB connection — the same database the running backend writes to.
func resetDB(t *testing.T) {
	t.Helper()
	ctx := context.Background()
	for _, table := range []string{
		"rule_executions",
		"rules",
		"transactions",
		"holdings",
		"prices",
		"portfolios",
		"accounts",
		"assets",
		"users",
	} {
		if _, err := dbPool.Exec(ctx, "TRUNCATE TABLE "+table+" CASCADE"); err != nil {
			t.Fatalf("truncate %s: %v", table, err)
		}
	}
}
