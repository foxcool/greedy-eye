//go:build integration

package postgres

import (
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
)

const (
	// migrationsDir holds the ordered files an instance runs on deploy.
	migrationsDir = "migrations"
	// revisionsSchema keeps Atlas's bookkeeping out of the schema the
	// application owns, so a database and schema.hcl stay comparable.
	revisionsSchema = "atlas"
	// atlasSynced is what Atlas prints when two states hold the same objects.
	atlasSynced = "Schemas are synced"
)

// TestMigrationsMatchSchema is the reason schema.hcl can stay the authoring
// surface while migrations/ is the upgrade path: it replays the directory into
// an empty database and asks Atlas what still differs from schema.hcl.
//
// Without it the two drift silently, and the drift is invisible in exactly the
// place it matters — a developer's database is right because they applied
// schema.hcl to it, and someone else's instance is wrong because it ran the
// migrations. Every earlier version of this problem in this project (a fix
// nobody learned about, a measurement of one environment recorded as a fact
// about the system) had the same shape: two surfaces claiming one truth with
// nothing forcing them to agree.
func TestMigrationsMatchSchema(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	projectRoot, err := findProjectRoot()
	if err != nil {
		t.Fatalf("find project root: %v", err)
	}

	// Atlas needs a scratch database to materialise both states into. It drops
	// and recreates objects there, so it must be a database nothing else uses.
	devURL, terminate := startScratchPostgres(ctx, t)
	defer terminate()

	out, err := runAtlas(ctx, projectRoot,
		"schema", "diff",
		"--from", "file://"+filepath.Join(projectRoot, migrationsDir),
		"--to", "file://"+filepath.Join(projectRoot, "schema.hcl"),
		"--dev-url", devURL,
	)
	if err != nil {
		t.Fatalf("atlas schema diff failed: %v\noutput: %s", err, out)
	}

	if !strings.Contains(out, atlasSynced) {
		t.Fatalf("migrations/ and schema.hcl describe different databases.\n\n"+
			"Atlas would need this to turn the migrations into schema.hcl:\n%s\n"+
			"Generate the missing migration with: make migrate-diff name=<what_changed>", out)
	}
}

// TestMigrationsApplyOntoAnEmptyDatabase pins the property the deploy depends
// on: the directory runs, in order, against a database that holds nothing.
// The harness in NewTestDB exercises it on every integration run; this states
// it as a claim of its own, so a failure names the cause instead of taking
// every store test down with an unrelated-looking error.
func TestMigrationsApplyOntoAnEmptyDatabase(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	projectRoot, err := findProjectRoot()
	if err != nil {
		t.Fatalf("find project root: %v", err)
	}

	connStr, terminate := startScratchPostgres(ctx, t)
	defer terminate()

	if err := applyMigrations(ctx, projectRoot, connStr); err != nil {
		t.Fatalf("applying migrations to an empty database: %v", err)
	}

	// A second run must be a no-op: a deploy that restarts the migrate service
	// twice, or a playbook run twice, is not a special case.
	if err := applyMigrations(ctx, projectRoot, connStr); err != nil {
		t.Fatalf("re-applying migrations: %v", err)
	}
}

// startScratchPostgres returns a connection string to an empty database and a
// function that tears it down.
func startScratchPostgres(ctx context.Context, t *testing.T) (string, func()) {
	t.Helper()

	container, err := postgres.Run(ctx,
		"postgres:17-alpine",
		postgres.WithDatabase("atlas_dev"),
		postgres.WithUsername("test"),
		postgres.WithPassword("test"),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).
				WithStartupTimeout(30*time.Second),
		),
	)
	if err != nil {
		t.Fatalf("start scratch postgres: %v", err)
	}

	connStr, err := container.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		_ = container.Terminate(ctx)
		t.Fatalf("scratch postgres connection string: %v", err)
	}

	return connStr, func() { _ = container.Terminate(context.Background()) }
}

// runAtlas invokes the Atlas CLI and returns its combined output.
func runAtlas(ctx context.Context, projectRoot string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "atlas", args...)
	cmd.Dir = projectRoot

	output, err := cmd.CombinedOutput()
	if err != nil {
		return string(output), fmt.Errorf("atlas %s: %w", strings.Join(args, " "), err)
	}

	return string(output), nil
}
