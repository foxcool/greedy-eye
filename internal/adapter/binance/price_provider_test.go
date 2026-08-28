package binance

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/foxcool/greedy-eye/internal/entity"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newTestClient creates a Client pointing to the given test server URL.
func newTestClient(serverURL string) *Client {
	c := NewClient(Config{})
	c.baseURL = serverURL
	return c
}

// bound returns a crypto asset already tied to its Binance pair, which is the
// steady state after one discovery pass. Pricing reads the binding and never
// the ticker, so a fixture without one is an asset this provider will not
// price — see the DiscoverRefs tests for that path.
func bound(id, symbol string) *entity.Asset {
	a := &entity.Asset{ID: id, Symbol: symbol, Market: entity.MarketCrypto}
	a.ExternalRefs = []entity.AssetExternalRef{{
		AssetID: id,
		Source:  RefSource,
		Ref:     venueSymbol(a),
		Origin:  entity.RefOriginAuto,
	}}
	return a
}

// testAssets are two ordinary crypto assets. The market is set because the
// store sets it: CreateAsset defaults it by type, so an asset reaching a price
// provider without one does not exist in practice — and this provider now
// declines to price anything outside the global crypto market.
func testAssets() []*entity.Asset {
	return []*entity.Asset{bound("uuid-btc", "BTC"), bound("uuid-eth", "ETH")}
}

// writeExchangeInfo answers the listing-universe request with the given pairs
// in TRADING status. The provider consults it before pricing, so every price
// test has to serve it — a test server that does not is a provider that cannot
// tell a listed pair from an airdropped one.
func writeExchangeInfo(w http.ResponseWriter, symbols ...string) {
	out := exchangeInfoResponse{}
	for _, sym := range symbols {
		out.Symbols = append(out.Symbols, exchangeInfoSymbol{Symbol: sym, Status: "TRADING"})
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(out)
}

func TestFetchPrices_OK(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v3/exchangeInfo" {
			writeExchangeInfo(w, "BTCUSDT", "ETHUSDT")
			return
		}
		assert.Equal(t, "/api/v3/ticker/price", r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode([]TickerPrice{
			{Symbol: "BTCUSDT", Price: "67000.12345678"},
			{Symbol: "ETHUSDT", Price: "3500.00000000"},
		})
	}))
	defer srv.Close()

	p := NewProvider(newTestClient(srv.URL))

	prices, err := p.FetchPrices(context.Background(), testAssets())
	require.NoError(t, err)
	require.Len(t, prices, 2)

	byAsset := make(map[string]string, 2)
	for _, sp := range prices {
		assert.Equal(t, "binance", sp.SourceID)
		assert.Empty(t, sp.BaseAssetID) // resolved by handler, not provider
		assert.Equal(t, "latest", sp.Interval)
		assert.Equal(t, uint32(8), sp.Decimals)
		byAsset[sp.AssetID] = sp.Last.String()
	}
	assert.Equal(t, "6700012345678", byAsset["uuid-btc"])
	assert.Equal(t, "350000000000", byAsset["uuid-eth"])
}

func TestFetchPrices_EmptyAssets(t *testing.T) {
	called := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
	}))
	defer srv.Close()

	p := NewProvider(newTestClient(srv.URL))

	prices, err := p.FetchPrices(context.Background(), nil)
	require.NoError(t, err)
	assert.Empty(t, prices)
	assert.False(t, called, "no HTTP call expected for empty assets")
}

func TestFetchPrices_BinanceError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	p := NewProvider(newTestClient(srv.URL))

	_, err := p.FetchPrices(context.Background(), testAssets())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "binance")
}

