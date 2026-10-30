package main

import (
	"github.com/foxcool/greedy-eye/api/v1/apiv1connect"
	"github.com/foxcool/greedy-eye/internal/pricefresh"
	"github.com/foxcool/greedy-eye/internal/quoting"
	"github.com/foxcool/greedy-eye/internal/service/analytics"
	"github.com/foxcool/greedy-eye/internal/service/portfolio"
)

// A domain moves to remotes: only if every port its callers read is satisfied
// by the generated client as-is. Checked here, against the client types
// themselves, so it holds whatever the wiring in main passes around; the
// constructors return these interfaces, so the generated structs are covered.
var (
	_ portfolio.MarketDataClient = apiv1connect.MarketDataServiceClient(nil)
	_ portfolio.SettingsClient   = apiv1connect.SettingsServiceClient(nil)
	_ analytics.MarketDataClient = apiv1connect.MarketDataServiceClient(nil)
	_ analytics.PortfolioClient  = apiv1connect.PortfolioServiceClient(nil)
	_ quoting.PriceReader        = apiv1connect.MarketDataServiceClient(nil)
	_ quoting.AssetResolver      = apiv1connect.MarketDataServiceClient(nil)
	_ pricefresh.SettingReader   = apiv1connect.SettingsServiceClient(nil)
)
