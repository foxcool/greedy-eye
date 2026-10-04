package coingecko

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/foxcool/greedy-eye/internal/entity"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// catalogServer serves one /coins/list snapshot and counts how often it is hit.
func catalogServer(t *testing.T, calls *int) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*calls++
		assert.Equal(t, "/coins/list", r.URL.Path)
		assert.Equal(t, "true", r.URL.Query().Get("include_platform"))
		_, _ = w.Write([]byte(`[
			{"id":"tether","symbol":"usdt","platforms":{
				"ethereum":"0xdAC17F958D2ee523a2206206994597C13D831ec7",
				"binance-smart-chain":"0x55d398326f99059fF775485246999027B3197955"}},
			{"id":"usd-coin","symbol":"usdc","platforms":{"ethereum":"0xA0b8...","":""}},
			{"id":"catcoin-cash","symbol":"cat","platforms":{
				"binance-smart-chain":"0x59f4f336bf3D0C49dBfbA4A74eBD2A6aCE40539A"}},
			{"id":"cat-inu","symbol":"cat","platforms":{
				"binance-smart-chain":"0xaf8E0Bce56615eDf2810Fab024C307dE352a431F"}}
		]`))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func indexAgainst(srv *httptest.Server) *contractIndex {
	client := NewClient(Config{APIKey: "demo-key"})
	client.baseURL = srv.URL
	client.httpClient = srv.Client()
	return newContractIndex(client)
}

// TestContractIndex_ConfirmsAndRejects covers the decision the asset resolver
// depends on: the genuine BSC-USD contract is listed under USDT, an address
// nobody listed is not, and a chain outside the platform map cannot confirm
// anything.
func TestContractIndex_ConfirmsAndRejects(t *testing.T) {
	calls := 0
	idx := indexAgainst(catalogServer(t, &calls))

	coin, found, err := idx.lookup(context.Background(), "bsc", "0x55D398326f99059fF775485246999027B3197955")
	require.NoError(t, err)
	assert.True(t, found, "the real BSC-USD contract is listed")
	assert.Equal(t, coinRef{id: "tether", symbol: "USDT"}, coin,
		"lookup is case-insensitive on the address and carries the coin id")

	_, found, err = idx.lookup(context.Background(), "bsc", "0x1280d5752daf7d2ff4f14b31e5c3b2d02cbc0e1f")
	require.NoError(t, err)
	assert.False(t, found, "a counterfeit contract is listed nowhere")

	_, found, err = idx.lookup(context.Background(), "kusama", "0x55d398326f99059ff775485246999027b3197955")
	require.NoError(t, err)
	assert.False(t, found, "a chain with no CoinGecko platform confirms nothing")

	assert.Equal(t, 1, calls, "the catalog is downloaded once and reused")
}

// TestContractIndex_OneTickerTwoCoinsOnOneChain is the measurement personal-dvgm
// was opened on, kept as a fixture: CoinGecko lists catcoin-cash and cat-inu
// both as "CAT" on bsc. The ticker cannot tell them apart and the id can, which
// is the whole reason the index carries the id.
func TestContractIndex_OneTickerTwoCoinsOnOneChain(t *testing.T) {
	calls := 0
	idx := indexAgainst(catalogServer(t, &calls))

	first, found, err := idx.lookup(context.Background(), "bsc", "0x59f4f336bf3d0c49dbfba4a74ebd2a6ace40539a")
	require.NoError(t, err)
	require.True(t, found)

	second, found, err := idx.lookup(context.Background(), "bsc", "0xaf8e0bce56615edf2810fab024c307de352a431f")
	require.NoError(t, err)
	require.True(t, found)

	assert.Equal(t, first.symbol, second.symbol, "both are published under the same ticker")
	assert.NotEqual(t, first.id, second.id, "and they are different coins")
	assert.Equal(t, "catcoin-cash", first.id)
	assert.Equal(t, "cat-inu", second.id)
}