// TestFetchPrices_IsolatedContractIsNotPricedByTicker: a counterfeit quarantined
// on its own contract market claims a famous ticker, and this provider matches
// by ticker alone. Asking Binance about it would hand it the real asset's price
// — the c3b failure, re-entered through the price path instead of the sync path.
func TestFetchPrices_IsolatedContractIsNotPricedByTicker(t *testing.T) {
	var asked []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v3/exchangeInfo" {
			writeExchangeInfo(w, "BTCUSDT")
			return
		}
		asked = append(asked, r.URL.Query().Get("symbols"))
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode([]TickerPrice{{Symbol: "BTCUSDT", Price: "67000.00000000"}})
	}))
	defer srv.Close()

	p := NewProvider(newTestClient(srv.URL))
	assets := []*entity.Asset{
		bound("uuid-btc", "BTC"),
		{ID: "uuid-fake", Symbol: "BTC", Market: entity.ContractMarket("bsc", "0xdead")},
	}

	prices, err := p.FetchPrices(context.Background(), assets)
	require.NoError(t, err)

	require.Len(t, prices, 1)
	assert.Equal(t, "uuid-btc", prices[0].AssetID, "only the asset on the real market is priced")
	require.Len(t, asked, 1)
	assert.NotContains(t, asked[0], "uuid-fake")
}

// TestFetchPrices_AllIsolated: nothing to ask about is not an error, and it must
// not cost a request either.
func TestFetchPrices_AllIsolated(t *testing.T) {
	called := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode([]TickerPrice{})
	}))
	defer srv.Close()

	p := NewProvider(newTestClient(srv.URL))
	prices, err := p.FetchPrices(context.Background(), []*entity.Asset{
		{ID: "uuid-fake", Symbol: "USDT", Market: entity.ContractMarket("bsc", "0xdead")},
	})
	require.NoError(t, err)
	assert.Empty(t, prices)
	assert.False(t, called, "no HTTP call expected when no asset qualifies")
}

// TestGetTickerPrices_OneBadBatchDoesNotCostTheRest: Binance answers 400 for the
// whole request when one symbol in it is unknown, so an unbatched sweep loses
// every price to the worst entry. Batching bounds that to the batch.
func TestGetTickerPrices_OneBadBatchDoesNotCostTheRest(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Query().Get("symbols"), "NOTAREALCOINUSDT") {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"code":-1121,"msg":"Invalid symbol."}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode([]TickerPrice{{Symbol: "BTCUSDT", Price: "67000.00000000"}})
	}))
	defer srv.Close()

	c := newTestClient(srv.URL)
	symbols := make([]string, 0, tickerBatchSize+1)
	for range tickerBatchSize {
		symbols = append(symbols, "BTCUSDT")
	}
	symbols = append(symbols, "NOTAREALCOINUSDT")

	got, err := c.GetTickerPrices(context.Background(), symbols)
	require.NoError(t, err, "a partial answer is an answer")
	assert.NotEmpty(t, got, "the good batch survives the bad one")
}

// TestGetTickerPrices_EveryBatchFailing: "some prices" and "no prices" are
// different answers, and only the second is an error.
func TestGetTickerPrices_EveryBatchFailing(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
	}))
	defer srv.Close()

	_, err := newTestClient(srv.URL).GetTickerPrices(context.Background(), []string{"BTCUSDT"})
	require.Error(t, err)
}

func TestGetTickerPrices_BatchRequest(t *testing.T) {
	var capturedURL *url.URL
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedURL = r.URL
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode([]TickerPrice{})
	}))
	defer srv.Close()

	c := newTestClient(srv.URL)
	_, err := c.GetTickerPrices(context.Background(), []string{"BTCUSDT", "ETHUSDT"})
	require.NoError(t, err)

	require.NotNil(t, capturedURL)
	symbolsParam := capturedURL.Query().Get("symbols")
	assert.True(t, strings.Contains(symbolsParam, "BTCUSDT"), "BTCUSDT should be in symbols param")
	assert.True(t, strings.Contains(symbolsParam, "ETHUSDT"), "ETHUSDT should be in symbols param")
}

