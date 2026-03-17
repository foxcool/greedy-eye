package coingecko

import (
	"context"

	"github.com/foxcool/greedy-eye/internal/service/marketdata"
)

// Ensure *Client implements marketdata.CoinGeckoProvider.
var _ marketdata.CoinGeckoProvider = (*clientProvider)(nil)

// clientProvider wraps *Client and adapts it to the marketdata.CoinGeckoProvider interface.
type clientProvider struct {
	client *Client
}

// NewProvider wraps a *Client as a marketdata.CoinGeckoProvider.
func NewProvider(c *Client) marketdata.CoinGeckoProvider {
	return &clientProvider{client: c}
}

func (p *clientProvider) GetMultiplePrices(ctx context.Context, assetIDs []string, currency string) (map[string]*marketdata.CoinGeckoPriceData, error) {
	raw, err := p.client.GetMultiplePrices(ctx, assetIDs, currency)
	if err != nil {
		return nil, err
	}

	result := make(map[string]*marketdata.CoinGeckoPriceData, len(raw))
	for id, pd := range raw {
		result[id] = &marketdata.CoinGeckoPriceData{
			AssetID:   pd.AssetID,
			Price:     pd.Price,
			High24h:   pd.High24h,
			Low24h:    pd.Low24h,
			Timestamp: pd.Timestamp,
		}
	}

	return result, nil
}
