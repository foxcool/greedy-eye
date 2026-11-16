//go:build integration

package postgres

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/foxcool/greedy-eye/internal/entity"
	"github.com/foxcool/greedy-eye/internal/entity/entitytest"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// exclusionCheckMigration is the migration that repairs stale exclusion authors
// and then forbids them; the repair test stops the directory just before it.
const exclusionCheckMigration = "_holdings_exclusion_author_check.sql"

// checkViolation is the SQLSTATE PostgreSQL returns for a failed CHECK.
const checkViolation = "23514"

// createExcludedHolding writes a holding a person excluded, the one state in
// which an author is allowed, so a raw statement can then try to break the pair.
func createExcludedHolding(ctx context.Context, t *testing.T, pool *pgxpool.Pool) *entity.Holding {
	t.Helper()
	users := NewUserStore(pool)
	s := NewPortfolioStore(pool)
	assets := NewMarketDataStore(pool)

	user := createTestUser(t, users)
	account, err := s.CreateAccount(ctx, &entity.Account{UserID: user.ID, Name: "exclusion pair", Type: entity.AccountTypeWallet})
	require.NoError(t, err)
	asset, err := assets.CreateAsset(ctx, &entity.Asset{
		Symbol: "EXCL" + strconv.FormatInt(time.Now().UnixNano(), 36), Name: "Exclusion pair", Type: entity.AssetTypeCryptocurrency,
	})
	require.NoError(t, err)

	h, err := s.CreateHolding(ctx, entitytest.Holding(entity.Holding{
		AssetID:   asset.ID,
		AccountID: account.ID,
		Amount:    decimal.RequireFromString("1"),
		Decimals:  8,
	}, entitytest.HoldingState{
		Source:         entity.SourceSync,
		Excluded:       true,
		ExcludedSource: entity.ExcludedByUser,
	}))
	require.NoError(t, err)
	return h
}

// The entity keeps the author empty while the flag is down (SetExcluded); the
// database must refuse the same pair from any writer that bypasses the entity.
func TestHoldingExclusionAuthorRequiresExclusion(t *testing.T) {
	pool := getTestPool(t)
	ctx := context.Background()
	h := createExcludedHolding(ctx, t, pool)

	_, err := pool.Exec(ctx, "UPDATE holdings SET excluded = false WHERE id = $1", h.ID)
	var pgErr *pgconn.PgError
	require.True(t, errors.As(err, &pgErr), "an included row with an author must be refused, got %v", err)
	assert.Equal(t, checkViolation, pgErr.Code)

	// Lowering both together is the legal release.
	_, err = pool.Exec(ctx, "UPDATE holdings SET excluded = false, excluded_source = '' WHERE id = $1", h.ID)
	require.NoError(t, err)
}

// The constraint lands on instances that already hold rows written by the old
// re-include path (excluded = false, excluded_source = 'user'), so the same
// migration must clear them before it adds the check, or the apply fails.
func TestExclusionAuthorMigrationRepairsOldRows(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	projectRoot, err := findProjectRoot()
	require.NoError(t, err)

	entries, err := os.ReadDir(filepath.Join(projectRoot, migrationsDir))
	require.NoError(t, err)
	var files []string
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".sql") {
			files = append(files, e.Name())
		}
	}
	slices.Sort(files)
	before := slices.IndexFunc(files, func(name string) bool { return strings.HasSuffix(name, exclusionCheckMigration) })
	require.GreaterOrEqual(t, before, 0, "migration %s not found", exclusionCheckMigration)

	connStr, terminate := startScratchPostgres(ctx, t)
	defer terminate()

	_, err = runAtlas(ctx, projectRoot, "migrate", "apply",
		"--url", connStr,
		"--dir", "file://"+filepath.Join(projectRoot, migrationsDir),
		"--revisions-schema", revisionsSchema,
		strconv.Itoa(before),
	)
	require.NoError(t, err)

	pool, err := pgxpool.New(ctx, connStr)
	require.NoError(t, err)
	defer pool.Close()

	h := createExcludedHolding(ctx, t, pool)
	_, err = pool.Exec(ctx, "UPDATE holdings SET excluded = false WHERE id = $1", h.ID)
	require.NoError(t, err, "before the check, the old re-include path could write this pair")

	require.NoError(t, applyMigrations(ctx, projectRoot, connStr))

	var excluded bool
	var source string
	require.NoError(t, pool.QueryRow(ctx, "SELECT excluded, excluded_source FROM holdings WHERE id = $1", h.ID).Scan(&excluded, &source))
	assert.False(t, excluded, "the repair must not re-exclude a row a person included")
	assert.Empty(t, source, "an included row keeps no author")
}