// TestFetchPrices_UnlistableSymbolCostsOnlyItsOwnQuote is the acceptance test
// for personal-edtu, measured against the live API on 2026-08-26: Binance
// answers 400 for the WHOLE request when one symbol in it is not a tradable
// pair, so a batch carrying an airdropped jetton used to store nothing at all.
//
// The listing universe now excludes it before the request is built, so the good
// symbols are still stored. The test asserts the jetton never reaches the wire:
// keeping it out is the fix, surviving its rejection would not be.
func TestFetchPrices_UnlistableSymbolCostsOnlyItsOwnQuote(t *testing.T) {
	var asked []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v3/exchangeInfo" {
			writeExchangeInfo(w, "BTCUSDT", "ETHUSDT")
			return
		}
		symbols := r.URL.Query().Get("symbols")
		asked = append(asked, symbols)
		// The live behaviour: one unknown symbol rejects the entire request.
		if strings.Contains(symbols, "USD₮USDT") {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode([]TickerPrice{
			{Symbol: "BTCUSDT", Price: "67000.00000000"},
			{Symbol: "ETHUSDT", Price: "3500.00000000"},
		})
	}))
	defer srv.Close()

	p := NewProvider(newTestClient(srv.URL))
	assets := []*entity.Asset{
		bound("uuid-btc", "BTC"),
		// A TON jetton whose ticker carries U+20AE — the exact row that cost
		// prod a whole batch on 2026-08-18.
		bound("uuid-jetton", "USD₮"),
		bound("uuid-eth", "ETH"),
	}

	prices, err := p.FetchPrices(context.Background(), assets)
	require.NoError(t, err)
	require.Len(t, prices, 2, "the valid symbols are still stored")

	byAsset := map[string]bool{}
	for _, sp := range prices {
		byAsset[sp.AssetID] = true
	}
	assert.True(t, byAsset["uuid-btc"])
	assert.True(t, byAsset["uuid-eth"])
	assert.False(t, byAsset["uuid-jetton"])

	require.Len(t, asked, 1)
	assert.NotContains(t, asked[0], "USD₮", "the unlisted pair never reaches the wire")
}

// Asked is what the sweep records attempts from, so it must exclude the same
// rows FetchPrices excludes. An asset reported as asked but never requested
// collects a miss for someone else's silence, and the back-off doubles per miss
// up to a week (personal-7994).
func TestAsked_ExcludesWhatWasNeverRequested(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v3/exchangeInfo" {
			writeExchangeInfo(w, "BTCUSDT")
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode([]TickerPrice{{Symbol: "BTCUSDT", Price: "67000.00000000"}})
	}))
	defer srv.Close()

	p := NewProvider(newTestClient(srv.URL))
	assets := []*entity.Asset{
		bound("uuid-btc", "BTC"),
		bound("uuid-jetton", "USD₮"),
		{ID: "uuid-moex", Symbol: "SBER", Market: "moex"},
	}

	// The universe is warm after a fetch, which is how the handler calls it.
	_, err := p.FetchPrices(context.Background(), assets)
	require.NoError(t, err)

	asked := p.Asked(assets)
	require.Len(t, asked, 1)
	assert.Equal(t, "uuid-btc", asked[0].ID)
}

// Before the universe is known, Asked must not silence the provider: an
// unloaded snapshot is absence of evidence, not evidence that nothing is
// listed. Reading it the other way would stop pricing everything the first time
// exchangeInfo is unreachable.
func TestAsked_WithoutUniverseFallsBackToSpeaksFor(t *testing.T) {
	p := NewProvider(newTestClient("http://127.0.0.1:1"))
	assets := []*entity.Asset{
		bound("uuid-btc", "BTC"),
		{ID: "uuid-moex", Symbol: "SBER", Market: "moex"},
	}

	asked := p.Asked(assets)
	require.Len(t, asked, 1, "market filtering still applies")
	assert.Equal(t, "uuid-btc", asked[0].ID)
}

