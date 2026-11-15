package execution

import (
	"fmt"
	"strconv"
	"strings"

	apiv1 "github.com/foxcool/greedy-eye/api/v1"
	"github.com/shopspring/decimal"
)

// Account data keys of a trading policy. They sit in accounts.data next to
// the account's other settings (quota, period) and are not secrets.
const (
	PolicyMaxTradeAmount = "max_trade_amount"
	PolicyAllowedAssets  = "allowed_assets"
	PolicyMaxSlippageBps = "max_slippage_bps"
)

const (
	// DefaultSlippageBps is the slippage a venue allows when the account
	// names none: 0.5%.
	DefaultSlippageBps = 50
	// slippageCeilingBps is the most an account may allow: past 3% a minimum
	// output protects little against a moved pool.
	slippageCeilingBps = 300
)

// Policy bounds what a rule may trade on one account. It is checked between
// Plan and Submit for every venue; a venue that signs on its own carries a
// second copy (ADR-012).
type Policy struct {
	unlimited     bool
	maxTrade      decimal.Decimal
	allowedAssets map[string]bool
	// SlippageBps is what a venue that quotes before it trades allows below
	// the quote.
	SlippageBps uint32
}

// ParsePolicy reads an account's policy. A manual account trades on paper,
// where nothing real is at stake, and without policy keys it has no limits.
// Any other account must name a per-trade maximum and the assets it may
// trade: real money starts refused, not allowed.
func ParsePolicy(accountType apiv1.AccountType, data map[string]string) (Policy, error) {
	maxRaw := strings.TrimSpace(data[PolicyMaxTradeAmount])
	assetsRaw := strings.TrimSpace(data[PolicyAllowedAssets])
	slipRaw := strings.TrimSpace(data[PolicyMaxSlippageBps])

	if maxRaw == "" && assetsRaw == "" && slipRaw == "" {
		if accountType == apiv1.AccountType_ACCOUNT_TYPE_MANUAL {
			return Policy{unlimited: true, SlippageBps: DefaultSlippageBps}, nil
		}
		return Policy{}, fmt.Errorf("account has no trading policy: set %s and %s", PolicyMaxTradeAmount, PolicyAllowedAssets)
	}
	if maxRaw == "" || assetsRaw == "" {
		return Policy{}, fmt.Errorf("trading policy needs both %s and %s", PolicyMaxTradeAmount, PolicyAllowedAssets)
	}

	maxTrade, err := decimal.NewFromString(maxRaw)
	if err != nil || !maxTrade.IsPositive() {
		return Policy{}, fmt.Errorf("%s %q is not a positive decimal", PolicyMaxTradeAmount, maxRaw)
	}
	allowed := map[string]bool{}
	for _, id := range strings.Split(assetsRaw, ",") {
		if id = strings.TrimSpace(id); id != "" {
			allowed[id] = true
		}
	}
	if len(allowed) == 0 {
		return Policy{}, fmt.Errorf("%s names no asset", PolicyAllowedAssets)
	}
	slippage := uint64(DefaultSlippageBps)
	if slipRaw != "" {
		if slippage, err = strconv.ParseUint(slipRaw, 10, 32); err != nil {
			return Policy{}, fmt.Errorf("%s %q is not a whole number of basis points", PolicyMaxSlippageBps, slipRaw)
		}
	}
	if slippage > slippageCeilingBps {
		return Policy{}, fmt.Errorf("%s %d is above the %d ceiling", PolicyMaxSlippageBps, slippage, slippageCeilingBps)
	}
	return Policy{maxTrade: maxTrade, allowedAssets: allowed, SlippageBps: uint32(slippage)}, nil // #nosec G115 -- at most slippageCeilingBps
}

// Check refuses a trade the policy does not allow, saying which limit it hit.
func (p Policy) Check(t Trade) error {
	if p.unlimited {
		return nil
	}
	if t.QuoteAmount.GreaterThan(p.maxTrade) {
		return fmt.Errorf("policy: trade of %s is above the account's %s of %s", t.QuoteAmount, PolicyMaxTradeAmount, p.maxTrade)
	}
	for _, id := range []string{t.AssetID, t.QuoteAssetID} {
		if !p.allowedAssets[id] {
			return fmt.Errorf("policy: asset %s is not in the account's %s", id, PolicyAllowedAssets)
		}
	}
	return nil
}
