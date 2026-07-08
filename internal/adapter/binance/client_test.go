package binance

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestBinanceClient_GetAccountBalances(t *testing.T) {
	const apiKey, apiSecret = "test-api-key", "test-api-secret"

	t.Run("returns non-zero balances and signs the request", func(t *testing.T) {
		var gotAPIKey, gotSignature, gotQuery string
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			assert.Equal(t, "/api/v3/account", r.URL.Path)
			gotAPIKey = r.Header.Get("X-MBX-APIKEY")
			q := r.URL.Query()
			gotSignature = q.Get("signature")
			assert.NotEmpty(t, q.Get("timestamp"))
			// Recompute the expected signature over the query minus the signature param.
			q.Del("signature")
			gotQuery = q.Encode()
			_, _ = w.Write([]byte(`{"balances":[
				{"asset":"BTC","free":"0.50000000","locked":"0.10000000"},
				{"asset":"ETH","free":"2.00000000","locked":"0.00000000"},
				{"asset":"USDT","free":"0.00000000","locked":"0.00000000"}
			]}`))
		}))
		defer srv.Close()

		client := NewClient(Config{APIKey: apiKey, APISecret: apiSecret}).WithBaseURL(srv.URL)
		balances, err := client.GetAccountBalances(context.Background(), "acct")
		require.NoError(t, err)

		// Zero-balance USDT dropped; BTC and ETH kept.
		require.Len(t, balances, 2)
		assert.Equal(t, "BTC", balances[0].Asset)
		assert.Equal(t, 0.5, balances[0].Free)
		assert.Equal(t, 0.1, balances[0].Locked)

		assert.Equal(t, apiKey, gotAPIKey)
		mac := hmac.New(sha256.New, []byte(apiSecret))
		mac.Write([]byte(gotQuery))
		assert.Equal(t, hex.EncodeToString(mac.Sum(nil)), gotSignature, "signature must cover the query params")
	})

	t.Run("surfaces Binance error body", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"code":-2015,"msg":"Invalid API-key, IP, or permissions for action."}`))
		}))
		defer srv.Close()

		client := NewClient(Config{APIKey: apiKey, APISecret: apiSecret}).WithBaseURL(srv.URL)
		_, err := client.GetAccountBalances(context.Background(), "acct")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "-2015")
		assert.Contains(t, err.Error(), "Invalid API-key")
	})

	t.Run("requires credentials", func(t *testing.T) {
		client := NewClient(Config{})
		_, err := client.GetAccountBalances(context.Background(), "acct")
		require.Error(t, err)
		assert.Equal(t, codes.FailedPrecondition, status.Code(err))
	})
}

func TestBinanceClient_signedGetEncodesParams(t *testing.T) {
	// Ensure signedGet round-trips arbitrary params without dropping them.
	var received url.Values
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		received = r.URL.Query()
		_, _ = w.Write([]byte(`{"balances":[]}`))
	}))
	defer srv.Close()

	client := NewClient(Config{APIKey: "k", APISecret: "s"}).WithBaseURL(srv.URL)
	_, err := client.fetchAccount(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "5000", received.Get("recvWindow"))
	assert.NotEmpty(t, received.Get("signature"))
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
