package execution

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/foxcool/greedy-eye/internal/entity"
	"github.com/shopspring/decimal"
)

// defaultDecimals is the storage scale of a holding a fill creates. Assets
// carry no scale of their own; ImportPositions defaults to the same.
const defaultDecimals = 8

// baseDecimals is the scale a fill of assetID is kept at: its holding's,
// else the default.
func baseDecimals(snap Snapshot, assetID string) uint32 {
	if h, found, _ := snap.Holding(assetID); found {
		return h.Decimals
	}
	return defaultDecimals
}

// baseBought is what a buy's quote amount gets at its price, rounded down to
// decimals: a fill never credits more than was paid for.
func baseBought(t Trade, decimals uint32) decimal.Decimal {
	q, _ := t.QuoteAmount.QuoRem(t.Price, int32(decimals)) // #nosec G115 -- decimals is a small storage scale
	return q
}

// TradeState is where a trade is in its life. Waiting states arrive with the
// first venue that answers later than Submit returns.
type TradeState string

const (
	StatePlanned  TradeState = "planned"
	StateFilled   TradeState = "filled"
	StateRejected TradeState = "rejected"
)

// ErrAsyncVenue is returned for an Outcome that is neither filled nor
// rejected: nothing persists a waiting trade yet, so accepting one would lose it.
var ErrAsyncVenue = errors.New("async venue not supported")

// Transition allows planned -> filled | rejected and nothing else; filled and
// rejected are terminal.
func Transition(from, to TradeState) error {
	if from == StatePlanned && (to == StateFilled || to == StateRejected) {
		return nil
	}
	return fmt.Errorf("trade cannot move from %q to %q", from, to)
}

// TradeResult is a trade and what became of it.
type TradeResult struct {
	Trade      Trade
	State      TradeState
	Reason     string
	Fill       *Fill
	HoldingIDs []string
}

// Planned starts every trade of a plan in StatePlanned.
func Planned(plan TradePlan) []TradeResult {
	results := make([]TradeResult, len(plan.Trades))
	for i, t := range plan.Trades {
		results[i] = TradeResult{Trade: t, State: StatePlanned}
	}
	return results
}

// Apply records a venue's answer on a trade.
func Apply(r TradeResult, o Outcome) (TradeResult, error) {
	var to TradeState
	switch o.Status {
	case OutcomeFilled:
		to = StateFilled
	case OutcomeRejected:
		to = StateRejected
	default:
		return r, ErrAsyncVenue
	}
	if err := Transition(r.State, to); err != nil {
		return r, err
	}
	r.State = to
	r.HoldingIDs = o.HoldingIDs
	if to == StateFilled {
		fill := o.Fill
		r.Fill = &fill
	} else {
		r.Reason = o.Reason
	}
	return r, nil
}

// Settle records the answer to one Submit. A venue error, or an answer the
// core cannot hold (ErrAsyncVenue), rejects the trade with that error: nothing
// a venue says is dropped. An error means the trade was not planned.
func Settle(r TradeResult, o Outcome, submitErr error) (TradeResult, error) {
	if submitErr == nil {
		settled, err := Apply(r, o)
		if err == nil {
			return settled, nil
		}
		if !errors.Is(err, ErrAsyncVenue) {
			return r, err
		}
		submitErr = err
	}
	return Reject(r, submitErr.Error())
}

// Reject closes a trade the venue never filled.
func Reject(r TradeResult, reason string) (TradeResult, error) {
	if err := Transition(r.State, StateRejected); err != nil {
		return r, err
	}
	r.State = StateRejected
	r.Reason = reason
	return r, nil
}

// ExecutionStatus derives an execution's status from its trades: completed
// when every trade filled (an empty plan included), failed with the first
// rejected trade's reason otherwise.
func ExecutionStatus(results []TradeResult) (entity.ExecutionStatus, string) {
	for i, r := range results {
		switch r.State {
		case StateFilled:
		case StateRejected:
			return entity.ExecutionStatusFailed, fmt.Sprintf("trade %d rejected: %s", i, r.Reason)
		default:
			return entity.ExecutionStatusFailed, fmt.Sprintf("trade %d left %s", i, r.State)
		}
	}
	return entity.ExecutionStatusCompleted, ""
}

type summaryTrade struct {
	Index        int        `json:"index"`
	Side         Side       `json:"side"`
	AssetID      string     `json:"asset_id"`
	QuoteAssetID string     `json:"quote_asset_id"`
	QuoteAmount  string     `json:"quote_amount"`
	Price        string     `json:"price"`
	PriceAsOf    string     `json:"price_as_of"`
	State        TradeState `json:"state"`
	Reason       string     `json:"reason,omitempty"`
	BaseAmount   string     `json:"base_amount,omitempty"`
}

// Summary is the execution_summary of a run: each trade and its state.
// Amounts are decimal strings, never floats.
func Summary(results []TradeResult) (json.RawMessage, error) {
	trades := make([]summaryTrade, len(results))
	for i, r := range results {
		trades[i] = summaryTrade{
			Index:        i,
			Side:         r.Trade.Side,
			AssetID:      r.Trade.AssetID,
			QuoteAssetID: r.Trade.QuoteAssetID,
			QuoteAmount:  r.Trade.QuoteAmount.String(),
			Price:        r.Trade.Price.String(),
			PriceAsOf:    r.Trade.PriceAsOf.UTC().Format(time.RFC3339),
			State:        r.State,
			Reason:       r.Reason,
		}
		if r.Fill != nil {
			trades[i].BaseAmount = r.Fill.BaseAmount.String()
		}
	}
	return json.Marshal(map[string]any{"trades": trades})
}
