//go:build integration

package storage

import (
	"fmt"
	"log/slog"
	"os"
	"strings"
	"testing"

	"github.com/foxcool/greedy-eye/internal/services/storage/ent/enttest"
	_ "github.com/lib/pq"
)

var dbURL string

// TestMain sets up and tears down the test environment for all test files in the package.
func TestMain(m *testing.M) {
	log := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelDebug}))

	// Check if running in a Docker Compose environment.
	if os.Getenv("DOCKER_COMPOSE_TEST") != "true" {
		log.Info("Skipping tests in non-Docker Compose environment")
		os.Exit(0)
	}
	// DB is needed for testing.
	dbURL = os.Getenv("EYE_DB_URL")
	if dbURL == "" {
		log.Error("EYE_DB_URL environment variable not set")
		os.Exit(1)
	}

	// Run the tests
	code := m.Run()

	os.Exit(code)
}

// getTransactionedService is a helper function that returns a StorageService with a transaction client and a rollback function.
// service, rollback := getTransactionedService(t.Context(), "accounts", "assets", "holdings", "portfolios", "prices", "transactions", "users")
func getTransactionedService(t *testing.T, truncateTables ...string) *StorageService {
	log := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelDebug}))

	client := enttest.Open(t, "postgres", dbURL)

	tx, err := client.Tx(t.Context())
	if err != nil {
		t.Fatalf("starting transaction failed: %v", err)
	}

	// Cleanup tables. May be necessary for some tests.
	for _, table := range truncateTables {
		if _, err := tx.ExecContext(t.Context(), fmt.Sprintf("TRUNCATE TABLE %s CASCADE", table)); err != nil {
			t.Fatalf("truncating table failed: %v", err)
		}
	}

	// When the test is finished, rollback the transaction automatically.
	t.Cleanup(func() {
		if err := tx.Rollback(); err != nil {
			if strings.Contains(err.Error(), "bad connection") ||
				strings.Contains(err.Error(), "connection reset") ||
				strings.Contains(err.Error(), "broken pipe") {
				// don't fail on connection problems
				t.Logf("Note: connection issue during rollback: %v", err)
			} else {
				t.Fatalf("rolling back transaction failed: %v", err)
			}
		}

		if err := client.Close(); err != nil {
			t.Fatalf("closing database connection failed: %v", err)
		}
	})

	// Return the StorageService with the transaction client and rollback function.
	return NewService(tx.Client(), log)
}
