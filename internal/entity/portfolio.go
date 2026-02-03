package entity

import (
	"encoding/json"
	"time"
)

// Portfolio represents a collection of holdings managed by a user.
type Portfolio struct {
	ID          string
	UserID      string
	Name        string
	Description string
	Data        map[string]json.RawMessage // Flexible metadata
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// AccountType represents the type of financial account.
type AccountType int32

const (
	AccountTypeUnspecified AccountType = iota
	AccountTypeWallet
	AccountTypeExchange
	AccountTypeBank
	AccountTypeBroker
)

// Account represents a user's connection to an external financial entity.
type Account struct {
	ID          string
	UserID      string
	Name        string
	Description string
	Type        AccountType
	Data        map[string]string // API keys, identifiers, etc.
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// Holding represents a specific quantity of an Asset held within an Account.
type Holding struct {
	ID          string
	Amount      int64
	Decimals    uint32
	AssetID     string
	AccountID   string
	PortfolioID string // Optional
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// TransactionType represents the type of financial transaction.
type TransactionType int32

const (
	TransactionTypeUnspecified TransactionType = iota
	TransactionTypeExtended                    // Complex transactions: DeFi, NFT, Governance, etc.
	TransactionTypeTrade                       // Exchanging one asset for another
	TransactionTypeTransfer                    // Transferring asset between accounts
	TransactionTypeDeposit                     // Depositing asset into an account
	TransactionTypeWithdrawal                  // Withdrawing asset from an account
)

// TransactionStatus represents the current status of a transaction.
type TransactionStatus int32

const (
	TransactionStatusUnspecified TransactionStatus = iota
	TransactionStatusPending
	TransactionStatusProcessing
	TransactionStatusCompleted
	TransactionStatusFailed
	TransactionStatusCancelled
)

// Transaction represents a financial event involving assets, accounts, and portfolios.
type Transaction struct {
	ID        string
	Type      TransactionType
	Status    TransactionStatus
	AccountID string
	AssetID   string // Optional, for linking to specific asset
	Data      map[string]string
	CreatedAt time.Time
	UpdatedAt time.Time
}
