package moralis

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestMoralisClient_GetWalletBalance(t *testing.T) {
	client := NewClient(Config{
		APIKey: "test-api-key",
	})

	t.Run("returns error without valid server", func(t *testing.T) {
		balance, err := client.GetWalletBalance(context.Background(), "eth", "0x1234567890abcdef")

		assert.Empty(t, balance)
		assert.Error(t, err)
	})
}

func TestMoralisClient_GetWalletTokenBalances(t *testing.T) {
	client := NewClient(Config{
		APIKey: "test-api-key",
	})

	t.Run("returns error without valid server", func(t *testing.T) {
		balances, err := client.GetWalletTokenBalances(context.Background(), "eth", "0x1234567890abcdef")

		assert.Nil(t, balances)
		assert.Error(t, err)
	})
}

func TestMoralisClient_GetTransactionHistory(t *testing.T) {
	client := NewClient(Config{
		APIKey: "test-api-key",
	})

	t.Run("not implemented", func(t *testing.T) {
		txs, err := client.GetTransactionHistory(context.Background(), "eth", "0x1234567890abcdef", 10)

		assert.Nil(t, txs)
		assert.ErrorContains(t, err, "not implemented")
	})
}

func TestMoralisClient_GetTokenPrice(t *testing.T) {
	client := NewClient(Config{
		APIKey: "test-api-key",
	})

	t.Run("not implemented", func(t *testing.T) {
		price, err := client.GetTokenPrice(context.Background(), "eth", "0xdac17f958d2ee523a2206206994597c13d831ec7")

		assert.Equal(t, float64(0), price)
		assert.ErrorContains(t, err, "not implemented")
	})
}

func TestMoralisClient_ValidateAddress(t *testing.T) {
	client := NewClient(Config{
		APIKey: "test-api-key",
	})

	t.Run("not implemented", func(t *testing.T) {
		valid, err := client.ValidateAddress(context.Background(), "eth", "0x1234567890abcdef")

		assert.False(t, valid)
		assert.ErrorContains(t, err, "not implemented")
	})
}
