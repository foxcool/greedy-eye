package entity

import "github.com/shopspring/decimal"

type ExplorationJob struct {
	FromAsset *AssetSymbol
	ToAsset   *AssetSymbol

	MinimumInterest   *decimal.Decimal
	MaximumFromAmount *decimal.Decimal
	FromAmountStep    *decimal.Decimal

	// Last found trading opportunity
	CurrentOpportunity *TradingOpportunity
	// Opportunity with best profit
	BestOpportunity *TradingOpportunity
}
