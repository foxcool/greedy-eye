//go:build integration

package postgres

import (
	"context"
	"log/slog"
	"os"
	"testing"

	"github.com/foxcool/greedy-eye/internal/testutil"
	"github.com/jackc/pgx/v5/pgxpool"
)

var testDB *testutil.TestDB

// TestMain sets up and tears down the test environment.
func TestMain(m *testing.M) {
	log := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	ctx := context.Background()

	var err error
	testDB, err = testutil.NewTestDB(ctx)
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
		"transactions",
		"holdings",
		"prices",
		"portfolios",
		"accounts",
		"assets",
		"users",
	)

	return testDB.Pool
}

// withTx runs a function within a transaction that is rolled back after completion.
// This ensures test isolation without needing to truncate tables.
func withTx(t *testing.T, pool *pgxpool.Pool, fn func(ctx context.Context)) {
	t.Helper()

	ctx := context.Background()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin transaction: %v", err)
	}

	defer func() {
		if err := tx.Rollback(ctx); err != nil {
			// Ignore rollback errors after commit or context cancellation
			t.Logf("rollback: %v", err)
		}
	}()

	fn(ctx)
}
