//go:build integration

package postgres

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/foxcool/greedy-eye/internal/entity"
	"github.com/foxcool/greedy-eye/internal/service/portfolio"
	storecrypto "github.com/foxcool/greedy-eye/internal/store/crypto"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// unreadableFixture is one owner whose accounts include rows this instance
// cannot read, created before the readable ones so they sort first by id and
// by staleness: the position where a row that takes a slot does the most harm.
type unreadableFixture struct {
	store      *PortfolioStore
	user       *entity.User
	sealedAway *entity.Account // sealed under a key this instance does not hold
	marketAway *entity.Account // the same, matching the capability listings
	corrupt    *entity.Account // data that does not parse
	readable   []*entity.Account
	market     *entity.Account
}

func newUnreadableFixture(t *testing.T) *unreadableFixture {
	t.Helper()
	pool := getTestPool(t)
	ctx := context.Background()
	user := createTestUser(t, NewUserStore(pool))

	encFor := func(b byte) *storecrypto.Encryptor {
		e, err := storecrypto.NewEncryptor(bytes.Repeat([]byte{b}, 32))
		require.NoError(t, err)
		return e
	}
	s := NewPortfolioStore(pool, WithEncryptor(encFor(7)))
	elsewhere := NewPortfolioStore(pool, WithEncryptor(encFor(9)))

	create := func(st *PortfolioStore, a *entity.Account) *entity.Account {
		a.UserID = user.ID
		got, err := st.CreateAccount(ctx, a)
		require.NoError(t, err)
		return got
	}
	f := &unreadableFixture{store: s, user: user}
	f.sealedAway = create(elsewhere, &entity.Account{
		Name: "sealed elsewhere", Type: entity.AccountTypeWallet,
		Data: map[string]string{"address": "0xsealedaway", "api_key": "top-secret"},
	})
	f.marketAway = create(elsewhere, &entity.Account{
		Name: "price key sealed elsewhere", Type: entity.AccountTypeService,
		Data:         map[string]string{"provider": "coingecko", "api_key": "top-secret"},
		Capabilities: []entity.AccountCapability{entity.CapabilityMarketData},
		SystemScopes: []entity.AccountCapability{entity.CapabilityMarketData},
	})
	f.corrupt = create(s, &entity.Account{
		Name: "corrupt data", Type: entity.AccountTypeWallet,
		Data: map[string]string{"address": "0xcorrupt"},
	})
	_, err := pool.Exec(ctx, `UPDATE accounts SET data = '{"address": 1}' WHERE id = $1`, f.corrupt.ID)
	require.NoError(t, err)

	for _, name := range []string{"wallet one", "wallet two"} {
		f.readable = append(f.readable, create(s, &entity.Account{
			Name: name, Type: entity.AccountTypeWallet, Data: map[string]string{"address": "0x" + name},
		}))
	}
	f.market = create(s, &entity.Account{
		Name: "price key", Type: entity.AccountTypeService,
		Data:         map[string]string{"provider": "coingecko", "api_key": "k"},
		Capabilities: []entity.AccountCapability{entity.CapabilityMarketData},
		SystemScopes: []entity.AccountCapability{entity.CapabilityMarketData},
	})
	f.readable = append(f.readable, f.market)
	return f
}

func accountIDs(as []*entity.Account) []string {
	ids := make([]string, 0, len(as))
	for _, a := range as {
		ids = append(ids, a.ID)
	}
	return ids
}

