package execution

import (
	"encoding/json"
	"testing"
	"time"

	apiv1 "github.com/foxcool/greedy-eye/api/v1"
	"github.com/foxcool/greedy-eye/internal/entity"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTradeTransitions(t *testing.T) {
	states := []TradeState{StatePlanned, StateFilled, StateRejected, StatePendingSignature, StateSubmitted, StateFailed, StateExpired}
	allowed := map[[2]TradeState]bool{
		{StatePlanned, StateFilled}:             true,
		{StatePlanned, StateRejected}:           true,
		{StatePlanned, StatePendingSignature}:   true,
		{StatePendingSignature, StateSubmitted}: true,
		{StatePendingSignature, StateExpired}:   true,
		{StatePendingSignature, StateRejected}:  true,
		{StateSubmitted, StateFilled}:           true,
		{StateSubmitted, StateFailed}:           true,
	}
	for _, from := range states {
		for _, to := range states {
			t.Run(string(from)+"->"+string(to), func(t *testing.T) {
				err := Transition(from, to)
				if allowed[[2]TradeState{from, to}] {
					assert.NoError(t, err)
				} else {
					assert.Error(t, err)
				}
			})
		}
	}
}

// TestSignedTradeLifecycle walks a proposal to a filled trade through the
// venue's answers, and checks a waiting trade answered "still waiting" is left
// as it is.
func TestSignedTradeLifecycle(t *testing.T) {
	r := TradeResult{State: StatePlanned}
	var err error

	r, err = Apply(r, Outcome{Status: OutcomePendingSignature, VenueRef: "0xsafetx"})
	require.NoError(t, err)
	assert.Equal(t, StatePendingSignature, r.State)
	assert.Equal(t, "0xsafetx", r.VenueRef)

	same, err := Apply(r, Outcome{Status: OutcomePendingSignature})
	require.NoError(t, err, "still waiting is an answer, not an error")
	assert.Equal(t, r, same)

	r, err = Apply(r, Outcome{Status: OutcomeSubmitted, TxHash: "0xtx"})
	require.NoError(t, err)
	assert.Equal(t, StateSubmitted, r.State)
	assert.Equal(t, "0xsafetx", r.VenueRef, "a later answer without a ref keeps the one it had")

	r, err = Apply(r, Outcome{Status: OutcomeFilled, Fill: Fill{BaseAmount: decimal.RequireFromString("0.0074")}})
	require.NoError(t, err)
	assert.Equal(t, StateFilled, r.State)
	assert.Equal(t, "0xtx", r.TxHash)
	require.NotNil(t, r.Fill)

	failed, err := Apply(TradeResult{State: StateSubmitted}, Outcome{Status: OutcomeFailed, Reason: "reverted"})
	require.NoError(t, err)
	assert.Equal(t, StateFailed, failed.State)
	assert.Equal(t, "reverted", failed.Reason)

	expired, err := Expire(TradeResult{State: StatePendingSignature}, "not signed")
	require.NoError(t, err)
	assert.Equal(t, StateExpired, expired.State)
	_, err = Expire(TradeResult{State: StateSubmitted}, "too late")
	assert.Error(t, err, "a submitted transaction is not withdrawn")
}

func TestExecutionStatus(t *testing.T) {
	filled := TradeResult{State: StateFilled}
	rejected := func(reason string) TradeResult { return TradeResult{State: StateRejected, Reason: reason} }

	tests := []struct {
		name       string
		results    []TradeResult
		wantStatus entity.ExecutionStatus
		wantReason string
		notReason  string
	}{
		{"empty plan", nil, entity.ExecutionStatusCompleted, "", ""},
		{"all filled", []TradeResult{filled, filled}, entity.ExecutionStatusCompleted, "", ""},
		{"one rejected", []TradeResult{filled, rejected("insufficient USDT")}, entity.ExecutionStatusFailed, "insufficient USDT", ""},
		{"first rejection wins", []TradeResult{rejected("first"), rejected("second")}, entity.ExecutionStatusFailed, "first", "second"},
		{"planned left over", []TradeResult{{State: StatePlanned}}, entity.ExecutionStatusFailed, "planned", ""},
		{"waiting for a signature", []TradeResult{filled, {State: StatePendingSignature}}, entity.ExecutionStatusPending, "", ""},
		{"waiting for a block", []TradeResult{{State: StateSubmitted}}, entity.ExecutionStatusPending, "", ""},
		{"expired proposal", []TradeResult{{State: StateExpired, Reason: "not signed"}}, entity.ExecutionStatusCancelled, "not signed", ""},
		{"reverted on chain", []TradeResult{{State: StateFailed, Reason: "reverted"}}, entity.ExecutionStatusFailed, "reverted", ""},
		{"waiting outranks a failure", []TradeResult{{State: StateFailed, Reason: "reverted"}, {State: StatePendingSignature}}, entity.ExecutionStatusPending, "", ""},
		{"waiting outranks an expiry", []TradeResult{{State: StateExpired}, {State: StateSubmitted}}, entity.ExecutionStatusPending, "", ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			status, reason := ExecutionStatus(tc.results)
			assert.Equal(t, tc.wantStatus, status)
			if tc.wantReason == "" {
				assert.Empty(t, reason)
			} else {
				assert.Contains(t, reason, tc.wantReason)
			}
			if tc.notReason != "" {
				assert.NotContains(t, reason, tc.notReason)
			}
		})
	}
}

