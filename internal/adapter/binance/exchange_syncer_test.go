package binance

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestExchangeSyncer_SyncExchange(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"balances":[
			{"asset":"BTC","free":"0.50000000","locked":"0.10000000"},
			{"asset":"DOGE","free":"1234.56789012","locked":"0"},
			{"asset":"USDT","free":"0","locked":"0"}
		]}`))
	}))
	defer srv.Close()

	syncer := NewExchangeSyncer(NewClient(Config{APIKey: "k", APISecret: "s"}).WithBaseURL(srv.URL))
	balances, err := syncer.SyncExchange(context.Background())
	require.NoError(t, err)
	require.Len(t, balances, 2) // zero-balance USDT dropped

	bySymbol := map[string]string{}
	for _, b := range balances {
		assert.Equal(t, balanceDecimals, b.Decimals)
		bySymbol[b.Symbol] = b.Amount
	}
	// BTC = (0.5 + 0.1) * 10^8 = 60000000
	assert.Equal(t, "60000000", bySymbol["BTC"])
	// DOGE = 1234.56789012 * 10^8 = 123456789012
	assert.Equal(t, "123456789012", bySymbol["DOGE"])
}

func TestExchangeSyncer_PropagatesError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"code":-1022,"msg":"Signature for this request is not valid."}`))
	}))
	defer srv.Close()

	syncer := NewExchangeSyncer(NewClient(Config{APIKey: "k", APISecret: "s"}).WithBaseURL(srv.URL))
	_, err := syncer.SyncExchange(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "-1022")
}