// An unreachable exchangeInfo must not stop pricing: the provider falls back to
// asking for everything it speaks for, which is the behaviour that existed
// before the universe was consulted at all.
func TestFetchPrices_UnreachableExchangeInfoStillPrices(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v3/exchangeInfo" {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode([]TickerPrice{{Symbol: "BTCUSDT", Price: "67000.00000000"}})
	}))
	defer srv.Close()

	p := NewProvider(newTestClient(srv.URL))
	prices, err := p.FetchPrices(context.Background(),
		[]*entity.Asset{bound("uuid-btc", "BTC")})
	require.NoError(t, err)
	require.Len(t, prices, 1)
	assert.Equal(t, "uuid-btc", prices[0].AssetID)
}

// A halted pair (status BREAK) exists but cannot be traded, so asking for it
// spends a request to learn nothing.
func TestFetchPrices_HaltedPairIsNotAsked(t *testing.T) {
	var asked []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v3/exchangeInfo" {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(exchangeInfoResponse{Symbols: []exchangeInfoSymbol{
				{Symbol: "BTCUSDT", Status: "TRADING"},
				{Symbol: "HALTEDUSDT", Status: "BREAK"},
			}})
			return
		}
		asked = append(asked, r.URL.Query().Get("symbols"))
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode([]TickerPrice{{Symbol: "BTCUSDT", Price: "67000.00000000"}})
	}))
	defer srv.Close()

	p := NewProvider(newTestClient(srv.URL))
	_, err := p.FetchPrices(context.Background(), []*entity.Asset{
		bound("uuid-btc", "BTC"),
		bound("uuid-halted", "HALTED"),
	})
	require.NoError(t, err)
	require.Len(t, asked, 1)
	assert.NotContains(t, asked[0], "HALTEDUSDT")
}

// The defect this binding closes: a token minted with a famous ticker, on the
// global crypto market and not yet carrying a quarantine verdict, was handed the
// real coin's price because identity was the ticker and nothing else. The window
// is exactly the life of a fresh impostor, before the scam filter judges it.
func TestDiscoverRefs_ContestedPairBindsNobody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/api/v3/exchangeInfo", r.URL.Path)
		writeExchangeInfo(w, "USDTUSDT", "BTCUSDT")
	}))
	defer srv.Close()

	p := NewProvider(newTestClient(srv.URL))
	refs, err := p.DiscoverRefs(context.Background(), []*entity.Asset{
		{ID: "uuid-tether", Symbol: "USDT", Market: entity.MarketCrypto},
		{ID: "uuid-impostor", Symbol: "USDT", Market: entity.MarketCrypto},
		{ID: "uuid-btc", Symbol: "BTC", Market: entity.MarketCrypto},
	})
	require.NoError(t, err)

	require.Len(t, refs, 1, "the contested pair binds nobody; the uncontested one binds")
	assert.Equal(t, "uuid-btc", refs[0].AssetID)
	assert.Equal(t, "BTCUSDT", refs[0].Ref)
	assert.Equal(t, RefSource, refs[0].Source)
}

// A contest is resolved by removing a claimant, not by the adapter picking one.
// Once the impostor is quarantined onto its own contract market, speaksFor drops
// it and the original is alone again.
func TestDiscoverRefs_QuarantineResolvesTheContest(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeExchangeInfo(w, "USDTUSDT")
	}))
	defer srv.Close()

	p := NewProvider(newTestClient(srv.URL))
	refs, err := p.DiscoverRefs(context.Background(), []*entity.Asset{
		{ID: "uuid-tether", Symbol: "USDT", Market: entity.MarketCrypto},
		{ID: "uuid-impostor", Symbol: "USDT", Market: entity.ContractMarket("bsc", "0xdead")},
	})
	require.NoError(t, err)

	require.Len(t, refs, 1)
	assert.Equal(t, "uuid-tether", refs[0].AssetID)
}

