//go:build integration

package postgres

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	apiv1 "github.com/foxcool/greedy-eye/api/v1"
	"github.com/foxcool/greedy-eye/internal/entity"
	"github.com/foxcool/greedy-eye/internal/entity/entitytest"
	"github.com/foxcool/greedy-eye/internal/service/marketdata"
	"github.com/foxcool/greedy-eye/internal/store"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// mergeTables are the columns naming an asset by foreign key;
// TestMergeAssets_CoversEveryAssetForeignKey keeps the list honest.
var mergeTables = map[string][]string{
	"holdings":                {"asset_id"},
	"ledger_legs":             {"asset_id"},
	"ledger_events":           {"payer_asset_id"},
	"prices":                  {"asset_id", "base_asset_id"},
	"asset_external_refs":     {"asset_id"},
	"asset_risk_flags":        {"asset_id"},
	"price_fetch_attempts":    {"asset_id"},
	"price_unasked_deferrals": {"asset_id"},
}

// rowsNaming counts, per column, the rows naming the asset, plus the rules
// whose configuration names it.
func rowsNaming(t *testing.T, assetID string) map[string]int {
	t.Helper()
	ctx := context.Background()
	out := map[string]int{}
	count := func(key, sql string) {
		var n int
		require.NoError(t, testDB.Pool.QueryRow(ctx, sql, assetID).Scan(&n))
		out[key] = n
	}
	for table, cols := range mergeTables {
		for _, col := range cols {
			count(table+"."+col, "SELECT count(*) FROM "+table+" WHERE "+col+" = $1")
		}
	}
	var inRules int
	require.NoError(t, testDB.Pool.QueryRow(ctx,
		"SELECT count(*) FROM rules WHERE configuration::text ~* $1", configIDPattern(assetID)).Scan(&inRules))
	out["rules.configuration"] = inRules
	return out
}

func mergePrice(t *testing.T, s *MarketDataStore, assetID, baseID, source string, at time.Time) {
	t.Helper()
	_, err := s.CreatePrice(context.Background(), &entity.StoredPrice{
		SourceID: source, AssetID: assetID, BaseAssetID: baseID,
		Interval: "latest", Decimals: 2, Last: decimal.NewFromInt(100), Timestamp: at,
	})
	require.NoError(t, err)
}

func mergeHolding(t *testing.T, f *ledgerFixture, accountID, assetID string) string {
	t.Helper()
	h, err := f.store.CreateHolding(f.ctx, entitytest.Holding(entity.Holding{
		AssetID: assetID, AccountID: accountID, Decimals: 8,
	}, entitytest.HoldingState{Source: entity.SourceManual}))
	require.NoError(t, err)
	return h.ID
}

// mergeRule stores a DCA rule buying assetID with quoteID.
func mergeRule(t *testing.T, f *ledgerFixture, assetID, quoteID string) string {
	t.Helper()
	cfg, err := json.Marshal(map[string]string{
		"account_id": f.account.ID, "asset_id": assetID, "quote_asset_id": quoteID, "quote_amount": "10",
	})
	require.NoError(t, err)
	portfolioID := insertTestPortfolio(t, testDB.Pool, f.user.ID)
	rule, err := NewAutomationStore(testDB.Pool).CreateRule(f.ctx, &entity.Rule{
		Name: "merge dca", RuleType: "dca", PortfolioID: portfolioID, UserID: f.user.ID,
		Status: entity.RuleStatusActive, Configuration: cfg,
	})
	require.NoError(t, err)
	return rule.ID
}

func ruleConfig(t *testing.T, id string) map[string]string {
	t.Helper()
	var raw []byte
	require.NoError(t, testDB.Pool.QueryRow(context.Background(),
		"SELECT configuration FROM rules WHERE id = $1", id).Scan(&raw))
	var cfg map[string]string
	require.NoError(t, json.Unmarshal(raw, &cfg))
	return cfg
}

func mergeOpts(src, tgt *entity.Asset, dryRun bool) marketdata.MergeAssetsOpts {
	return marketdata.MergeAssetsOpts{
		SourceID: src.ID, TargetID: tgt.ID,
		ExpectedSourceSymbol: src.Symbol, ExpectedTargetSymbol: tgt.Symbol,
		DryRun: dryRun,
	}
}