func TestSettle(t *testing.T) {
	planned := TradeResult{State: StatePlanned}

	tests := []struct {
		name       string
		outcome    Outcome
		submitErr  error
		wantState  TradeState
		wantReason string
	}{
		{"filled", Outcome{Status: OutcomeFilled, HoldingIDs: []string{"h-1"}}, nil, StateFilled, ""},
		{"rejected", Outcome{Status: OutcomeRejected, Reason: "no funds"}, nil, StateRejected, "no funds"},
		{"unknown answer is refused, not lost", Outcome{}, nil, StateRejected, "unknown status"},
		{"venue error", Outcome{}, assert.AnError, StateRejected, assert.AnError.Error()},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Settle(planned, tc.outcome, tc.submitErr)
			require.NoError(t, err)
			assert.Equal(t, tc.wantState, got.State)
			assert.Contains(t, got.Reason, tc.wantReason)
			assert.Equal(t, tc.wantState == StateFilled, got.Fill != nil)
		})
	}

	_, err := Settle(TradeResult{State: StateFilled}, Outcome{Status: OutcomeRejected}, nil)
	assert.Error(t, err, "a terminal trade cannot be settled again")
	_, err = Settle(TradeResult{State: StateRejected}, Outcome{}, assert.AnError)
	assert.Error(t, err, "a terminal trade cannot be rejected again")
}

func TestJournalItem(t *testing.T) {
	asOf := time.Date(2025, 3, 14, 11, 50, 0, 0, time.UTC)
	r := TradeResult{
		Trade: Trade{Side: SideBuy, AssetID: "base", QuoteAssetID: "quote", QuoteAmount: decimal.NewFromInt(100), Price: decimal.NewFromInt(40000), PriceAsOf: asOf},
		State: StateFilled,
		Fill:  &Fill{BaseAmount: decimal.RequireFromString("0.0025"), QuoteAmount: decimal.NewFromInt(100), Price: decimal.NewFromInt(40000), PriceAsOf: asOf},
	}

	item := JournalItem(Key{ExecutionID: "exec-1", Index: 2}, r)

	assert.Equal(t, apiv1.TransactionType_TRANSACTION_TYPE_TRADE, item.Type)
	assert.Equal(t, "base", item.GetAssetId())
	assert.Equal(t, "exec-1:2", item.GetExternalId())
	assert.Equal(t, map[string]string{
		"side":           "buy",
		"amount":         "0.0025",
		"quote_asset_id": "quote",
		"quote_amount":   "100",
		"price":          "40000",
		"price_as_of":    "2025-03-14T11:50:00Z",
		"execution_id":   "exec-1",
	}, item.Data)
}

func TestSummary(t *testing.T) {
	trade := Trade{Side: SideBuy, AssetID: "base", QuoteAssetID: "quote", QuoteAmount: decimal.NewFromInt(100), Price: decimal.NewFromInt(40000)}
	raw, err := Summary([]TradeResult{
		{Trade: trade, State: StateFilled, Fill: &Fill{BaseAmount: decimal.RequireFromString("0.0025")}},
		{Trade: trade, State: StateRejected, Reason: "not placed"},
	})
	require.NoError(t, err)
	var got struct {
		Trades []map[string]any `json:"trades"`
	}
	require.NoError(t, json.Unmarshal(raw, &got))
	require.Len(t, got.Trades, 2)
	assert.Equal(t, "0.0025", got.Trades[0]["base_amount"])
	assert.Equal(t, "100", got.Trades[0]["quote_amount"], "amounts are strings, never floats")
	assert.NotContains(t, got.Trades[1], "base_amount", "an unfilled trade bought nothing")
	assert.Equal(t, "not placed", got.Trades[1]["reason"])
}

// TestSummaryRoundTrip: a waiting execution is resumed from its summary, so
// what Summary writes ParseSummary must read back, venue reference included.
func TestSummaryRoundTrip(t *testing.T) {
	asOf := time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC)
	trade := Trade{Side: SideBuy, AssetID: "weth", QuoteAssetID: "usdc", QuoteAmount: decimal.NewFromInt(25), Price: decimal.RequireFromString("3400.5"), PriceAsOf: asOf}
	in := []TradeResult{
		{Trade: trade, State: StatePendingSignature, VenueRef: "0xsafetx"},
		{Trade: trade, State: StateFilled, TxHash: "0xtx", Fill: &Fill{BaseAmount: decimal.RequireFromString("0.0073")}},
	}
	raw, err := Summary(in)
	require.NoError(t, err)

	out, err := ParseSummary(raw)
	require.NoError(t, err)
	require.Len(t, out, 2)
	assert.Equal(t, StatePendingSignature, out[0].State)
	assert.Equal(t, "0xsafetx", out[0].VenueRef)
	assert.True(t, out[0].Trade.QuoteAmount.Equal(trade.QuoteAmount))
	assert.True(t, out[0].Trade.Price.Equal(trade.Price))
	assert.True(t, out[0].Trade.PriceAsOf.Equal(asOf))
	assert.Equal(t, "0xtx", out[1].TxHash)
	require.NotNil(t, out[1].Fill)
	assert.Equal(t, "0.0073", out[1].Fill.BaseAmount.String())

	_, err = ParseSummary([]byte(`{"trades":[{"index":0,"state":"teleported","quote_amount":"1","price":"1","price_as_of":"2026-11-15T12:00:00Z"}]}`))
	assert.Error(t, err, "an unknown state is not guessed at")
}
