//go:build integration

package testutil

import (
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
)

// TestDB provides an ephemeral PostgreSQL database for integration tests.
type TestDB struct {
	Pool      *pgxpool.Pool
	container *postgres.PostgresContainer
	connStr   string
}

// NewTestDB creates a new PostgreSQL container and applies the schema using Atlas.
func NewTestDB(ctx context.Context) (*TestDB, error) {
	container, err := postgres.Run(ctx,
		"postgres:17-alpine",
		postgres.WithDatabase("greedy_eye_test"),
		postgres.WithUsername("test"),
		postgres.WithPassword("test"),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).
				WithStartupTimeout(30*time.Second),
		),
	)
	if err != nil {
		return nil, fmt.Errorf("start postgres container: %w", err)
	}

	connStr, err := container.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		container.Terminate(ctx)
		return nil, fmt.Errorf("get connection string: %w", err)
	}

	// Find the project root (where schema.hcl is located).
	projectRoot, err := findProjectRoot()
	if err != nil {
		container.Terminate(ctx)
		return nil, fmt.Errorf("find project root: %w", err)
	}

	// Apply schema using Atlas CLI.
	if err := applySchema(ctx, projectRoot, connStr); err != nil {
		container.Terminate(ctx)
		return nil, fmt.Errorf("apply schema: %w", err)
	}

	pool, err := pgxpool.New(ctx, connStr)
	if err != nil {
		container.Terminate(ctx)
		return nil, fmt.Errorf("create connection pool: %w", err)
	}

	return &TestDB{
		Pool:      pool,
		container: container,
		connStr:   connStr,
	}, nil
}

// Close terminates the database connection and container.
func (db *TestDB) Close(ctx context.Context) {
	if db.Pool != nil {
		db.Pool.Close()
	}
	if db.container != nil {
		db.container.Terminate(ctx)
	}
}

// Truncate removes all data from the specified tables.
// Tables should be provided in order that respects foreign key constraints
// (child tables first).
func (db *TestDB) Truncate(ctx context.Context, tables ...string) error {
	for _, table := range tables {
		if _, err := db.Pool.Exec(ctx, "TRUNCATE TABLE "+table+" CASCADE"); err != nil {
			return fmt.Errorf("truncate table %s: %w", table, err)
		}
	}
	return nil
}

// MustTruncate is like Truncate but fails the test on error.
func (db *TestDB) MustTruncate(t *testing.T, tables ...string) {
	t.Helper()
	if err := db.Truncate(context.Background(), tables...); err != nil {
		t.Fatalf("truncate tables: %v", err)
	}
}

// findProjectRoot locates the project root by looking for schema.hcl.
func findProjectRoot() (string, error) {
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		return "", fmt.Errorf("unable to get caller information")
	}

	// Start from the directory containing this file and walk up.
	dir := filepath.Dir(filename)
	for {
		schemaPath := filepath.Join(dir, "schema.hcl")
		if fileExists(schemaPath) {
			return dir, nil
		}

		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("schema.hcl not found in any parent directory")
		}
		dir = parent
	}
}

// fileExists checks if a file exists.
func fileExists(path string) bool {
	cmd := exec.Command("test", "-f", path)
	return cmd.Run() == nil
}

// applySchema applies the database schema using Atlas CLI.
func applySchema(ctx context.Context, projectRoot, connStr string) error {
	schemaPath := filepath.Join(projectRoot, "schema.hcl")

	cmd := exec.CommandContext(ctx, "atlas", "schema", "apply",
		"--url", connStr,
		"--to", "file://"+schemaPath,
		"--auto-approve",
	)
	cmd.Dir = projectRoot

	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("atlas schema apply failed: %w\noutput: %s", err, string(output))
	}

	return nil
}
