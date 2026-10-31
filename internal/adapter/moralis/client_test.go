package moralis

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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

func TestMoralisClient_GetWalletTokenBalances_FollowsCursor(t *testing.T) {
	var queries []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/api/v2.2/wallets/0xabc/tokens", r.URL.Path)
		queries = append(queries, r.URL.RawQuery)
		switch r.URL.Query().Get("cursor") {
		case "":
			_, _ = w.Write([]byte(`{"cursor":"next page+1","result":[
				{"token_address":"0x01","symbol":"A","decimals":18,"balance":"1"},
				{"token_address":"","symbol":"ETH","decimals":18,"balance":"9","native_token":true}]}`))
		case "next page+1":
			_, _ = w.Write([]byte(`{"cursor":null,"result":[{"token_address":"0x02","symbol":"B","decimals":6,"balance":"2"}]}`))
		default:
			http.Error(w, "bad cursor", http.StatusBadRequest)
		}
	}))
	defer srv.Close()

	client := NewClient(Config{APIKey: "k"})
	client.baseURL = srv.URL + "/api"

	got, err := client.GetWalletTokenBalances(context.Background(), "eth", "0xabc")
	require.NoError(t, err)
	require.Len(t, got, 2, "tokens from both pages, native entry skipped")
	assert.Equal(t, "A", got[0].Symbol)
	assert.Equal(t, "B", got[1].Symbol)
	require.Len(t, queries, 2)
	assert.Contains(t, queries[0], "chain=eth")
	assert.Contains(t, queries[0], "exclude_native=true")
}

func TestMoralisClient_GetWalletTokenBalances_RepeatedCursorFails(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"cursor":"same","result":[]}`))
	}))
	defer srv.Close()

	client := NewClient(Config{APIKey: "k"})
	client.baseURL = srv.URL + "/api"

	got, err := client.GetWalletTokenBalances(context.Background(), "eth", "0xabc")
	assert.Nil(t, got)
	assert.ErrorContains(t, err, "cursor repeated")
}

// TestMoralisClient_PathSegmentsEscaped: address and chain come from account
// data; a "/" or "?" must not rewrite the request path or query.
func TestMoralisClient_PathSegmentsEscaped(t *testing.T) {
	var paths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.EscapedPath())
		_, _ = w.Write([]byte(`{"result":[],"balance":"0","active_chains":[]}`))
	}))
	defer srv.Close()

	client := NewClient(Config{APIKey: "k"})
	client.baseURL = srv.URL + "/api"
	const evil = "0xa/../b?c#d"

	_, err := client.GetWalletTokenBalances(context.Background(), "eth", evil)
	require.NoError(t, err)
	_, err = client.GetWalletBalance(context.Background(), "eth", evil)
	require.NoError(t, err)
	_, err = client.GetActiveChains(context.Background(), evil)
	require.NoError(t, err)

	require.Len(t, paths, 3)
	assert.Equal(t, "/api/v2.2/wallets/0xa%2F..%2Fb%3Fc%23d/tokens", paths[0])
	assert.Equal(t, "/api/v2/0xa%2F..%2Fb%3Fc%23d/balance", paths[1])
	assert.Equal(t, "/api/v2.2/wallets/0xa%2F..%2Fb%3Fc%23d/chains", paths[2])
}
