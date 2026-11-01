// Package execution turns automation rules into trades and trades into fills.
//
// Functional core, imperative shell. Decisions are pure functions without a
// context: an Engine's Plan, trade state transitions, the execution status
// derived from trades, the journal item for a fill. They read time and prices
// only from a Snapshot, so a backtest can later fold them over price history.
// The shell — venue clients, locks, the automation handler that builds the
// Snapshot and persists the execution — does all I/O. Core and shell share
// this package and are kept apart by file.
//
// An Engine is chosen by rule type, a Venue by the type of the account the
// rule trades on. Nothing outside a venue knows which venue it is.
package execution

import (
	"time"

	apiv1 "github.com/foxcool/greedy-eye/api/v1"
	"github.com/foxcool/greedy-eye/internal/entity"
	"github.com/shopspring/decimal"
)

// Side is the direction of a trade relative to its base asset.
type Side string

const SideBuy Side = "buy"

// Pair is a direct quote: Asset priced in Quote. No cross rates are derived.
type Pair struct {
	Asset string
	Quote string
}

// Price is the cost of one Asset unit in Quote units, as of AsOf.
type Price struct {
	Value decimal.Decimal
	AsOf  time.Time
}

// Holding is a position of the rule's account in asset units.
type Holding struct {
	ID       string
	AssetID  string
	Amount   decimal.Decimal
	Decimals uint32
}

// Snapshot is everything Plan may read. Now is the only clock Plan sees.
type Snapshot struct {
	Now      time.Time
	Holdings []Holding
	Prices   map[Pair]Price
}

// Holding returns the account's position in assetID. More than one row for
// the asset is reported as not unique: a trade must not pick one at random.
func (s Snapshot) Holding(assetID string) (h Holding, found, unique bool) {
	n := 0
	for _, c := range s.Holdings {
		if c.AssetID == assetID {
			h = c
			n++
		}
	}
	return h, n > 0, n <= 1
}

// Inputs is what the shell has to load before Plan: the account the rule
// trades on, and the prices Plan reads.
type Inputs struct {
	AccountID string
	Pairs     []Pair
}

// Trade is one planned order on the rule's account.
type Trade struct {
	Side         Side
	AssetID      string
	QuoteAssetID string
	// QuoteAmount is what a buy spends, in quote units.
	QuoteAmount decimal.Decimal
	Price       decimal.Decimal
	PriceAsOf   time.Time
}

// TradePlan is an engine's decision for one execution.
type TradePlan struct {
	Trades []Trade
}

// Engine plans the trades of one rule type.
type Engine interface {
	Type() string
	// Inputs reads the rule's configuration; an error means it is invalid.
	Inputs(rule *entity.Rule) (Inputs, error)
	Plan(rule *entity.Rule, snap Snapshot) (TradePlan, error)
	// Simulate describes a plan in SimulateRule's terms.
	Simulate(rule *entity.Rule, snap Snapshot, plan TradePlan) *apiv1.SimulationResult
}

// Engines is the registry of engines by rule type.
type Engines map[string]Engine

func NewEngines(engines ...Engine) Engines {
	r := make(Engines, len(engines))
	for _, e := range engines {
		r[e.Type()] = e
	}
	return r
}

func (r Engines) Lookup(ruleType string) (Engine, bool) {
	e, ok := r[ruleType]
	return e, ok
}
