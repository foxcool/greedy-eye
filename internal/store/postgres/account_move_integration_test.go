//go:build integration

package postgres

import (
	"context"
	"fmt"
	"log/slog"
	"testing"
	"time"

	"connectrpc.com/connect"
	apiv1 "github.com/foxcool/greedy-eye/api/v1"
	"github.com/foxcool/greedy-eye/internal/entity"
	"github.com/foxcool/greedy-eye/internal/middleware"
	"github.com/foxcool/greedy-eye/internal/service/marketdata"
	"github.com/foxcool/greedy-eye/internal/service/portfolio"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/fieldmaskpb"
)

// moveFixture is one account in portfolio a holding three positions in a cash
// asset, so a valuation in that asset needs no price row:
//   - stamped: portfolio_id = a, the way sync and import write it (100.00)
//   - inherited: portfolio_id NULL, following the account (50.00)
//   - placed: portfolio_id = c, put there by the owner (7.00)
type moveFixture struct {
	store                      *PortfolioStore
	user                       *entity.User
	a, b, c                    *entity.Portfolio
	account                    *entity.Account
	cash                       *entity.Asset
	stamped, inherited, placed *entity.Holding
}

func newMoveFixture(t *testing.T) *moveFixture {
	t.Helper()
	pool := getTestPool(t)
	ctx := context.Background()
	f := &moveFixture{store: NewPortfolioStore(pool)}
	f.user = createTestUser(t, NewUserStore(pool))
	f.cash = createTestAsset(t, NewMarketDataStore(pool), "Move Cash")

	newPortfolio := func(name string) *entity.Portfolio {
		p, err := f.store.CreatePortfolio(ctx, &entity.Portfolio{UserID: f.user.ID, Name: name})
		require.NoError(t, err)
		return p
	}
	f.a, f.b, f.c = newPortfolio("A"), newPortfolio("B"), newPortfolio("C")

	var err error
	f.account, err = f.store.CreateAccount(ctx, &entity.Account{
		UserID: f.user.ID, Name: "mover", Type: entity.AccountTypeManual, PortfolioID: f.a.ID,
	})
	require.NoError(t, err)

	newHolding := func(portfolioID string, cents int64) *entity.Holding {
		h, err := f.store.CreateHolding(ctx, &entity.Holding{
			AssetID: f.cash.ID, AccountID: f.account.ID, PortfolioID: portfolioID,
			Amount: decimal.NewFromInt(cents), Decimals: 2, Source: entity.SourceManual,
		})
		require.NoError(t, err)
		return h
	}
	f.stamped = newHolding(f.a.ID, 10000)
	f.inherited = newHolding("", 5000)
	f.placed = newHolding(f.c.ID, 700)
	return f
}

func (f *moveFixture) holding(t *testing.T, id string) *entity.Holding {
	t.Helper()
	h, err := f.store.GetHolding(context.Background(), id)
	require.NoError(t, err)
	return h
}

// TestUpdateAccountMoveCarriesItsHoldings drives the move and the valuation
// through the real handlers: what the owner sees is the total, not the column.
func TestUpdateAccountMoveCarriesItsHoldings(t *testing.T) {
	f := newMoveFixture(t)
	log := slog.New(slog.DiscardHandler)
	md := marketdata.NewHandler(NewMarketDataStore(testDB.Pool), log)
	h := portfolio.NewHandler(f.store, log).WithMarketDataClient(md)
	ctx := middleware.ContextWithUser(context.Background(), f.user)

	value := func(p *entity.Portfolio) decimal.Decimal {
		t.Helper()
		resp, err := h.CalculatePortfolioValue(ctx, connect.NewRequest(&apiv1.CalculatePortfolioValueRequest{
			PortfolioId: p.ID, QuoteAssetId: f.cash.ID,
		}))
		require.NoError(t, err)
		total, err := decimal.NewFromString(resp.Msg.TotalValueAmount)
		require.NoError(t, err)
		return total.Shift(-int32(resp.Msg.Decimals))
	}
	holdingIDs := func(p *entity.Portfolio) []string {
		t.Helper()
		resp, err := h.ListHoldings(ctx, connect.NewRequest(&apiv1.ListHoldingsRequest{PortfolioId: &p.ID}))
		require.NoError(t, err)
		var ids []string
		for _, x := range resp.Msg.Holdings {
			ids = append(ids, x.Id)
		}
		return ids
	}

	require.Equal(t, "150", value(f.a).String())
	require.Equal(t, "0", value(f.b).String())

	_, err := h.UpdateAccount(ctx, connect.NewRequest(&apiv1.UpdateAccountRequest{
		Account:    &apiv1.Account{Id: f.account.ID, PortfolioId: &f.b.ID},
		UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"portfolio_id"}},
	}))
	require.NoError(t, err)

	assert.Equal(t, "0", value(f.a).String(), "the old portfolio no longer counts the account")
	assert.Equal(t, "150", value(f.b).String(), "the new portfolio counts all of it, not only the inherited part")
	assert.Empty(t, holdingIDs(f.a))
	assert.ElementsMatch(t, []string{f.stamped.ID, f.inherited.ID}, holdingIDs(f.b))

	assert.Equal(t, "7", value(f.c).String(), "a holding the owner placed elsewhere stays there")
	assert.Equal(t, []string{f.placed.ID}, holdingIDs(f.c))
	assert.Equal(t, f.c.ID, f.holding(t, f.placed.ID).PortfolioID)
	assert.Empty(t, f.holding(t, f.inherited.ID).PortfolioID, "an inherited holding keeps inheriting")
}

