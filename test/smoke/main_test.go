//go:build smoke

package smoke_test

import (
	"context"
	"log/slog"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

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

	code := m.Run()
	dbPool.Close()
	os.Exit(code)
}

// backendURL returns the backend URL, defaulting to the compose service name.
func backendURL() string {
	if u := os.Getenv("SMOKE_BACKEND_URL"); u != "" {
		return u
	}
	return "http://eye-dev:8080"
}
