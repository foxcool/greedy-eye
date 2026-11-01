package automation

import (
	"context"

	"connectrpc.com/connect"
	apiv1 "github.com/foxcool/greedy-eye/api/v1"
)

// PortfolioClient is the subset of PortfolioServiceClient that Automation
// needs. Satisfied by *portfolio.Handler (monolith) and
// apiv1connect.PortfolioServiceClient (microservice). Ownership of accounts and
// holdings is enforced by the portfolio service, not here.
type PortfolioClient interface {
	GetAccount(context.Context, *connect.Request[apiv1.GetAccountRequest]) (*connect.Response[apiv1.Account], error)
	ListHoldings(context.Context, *connect.Request[apiv1.ListHoldingsRequest]) (*connect.Response[apiv1.ListHoldingsResponse], error)
}

// MarketDataClient is the subset of MarketDataServiceClient that Automation
// needs, satisfied the same two ways.
type MarketDataClient interface {
	GetLatestPrice(context.Context, *connect.Request[apiv1.GetLatestPriceRequest]) (*connect.Response[apiv1.Price], error)
}
