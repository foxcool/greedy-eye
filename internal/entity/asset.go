package entity

import "time"

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
