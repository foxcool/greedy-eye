package execution

import (
	"testing"

	"github.com/foxcool/greedy-eye/internal/entity"
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
	}{
		{"empty plan", nil, entity.ExecutionStatusCompleted, ""},
		{"all filled", []TradeResult{filled, filled}, entity.ExecutionStatusCompleted, ""},
		{"one rejected", []TradeResult{filled, rejected("insufficient USDT")}, entity.ExecutionStatusFailed, "insufficient USDT"},
		{"first rejection wins", []TradeResult{rejected("first"), rejected("second")}, entity.ExecutionStatusFailed, "first"},
		{"planned left over", []TradeResult{{State: StatePlanned}}, entity.ExecutionStatusFailed, "planned"},
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
			if tc.name == "first rejection wins" {
				assert.NotContains(t, reason, "second")
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
