package entity

import (
	"encoding/json"
	"fmt"
	"slices"
	"time"

	"github.com/shopspring/decimal"
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
	AccountTypeService // Pure data-provider API key (Moralis, Etherscan, ...); no holdings of its own
	AccountTypeManual  // No connector or credentials; positions are entered by hand or imported
)

// String returns the canonical lowercase name of the account type.
func (t AccountType) String() string {
	switch t {
	case AccountTypeWallet:
		return "wallet"
	case AccountTypeExchange:
		return "exchange"
	case AccountTypeBank:
		return "bank"
	case AccountTypeBroker:
		return "broker"
	case AccountTypeService:
		return "service"
	case AccountTypeManual:
		return "manual"
	default:
		return "unspecified"
	}
}

// AccountCapability names an operation the account credentials allow.
type AccountCapability string

const (
	CapabilityPortfolioSync   AccountCapability = "portfolio_sync"
	CapabilityTrading         AccountCapability = "trading"
	CapabilityMarketData      AccountCapability = "market_data"
	CapabilityOnchainLookup   AccountCapability = "onchain_lookup"
	CapabilityManualPositions AccountCapability = "manual_positions"
)

// allowedCapabilities maps each account type to the capabilities its
// credentials can grant.
var allowedCapabilities = map[AccountType][]AccountCapability{
	AccountTypeWallet:   {CapabilityPortfolioSync},
	AccountTypeExchange: {CapabilityPortfolioSync, CapabilityTrading, CapabilityMarketData},
	AccountTypeBank:     {CapabilityPortfolioSync},
	AccountTypeBroker:   {CapabilityPortfolioSync, CapabilityTrading, CapabilityMarketData},
	AccountTypeService:  {CapabilityMarketData, CapabilityOnchainLookup},
	AccountTypeManual:   {CapabilityManualPositions},
}

// systemScopeableCapabilities are user-agnostic: results don't belong to the
// credential owner, so an admin may share them system-wide. portfolio_sync and
// trading always stay personal.
var systemScopeableCapabilities = []AccountCapability{
	CapabilityMarketData,
	CapabilityOnchainLookup,
}

// Account represents a user's connection to an external financial entity.
type Account struct {
	ID           string
	UserID       string
	Name         string
	Description  string
	Type         AccountType
	Data         map[string]string   // API keys, identifiers, etc.
	Capabilities []AccountCapability // What the account credentials allow
	SystemScopes []AccountCapability // Subset of Capabilities usable system-wide for any user; admin-managed
	PortfolioID  string              // Optional; holdings synced from this account belong to this portfolio by default
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

// ValidateCapabilities checks the capability invariants:
// capabilities must be allowed for the account type, and system scopes must be
// a subset of capabilities limited to user-agnostic ones.
func (a *Account) ValidateCapabilities() error {
	allowed := allowedCapabilities[a.Type]
	for _, c := range a.Capabilities {
		if !slices.Contains(allowed, c) {
			return fmt.Errorf("capability %q is not allowed for account type %q", c, a.Type)
		}
	}
	for _, s := range a.SystemScopes {
		if !slices.Contains(a.Capabilities, s) {
			return fmt.Errorf("system scope %q is not among account capabilities", s)
		}
		if !slices.Contains(systemScopeableCapabilities, s) {
			return fmt.Errorf("capability %q cannot be system-scoped", s)
		}
	}
	return nil
}

// ProvenanceSource records how a holding or transaction entered the system.
// It is stamped by the server at creation time and never changed afterwards.
type ProvenanceSource string

const (
	SourceSync      ProvenanceSource = "sync"       // Created by an automatic account sync
	SourceManual    ProvenanceSource = "manual"     // Entered by hand via CRUD API
	SourceLLMImport ProvenanceSource = "llm_import" // Created by a batch import (LLM-parsed export)
)

// Valid reports whether s is one of the known provenance sources.
func (s ProvenanceSource) Valid() bool {
	switch s {
	case SourceSync, SourceManual, SourceLLMImport:
		return true
	default:
		return false
	}
}

// Holding represents a specific quantity of an Asset held within an Account.
type Holding struct {
	ID          string
	Amount      decimal.Decimal // Raw integer balance scaled by Decimals (value = Amount / 10^Decimals); holds uint256.
	Decimals    uint32
	AssetID     string
	AccountID   string
	PortfolioID string // Optional; empty = inherit from account's portfolio_id
	// Chain names the network this amount sits on ("eth", "base", "solana").
	// Empty means the position is not chain-scoped (exchange balance, manual
	// entry) or was written before positions carried a chain — never "assume
	// Ethereum". Sync keys positions by (account, asset, chain), so the same
	// token on three chains is three rows.
	Chain     string
	Excluded  bool             // If true, holding is explicitly excluded from all portfolio calculations
	Source    ProvenanceSource // Creation provenance; immutable after create
	ImportID  string           // Optional batch id linking rows created by one import
	CreatedAt time.Time
	UpdatedAt time.Time
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
	Source    ProvenanceSource // Creation provenance; immutable after create
	ImportID  string           // Optional batch id linking rows created by one import
	CreatedAt time.Time
	UpdatedAt time.Time
}
