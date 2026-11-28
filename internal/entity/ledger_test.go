package entity

import (
	"testing"
	"time"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
)

func validTrade() *LedgerEvent {
	return &LedgerEvent{
		AccountID:  "acct",
		Kind:       LedgerKindTrade,
		ExecutedAt: time.Date(2026, 3, 14, 12, 0, 0, 0, time.UTC),
		Source:     string(SourceLLMImport),
		Legs: []LedgerLeg{
			{AssetID: "btc", Direction: LedgerIn, Role: LedgerRolePrincipal, Amount: decimal.NewFromInt(25), Decimals: 4},
			{AssetID: "usdt", Direction: LedgerOut, Role: LedgerRoleCounter, Amount: decimal.NewFromInt(100), Decimals: 0},
		},
	}
}

func TestLedgerEventValidate(t *testing.T) {
	assert.NoError(t, validTrade().Validate())

	cases := map[string]func(e *LedgerEvent){
		"no account":         func(e *LedgerEvent) { e.AccountID = "" },
		"unknown kind":       func(e *LedgerEvent) { e.Kind = "swap" },
		"no executed_at":     func(e *LedgerEvent) { e.ExecutedAt = time.Time{} },
		"unknown source":     func(e *LedgerEvent) { e.Source = "sync" },
		"sync w/o provider":  func(e *LedgerEvent) { e.Source = "sync:" },
		"no legs":            func(e *LedgerEvent) { e.Legs = nil },
		"leg without asset":  func(e *LedgerEvent) { e.Legs[0].AssetID = "" },
		"leg bad direction":  func(e *LedgerEvent) { e.Legs[0].Direction = "both" },
		"leg bad role":       func(e *LedgerEvent) { e.Legs[0].Role = "tip" },
		"leg zero amount":    func(e *LedgerEvent) { e.Legs[0].Amount = decimal.Zero },
		"leg negative":       func(e *LedgerEvent) { e.Legs[0].Amount = decimal.NewFromInt(-1) },
		"leg fractional raw": func(e *LedgerEvent) { e.Legs[0].Amount = decimal.RequireFromString("2.5") },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			e := validTrade()
			mutate(e)
			assert.Error(t, e.Validate())
		})
	}

	e := validTrade()
	e.Source = LedgerSyncSource("binance")
	assert.NoError(t, e.Validate(), "a provider-named sync source is valid")
}

// An income event says what it paid; no other event may claim an income kind.
func TestLedgerEventValidate_IncomeKind(t *testing.T) {
	event := func(kind LedgerEventKind, income IncomeKind) *LedgerEvent {
		return &LedgerEvent{
			AccountID: "acct", Kind: kind, IncomeKind: income, ExecutedAt: time.Now(), Source: "manual",
			Legs: []LedgerLeg{{AssetID: "rub", Direction: LedgerIn, Role: LedgerRoleIncome, Amount: decimal.NewFromInt(100)}},
		}
	}
	assert.NoError(t, event(LedgerKindIncome, IncomeCoupon).Validate())
	assert.Error(t, event(LedgerKindIncome, "").Validate(), "income needs a kind")
	assert.Error(t, event(LedgerKindIncome, "bonus").Validate(), "an unknown kind is refused")
	assert.Error(t, event(LedgerKindDeposit, IncomeInterest).Validate(), "a deposit is not income")
}

func TestLedgerEventValidate_Payer(t *testing.T) {
	event := func(kind LedgerEventKind, income IncomeKind) *LedgerEvent {
		return &LedgerEvent{
			AccountID: "acct", Kind: kind, IncomeKind: income, PayerAssetID: "share", ExecutedAt: time.Now(), Source: "manual",
			Legs: []LedgerLeg{{AssetID: "rub", Direction: LedgerIn, Role: LedgerRoleIncome, Amount: decimal.NewFromInt(100)}},
		}
	}
	assert.NoError(t, event(LedgerKindIncome, IncomeDividend).Validate())
	assert.Error(t, event(LedgerKindIncome, IncomeTax).Validate(), "no paper paid a tax that names no income")
	assert.Error(t, event(LedgerKindTrade, "").Validate(), "only income has a payer")
}
