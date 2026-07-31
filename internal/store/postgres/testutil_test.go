//go:build integration

package postgres

import (
	"context"
	"log/slog"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

var testDB *TestDB

// TestMain sets up and tears down the test environment.
func TestMain(m *testing.M) {
	log := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	ctx := context.Background()

	var err error
	testDB, err = NewTestDB(ctx)
	if err != nil {
		log.Error("Failed to create test database", "error", err)
		os.Exit(1)
	}

	code := m.Run()

	testDB.Close(ctx)
	os.Exit(code)
}

// getTestPool returns the shared test connection pool after truncating tables.
// Tables are truncated in order respecting foreign key constraints.
func getTestPool(t *testing.T) *pgxpool.Pool {
	t.Helper()

	// Truncate in order: child tables first (those with foreign keys to others).
	testDB.MustTruncate(t,
		"rule_executions",
		"rules",
		"transactions",
		"holdings",
		"prices",
		"price_fetch_attempts",
		"provider_usage",
		"portfolios",
		"accounts",
		"assets",
		"users",
	)

	return testDB.Pool
}
