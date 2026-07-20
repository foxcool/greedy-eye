package coingecko

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
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

func TestCoinGeckoClient_GetTokenPricesByContract_KeylessSingleAddressMode(t *testing.T) {
	goodAddr := "0x" + strings.Repeat("ab", 20)
	badAddr := "0x" + strings.Repeat("cd", 20)

	var calls []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		addrs := r.URL.Query().Get("contract_addresses")
		calls = append(calls, addrs)
		// The keyless public API rejects multi-address requests outright.
		if strings.Contains(addrs, ",") || strings.Contains(addrs, badAddr) {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		_, _ = fmt.Fprintf(w, `{"%s": {"usd": 1.23, "usd_24h_high": 1.30, "usd_24h_low": 1.10}}`, goodAddr)
	}))
	defer srv.Close()

	client := NewClient(Config{}) // no API key → one address per request
	client.baseURL = srv.URL
	client.httpClient = srv.Client()

	addresses := []string{
		goodAddr,
		goodAddr,                             // duplicate — must be deduplicated
		badAddr,                              // server rejects this one
		"VISIT [SCAM.XYZ] AND CLAIM REWARDS", // garbage from a poisoned catalog
		"0x1234",                             // truncated
	}

	result, err := client.GetTokenPricesByContract(context.Background(), "ethereum", addresses, "usd")

	// The failed request is reported, but the succeeded one's price survives.
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "unexpected status 400")
	if assert.Contains(t, result, goodAddr) {
		assert.InDelta(t, 1.23, result[goodAddr].Price, 1e-9)
	}

	// One request per unique valid address; malformed ones never reach the API.
	assert.Equal(t, []string{goodAddr, badAddr}, calls)
}

func TestCoinGeckoClient_GetTokenPricesByContract_KeyedBatchMode(t *testing.T) {
	addrA := "0x" + strings.Repeat("ab", 20)
	addrB := "0x" + strings.Repeat("cd", 20)

	var calls []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls = append(calls, r.URL.Query().Get("contract_addresses"))
		_, _ = fmt.Fprintf(w, `{"%s": {"usd": 1.0}, "%s": {"usd": 2.0}}`, addrA, addrB)
	}))
	defer srv.Close()

	client := NewClient(Config{APIKey: "demo-key"}) // keyed → comma-separated batch
	client.baseURL = srv.URL
	client.httpClient = srv.Client()

	result, err := client.GetTokenPricesByContract(context.Background(), "ethereum",
		[]string{addrA, addrB}, "usd")

	assert.NoError(t, err)
	assert.Len(t, result, 2)
	assert.Equal(t, []string{addrA + "," + addrB}, calls)
}

func TestCoinGeckoClient_GetTokenPricesByContract_AllInvalid(t *testing.T) {
	client := NewClient(Config{})

	result, err := client.GetTokenPricesByContract(context.Background(), "ethereum",
		[]string{"not-an-address", "0xZZ"}, "usd")

	assert.NoError(t, err)
	assert.Empty(t, result)
}
