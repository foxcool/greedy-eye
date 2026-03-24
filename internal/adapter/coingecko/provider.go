package coingecko

import (
	"context"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/foxcool/greedy-eye/internal/entity"
)

const (
	// ProviderName is the canonical source identifier for CoinGecko prices.
	ProviderName = "coingecko"

	sourceID    = ProviderName
	baseAssetID = "usd"
	priceDecimals = uint32(8)
	divisor     = 1e8
	interval    = "latest"
)

// Provider adapts *Client to marketdata.PriceProvider.
type Provider struct {
	client *Client
}

// NewProvider wraps a *Client as a price provider.
func NewProvider(c *Client) *Provider {
	return &Provider{client: c}
}

// FetchPrices fetches prices from CoinGecko and returns them as StoredPrice records.
// CoinGecko coin IDs are derived from asset symbols in lowercase (e.g., "BTC" → "btc").
// This requires asset symbols to match CoinGecko coin IDs.
func (p *Provider) FetchPrices(ctx context.Context, assets []*entity.Asset) ([]entity.StoredPrice, error) {
	if len(assets) == 0 {
		return nil, nil
	}

	// Build coinID → assetID map and collect CoinGecko IDs.
	coinToAsset := make(map[string]string, len(assets))
	coinIDs := make([]string, 0, len(assets))
	for _, a := range assets {
		coinID := strings.ToLower(a.Symbol)
		coinToAsset[coinID] = a.ID
		coinIDs = append(coinIDs, coinID)
	}

	raw, err := p.client.GetMultiplePrices(ctx, coinIDs, baseAssetID)
	if err != nil {
		return nil, fmt.Errorf("coingecko: %w", err)
	}

	now := time.Now()
	result := make([]entity.StoredPrice, 0, len(raw))
	for coinID, pd := range raw {
		assetID, ok := coinToAsset[coinID]
		if !ok {
			continue
		}
		last := int64(math.Round(pd.Price * divisor))
		high := int64(math.Round(pd.High24h * divisor))
		low := int64(math.Round(pd.Low24h * divisor))
		ts := pd.Timestamp
		if ts.IsZero() {
			ts = now
		}
		result = append(result, entity.StoredPrice{
			SourceID:    sourceID,
			AssetID:     assetID,
			BaseAssetID: baseAssetID,
			Interval:    interval,
			Decimals:    priceDecimals,
			Last:        last,
			High:        &high,
			Low:         &low,
			Timestamp:   ts,
		})
	}
	return result, nil
}
