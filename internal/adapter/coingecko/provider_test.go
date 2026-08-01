package coingecko

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/foxcool/greedy-eye/internal/entity"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// contractAssetOn builds a synced token: the contract lives in both the pricing
// tag and the external ref that names its chain, the way FindOrCreateAsset
// writes it.
func contractAssetOn(id, chain, address string) *entity.Asset {
	return &entity.Asset{
		ID:     id,
		Symbol: "TKN" + id,
		Market: entity.MarketCrypto,
		Tags:   []string{"contract:" + address},
		ExternalRefs: []entity.AssetExternalRef{
			{AssetID: id, Source: entity.OnchainSource(chain), Ref: address},
		},
	}
}

// TestFetchPrices_RoutesContractsByChain: a token is priced on the platform its
// own chain maps to. Sending every contract to Ethereum spent a request on a
// certain miss and, when an address collided, priced the token as an unrelated
// Ethereum contract.
func TestFetchPrices_RoutesContractsByChain(t *testing.T) {
	ethAddr := "0x" + strings.Repeat("a1", 20)
	baseAddr := "0x" + strings.Repeat("b2", 20)
	ksmAddr := "0x" + strings.Repeat("c3", 20)

	var paths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		addr := r.URL.Query().Get("contract_addresses")
		_, _ = fmt.Fprintf(w, `{"%s": {"usd": 2.5}}`, addr)
	}))
	defer srv.Close()

	client := NewClient(Config{APIKey: "demo-key"})
	client.baseURL = srv.URL
	client.httpClient = srv.Client()
	p := NewProvider(client)

	got, err := p.FetchPrices(context.Background(), []*entity.Asset{
		contractAssetOn("eth-1", "eth", ethAddr),
		contractAssetOn("base-1", "base", baseAddr),
		contractAssetOn("ksm-1", "kusama", ksmAddr),
	})
	require.NoError(t, err)

	assert.ElementsMatch(t,
		[]string{"/simple/token_price/ethereum", "/simple/token_price/base"},
		paths,
		"one request per platform, and none for a chain CoinGecko does not list")
	assert.Len(t, got, 2, "the unlisted chain yields no price rather than a wrong one")
}

// TestFetchPrices_SkipsContractWithoutChain: with no external ref there is
// nothing to route on, and guessing a platform is how a token gets priced as
// somebody else's.
func TestFetchPrices_SkipsContractWithoutChain(t *testing.T) {
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		_, _ = fmt.Fprint(w, `{}`)
	}))
	defer srv.Close()

	client := NewClient(Config{APIKey: "demo-key"})
	client.baseURL = srv.URL
	client.httpClient = srv.Client()
	p := NewProvider(client)

	chainless := &entity.Asset{
		ID:     "orphan-1",
		Symbol: "ORPHAN",
		Market: entity.MarketCrypto,
		Tags:   []string{"contract:0x" + strings.Repeat("d4", 20)},
	}

	got, err := p.FetchPrices(context.Background(), []*entity.Asset{chainless})
	require.NoError(t, err)
	assert.Empty(t, got)
	assert.Zero(t, calls, "no request may go out for an unroutable contract")
}

// TestBudgetExemptSymbols covers the curated set one /coins/markets call
// carries: it is what the sweep refreshes without spending its portion.
func TestBudgetExemptSymbols(t *testing.T) {
	p := NewProvider(NewClient(Config{}))

	got := p.BudgetExemptSymbols()

	assert.Len(t, got, len(nativeCoinID))
	assert.Contains(t, got, "BTC", "symbols are upper-cased to match the catalogue")
}
