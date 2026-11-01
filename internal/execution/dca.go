package execution

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	apiv1 "github.com/foxcool/greedy-eye/api/v1"
	"github.com/foxcool/greedy-eye/internal/entity"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

// DCAType is the rule type DCA plans.
const DCAType = "dca"

// maxDCAPriceAge is how old a price a DCA buy may still be filled at. Older,
// the fill would be priced by a market that has moved on.
const maxDCAPriceAge = time.Hour

// maxPriceLead is how far a price may be stamped after Snapshot.Now: clock
// skew between hosts. Further ahead it is a bad row that would stay "latest",
// and in a backtest a look into the future.
const maxPriceLead = time.Minute

// DCA buys a base asset for a fixed amount of a quote asset on every run.
//
// Configuration: {account_id, asset_id, quote_asset_id, quote_amount}, ids as
// UUIDs (holdings and imports address assets by id), quote_amount a decimal
// string in quote units — a JSON number would arrive as a float.
type DCA struct{}

type dcaConfig struct {
	AccountID    string
	AssetID      string
	QuoteAssetID string
	QuoteAmount  decimal.Decimal
}

func parseDCAConfig(raw json.RawMessage) (dcaConfig, error) {
	if len(raw) == 0 {
		return dcaConfig{}, errors.New("configuration is required")
	}
	var doc struct {
		AccountID    string          `json:"account_id"`
		AssetID      string          `json:"asset_id"`
		QuoteAssetID string          `json:"quote_asset_id"`
		QuoteAmount  json.RawMessage `json:"quote_amount"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		return dcaConfig{}, fmt.Errorf("configuration is not a JSON object: %w", err)
	}
	for _, f := range []struct{ name, value string }{
		{"account_id", doc.AccountID},
		{"asset_id", doc.AssetID},
		{"quote_asset_id", doc.QuoteAssetID},
	} {
		if f.value == "" {
			return dcaConfig{}, fmt.Errorf("%s is required", f.name)
		}
		if _, err := uuid.Parse(f.value); err != nil {
			return dcaConfig{}, fmt.Errorf("%s must be a UUID, got %q", f.name, f.value)
		}
	}
	if doc.AssetID == doc.QuoteAssetID {
		return dcaConfig{}, errors.New("asset_id and quote_asset_id must differ")
	}
	amount, err := parseQuoteAmount(doc.QuoteAmount)
	if err != nil {
		return dcaConfig{}, err
	}
	return dcaConfig{
		AccountID:    doc.AccountID,
		AssetID:      doc.AssetID,
		QuoteAssetID: doc.QuoteAssetID,
		QuoteAmount:  amount,
	}, nil
}

// quoteAmountPattern admits plain decimals only. The decimal parser takes any
// exponent, and "1e2000000000" passes a sign check yet costs gigabytes the
// first time it is compared or printed; no real buy needs more digits.
const (
	maxQuoteIntDigits  = 30
	maxQuoteFracDigits = 18
)

var quoteAmountPattern = regexp.MustCompile(fmt.Sprintf(`^[0-9]{1,%d}(\.[0-9]{1,%d})?$`, maxQuoteIntDigits, maxQuoteFracDigits))

func parseQuoteAmount(raw json.RawMessage) (decimal.Decimal, error) {
	if len(raw) == 0 {
		return decimal.Decimal{}, errors.New("quote_amount is required")
	}
	var text string
	if err := json.Unmarshal(raw, &text); err != nil {
		return decimal.Decimal{}, errors.New("quote_amount must be a decimal string")
	}
	if strings.HasPrefix(text, "-") {
		return decimal.Decimal{}, errors.New("quote_amount must be positive")
	}
	if !quoteAmountPattern.MatchString(text) {
		return decimal.Decimal{}, fmt.Errorf("quote_amount %q is not a plain decimal of at most %d integer and %d fraction digits",
			text, maxQuoteIntDigits, maxQuoteFracDigits)
	}
	amount, err := decimal.NewFromString(text)
	if err != nil {
		return decimal.Decimal{}, fmt.Errorf("quote_amount %q is not a number", text)
	}
	if !amount.IsPositive() {
		return decimal.Decimal{}, errors.New("quote_amount must be positive")
	}
	return amount, nil
}

func (DCA) Type() string { return DCAType }

func (DCA) Inputs(rule *entity.Rule) (Inputs, error) {
	cfg, err := parseDCAConfig(rule.Configuration)
	if err != nil {
		return Inputs{}, err
	}
	return Inputs{
		AccountID: cfg.AccountID,
		Pairs:     []Pair{{Asset: cfg.AssetID, Quote: cfg.QuoteAssetID}},
	}, nil
}

// Plan is one buy of quote_amount at the direct pair's latest price.
func (DCA) Plan(rule *entity.Rule, snap Snapshot) (TradePlan, error) {
	cfg, err := parseDCAConfig(rule.Configuration)
	if err != nil {
		return TradePlan{}, err
	}
	price, ok := snap.Prices[Pair{Asset: cfg.AssetID, Quote: cfg.QuoteAssetID}]
	if !ok {
		return TradePlan{}, fmt.Errorf("no price for %s in %s", cfg.AssetID, cfg.QuoteAssetID)
	}
	// Both durations are taken forward: Sub saturates, and a negated
	// saturated duration stays negative.
	if age := snap.Now.Sub(price.AsOf); age > maxDCAPriceAge {
		return TradePlan{}, fmt.Errorf("price of %s in %s is %s old, more than %s", cfg.AssetID, cfg.QuoteAssetID, age.Round(time.Second), maxDCAPriceAge)
	}
	if lead := price.AsOf.Sub(snap.Now); lead > maxPriceLead {
		return TradePlan{}, fmt.Errorf("price of %s in %s is dated %s after now", cfg.AssetID, cfg.QuoteAssetID, lead.Round(time.Second))
	}
	if !price.Value.IsPositive() {
		return TradePlan{}, fmt.Errorf("price of %s in %s must be positive, got %s", cfg.AssetID, cfg.QuoteAssetID, price.Value)
	}
	return TradePlan{Trades: []Trade{{
		Side:         SideBuy,
		AssetID:      cfg.AssetID,
		QuoteAssetID: cfg.QuoteAssetID,
		QuoteAmount:  cfg.QuoteAmount,
		Price:        price.Value,
		PriceAsOf:    price.AsOf,
	}}}, nil
}

// Simulate reports the planned buy against the quote balance on hand. Floats
// appear only here, at the proto boundary.
func (DCA) Simulate(_ *entity.Rule, snap Snapshot, plan TradePlan) *apiv1.SimulationResult {
	// A DCA plan holds exactly one trade; anything else describes nothing.
	sim := &apiv1.DCASimulation{}
	if len(plan.Trades) == 1 {
		t := plan.Trades[0]
		available := decimal.Zero
		if q, found, _ := snap.Holding(t.QuoteAssetID); found {
			available = q.Amount
		}
		sim = &apiv1.DCASimulation{
			AvailableBalance:        available.InexactFloat64(),
			DcaAmount:               t.QuoteAmount.InexactFloat64(),
			TargetAssetId:           t.AssetID,
			EstimatedPurchaseAmount: baseBought(t, baseDecimals(snap, t.AssetID)).InexactFloat64(),
			EstimatedPrice:          t.Price.InexactFloat64(),
			SufficientFunds:         available.GreaterThanOrEqual(t.QuoteAmount),
		}
	}
	return &apiv1.SimulationResult{
		EstimatedSteps: int32(len(plan.Trades)), // #nosec G115 -- a DCA plan holds one trade
		RuleResult:     &apiv1.SimulationResult_Dca{Dca: sim},
	}
}
