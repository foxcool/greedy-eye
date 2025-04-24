package storage

import (
	"fmt"
	"os"
	"testing"

	"github.com/foxcool/greedy-eye/internal/services/storage/ent/enttest"
	_ "github.com/lib/pq"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest"
)

var (
	dbURL string
)

// TestMain sets up and tears down the test environment for all test files in the package.
func TestMain(m *testing.M) {
	log := zap.Must(zap.NewDevelopment())
	defer func() {
		err := log.Sync()
		if err != nil {
			log.Error("syncing logger failed", zap.Error(err))
		}
	}()

	// Check if running in a Docker Compose environment.
	if os.Getenv("DOCKER_COMPOSE_TEST") != "true" {
		log.Info("Skipping tests in non-Docker Compose environment")
		os.Exit(0)
	}
	// DB is needed for testing.
	dbURL = os.Getenv("EYE_DB_URL")
	if dbURL == "" {
		log.Fatal("EYE_DB_URL environment variable not set")
	}

	// Run the tests
	code := m.Run()

	os.Exit(code)
}

// getTransactionedService is a helper function that returns a StorageService with a transaction client and a rollback function.
// service, rollback := getTransactionedService(t.Context(), "accounts", "assets", "holdings", "portfolios", "prices", "transactions", "users")
func getTransactionedService(t *testing.T, truncateTables ...string) *StorageService {
	log := zaptest.NewLogger(t)

	client := enttest.Open(t, "postgres", dbURL)

	tx, err := client.Tx(t.Context())
	if err != nil {
		t.Fatal("starting transaction failed", zap.Error(err))
	}

	// Cleanup tables. May be necessary for some tests.
	for _, table := range truncateTables {
		if _, err := tx.ExecContext(t.Context(), fmt.Sprintf("TRUNCATE TABLE %s CASCADE", table)); err != nil {
			t.Fatal("truncating table failed", zap.Error(err))
		}
	}

	// When the test is finished, rollback the transaction automatically.
	t.Cleanup(func() {
		if err := tx.Rollback(); err != nil {
			t.Fatal("rolling back transaction failed", zap.Error(err))
		}

		if err := client.Close(); err != nil {
			t.Fatal("closing database connection failed", zap.Error(err))
		}
	})

	// Return the StorageService with the transaction client and rollback function.
	return NewService(tx.Client(), log)
}
