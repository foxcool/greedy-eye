package coingecko

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/foxcool/greedy-eye/internal/entities"
	"github.com/shopspring/decimal"
)

// Warn: test making HTTP requests to coingecko without mocking.

type CoinData struct {
	ID                 string    `json:"id"`
	Symbol             string    `json:"symbol"`
	Name               string    `json:"name"`
	CurrentPrice       float64   `json:"current_price"`
	MarketCap          float64   `json:"market_cap"`
	TotalVolume        float64   `json:"total_volume"`
	High24h            float64   `json:"high_24h"`
	Low24h             float64   `json:"low_24h"`
	PriceChange24h     float64   `json:"price_change_24h"`
	PriceChangePercent float64   `json:"price_change_percentage_24h"`
	LastUpdated        time.Time `json:"last_updated"`
}

// Service used for getting index prices anf other.
type Service struct{}

func Get() ([]entities.Price, error) {
	// List of assets for which we want to get the current prices
	assets := []string{"bitcoin", "ethereum", "polkadot", "the-open-network", "dai", "usd-coin", "uniswap", "1inch", "moonbeam", "optimism", "kusama", "tezos", "aave", "ethereum-name-service", "gitcoin", "maker", "binancecoin", "tether"}

	// Make a GET request to the CoinGecko API
	response, err := http.Get(fmt.Sprintf("https://api.coingecko.com/api/v3/coins/markets?vs_currency=usd&ids=%s", strings.Join(assets, ",")))
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()

	// Read the response body
	body, err := io.ReadAll(response.Body)
	if err != nil {
		return nil, err
	}

	// Unmarshal the JSON response into a struct
	var resp []CoinData
	err = json.Unmarshal(body, &resp)
	if err != nil {
		return nil, err
	}

	// Convert getted information to our local entity
	var result []entities.Price
	for _, data := range resp {
		result = append(result, entities.Price{
			Source:    "coingecko",
			BaseAsset: entities.Asset(data.Symbol),
			LastPrice: decimal.NewFromFloat32(float32(data.CurrentPrice)),
			Time:      data.LastUpdated,
		})
	}

	return result, nil
}

// Work starts service and handle events.
func (s *Service) Work(ctx context.Context, priceChan chan entities.Price, errorChan chan error) {
	ticker := time.NewTicker(time.Minute)
	for {
		select {
		case <-ticker.C:
			prices, err := Get()
			if err != nil {
				errorChan <- err

				continue
			}

			for _, price := range prices {
				priceChan <- price
			}
		case <-ctx.Done():
			return
		}
	}
}
