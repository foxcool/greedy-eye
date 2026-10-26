//go:build smoke

package smoke_test

// Cross-domain edges a handler reaches through another service's client.
// In a monolith the client is the local handler; with remotes: set it is a
// network hop, and these same tests then walk the wire. The value and heatmap
// tests price only in the caller's own display currency, so a settings hop
// that lost X-User-Id falls back to dollars and fails them.
//
// Not covered: marketdata -> portfolio (holding provenance). It is asked only
// by SyncAccount with a wallet provider or by the rescore cron, and smoke has
// neither a provider key nor a cron it can wait for deterministically.

import (
	"context"
	"testing"
	"time"

	"connectrpc.com/connect"
	v1 "github.com/foxcool/greedy-eye/api/v1"
	"github.com/foxcool/greedy-eye/internal/pricefresh"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// edgeCoin is priced only in EUR, so a total in dollars cannot price it: the
// display currency has to come from the caller's own settings.
const edgeCoin = "SMK"

// pricedCoin seeds EUR and edgeCoin with two quotes inside the last day,
// 100.00 then 110.00 EUR, and returns their ids.
func pricedCoin(t *testing.T, userID string) (eurID, coinID string) {
	t.Helper()
	ctx := context.Background()
	admin := newMDAdminClient(userID)

	eur, coin := "EUR", edgeCoin
	eurResp, err := admin.CreateAsset(ctx, connect.NewRequest(&v1.CreateAssetRequest{
		Asset: &v1.Asset{Name: "Euro", Symbol: &eur, Type: v1.AssetType_ASSET_TYPE_FOREX},
	}))
	require.NoError(t, err)
	coinResp, err := admin.CreateAsset(ctx, connect.NewRequest(&v1.CreateAssetRequest{
		Asset: &v1.Asset{Name: "Smoke Coin", Symbol: &coin, Type: v1.AssetType_ASSET_TYPE_CRYPTOCURRENCY},
	}))
	require.NoError(t, err)
	eurID, coinID = eurResp.Msg.GetId(), coinResp.Msg.GetId()

	now := time.Now()
	for _, q := range []struct {
		last string
		at   time.Time
	}{
		{"10000", now.Add(-23 * time.Hour)},
		{"11000", now.Add(-time.Minute)},
	} {
		_, err := admin.CreatePrice(ctx, connect.NewRequest(&v1.CreatePriceRequest{Price: &v1.Price{
			SourceId:    "smoke",
			AssetId:     coinID,
			BaseAssetId: eurID,
			Interval:    "1h",
			Decimals:    2,
			Last:        q.last,
			Timestamp:   timestamppb.New(q.at),
		}}))
		require.NoError(t, err)
	}
	return eurID, coinID
}

// displayInEUR stores the caller's valuation setting naming EUR.
func displayInEUR(t *testing.T, userID string) {
	t.Helper()
	value, err := pricefresh.Policy{DisplayCurrency: "EUR"}.Encode()
	require.NoError(t, err)
	_, err = newSettingsClient(userID).SetSetting(context.Background(), connect.NewRequest(&v1.SetSettingRequest{
		Key:   pricefresh.SettingKey,
		Value: string(value),
	}))
	require.NoError(t, err)
}

// holdTwoCoins imports 2 edgeCoin into a fresh manual account and returns the
// portfolio id.
func holdTwoCoins(t *testing.T, userID string) string {
	t.Helper()
	ctx := context.Background()
	client := newPortfolioClient(userID)

	portResp, err := client.CreatePortfolio(ctx, connect.NewRequest(&v1.CreatePortfolioRequest{
		Portfolio: &v1.Portfolio{Name: "Edges"},
	}))
	require.NoError(t, err)
	portID := portResp.Msg.GetId()

	acctResp, err := client.CreateAccount(ctx, connect.NewRequest(&v1.CreateAccountRequest{
		Account: &v1.Account{Name: "Manual", Type: v1.AccountType_ACCOUNT_TYPE_MANUAL, PortfolioId: &portID},
	}))
	require.NoError(t, err)

	importResp, err := client.ImportPositions(ctx, connect.NewRequest(&v1.ImportPositionsRequest{
		AccountId: acctResp.Msg.GetId(),
		Positions: []*v1.ImportPositionItem{{
			Symbol:    edgeCoin,
			Amount:    "2",
			AssetType: v1.AssetType_ASSET_TYPE_CRYPTOCURRENCY,
		}},
	}))
	require.NoError(t, err)
	require.Equal(t, int32(1), importResp.Msg.GetCreated(), "import result: %v", importResp.Msg.GetItems())
	return portID
}

// TestEdge_PortfolioToMarketData_Import: ImportPositions resolves a symbol
// through marketdata's FindOrCreateAsset.
func TestEdge_PortfolioToMarketData_Import(t *testing.T) {
	resetDB(t)
	ctx := context.Background()
	userID := newUserID()
	_, coinID := pricedCoin(t, userID)

	portID := holdTwoCoins(t, userID)

	holdings, err := newPortfolioClient(userID).ListHoldings(ctx, connect.NewRequest(&v1.ListHoldingsRequest{
		PortfolioId: &portID,
	}))
	require.NoError(t, err)
	require.Len(t, holdings.Msg.GetHoldings(), 1)
	assert.Equal(t, coinID, holdings.Msg.GetHoldings()[0].GetAssetId(),
		"the symbol must resolve to the existing catalogue row, not a new one")
}

// TestEdge_PortfolioToSettingsAndMarketData_Value: CalculatePortfolioValue
// reads the caller's display currency from settings and prices from marketdata.
func TestEdge_PortfolioToSettingsAndMarketData_Value(t *testing.T) {
	resetDB(t)
	ctx := context.Background()
	userID := newUserID()
	eurID, _ := pricedCoin(t, userID)
	portID := holdTwoCoins(t, userID)
	displayInEUR(t, userID)

	resp, err := newPortfolioClient(userID).CalculatePortfolioValue(ctx, connect.NewRequest(&v1.CalculatePortfolioValueRequest{
		PortfolioId: portID,
	}))
	require.NoError(t, err)
	assert.Equal(t, eurID, resp.Msg.GetQuoteAssetId(), "display currency must come from the caller's setting")
	assert.Equal(t, "22000", resp.Msg.GetTotalValueAmount(), "2 x 110.00 EUR")
	assert.Equal(t, uint32(2), resp.Msg.GetDecimals())
	assert.Equal(t, uint32(1), resp.Msg.GetCoverage().GetPricedCount())
}

// TestEdge_AnalyticsToAll_Heatmap: GetHeatmap reads holdings from portfolio,
// the display currency from settings, prices and history from marketdata.
func TestEdge_AnalyticsToAll_Heatmap(t *testing.T) {
	resetDB(t)
	ctx := context.Background()
	userID := newUserID()
	eurID, coinID := pricedCoin(t, userID)
	portID := holdTwoCoins(t, userID)
	displayInEUR(t, userID)

	resp, err := newAnalyticsClient(userID).GetHeatmap(ctx, connect.NewRequest(&v1.GetHeatmapRequest{
		Scope:   v1.HeatmapScope_HEATMAP_SCOPE_PORTFOLIO,
		ScopeId: portID,
		Window:  v1.HeatmapWindow_HEATMAP_WINDOW_24H,
	}))
	require.NoError(t, err)
	assert.Equal(t, eurID, resp.Msg.GetQuoteAssetId(), "display currency must come from the caller's setting")

	var leaf *v1.HeatmapNode
	for _, n := range resp.Msg.GetNodes() {
		if n.GetAssetId() == coinID {
			leaf = n
		}
	}
	require.NotNil(t, leaf, "heatmap must hold the imported coin, nodes: %v", resp.Msg.GetNodes())
	assert.Equal(t, edgeCoin, leaf.GetLabel())
	assert.InDelta(t, 220.0, leaf.GetSize(), 1e-9, "2 x 110.00 EUR")
	assert.InDelta(t, 10.0, leaf.GetColorValue(), 1e-9, "100.00 -> 110.00 within the window")
}
