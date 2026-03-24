package binance

import (
	"context"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/foxcool/greedy-eye/internal/entity"
)

const (
	// ProviderName is the canonical source identifier for Binance prices.
	ProviderName = "binance"

	sourceID      = ProviderName
	baseAssetID   = "usdt"
	priceDecimals = uint32(8)
	divisor       = 1e8
	interval      = "latest"
)

// Provider adapts *Client to marketdata.PriceProvider.
type Provider struct {
	client *Client
}

// NewProvider wraps a *Client as a Binance price provider.
func NewProvider(client *Client) *Provider {
	return &Provider{client: client}
}

// FetchPrices fetches current prices from Binance for the given assets.
// Binance symbols are derived from asset symbols as UPPER(symbol)+"USDT"
// (e.g., asset.Symbol="BTC" → "BTCUSDT").
// Assets whose symbols produce no Binance response are silently skipped.
func (p *Provider) FetchPrices(ctx context.Context, assets []*entity.Asset) ([]entity.StoredPrice, error) {
	if len(assets) == 0 {
		return nil, nil
	}

	// Build Binance symbol → assetID map and collect symbols to fetch.
	symbolToAsset := make(map[string]string, len(assets))
	symbols := make([]string, 0, len(assets))
	for _, a := range assets {
		sym := strings.ToUpper(a.Symbol) + "USDT"
		symbolToAsset[sym] = a.ID
		symbols = append(symbols, sym)
	}

	tickers, err := p.client.GetTickerPrices(ctx, symbols)
	if err != nil {
		return nil, fmt.Errorf("binance: %w", err)
	}

	now := time.Now()
	result := make([]entity.StoredPrice, 0, len(tickers))
	for _, t := range tickers {
		assetID, ok := symbolToAsset[t.Symbol]
		if !ok {
			continue
		}
		price, err := strconv.ParseFloat(t.Price, 64)
		if err != nil {
			continue
		}
		last := int64(math.Round(price * divisor))
		result = append(result, entity.StoredPrice{
			SourceID:    sourceID,
			AssetID:     assetID,
			BaseAssetID: baseAssetID,
			Interval:    interval,
			Decimals:    priceDecimals,
			Last:        last,
			Timestamp:   now,
		})
	}
	return result, nil
}