// TestMergeAssets_CoversEveryAssetForeignKey: a new table referencing assets
// must be handled by the merge (and listed above), or the delete of the
// source fails and the "nothing names the source" assertion stops proving it.
func TestMergeAssets_CoversEveryAssetForeignKey(t *testing.T) {
	rows, err := testDB.Pool.Query(context.Background(), `
		SELECT c.conrelid::regclass::text, a.attname
		FROM pg_constraint c
		JOIN pg_attribute a ON a.attrelid = c.conrelid AND a.attnum = ANY (c.conkey)
		WHERE c.contype = 'f' AND c.confrelid = 'assets'::regclass`)
	require.NoError(t, err)
	got := map[string][]string{}
	for rows.Next() {
		var table, col string
		require.NoError(t, rows.Scan(&table, &col))
		got[table] = append(got[table], col)
	}
	require.NoError(t, rows.Err())
	require.Len(t, got, len(mergeTables))
	for table, cols := range mergeTables {
		assert.ElementsMatch(t, cols, got[table], table)
	}
}

// TestMergeAssets_MovesEverythingAndDeletesSource: a dry run reports exactly
// what the commit then does and writes nothing; the commit leaves no row
// naming the source anywhere, keeps target's copy of a duplicated price, and
// drops a price that would value target in itself.
func TestMergeAssets_MovesEverythingAndDeletesSource(t *testing.T) {
	f := newLedgerFixture(t)
	md := NewMarketDataStore(testDB.Pool)
	src, tgt := f.usdt, f.btc
	quote := createTestAsset(t, md, "Merge Quote")
	other := createTestAsset(t, md, "Merge Other")
	at := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)

	otherAccount, err := f.store.CreateAccount(f.ctx, &entity.Account{UserID: f.user.ID, Name: "other", Type: entity.AccountTypeManual})
	require.NoError(t, err)
	srcHolding := mergeHolding(t, f, f.account.ID, src.ID)
	mergeHolding(t, f, otherAccount.ID, tgt.ID)

	ref := "merge-leg-1"
	f.importBatch(t, []*apiv1.ImportTransactionItem{{
		Kind:        apiv1.LedgerEventKind_LEDGER_EVENT_KIND_DEPOSIT,
		ExecutedAt:  timestamppb.New(at),
		ExternalRef: &ref,
		Legs:        []*apiv1.ImportLedgerLeg{importLeg(src.ID, "1", legIn, principal)},
	}})

	mergePrice(t, md, src.ID, quote.ID, "binance", at)   // target has the same: dropped
	mergePrice(t, md, tgt.ID, quote.ID, "binance", at)   // kept
	mergePrice(t, md, src.ID, quote.ID, "coingecko", at) // moved
	mergePrice(t, md, other.ID, src.ID, "binance", at)   // target has the same base: dropped
	mergePrice(t, md, other.ID, tgt.ID, "binance", at)   // kept
	mergePrice(t, md, other.ID, src.ID, "coingecko", at) // base moved
	mergePrice(t, md, src.ID, tgt.ID, "binance", at)     // would price target in itself: dropped

	for _, r := range []*entity.AssetExternalRef{
		{AssetID: src.ID, Source: "onchain:bsc", Ref: "0x" + uuid.NewString()[:8], Origin: entity.RefOriginManual},
		{AssetID: tgt.ID, Source: "onchain:bsc", Ref: "0x" + uuid.NewString()[:8], Origin: entity.RefOriginManual},
		{AssetID: src.ID, Source: "binance", Ref: "MRG" + uuid.NewString()[:6], Origin: entity.RefOriginManual},
	} {
		_, err := md.CreateAssetExternalRef(f.ctx, r)
		require.NoError(t, err)
	}
	review := at.Add(30 * 24 * time.Hour)
	_, err = md.CreateAssetRiskFlag(f.ctx, &entity.AssetRiskFlag{AssetID: src.ID, Kind: "depeg", ReviewAt: &review})
	require.NoError(t, err)
	rule := mergeRule(t, f, src.ID, quote.ID)

	want := &marketdata.MergeAssetsResult{
		HoldingsMoved: 1, LedgerLegsMoved: 1,
		PricesMoved: 2, PricesDropped: 3,
		ExternalRefsMoved: 2, RiskFlagsMoved: 1, RulesRewritten: 1,
	}

	before := rowsNaming(t, src.ID)
	plan, err := md.MergeAssets(f.ctx, mergeOpts(src, tgt, true))
	require.NoError(t, err)
	assert.Equal(t, want, plan)
	assert.Equal(t, before, rowsNaming(t, src.ID), "a dry run writes nothing")
	_, err = md.GetAsset(f.ctx, src.ID)
	require.NoError(t, err, "a dry run keeps the source")

	done, err := md.MergeAssets(f.ctx, mergeOpts(src, tgt, false))
	require.NoError(t, err)
	assert.Equal(t, plan, done, "the plan is what the commit did")

	for col, n := range rowsNaming(t, src.ID) {
		assert.Zero(t, n, "%s still names the merged asset", col)
	}
	_, err = md.GetAsset(f.ctx, src.ID)
	require.ErrorIs(t, err, store.ErrNotFound)

	moved, err := f.store.GetHolding(f.ctx, srcHolding)
	require.NoError(t, err)
	assert.Equal(t, tgt.ID, moved.AssetID)

	cfg := ruleConfig(t, rule)
	assert.Equal(t, tgt.ID, cfg["asset_id"])
	assert.Equal(t, quote.ID, cfg["quote_asset_id"], "the rest of the configuration is untouched")

	var tgtPrices int
	require.NoError(t, testDB.Pool.QueryRow(f.ctx,
		"SELECT count(*) FROM prices WHERE asset_id = $1 OR base_asset_id = $1", tgt.ID).Scan(&tgtPrices))
	// target/binance (kept) + target/coingecko (moved) + other-in-target binance
	// (kept) + other-in-target coingecko (base moved)
	assert.Equal(t, 4, tgtPrices)
}

