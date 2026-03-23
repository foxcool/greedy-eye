package coingecko

import (
	"context"
	"fmt"
	"math"
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
func (p *Provider) FetchPrices(ctx context.Context, assetIDs []string) ([]entity.StoredPrice, error) {
	raw, err := p.client.GetMultiplePrices(ctx, assetIDs, baseAssetID)
	if err != nil {
		return nil, fmt.Errorf("coingecko: %w", err)
	}

	now := time.Now()
	result := make([]entity.StoredPrice, 0, len(raw))
	for _, pd := range raw {
		last := int64(math.Round(pd.Price * divisor))
		high := int64(math.Round(pd.High24h * divisor))
		low := int64(math.Round(pd.Low24h * divisor))
		ts := pd.Timestamp
		if ts.IsZero() {
			ts = now
		}
		result = append(result, entity.StoredPrice{
			SourceID:    sourceID,
			AssetID:     pd.AssetID,
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
