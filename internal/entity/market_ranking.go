package entity

import (
	"context"
	"time"
)

// RankedCoin is one coin of a provider's market-capitalisation ranking.
//
// It is not an Asset and is never stored as one: a ranking names coins nobody
// here holds, and the catalogue is what valuation and scam scoring iterate.
// Values are float64 for drawing, not for accounting.
type RankedCoin struct {
	// CoinID is the provider's own id for the coin ("bitcoin").
	CoinID string
	Symbol string
	Name   string
	// Rank is 1-based and fits an int32, the API's rank type.
	Rank      int
	Price     float64
	MarketCap float64
	Volume24h float64
	// Signed price change in percent over each window; nil when the source
	// did not report one.
	Change24h *float64
	Change7d  *float64
	Change30d *float64
}

// MarketRanking is one read of a ranking: the coins in rank order and when
// they were read from the source.
type MarketRanking struct {
	Source string
	Coins  []RankedCoin
	ReadAt time.Time
}

// MarketRanker reads the top of a market by capitalisation.
type MarketRanker interface {
	TopByMarketCap(ctx context.Context, n int) (MarketRanking, error)
	// RankedCoinID reports which ranked coin a catalogue asset is, by the same
	// identity the provider prices it under; ok is false when it prices the
	// asset another way or not at all.
	RankedCoinID(a *Asset) (coinID string, ok bool)
}
