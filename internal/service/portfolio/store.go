package portfolio

import (
	"context"

	"github.com/foxcool/greedy-eye/internal/entity"
)

// Store defines the data access contract for PortfolioService.
type Store interface {
	// Portfolios
	CreatePortfolio(ctx context.Context, p *entity.Portfolio) (*entity.Portfolio, error)
	GetPortfolio(ctx context.Context, id string) (*entity.Portfolio, error)
	UpdatePortfolio(ctx context.Context, p *entity.Portfolio, fields []string) (*entity.Portfolio, error)
	DeletePortfolio(ctx context.Context, id string) error
	ListPortfolios(ctx context.Context, opts ListPortfoliosOpts) ([]*entity.Portfolio, string, error)

	// Accounts
	CreateAccount(ctx context.Context, a *entity.Account) (*entity.Account, error)
	GetAccount(ctx context.Context, id string) (*entity.Account, error)
	UpdateAccount(ctx context.Context, a *entity.Account, fields []string) (*entity.Account, error)
	DeleteAccount(ctx context.Context, id string) error
	ListAccounts(ctx context.Context, opts ListAccountsOpts) ([]*entity.Account, string, error)

	// Holdings
	CreateHolding(ctx context.Context, h *entity.Holding) (*entity.Holding, error)
	GetHolding(ctx context.Context, id string) (*entity.Holding, error)
	UpdateHolding(ctx context.Context, h *entity.Holding, fields []string) (*entity.Holding, error)
	DeleteHolding(ctx context.Context, id string) error
	ListHoldings(ctx context.Context, opts ListHoldingsOpts) ([]*entity.Holding, string, error)

	// Transactions
	CreateTransaction(ctx context.Context, t *entity.Transaction) (*entity.Transaction, error)
	GetTransaction(ctx context.Context, id string) (*entity.Transaction, error)
	UpdateTransaction(ctx context.Context, t *entity.Transaction, fields []string) (*entity.Transaction, error)
	ListTransactions(ctx context.Context, opts ListTransactionsOpts) ([]*entity.Transaction, string, error)
}

// ListPortfoliosOpts contains options for listing portfolios.
type ListPortfoliosOpts struct {
	UserID    string
	PageSize  int
	PageToken string
}

// ListAccountsOpts contains options for listing accounts.
type ListAccountsOpts struct {
	UserID    string
	Type      entity.AccountType
	PageSize  int
	PageToken string
}

// ListHoldingsOpts contains options for listing holdings.
type ListHoldingsOpts struct {
	PortfolioID string
	AccountID   string
	AssetID     string
	PageSize    int
	PageToken   string
}

// ListTransactionsOpts contains options for listing transactions.
type ListTransactionsOpts struct {
	AccountID string
	AssetID   string
	Type      entity.TransactionType
	Status    entity.TransactionStatus
	PageSize  int
	PageToken string
}
