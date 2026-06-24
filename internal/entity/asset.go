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
type Asset struct {
	ID        string
	Name      string
	Symbol    string
	Type      AssetType
	Tags      []string
	CreatedAt time.Time
	UpdatedAt time.Time
}