func TestUpdateAccountMoveOutOfAnyPortfolio(t *testing.T) {
	f := newMoveFixture(t)
	ctx := context.Background()

	_, err := f.store.UpdateAccount(ctx, &entity.Account{ID: f.account.ID}, []string{"portfolio_id"})
	require.NoError(t, err)

	assert.Empty(t, f.holding(t, f.stamped.ID).PortfolioID, "follows the account out instead of staying behind in a")
	assert.Equal(t, f.c.ID, f.holding(t, f.placed.ID).PortfolioID)

	// And back in: nothing is stamped with the old (absent) portfolio, so
	// nothing is rewritten — every NULL row already follows the account.
	stamped := f.holding(t, f.stamped.ID)
	_, err = f.store.UpdateAccount(ctx, &entity.Account{ID: f.account.ID, PortfolioID: f.b.ID}, []string{"portfolio_id"})
	require.NoError(t, err)
	again := f.holding(t, f.stamped.ID)
	assert.Empty(t, again.PortfolioID)
	assert.True(t, stamped.UpdatedAt.Equal(again.UpdatedAt), "not rewritten")
	assert.Empty(t, f.holding(t, f.inherited.ID).PortfolioID)
}

// failWritesOf installs a trigger that refuses any UPDATE of table rows whose
// column equals id, standing in for a write that fails mid-move. atCommit
// defers the refusal to COMMIT, after every statement of the move succeeded.
func failWritesOf(t *testing.T, table, column, id string, atCommit bool) {
	t.Helper()
	ctx := context.Background()
	name := "fail_" + table + "_" + uuid.NewString()[:8]
	when := "BEFORE UPDATE ON " + table
	if atCommit {
		when = "AFTER UPDATE ON " + table + " DEFERRABLE INITIALLY DEFERRED"
	}
	constraint := ""
	if atCommit {
		constraint = "CONSTRAINT"
	}
	_, err := testDB.Pool.Exec(ctx, fmt.Sprintf(`
		CREATE FUNCTION %[1]s() RETURNS trigger LANGUAGE plpgsql AS $$
		BEGIN RAISE EXCEPTION 'forced failure'; END $$;
		CREATE %[5]s TRIGGER %[1]s %[2]s FOR EACH ROW
		WHEN (OLD.%[3]s = '%[4]s') EXECUTE FUNCTION %[1]s();`, name, when, column, id, constraint))
	require.NoError(t, err)
	t.Cleanup(func() {
		_, err := testDB.Pool.Exec(ctx, fmt.Sprintf(
			`DROP TRIGGER %[1]s ON %[2]s; DROP FUNCTION %[1]s();`, name, table))
		require.NoError(t, err)
	})
}

func TestUpdateAccountMoveIsAtomic(t *testing.T) {
	for _, tc := range []struct {
		name, table, column string
		atCommit            bool
	}{
		{"holdings write fails", "holdings", "account_id", false},
		{"account write fails", "accounts", "id", false},
		{"commit fails after both writes", "accounts", "id", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newMoveFixture(t)
			failWritesOf(t, tc.table, tc.column, f.account.ID, tc.atCommit)

			_, err := f.store.UpdateAccount(context.Background(),
				&entity.Account{ID: f.account.ID, PortfolioID: f.b.ID}, []string{"portfolio_id"})
			require.Error(t, err)

			got, err := f.store.GetAccount(context.Background(), f.account.ID)
			require.NoError(t, err)
			assert.Equal(t, f.a.ID, got.PortfolioID, "the account stays where it was")
			assert.Equal(t, f.a.ID, f.holding(t, f.stamped.ID).PortfolioID, "and so do its holdings")
		})
	}
}

func TestUpdateAccountWithoutMoveTouchesNoHoldings(t *testing.T) {
	for _, tc := range []struct {
		name   string
		fields []string
	}{
		{"same portfolio", []string{"portfolio_id"}},
		{"portfolio_id not in mask", []string{"name"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newMoveFixture(t)
			before := map[string]time.Time{}
			for _, h := range []*entity.Holding{f.stamped, f.inherited, f.placed} {
				before[h.ID] = f.holding(t, h.ID).UpdatedAt
			}

			_, err := f.store.UpdateAccount(context.Background(),
				&entity.Account{ID: f.account.ID, Name: "renamed", PortfolioID: f.a.ID}, tc.fields)
			require.NoError(t, err)

			for id, at := range before {
				got := f.holding(t, id)
				assert.True(t, at.Equal(got.UpdatedAt), "holding %s was rewritten", id)
			}
			assert.Equal(t, f.a.ID, f.holding(t, f.stamped.ID).PortfolioID)
		})
	}
}
