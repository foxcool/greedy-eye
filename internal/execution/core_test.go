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
	states := []TradeState{StatePlanned, StateFilled, StateRejected}
	allowed := map[[2]TradeState]bool{
		{StatePlanned, StateFilled}:   true,
		{StatePlanned, StateRejected}: true,
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
		{"non-terminal answer is refused, not lost", Outcome{}, nil, StateRejected, ErrAsyncVenue.Error()},
		{"venue error", Outcome{}, assert.AnError, StateRejected, assert.AnError.Error()},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Settle(planned, tc.outcome, tc.submitErr)
			require.NoError(t, err)
			assert.Equal(t, tc.wantState, got.State)
			assert.Equal(t, tc.wantReason, got.Reason)
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
