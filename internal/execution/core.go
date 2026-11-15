package execution

import (
	"encoding/json"
	"fmt"
	"time"

	apiv1 "github.com/foxcool/greedy-eye/api/v1"
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
	q, _ := t.QuoteAmount.QuoRem(t.Price, entity.DecI32(decimals))
	return q
}

// TradeState is where a trade is in its life. Paper trades go planned ->
// filled | rejected at once; a trade signed by a person waits in
// pending_signature, then submitted, until its block says filled or failed.
type TradeState string

const (
	StatePlanned          TradeState = "planned"
	StateFilled           TradeState = "filled"
	StateRejected         TradeState = "rejected"
	StatePendingSignature TradeState = "pending_signature"
	StateSubmitted        TradeState = "submitted"
	StateFailed           TradeState = "failed"
	StateExpired          TradeState = "expired"
)

// transitions is every move a trade may make. Filled, rejected, failed and
// expired are final. Rejected means the venue refused before anything reached
// a chain; failed means a transaction reached it and reverted.
var transitions = map[TradeState][]TradeState{
	StatePlanned:          {StateFilled, StateRejected, StatePendingSignature},
	StatePendingSignature: {StateSubmitted, StateExpired, StateRejected},
	StateSubmitted:        {StateFilled, StateFailed},
}

// Transition allows the moves in transitions and nothing else.
func Transition(from, to TradeState) error {
	for _, allowed := range transitions[from] {
		if to == allowed {
			return nil
		}
	}
	return fmt.Errorf("trade cannot move from %q to %q", from, to)
}

// Waiting is a trade someone else still has to move: a person to sign, a
// chain to include.
func (s TradeState) Waiting() bool {
	return s == StatePendingSignature || s == StateSubmitted
}

// TradeResult is a trade and what became of it.
type TradeResult struct {
	Trade      Trade
	State      TradeState
	Reason     string
	Fill       *Fill
	HoldingIDs []string
	VenueRef   string
	TxHash     string
}

// Planned starts every trade of a plan in StatePlanned.
func Planned(plan TradePlan) []TradeResult {
	results := make([]TradeResult, len(plan.Trades))
	for i, t := range plan.Trades {
		results[i] = TradeResult{Trade: t, State: StatePlanned}
	}
	return results
}

var outcomeStates = map[OutcomeStatus]TradeState{
	OutcomeFilled:           StateFilled,
	OutcomeRejected:         StateRejected,
	OutcomePendingSignature: StatePendingSignature,
	OutcomeSubmitted:        StateSubmitted,
	OutcomeFailed:           StateFailed,
}

// Apply records a venue's answer on a trade. An answer naming the state the
// trade is already in changes nothing: that is a waiting trade still waiting.
func Apply(r TradeResult, o Outcome) (TradeResult, error) {
	to, ok := outcomeStates[o.Status]
	if !ok {
		return r, fmt.Errorf("venue answered with unknown status %d", o.Status)
	}
	if to == r.State && to.Waiting() {
		return r, nil
	}
	if err := Transition(r.State, to); err != nil {
		return r, err
	}
	r.State = to
	if o.VenueRef != "" {
		r.VenueRef = o.VenueRef
	}
	if o.TxHash != "" {
		r.TxHash = o.TxHash
	}
	if len(o.HoldingIDs) > 0 {
		r.HoldingIDs = o.HoldingIDs
	}
	switch to {
	case StateFilled:
		fill := o.Fill
		r.Fill = &fill
	case StateRejected, StateFailed:
		r.Reason = o.Reason
	}
	return r, nil
}

