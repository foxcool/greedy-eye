package portfolio

import (
	"context"

	"connectrpc.com/connect"
	apiv1 "github.com/foxcool/greedy-eye/api/v1"
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
	GetPricingStatus(context.Context, *connect.Request[apiv1.GetPricingStatusRequest]) (*connect.Response[apiv1.GetPricingStatusResponse], error)
}

// SettingsClient is the subset of SettingsServiceClient that Portfolio needs:
// the rules a valuation runs under are the caller's own setting, not a server
// flag. Satisfied by *settings.Handler (monolith) and
// apiv1connect.SettingsServiceClient (microservice), same as MarketDataClient.
//
// A deployment without a settings service leaves this nil and every valuation
// uses the built-in defaults, which is why nothing here returns an error the
// caller has to handle.
type SettingsClient interface {
	GetSetting(context.Context, *connect.Request[apiv1.GetSettingRequest]) (*connect.Response[apiv1.GetSettingResponse], error)
}
