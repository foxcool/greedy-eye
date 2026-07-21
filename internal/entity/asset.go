package entity

import (
	"strings"
	"time"
)

// NormalizeSymbol canonicalizes an asset symbol (trim + uppercase) so symbol lookups
// and the unique constraint are case-insensitive. Tickers are conventionally uppercase
// (BTC, USDC); applying this on every write keeps "usdc" and "USDC" the same asset.
func NormalizeSymbol(symbol string) string {
	return strings.ToUpper(strings.TrimSpace(symbol))
}

// MarketCrypto is the global market for crypto assets. Crypto trades everywhere at
// once, so one asset row represents e.g. BTC regardless of which provider priced it;
// mapping to provider-native identifiers is the adapter's concern.
const MarketCrypto = "crypto"

// NormalizeMarket canonicalizes a market identifier (trim + lowercase) so market
// lookups and the composite unique constraint are case-insensitive.
func NormalizeMarket(market string) string {
	return strings.ToLower(strings.TrimSpace(market))
}

// DefaultMarket returns the implied market for asset types that have a single
// global venue, or "" when the market must be provided explicitly (stocks, bonds
// and funds are listed per-exchange: nasdaq, moex, ...).
func DefaultMarket(t AssetType) string {
	switch t {
	case AssetTypeCryptocurrency:
		return MarketCrypto
	case AssetTypeForex:
		return "forex"
	default:
		return ""
	}
}

// AssetSymbol is a simple string representation of an asset symbol.
// Kept for backward compatibility with existing code.
type AssetSymbol string

// AssetType represents the category of a financial instrument.
type AssetType int32

const (
	AssetTypeUnspecified AssetType = iota
	AssetTypeCryptocurrency
	AssetTypeStock
	AssetTypeBond
	AssetTypeCommodity
	AssetTypeForex
	AssetTypeFund
)

// Asset represents a financial instrument for storage/service layers.
// Identity is the composite (Symbol, Market, Type): the same ticker may exist
// on different markets (AAPL on nasdaq vs an AAPL token on crypto).
type Asset struct {
	ID     string
	Name   string
	Symbol string
	Type   AssetType
	// Market is the listing market/venue: "crypto" (global), "nasdaq", "moex".
	Market string
	// Quote is the quote currency where applicable ("" when not meaningful).
	Quote string
	Tags  []string
	// IdentityVerdict is the scam-filtering axis-1 judgement:
	// "unknown" | "legit" | "suspect" | "scam" | "impersonation". Empty is
	// treated as "unknown". Derives holdings.excluded for scam/impersonation.
	IdentityVerdict string
	// IdentityScore is the last automated score (0..1); nil for user verdicts
	// or never-scored assets.
	IdentityScore *float64
	// IdentitySignals maps each signal that fired to its weight, for UI
	// explainability; nil until first scored.
	IdentitySignals map[string]float64
	// VerdictSource is the verdict provenance: "heuristic" | "provider:<name>" |
	// "curated" | "user:<id>". A user verdict is terminal for rescoring.
	VerdictSource string
	// VerdictSetAt is when the current verdict was set; nil if never.
	VerdictSetAt *time.Time
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

// AssetExternalRef maps an asset to its identifier in an external namespace
// (an on-chain contract/mint, a provider coin id, a broker FIGI). Identity on a
// chain is the contract, not the symbol, so sync resolves a token by (Source,
// Ref) and a scam clone of a real ticker cannot merge into the real asset.
type AssetExternalRef struct {
	ID        string
	AssetID   string
	Source    string // "onchain:<chain>", "coingecko", "cmc", ...
	Ref       string // contract address, mint, coin id
	Origin    string // "auto" | "manual" | "seed"
	CreatedAt time.Time
}

// External-ref origins. A manual link is terminal for auto-discovery.
const (
	RefOriginAuto   = "auto"
	RefOriginManual = "manual"
	RefOriginSeed   = "seed"
)

// OnchainSource builds the external-ref source namespace for a chain's
// contract/mint space, keeping the same address on two chains distinct.
func OnchainSource(chain string) string {
	return "onchain:" + strings.ToLower(strings.TrimSpace(chain))
}

// AssetRiskFlag is a situational-risk flag on a real asset (scam-filtering
// axis 2): exploit, depeg, freeze, delisting, sanctions. Unlike an identity
// verdict it does not exclude the asset from sums — the value is real. ReviewAt
// bounds a temporary flag so it does not hang forever.
type AssetRiskFlag struct {
	ID         string
	AssetID    string
	Kind       string
	Note       string
	ActionHint string
	ReviewAt   *time.Time
	SetBy      string
	CreatedAt  time.Time
}
