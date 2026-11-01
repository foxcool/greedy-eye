package execution

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/foxcool/greedy-eye/internal/entity"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	dcaAccount = "0190a3a0-0000-7000-8000-00000000000a"
	dcaBase    = "0190a3a0-0000-7000-8000-00000000000b"
	dcaQuote   = "0190a3a0-0000-7000-8000-00000000000c"
)

func dcaRule(t *testing.T, cfg map[string]any) *entity.Rule {
	t.Helper()
	raw, err := json.Marshal(cfg)
	require.NoError(t, err)
	return &entity.Rule{ID: "r-1", RuleType: DCAType, PortfolioID: "p-1", Configuration: raw}
}

func validDCAConfig() map[string]any {
	return map[string]any{
		"account_id":     dcaAccount,
		"asset_id":       dcaBase,
		"quote_asset_id": dcaQuote,
		"quote_amount":   "100",
	}
}

func TestDCAInputs(t *testing.T) {
	with := func(key string, value any) map[string]any {
		cfg := validDCAConfig()
		if value == nil {
			delete(cfg, key)
		} else {
			cfg[key] = value
		}
		return cfg
	}
	tests := []struct {
		name    string
		cfg     map[string]any
		wantErr string
	}{
		{"valid", validDCAConfig(), ""},
		{"largest accepted amount", with("quote_amount", "123456789012345678901234567890.123456789012345678"), ""},
		{"missing account", with("account_id", nil), "account_id"},
		{"empty asset", with("asset_id", ""), "asset_id"},
		{"asset not an id", with("asset_id", "BTC"), "asset_id"},
		{"missing quote asset", with("quote_asset_id", nil), "quote_asset_id"},
		{"same asset both sides", with("quote_asset_id", dcaBase), "differ"},
		{"zero amount", with("quote_amount", "0"), "positive"},
		{"negative amount", with("quote_amount", "-5"), "not a number"},
		{"huge exponent", with("quote_amount", "1e2000000000"), "not a number"},
		{"tiny exponent", with("quote_amount", "1e-2000000000"), "not a number"},
		{"too many integer digits", with("quote_amount", "1234567890123456789012345678901"), "not a number"},
		{"too many fraction digits", with("quote_amount", "1.1234567890123456789"), "not a number"},
		{"amount not a number", with("quote_amount", "lots"), "not a number"},
		{"amount as a JSON number", with("quote_amount", 100), "decimal string"},
		{"missing amount", with("quote_amount", nil), "quote_amount"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			inputs, err := DCA{}.Inputs(dcaRule(t, tc.cfg))
			if tc.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tc.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, dcaAccount, inputs.AccountID)
			assert.Equal(t, []Pair{{Asset: dcaBase, Quote: dcaQuote}}, inputs.Pairs)
		})
	}

	_, err := DCA{}.Inputs(&entity.Rule{RuleType: DCAType})
	assert.Error(t, err, "a rule with no configuration")
}

func TestDCAPlan(t *testing.T) {
	// A day long gone: Plan must judge age against Snapshot.Now, not the wall clock.
	now := time.Date(2025, 3, 14, 12, 0, 0, 0, time.UTC)
	pair := Pair{Asset: dcaBase, Quote: dcaQuote}
	priced := func(value string, asOf time.Time) map[Pair]Price {
		return map[Pair]Price{pair: {Value: decimal.RequireFromString(value), AsOf: asOf}}
	}

	tests := []struct {
		name    string
		prices  map[Pair]Price
		wantErr string
	}{
		{"no price", nil, "no price"},
		{"price of another pair", map[Pair]Price{{Asset: dcaQuote, Quote: dcaBase}: {Value: decimal.NewFromInt(1), AsOf: now}}, "no price"},
		{"price older than an hour", priced("50000", now.Add(-time.Hour-time.Second)), "old"},
		{"zero price", priced("0", now), "positive"},
		{"price from that same day", priced("50000", now.Add(-10*time.Minute)), ""},
		{"price exactly an hour old", priced("50000", now.Add(-time.Hour)), ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			plan, err := DCA{}.Plan(dcaRule(t, validDCAConfig()), Snapshot{Now: now, Prices: tc.prices})
			if tc.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tc.wantErr)
				return
			}
			require.NoError(t, err)
			require.Len(t, plan.Trades, 1)
			trade := plan.Trades[0]
			assert.Equal(t, SideBuy, trade.Side)
			assert.Equal(t, dcaBase, trade.AssetID)
			assert.Equal(t, dcaQuote, trade.QuoteAssetID)
			assert.True(t, trade.QuoteAmount.Equal(decimal.NewFromInt(100)))
			assert.True(t, trade.Price.Equal(decimal.NewFromInt(50000)))
			assert.Equal(t, tc.prices[pair].AsOf, trade.PriceAsOf)
		})
	}
}

func TestDCASimulate(t *testing.T) {
	now := time.Date(2025, 3, 14, 12, 0, 0, 0, time.UTC)
	snap := Snapshot{
		Now:      now,
		Holdings: []Holding{{ID: "h-q", AssetID: dcaQuote, Amount: decimal.NewFromInt(250), Decimals: 2}},
		Prices:   map[Pair]Price{{Asset: dcaBase, Quote: dcaQuote}: {Value: decimal.NewFromInt(30000), AsOf: now}},
	}
	rule := dcaRule(t, validDCAConfig())
	plan, err := DCA{}.Plan(rule, snap)
	require.NoError(t, err)

	got := DCA{}.Simulate(rule, snap, plan).GetDca()
	require.NotNil(t, got)
	assert.Equal(t, 100.0, got.DcaAmount)
	assert.Equal(t, dcaBase, got.TargetAssetId)
	assert.Equal(t, 30000.0, got.EstimatedPrice)
	assert.Equal(t, 0.00333333, got.EstimatedPurchaseAmount, "100/30000 rounded down to 8 decimals")
	assert.Equal(t, 250.0, got.AvailableBalance)
	assert.True(t, got.SufficientFunds)
	assert.Zero(t, got.EstimatedFee)

	snap.Holdings = nil
	got = DCA{}.Simulate(rule, snap, plan).GetDca()
	assert.Zero(t, got.AvailableBalance)
	assert.False(t, got.SufficientFunds, "no quote holding means nothing to spend")
}