// TestUnreadableAccountRowDegradesPerRow: one row that cannot be read must not
// fail a query over many — the sweep, the credential resolver and the owner's
// list run on these for every user. Reading that row by id still fails.
func TestUnreadableAccountRowDegradesPerRow(t *testing.T) {
	f := newUnreadableFixture(t)
	ctx := context.Background()
	now := time.Now()

	t.Run("sweep selection takes the readable accounts", func(t *testing.T) {
		got, err := f.store.ListStaleSyncTargets(ctx, now.Add(-time.Hour), now, 100)
		require.NoError(t, err)
		assert.Equal(t, []string{f.readable[0].ID, f.readable[1].ID}, accountIDs(got))
	})

	t.Run("an unreadable row does not take a sweep slot", func(t *testing.T) {
		got, err := f.store.ListStaleSyncTargets(ctx, now.Add(-time.Hour), now, 2)
		require.NoError(t, err)
		assert.Equal(t, []string{f.readable[0].ID, f.readable[1].ID}, accountIDs(got))
	})

	t.Run("capability listings take the readable accounts", func(t *testing.T) {
		byCap, err := f.store.ListUserAccountsByCapability(ctx, f.user.ID, entity.CapabilityMarketData)
		require.NoError(t, err)
		assert.Equal(t, []string{f.market.ID}, accountIDs(byCap))

		byScope, err := f.store.ListSystemAccountsByCapability(ctx, entity.CapabilityMarketData)
		require.NoError(t, err)
		assert.Equal(t, []string{f.market.ID}, accountIDs(byScope))
	})

	t.Run("the owner's list returns the readable accounts", func(t *testing.T) {
		got, next, err := f.store.ListAccounts(ctx, portfolio.ListAccountsOpts{UserID: f.user.ID, PageSize: 100})
		require.NoError(t, err)
		assert.Empty(t, next)
		assert.Equal(t, accountIDs(f.readable), accountIDs(got))
	})

	t.Run("paging walks past unreadable rows", func(t *testing.T) {
		opts := portfolio.ListAccountsOpts{UserID: f.user.ID, PageSize: 1}
		var seen []*entity.Account
		for range 10 {
			page, next, err := f.store.ListAccounts(ctx, opts)
			require.NoError(t, err)
			require.NotEmpty(t, page, "a page must not come back empty while more accounts follow")
			seen = append(seen, page...)
			if next == "" {
				break
			}
			opts.PageToken = next
		}
		assert.Equal(t, accountIDs(f.readable), accountIDs(seen))
	})

	t.Run("reading the row by id still fails", func(t *testing.T) {
		_, err := f.store.GetAccount(ctx, f.sealedAway.ID)
		require.Error(t, err)
		_, err = f.store.GetAccount(ctx, f.corrupt.ID)
		require.Error(t, err)
	})
}

// TestUnreadableAccountsAreNamedToTheOwner: what the lists leave out is what
// account health reports, each with its class.
func TestUnreadableAccountsAreNamedToTheOwner(t *testing.T) {
	f := newUnreadableFixture(t)

	got, err := f.store.ListUnreadableAccounts(context.Background(), f.user.ID)
	require.NoError(t, err)
	failures := map[string]entity.AccountReadFailure{}
	for _, u := range got {
		assert.Equal(t, f.user.ID, u.UserID)
		failures[u.ID] = u.Failure
	}
	assert.Equal(t, map[string]entity.AccountReadFailure{
		f.sealedAway.ID: entity.ReadFailureUndecryptable,
		f.marketAway.ID: entity.ReadFailureUndecryptable,
		f.corrupt.ID:    entity.ReadFailureCorrupt,
	}, failures)

	other := createTestUser(t, NewUserStore(f.store.pool))
	none, err := f.store.ListUnreadableAccounts(context.Background(), other.ID)
	require.NoError(t, err)
	assert.Empty(t, none, "another owner's unreadable rows are not theirs to see")
}

// TestUnreadableAccountLogLineNamesIDAndClassOnly: the line a skipped row
// leaves names the account and the class of failure, and nothing stored in it.
func TestUnreadableAccountLogLineNamesIDAndClassOnly(t *testing.T) {
	f := newUnreadableFixture(t)
	var buf bytes.Buffer
	f.store.log = slog.New(slog.NewJSONHandler(&buf, nil))

	_, _, err := f.store.ListAccounts(context.Background(), portfolio.ListAccountsOpts{UserID: f.user.ID, PageSize: 100})
	require.NoError(t, err)

	lines := map[string]map[string]any{}
	for _, raw := range strings.Split(strings.TrimSpace(buf.String()), "\n") {
		var line map[string]any
		require.NoError(t, json.Unmarshal([]byte(raw), &line))
		id, _ := line["account_id"].(string)
		lines[id] = line
	}
	require.Len(t, lines, 3, buf.String())
	assert.Equal(t, string(entity.ReadFailureUndecryptable), lines[f.sealedAway.ID]["failure"])
	assert.Equal(t, string(entity.ReadFailureUndecryptable), lines[f.marketAway.ID]["failure"])
	assert.Equal(t, string(entity.ReadFailureCorrupt), lines[f.corrupt.ID]["failure"])
	for _, leak := range []string{"top-secret", "0xsealedaway", "0xcorrupt", "coingecko", "failed to", "cipher"} {
		assert.NotContains(t, buf.String(), leak)
	}
}
