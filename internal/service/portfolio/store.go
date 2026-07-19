package portfolio

import (
	"context"

	"connectrpc.com/connect"
	apiv1 "github.com/foxcool/greedy-eye/api/v1"
	"github.com/foxcool/greedy-eye/internal/entity"
)

// MarketDataClient is the subset of MarketDataServiceClient that Portfolio needs.
// Satisfied by *marketdata.Handler (monolith) and apiv1connect.MarketDataServiceClient (microservice).
// No adapters required — both implement these methods with identical signatures.
type MarketDataClient interface {
	CreateAsset(context.Context, *connect.Request[apiv1.CreateAssetRequest]) (*connect.Response[apiv1.Asset], error)
	GetAsset(context.Context, *connect.Request[apiv1.GetAssetRequest]) (*connect.Response[apiv1.Asset], error)
	FindOrCreateAsset(context.Context, *connect.Request[apiv1.FindOrCreateAssetRequest]) (*connect.Response[apiv1.FindOrCreateAssetResponse], error)
	ListAssets(context.Context, *connect.Request[apiv1.ListAssetsRequest]) (*connect.Response[apiv1.ListAssetsResponse], error)
	GetLatestPrice(context.Context, *connect.Request[apiv1.GetLatestPriceRequest]) (*connect.Response[apiv1.Price], error)
	ListPriceHistory(context.Context, *connect.Request[apiv1.ListPriceHistoryRequest]) (*connect.Response[apiv1.ListPriceHistoryResponse], error)
	FetchExternalPrices(context.Context, *connect.Request[apiv1.FetchExternalPricesRequest]) (*connect.Response[apiv1.FetchExternalPricesResponse], error)
}

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
	// DeleteAccountWithHoldings removes the account and its holdings
	// atomically. Transaction history is never removed this way.
	DeleteAccountWithHoldings(ctx context.Context, id string) error
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
	// UserID scopes results to holdings whose owning portfolio (own or
	// inherited from the account) belongs to this user. Holdings outside
	// any portfolio are scoped by account owner instead.
	UserID      string
	PortfolioID string
	AccountID   string
	AssetID     string
	PageSize    int
	PageToken   string
	// HideExcluded filters out holdings where excluded=true when set to true.
	HideExcluded bool
}

// ListTransactionsOpts contains options for listing transactions.
type ListTransactionsOpts struct {
	// UserID scopes results to transactions of accounts owned by this user.
	UserID    string
	AccountID string
	AssetID   string
	Type      entity.TransactionType
	Status    entity.TransactionStatus
	PageSize  int
	PageToken string
}