// TestMergeAssets_Refusals: every refusal leaves both rows and everything
// naming them exactly as they were.
func TestMergeAssets_Refusals(t *testing.T) {
	f := newLedgerFixture(t)
	md := NewMarketDataStore(testDB.Pool)
	src, tgt := f.usdt, f.btc

	// assertRefused runs a merge that must be refused with want and checks it
	// wrote nothing; the refusal text must name each of names.
	assertRefused := func(t *testing.T, opts marketdata.MergeAssetsOpts, want error, names ...string) {
		t.Helper()
		beforeSrc, beforeTgt := rowsNaming(t, src.ID), rowsNaming(t, tgt.ID)
		_, err := md.MergeAssets(f.ctx, opts)
		require.ErrorIs(t, err, want)
		for _, n := range names {
			assert.ErrorContains(t, err, n)
		}
		assert.Equal(t, beforeSrc, rowsNaming(t, src.ID), "a refused merge writes nothing")
		assert.Equal(t, beforeTgt, rowsNaming(t, tgt.ID), "a refused merge writes nothing")
		_, getErr := md.GetAsset(f.ctx, src.ID)
		require.NoError(t, getErr, "a refused merge keeps the source")
	}

	t.Run("same sync key held by both", func(t *testing.T) {
		a := mergeHolding(t, f, f.account.ID, src.ID)
		b := mergeHolding(t, f, f.account.ID, tgt.ID)
		assertRefused(t, mergeOpts(src, tgt, false), store.ErrConstraint, a+" vs "+b)
		require.NoError(t, f.store.DeleteHolding(f.ctx, a))
		require.NoError(t, f.store.DeleteHolding(f.ctx, b))
	})

	t.Run("a provider names them as two instruments", func(t *testing.T) {
		refs := make([]*entity.AssetExternalRef, 0, 2)
		for _, asset := range []*entity.Asset{src, tgt} {
			r, err := md.CreateAssetExternalRef(f.ctx, &entity.AssetExternalRef{
				AssetID: asset.ID, Source: "coingecko", Ref: "cg-" + uuid.NewString()[:8], Origin: entity.RefOriginManual,
			})
			require.NoError(t, err)
			refs = append(refs, r)
		}
		assertRefused(t, mergeOpts(src, tgt, false), store.ErrConstraint, "coingecko")
		for _, r := range refs {
			require.NoError(t, md.DeleteAssetExternalRef(f.ctx, r.AssetID, r.ID))
		}
	})

	t.Run("a rule names both, in any spelling", func(t *testing.T) {
		// uuid.Parse accepts these, so a rule may store them as sent.
		rule := mergeRule(t, f, tgt.ID, "{"+strings.ToUpper(src.ID)+"}")
		assertRefused(t, mergeOpts(src, tgt, false), store.ErrConstraint, rule)
		_, err := testDB.Pool.Exec(f.ctx, "DELETE FROM rules WHERE id = $1", rule)
		require.NoError(t, err)
	})

	t.Run("rows are not what the caller saw", func(t *testing.T) {
		opts := mergeOpts(src, tgt, false)
		opts.ExpectedTargetSymbol = "NOT" + tgt.Symbol
		assertRefused(t, opts, store.ErrConstraint)
	})

	t.Run("same asset, also in another case", func(t *testing.T) {
		assertRefused(t, mergeOpts(src, src, false), store.ErrInvalidArgument)
		opts := mergeOpts(src, src, false)
		opts.TargetID = strings.ToUpper(src.ID)
		assertRefused(t, opts, store.ErrInvalidArgument)
	})

	t.Run("missing target", func(t *testing.T) {
		opts := mergeOpts(src, tgt, false)
		opts.TargetID = uuid.NewString()
		assertRefused(t, opts, store.ErrNotFound)
	})

	t.Run("with nothing in the way it merges, ids in any case", func(t *testing.T) {
		h := mergeHolding(t, f, f.account.ID, src.ID)
		rule := mergeRule(t, f, strings.ToUpper(strings.ReplaceAll(src.ID, "-", "")), uuid.NewString())
		opts := mergeOpts(src, tgt, false)
		opts.SourceID, opts.TargetID = strings.ToUpper(src.ID), strings.ToUpper(tgt.ID)
		res, err := md.MergeAssets(f.ctx, opts)
		require.NoError(t, err)
		assert.EqualValues(t, 1, res.HoldingsMoved)
		assert.EqualValues(t, 1, res.RulesRewritten)
		assert.Equal(t, tgt.ID, ruleConfig(t, rule)["asset_id"], "a rule spelling the id another way is rewritten too")
		_, err = md.GetAsset(f.ctx, src.ID)
		require.ErrorIs(t, err, store.ErrNotFound)
		moved, err := f.store.GetHolding(f.ctx, h)
		require.NoError(t, err)
		assert.Equal(t, tgt.ID, moved.AssetID)
	})
}

