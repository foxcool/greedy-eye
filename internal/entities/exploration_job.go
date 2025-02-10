package entities

import "github.com/shopspring/decimal"

type ExplorationJob struct {
	FromAsset *Asset
	ToAsset   *Asset

	MinimumInterest   *decimal.Decimal
	MaximumFromAmount *decimal.Decimal
	FromAmountStep    *decimal.Decimal

	// Last found trading opportunity
	CurrentOpportunity *TradingOpportunity
	// Opportunity with best profit
	BestOpportunity *TradingOpportunity
}