// TestContractIndex_PropagatesFetchError: an unreachable catalog must surface as
// an error, never as a silent "not listed" — the caller treats the two very
// differently.
func TestContractIndex_PropagatesFetchError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer srv.Close()

	_, found, err := indexAgainst(srv).lookup(context.Background(), "eth", "0xdac17f958d2ee523a2206206994597c13d831ec7")
	require.Error(t, err)
	assert.False(t, found)
}

// TestFetchPrices_CuratedMapSkipsPerContractMarket: a per-contract row carries a
// well-known ticker but is not the global asset, so the curated symbol map must
// not price it — that is how a counterfeit USDT was valued as real USDT
// (personal-c3b).
func TestFetchPrices_CuratedMapSkipsPerContractMarket(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		_, _ = w.Write([]byte(`[]`))
	}))
	defer srv.Close()

	client := NewClient(Config{APIKey: "demo-key"})
	client.baseURL = srv.URL
	client.httpClient = srv.Client()
	p := NewProvider(client)

	fake := &entity.Asset{
		ID:     "id-fake",
		Symbol: "USDT",
		Market: entity.ContractMarket("bsc", "0x1280d5752daf7d2ff4f14b31e5c3b2d02cbc0e1f"),
		Type:   entity.AssetTypeCryptocurrency,
	}
	prices, err := p.FetchPrices(context.Background(), []*entity.Asset{fake})
	require.NoError(t, err)
	assert.Empty(t, prices)
	assert.Zero(t, calls, "no coin-ID lookup may be issued for a per-contract row")
}

// TestPlatformAddress: one predicate decides both whether an asset is asked
// about and what is sent, so the two cannot disagree again.
func TestPlatformAddress(t *testing.T) {
	evm := "0x" + strings.Repeat("Ab", 20)
	cases := []struct {
		name, platform, in, want string
		ok                       bool
	}{
		{"evm as is", "ethereum", evm, evm, true},
		{"evm rejects base58", "base", "EPjFWdd5AufqSSqeM2qN1xzybapC8G4wEGGkZwyTDt1v", "", false},
		{"evm rejects garbage", "ethereum", "0xZZ", "", false},
		{"solana mint as is, case kept", "solana", "EPjFWdd5AufqSSqeM2qN1xzybapC8G4wEGGkZwyTDt1v", "EPjFWdd5AufqSSqeM2qN1xzybapC8G4wEGGkZwyTDt1v", true},
		{"solana rejects evm", "solana", evm, "", false},
		{"solana rejects non-base58", "solana", "0OIl" + strings.Repeat("1", 40), "", false},
		{"ton raw to bounceable", "the-open-network", "0:b113a994b5024a16719f69139328eb759596c38a25f59028b146fecdc3621dfe", "EQCxE6mUtQJKFnGfaROTKOt1lZbDiiX1kCixRv7Nw2Id_sDs", true},
		{"ton raw upper hex", "the-open-network", "0:B113A994B5024A16719F69139328EB759596C38A25F59028B146FECDC3621DFE", "EQCxE6mUtQJKFnGfaROTKOt1lZbDiiX1kCixRv7Nw2Id_sDs", true},
		{"ton friendly as is", "the-open-network", "EQCxE6mUtQJKFnGfaROTKOt1lZbDiiX1kCixRv7Nw2Id_sDs", "EQCxE6mUtQJKFnGfaROTKOt1lZbDiiX1kCixRv7Nw2Id_sDs", true},
		{"ton rejects short hash", "the-open-network", "0:b113", "", false},
		{"ton rejects garbage", "the-open-network", "not-a-ton-address", "", false},
		{"unknown platform", "kusama", evm, "", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, ok := platformAddress(c.platform, c.in)
			assert.Equal(t, c.ok, ok)
			assert.Equal(t, c.want, got)
		})
	}
}
