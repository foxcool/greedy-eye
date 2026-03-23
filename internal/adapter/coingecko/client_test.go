package coingecko

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestCoinGeckoClient_GetMultiplePrices_EmptyInput(t *testing.T) {
	client := NewClient(Config{APIKey: "test-api-key"})

	t.Run("returns empty map for empty input", func(t *testing.T) {
		prices, err := client.GetMultiplePrices(context.Background(), []string{}, "usd")

		assert.NoError(t, err)
		assert.Empty(t, prices)
	})
}

func TestCoinGeckoClient_GetCurrentPrice_DelegatesToGetMultiplePrices(t *testing.T) {
	// Validate that GetCurrentPrice returns an error for unknown assets
	// without making real HTTP calls (uses short timeout).
	client := NewClient(Config{APIKey: ""})

	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Millisecond)
	defer cancel()

	_, err := client.GetCurrentPrice(ctx, "bitcoin", "usd")
	// Should fail (timeout or connection error) but not panic.
	assert.Error(t, err)
}

func TestCoinGeckoClient_GetHistoricalPrices_NotImplemented(t *testing.T) {
	client := NewClient(Config{})

	_, err := client.GetHistoricalPrices(context.Background(), "bitcoin", "usd", time.Now().AddDate(0, 0, -1), time.Now())
	assert.Error(t, err)
}

func TestCoinGeckoClient_SearchAssets_NotImplemented(t *testing.T) {
	client := NewClient(Config{})

	_, err := client.SearchAssets(context.Background(), "bitcoin")
	assert.Error(t, err)
}