// Settle records the answer to one Submit. A venue error, or an answer the
// core cannot hold, rejects the trade with that error: nothing a venue says is
// dropped. An error means the trade was not planned.
func Settle(r TradeResult, o Outcome, submitErr error) (TradeResult, error) {
	if submitErr == nil {
		settled, err := Apply(r, o)
		if err == nil {
			return settled, nil
		}
		if r.State != StatePlanned {
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

// Expire closes a trade nobody signed in time. Only a proposal expires: a
// submitted transaction is on its way to a block and is left to finish.
func Expire(r TradeResult, reason string) (TradeResult, error) {
	if err := Transition(r.State, StateExpired); err != nil {
		return r, err
	}
	r.State = StateExpired
	r.Reason = reason
	return r, nil
}

// ExecutionStatus derives an execution's status from its trades: failed with
// the first rejected or failed trade's reason; cancelled when a proposal
// expired; pending while any trade waits; completed when every trade filled
// (an empty plan included).
func ExecutionStatus(results []TradeResult) (entity.ExecutionStatus, string) {
	expired, waiting := -1, false
	for i, r := range results {
		switch {
		case r.State == StateRejected || r.State == StateFailed:
			return entity.ExecutionStatusFailed, fmt.Sprintf("trade %d %s: %s", i, r.State, r.Reason)
		case r.State == StateExpired:
			if expired < 0 {
				expired = i
			}
		case r.State.Waiting():
			waiting = true
		case r.State != StateFilled:
			return entity.ExecutionStatusFailed, fmt.Sprintf("trade %d left %s", i, r.State)
		}
	}
	switch {
	case expired >= 0:
		return entity.ExecutionStatusCancelled, fmt.Sprintf("trade %d expired: %s", expired, results[expired].Reason)
	case waiting:
		return entity.ExecutionStatusPending, ""
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
	VenueRef     string     `json:"venue_ref,omitempty"`
	TxHash       string     `json:"tx_hash,omitempty"`
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
			VenueRef:     r.VenueRef,
			TxHash:       r.TxHash,
		}
		if r.Fill != nil {
			trades[i].BaseAmount = r.Fill.BaseAmount.String()
		}
	}
	return json.Marshal(map[string]any{"trades": trades})
}

// ParseSummary reads an execution_summary back into trade results, so an
// execution waiting on a venue can be picked up again after a restart. It is
// the summary, not a column per field, that holds a trade's state: it is
// already written for every run, and a waiting trade needs nothing more.
// A filled trade comes back with its base amount only; nothing that reads a
// parsed summary journals a fill again.
func ParseSummary(raw json.RawMessage) ([]TradeResult, error) {
	if len(raw) == 0 {
		// An execution that never got as far as a plan has no trades.
		return nil, nil
	}
	var doc struct {
		Trades []summaryTrade `json:"trades"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("execution summary: %w", err)
	}
	results := make([]TradeResult, len(doc.Trades))
	for i, st := range doc.Trades {
		if st.Index != i {
			return nil, fmt.Errorf("execution summary: trade %d is stored at position %d", st.Index, i)
		}
		if _, known := transitions[st.State]; !known && !final(st.State) {
			return nil, fmt.Errorf("execution summary: trade %d has unknown state %q", i, st.State)
		}
		quote, err := decimal.NewFromString(st.QuoteAmount)
		if err != nil {
			return nil, fmt.Errorf("execution summary: trade %d quote amount: %w", i, err)
		}
		price, err := decimal.NewFromString(st.Price)
		if err != nil {
			return nil, fmt.Errorf("execution summary: trade %d price: %w", i, err)
		}
		asOf, err := time.Parse(time.RFC3339, st.PriceAsOf)
		if err != nil {
			return nil, fmt.Errorf("execution summary: trade %d price time: %w", i, err)
		}
		r := TradeResult{
			Trade: Trade{
				Side: st.Side, AssetID: st.AssetID, QuoteAssetID: st.QuoteAssetID,
				QuoteAmount: quote, Price: price, PriceAsOf: asOf,
			},
			State: st.State, Reason: st.Reason, VenueRef: st.VenueRef, TxHash: st.TxHash,
		}
		if st.BaseAmount != "" {
			base, err := decimal.NewFromString(st.BaseAmount)
			if err != nil {
				return nil, fmt.Errorf("execution summary: trade %d base amount: %w", i, err)
			}
			r.Fill = &Fill{BaseAmount: base}
		}
		results[i] = r
	}
	return results, nil
}

// final is a state no transition leaves.
func final(s TradeState) bool {
	switch s {
	case StateFilled, StateRejected, StateFailed, StateExpired:
		return true
	}
	return false
}

// JournalItem is the transaction recording a filled trade, for any venue.
// external_id is the trade's key, so a repeated journal write is skipped by
// the import's dedup instead of doubling the record.
func JournalItem(key Key, r TradeResult) *apiv1.ImportTransactionItem {
	assetID, externalID := r.Trade.AssetID, key.String()
	data := map[string]string{
		"side":           string(r.Trade.Side),
		"quote_asset_id": r.Trade.QuoteAssetID,
		"execution_id":   key.ExecutionID,
	}
	if r.Fill != nil {
		data["amount"] = r.Fill.BaseAmount.String()
		data["quote_amount"] = r.Fill.QuoteAmount.String()
		data["price"] = r.Fill.Price.String()
		data["price_as_of"] = r.Fill.PriceAsOf.UTC().Format(time.RFC3339)
	}
	return &apiv1.ImportTransactionItem{
		Type:       apiv1.TransactionType_TRANSACTION_TYPE_TRADE,
		AssetId:    &assetID,
		Data:       data,
		ExternalId: &externalID,
	}
}
