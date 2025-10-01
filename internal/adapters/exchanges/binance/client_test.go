package binance

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestBinanceClient_GetAccountBalances(t *testing.T) {
	client := NewClient(Config{
		APIKey:    "test-api-key",
		APISecret: "test-api-secret",
		Sandbox:   true,
	})

	t.Run("should return unimplemented error", func(t *testing.T) {
		balances, err := client.GetAccountBalances(context.Background(), "test-account")

		assert.Nil(t, balances)
		assert.Error(t, err)
		assert.Equal(t, codes.Unimplemented, status.Code(err))
	})
}

func TestBinanceClient_PlaceOrder(t *testing.T) {
	client := NewClient(Config{
		APIKey:    "test-api-key",
		APISecret: "test-api-secret",
		Sandbox:   true,
	})

	t.Run("should return unimplemented error", func(t *testing.T) {
		order := &Order{
			Symbol:   "BTCUSDT",
			Side:     "BUY",
			Type:     "MARKET",
			Quantity: 0.001,
		}

		result, err := client.PlaceOrder(context.Background(), "test-account", order)

		assert.Nil(t, result)
		assert.Error(t, err)
		assert.Equal(t, codes.Unimplemented, status.Code(err))
	})
}

func TestBinanceClient_GetSymbolPrice(t *testing.T) {
	client := NewClient(Config{
		APIKey:    "test-api-key",
		APISecret: "test-api-secret",
		Sandbox:   true,
	})

	t.Run("should return unimplemented error", func(t *testing.T) {
		price, err := client.GetSymbolPrice(context.Background(), "BTCUSDT")

		assert.Equal(t, float64(0), price)
		assert.Error(t, err)
		assert.Equal(t, codes.Unimplemented, status.Code(err))
	})
}

func TestBinanceClient_ValidateAccount(t *testing.T) {
	client := NewClient(Config{
		APIKey:    "test-api-key",
		APISecret: "test-api-secret",
		Sandbox:   true,
	})

	t.Run("should return unimplemented error", func(t *testing.T) {
		err := client.ValidateAccount(context.Background(), "test-account")

		assert.Error(t, err)
		assert.Equal(t, codes.Unimplemented, status.Code(err))
	})
}
