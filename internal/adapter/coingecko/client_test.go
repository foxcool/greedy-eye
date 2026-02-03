package coingecko

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestCoinGeckoClient_GetCurrentPrice(t *testing.T) {
	client := NewClient(Config{
		APIKey: "test-api-key",
		Pro:    false,
	})

	t.Run("should return unimplemented error", func(t *testing.T) {
		price, err := client.GetCurrentPrice(context.Background(), "bitcoin", "usd")

		assert.Nil(t, price)
		assert.Error(t, err)
		assert.Equal(t, codes.Unimplemented, status.Code(err))
	})
}

func TestCoinGeckoClient_GetMultiplePrices(t *testing.T) {
	client := NewClient(Config{
		APIKey: "test-api-key",
		Pro:    false,
	})

	t.Run("should return unimplemented error", func(t *testing.T) {
		assetIDs := []string{"bitcoin", "ethereum", "polkadot"}
		prices, err := client.GetMultiplePrices(context.Background(), assetIDs, "usd")

		assert.Nil(t, prices)
		assert.Error(t, err)
		assert.Equal(t, codes.Unimplemented, status.Code(err))
	})
}

func TestCoinGeckoClient_GetHistoricalPrices(t *testing.T) {
	client := NewClient(Config{
		APIKey: "test-api-key",
		Pro:    false,
	})

	t.Run("should return unimplemented error", func(t *testing.T) {
		from := time.Now().AddDate(0, 0, -7)
		to := time.Now()
		prices, err := client.GetHistoricalPrices(context.Background(), "bitcoin", "usd", from, to)

		assert.Nil(t, prices)
		assert.Error(t, err)
		assert.Equal(t, codes.Unimplemented, status.Code(err))
	})
}

func TestCoinGeckoClient_SearchAssets(t *testing.T) {
	client := NewClient(Config{
		APIKey: "test-api-key",
		Pro:    false,
	})

	t.Run("should return unimplemented error", func(t *testing.T) {
		results, err := client.SearchAssets(context.Background(), "bitcoin")

		assert.Nil(t, results)
		assert.Error(t, err)
		assert.Equal(t, codes.Unimplemented, status.Code(err))
	})
}

func TestCoinGeckoClient_Ping(t *testing.T) {
	client := NewClient(Config{
		APIKey: "test-api-key",
		Pro:    false,
	})

	t.Run("should return unimplemented error", func(t *testing.T) {
		err := client.Ping(context.Background())

		assert.Error(t, err)
		assert.Equal(t, codes.Unimplemented, status.Code(err))
	})
}