// TestMergeAssets_WaitsForARuleWrite: a rule naming source that is being
// written while the merge starts is not invisible to it. Rules carry no
// foreign key, so only the table lock makes the merge wait for that write and
// then rewrite the rule, instead of committing past it and leaving the rule
// naming a deleted asset.
func TestMergeAssets_WaitsForARuleWrite(t *testing.T) {
	f := newLedgerFixture(t)
	md := NewMarketDataStore(testDB.Pool)
	src, tgt := f.usdt, f.btc
	portfolioID := insertTestPortfolio(t, testDB.Pool, f.user.ID)

	writer, err := testDB.Pool.Begin(f.ctx)
	require.NoError(t, err)
	defer func() { _ = writer.Rollback(f.ctx) }()
	ruleID := uuid.NewString()
	_, err = writer.Exec(f.ctx, `
		INSERT INTO rules (id, user_id, portfolio_id, name, rule_type, status, configuration, created_at, updated_at)
		VALUES ($1, $2, $3, 'racing dca', 'dca', 'active', jsonb_build_object('asset_id', $4::text), NOW(), NOW())`,
		ruleID, f.user.ID, portfolioID, src.ID)
	require.NoError(t, err)

	var res *marketdata.MergeAssetsResult
	done, result := goResult(func() error {
		var err error
		res, err = md.MergeAssets(context.Background(), mergeOpts(src, tgt, false))
		return err
	})
	waitOnLockOrDone(t, done)
	requireStillRunning(t, done, "the merge did not wait for a rule write in flight")

	require.NoError(t, writer.Commit(f.ctx))
	require.NoError(t, result())
	assert.EqualValues(t, 1, res.RulesRewritten)
	assert.Equal(t, tgt.ID, ruleConfig(t, ruleID)["asset_id"])
}
