package analytics

import (
	"context"

	"connectrpc.com/connect"
	apiv1 "github.com/foxcool/greedy-eye/api/v1"
	"github.com/foxcool/greedy-eye/internal/entity"
	"github.com/foxcool/greedy-eye/internal/service/portfolio"
)

// MarketDataClient is the subset of MarketDataServiceClient that Analytics needs.
// Satisfied by *marketdata.Handler (monolith) and apiv1connect.MarketDataServiceClient
// (microservice), same as portfolio.MarketDataClient.
type MarketDataClient interface {
	GetAsset(context.Context, *connect.Request[apiv1.GetAssetRequest]) (*connect.Response[apiv1.Asset], error)
	GetLatestPrice(context.Context, *connect.Request[apiv1.GetLatestPriceRequest]) (*connect.Response[apiv1.Price], error)
	ListPriceHistory(context.Context, *connect.Request[apiv1.ListPriceHistoryRequest]) (*connect.Response[apiv1.ListPriceHistoryResponse], error)
}

// Store defines the data access contract for AnalyticsService. It reuses the
// portfolio list options so postgres.PortfolioStore satisfies it as-is.
type Store interface {
	GetPortfolio(ctx context.Context, id string) (*entity.Portfolio, error)
	GetAccount(ctx context.Context, id string) (*entity.Account, error)
	ListHoldings(ctx context.Context, opts portfolio.ListHoldingsOpts) ([]*entity.Holding, string, error)
}