// An unlisted ticker binds nothing: the binding asserts the asset IS the listed
// instrument, and there is no instrument to be.
func TestDiscoverRefs_UnlistedPairBindsNothing(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeExchangeInfo(w, "BTCUSDT")
	}))
	defer srv.Close()

	p := NewProvider(newTestClient(srv.URL))
	refs, err := p.DiscoverRefs(context.Background(), []*entity.Asset{
		{ID: "uuid-jetton", Symbol: "AIRDROP", Market: entity.MarketCrypto},
	})
	require.NoError(t, err)
	assert.Empty(t, refs)
}

// "We could not ask" is not the claim "it is not listed". Binding on an
// unreachable listing set would write identity assertions from no evidence, and
// a wrong binding survives every later sweep.
func TestDiscoverRefs_UnreachableUniverseBindsNothing(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	p := NewProvider(newTestClient(srv.URL))
	refs, err := p.DiscoverRefs(context.Background(), []*entity.Asset{
		{ID: "uuid-btc", Symbol: "BTC", Market: entity.MarketCrypto},
	})
	require.Error(t, err, "the failure travels up rather than becoming a silent no-op")
	assert.Empty(t, refs)
}

// Re-discovering an already-bound asset proposes nothing: the store refuses to
// overwrite an existing binding, and identity is stable once established.
func TestDiscoverRefs_AlreadyBoundIsNotReproposed(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeExchangeInfo(w, "BTCUSDT")
	}))
	defer srv.Close()

	p := NewProvider(newTestClient(srv.URL))
	refs, err := p.DiscoverRefs(context.Background(), []*entity.Asset{bound("uuid-btc", "BTC")})
	require.NoError(t, err)
	assert.Empty(t, refs)
}

// The gate itself: an unbound asset is not priced, however ordinary it looks.
// Before the binding this asset would have been asked about by ticker.
func TestFetchPrices_UnboundAssetIsNotPriced(t *testing.T) {
	var asked []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v3/exchangeInfo" {
			writeExchangeInfo(w, "BTCUSDT")
			return
		}
		asked = append(asked, r.URL.Query().Get("symbols"))
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode([]TickerPrice{{Symbol: "BTCUSDT", Price: "67000.00000000"}})
	}))
	defer srv.Close()

	p := NewProvider(newTestClient(srv.URL))
	prices, err := p.FetchPrices(context.Background(), []*entity.Asset{
		{ID: "uuid-impostor", Symbol: "BTC", Market: entity.MarketCrypto},
	})
	require.NoError(t, err)
	assert.Empty(t, prices, "no binding, no price")
	assert.Empty(t, asked, "and no request spent learning that")
}

// An unbound asset is not asked about, so it accrues no miss either: back-off is
// evidence about an asset a source declined, and this one was never eligible
// (personal-edtu).
func TestAsked_UnboundAssetIsNotAsked(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeExchangeInfo(w, "BTCUSDT")
	}))
	defer srv.Close()

	p := NewProvider(newTestClient(srv.URL))
	_, _ = p.tradableSymbols(context.Background())

	got := p.Asked([]*entity.Asset{
		bound("uuid-btc", "BTC"),
		{ID: "uuid-impostor", Symbol: "BTC", Market: entity.MarketCrypto},
	})
	require.Len(t, got, 1)
	assert.Equal(t, "uuid-btc", got[0].ID)
}

// The sweep reaches DiscoverRefs through a type assertion (marketdata.
// RefDiscoverer), so a signature that drifts does not fail the build — discovery
// simply stops happening and every asset quietly stays unbound and unpriced.
// This pins the shape the sweep looks for.
func TestProviderIsARefDiscoverer(t *testing.T) {
	var _ interface {
		DiscoverRefs(context.Context, []*entity.Asset) ([]entity.AssetExternalRef, error)
	} = NewProvider(newTestClient("http://example.invalid"))
}
