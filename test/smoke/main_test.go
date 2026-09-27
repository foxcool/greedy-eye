//go:build smoke

package smoke_test

import (
	"context"
	"log/slog"
	"os"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

// smokeDBSuffix marks a database as disposable.
const smokeDBSuffix = "_smoke"

var (
	serverURL string
	dbPool    *pgxpool.Pool
)

func TestMain(m *testing.M) {
	log := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	ctx := context.Background()

	serverURL = backendURL()
	log.Info("smoke tests targeting backend", "url", serverURL)

	var err error
	dbPool, err = pgxpool.New(ctx, os.Getenv("EYE_DB_URL"))
	if err != nil {
		log.Error("failed to connect to database", "error", err)
		os.Exit(1)
	}

	// resetDB truncates every table, so pointing the suite at a database that
	// holds anything worth keeping destroys it. The name is the only check that
	// holds whatever the compose file or the environment says.
	var dbName string
	if err := dbPool.QueryRow(ctx, "SELECT current_database()").Scan(&dbName); err != nil {
		log.Error("failed to read database name", "error", err)
		os.Exit(1)
	}
	if !strings.HasSuffix(dbName, smokeDBSuffix) {
		log.Error("refusing to run: smoke tests truncate every table",
			"database", dbName, "required_suffix", smokeDBSuffix)
		os.Exit(1)
	}

	code := m.Run()
	dbPool.Close()
	os.Exit(code)
}

// backendURL returns the backend URL, defaulting to the compose service name.
func backendURL() string {
	if u := os.Getenv("SMOKE_BACKEND_URL"); u != "" {
		return u
	}
	return "http://eye-smoke:8080"
}
