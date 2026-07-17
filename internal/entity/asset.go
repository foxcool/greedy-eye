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
	Quote     string
	Tags      []string
	CreatedAt time.Time
	UpdatedAt time.Time
}
