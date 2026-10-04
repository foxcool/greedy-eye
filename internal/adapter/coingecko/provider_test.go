package coingecko

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/foxcool/greedy-eye/internal/entity"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// serveProvider builds a Provider whose client talks to a local server running
// handler instead of CoinGecko. The server is closed when the test ends.
func serveProvider(t *testing.T, handler http.HandlerFunc) *Provider {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	client := NewClient(Config{APIKey: "demo-key"})
	client.baseURL = srv.URL
	client.httpClient = srv.Client()
	return NewProvider(client)
}

// contractAssetOn builds a synced token the way FindOrCreateAsset writes it:
// the contract in the external ref that names its chain, mirrored as a tag.
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
	p := serveProvider(t, func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		addr := r.URL.Query().Get("contract_addresses")
		_, _ = fmt.Fprintf(w, `{"%s": {"usd": 2.5}}`, addr)
	})

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
	p := serveProvider(t, func(w http.ResponseWriter, _ *http.Request) {
		calls++
		_, _ = fmt.Fprint(w, `{}`)
	})

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

// TestFetchPrices_AddressComesFromTheRef: an asset whose counterfeit binding was
// replaced by the canonical one keeps the counterfeit address in its create-time
// tag. Pricing by the tag with the chain of the ref asked CoinGecko about the
// contract the asset had been unbound from (personal-qyxn); address and chain
// must come from the same ref.
func TestFetchPrices_AddressComesFromTheRef(t *testing.T) {
	counterfeit := "0x" + strings.Repeat("e5", 20)
	canonical := "0x" + strings.Repeat("f6", 20)
	onPolygon := "0x" + strings.Repeat("a7", 20)

	var asked []string
	p := serveProvider(t, func(w http.ResponseWriter, r *http.Request) {
		addr := r.URL.Query().Get("contract_addresses")
		asked = append(asked, r.URL.Path+"?"+addr)
		_, _ = fmt.Fprintf(w, `{"%s": {"usd": 1.0}}`, addr)
	})

	rebound := &entity.Asset{
		ID:     "rebound-1",
		Symbol: "TKNR",
		Market: entity.MarketCrypto,
		Tags:   []string{"contract:" + counterfeit},
		ExternalRefs: []entity.AssetExternalRef{
			{AssetID: "rebound-1", Source: entity.OnchainSource("eth"), Ref: canonical},
			{AssetID: "rebound-1", Source: entity.OnchainSource("polygon"), Ref: onPolygon},
		},
	}

	assert.Len(t, p.Asked([]*entity.Asset{rebound}), 1)
	got, err := p.FetchPrices(context.Background(), []*entity.Asset{rebound})
	require.NoError(t, err)

	assert.Equal(t, []string{"/simple/token_price/ethereum?" + canonical}, asked,
		"the oldest routable ref names both the platform and the address; the tag is never asked about")
	require.Len(t, got, 1)
	assert.Equal(t, "rebound-1", got[0].AssetID)
}

// TestFetchPrices_OldestBindingDecides: when the oldest ref is on a chain
// CoinGecko does not list, the asset stays unpriced instead of falling through
// to a later ref. A later ref on a global asset was bound by a ticker-only
// cross-chain guard and may be a lookalike; main never asked about it either.
func TestFetchPrices_OldestBindingDecides(t *testing.T) {
	onKusama := "0x" + strings.Repeat("b8", 20)
	onBase := "0x" + strings.Repeat("c9", 20)

	var calls int
	p := serveProvider(t, func(w http.ResponseWriter, _ *http.Request) {
		calls++
		_, _ = fmt.Fprint(w, `{}`)
	})

	multi := &entity.Asset{
		ID:     "multi-1",
		Symbol: "TKNM",
		Market: entity.MarketCrypto,
		Tags:   []string{"contract:" + onKusama},
		ExternalRefs: []entity.AssetExternalRef{
			{AssetID: "multi-1", Source: entity.OnchainSource("kusama"), Ref: onKusama},
			{AssetID: "multi-1", Source: entity.OnchainSource("base"), Ref: onBase},
		},
	}

	assert.Empty(t, p.Asked([]*entity.Asset{multi}), "not asked, so no miss is recorded against it")
	got, err := p.FetchPrices(context.Background(), []*entity.Asset{multi})
	require.NoError(t, err)
	assert.Empty(t, got)
	assert.Zero(t, calls, "the later base ref is never asked about")
}

// TestFetchPrices_UntaggedAssetIsNotPricedByContract: a global asset created by
// an exchange or broker sync carries refs bound later by a ticker-only
// cross-chain guard, so its oldest ref may be a lookalike deployed elsewhere.
// Routing by it would hand that contract the real asset's quote; the contract
// path stays limited to assets created from a contract.
func TestFetchPrices_UntaggedAssetIsNotPricedByContract(t *testing.T) {
	var calls int
	p := serveProvider(t, func(w http.ResponseWriter, _ *http.Request) {
		calls++
		_, _ = fmt.Fprint(w, `{}`)
	})

	fromExchange := &entity.Asset{
		ID:     "cex-1",
		Symbol: "XYZ",
		Market: entity.MarketCrypto,
		ExternalRefs: []entity.AssetExternalRef{
			{AssetID: "cex-1", Source: entity.OnchainSource("base"), Ref: "0x" + strings.Repeat("d0", 20)},
		},
	}

	assert.Empty(t, p.Asked([]*entity.Asset{fromExchange}), "not asked, so no miss is recorded against it")
	got, err := p.FetchPrices(context.Background(), []*entity.Asset{fromExchange})
	require.NoError(t, err)
	assert.Empty(t, got)
	assert.Zero(t, calls)
}

// TestBudgetExemptSymbols covers the curated set one /coins/markets call
// carries: it is what the sweep refreshes without spending its portion.
func TestBudgetExemptSymbols(t *testing.T) {
	p := NewProvider(NewClient(Config{}))

	got := p.BudgetExemptSymbols()

	assert.Len(t, got, len(nativeCoinID))
	assert.Contains(t, got, "BTC", "symbols are upper-cased to match the catalogue")
}

// TestFetchPrices_CarriesMarketContext: both price paths report volume and
// market cap, and both used to throw them away — prices.volume was NULL for
// every asset in the database, ETH included. Without them a print is all the
// system knows about a token, which is how an airdrop with no market stood
// second in the dev portfolio (personal-6ae).
func TestFetchPrices_CarriesMarketContext(t *testing.T) {
	addr := "0x" + strings.Repeat("a1", 20)

	p := serveProvider(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/coins/markets") {
			_, _ = fmt.Fprint(w, `[{"id":"ethereum","symbol":"eth","current_price":2000,`+
				`"market_cap":240000000000,"total_volume":15000000000}]`)
			return
		}
		_, _ = fmt.Fprintf(w, `{"%s": {"usd": 2.5, "usd_market_cap": 4000000, "usd_24h_vol": 125000}}`, addr)
	})

	got, err := p.FetchPrices(context.Background(), []*entity.Asset{
		{ID: "eth-1", Symbol: "ETH", Market: entity.MarketCrypto},
		contractAssetOn("tkn-1", "eth", addr),
	})
	require.NoError(t, err)
	require.Len(t, got, 2)

	byAsset := map[string]entity.StoredPrice{}
	for _, sp := range got {
		byAsset[sp.AssetID] = sp
	}

	native := byAsset["eth-1"]
	require.True(t, native.Volume.Valid, "the by-id path reports total_volume")
	assert.Equal(t, "1500000000000000000", native.Volume.Decimal.String())
	require.True(t, native.MarketCap.Valid)
	assert.Equal(t, "24000000000000000000", native.MarketCap.Decimal.String())

	token := byAsset["tkn-1"]
	require.True(t, token.Volume.Valid, "the by-contract path reports 24h volume too")
	assert.Equal(t, "12500000000000", token.Volume.Decimal.String())
	require.True(t, token.MarketCap.Valid)
	assert.Equal(t, "400000000000000", token.MarketCap.Decimal.String())
}

// TestStoredPrice_UnreportedFieldsStayNull: an absent number is not a zero one.
// The contract path used to ask for high/low that endpoint ignores and store
// the resulting 0 as a fact, which is how every contract-priced asset in the
// dev catalogue ended up claiming high = low = 0.
func TestStoredPrice_UnreportedFieldsStayNull(t *testing.T) {
	now := time.Now()

	sp := storedPrice("tkn-1", &PriceData{Price: 2.5}, now)

	assert.Equal(t, "250000000", sp.Last.String())
	assert.False(t, sp.High.Valid, "a high nobody reported is unknown, not zero")
	assert.False(t, sp.Low.Valid)
	assert.False(t, sp.Volume.Valid, "no volume means no market context, not an empty market")
	assert.False(t, sp.MarketCap.Valid)
	assert.Equal(t, now, sp.Timestamp)
}

// TestStoredPrice_NegativeValuesAreNotReported: BTL comes back from CoinGecko with
// market_cap = -1. That used to read as a reported value and got scaled into the
// database as a real number, which the confidence gate would then have to reason
// about. A capitalisation below zero is the source failing to say, not a fact.
func TestStoredPrice_NegativeValuesAreNotReported(t *testing.T) {
	sp := storedPrice("btl", &PriceData{Price: 0.5, MarketCap: -1, Volume24h: -1}, time.Now())

	assert.Equal(t, "50000000", sp.Last.String(), "the price itself is untouched")
	assert.False(t, sp.MarketCap.Valid, "market_cap = -1 is not a capitalisation")
	assert.False(t, sp.Volume.Valid)
}

// TestFetchPrices_NonEVMContractsAreSentInThePlatformsForm: Solana and TON were
// mapped to platforms, so Asked() counted their tokens as asked, while the
// client filtered every address through the EVM pattern and sent nothing. Each
// sweep then recorded a miss CoinGecko never earned, and the back-off reached
// its weekly cap — on dev, USD₮ on TON among them (personal-gl8w).
//
// TON is the half that needed more than a filter: wallets report the raw form
// "0:<hex>", and CoinGecko answers that with an empty object. It lists jettons
// by the user-friendly bounceable form, so the address is converted, and the
// price is still filed against the asset that holds the raw one.
func TestFetchPrices_NonEVMContractsAreSentInThePlatformsForm(t *testing.T) {
	const (
		usdtRaw      = "0:b113a994b5024a16719f69139328eb759596c38a25f59028b146fecdc3621dfe"
		usdtFriendly = "EQCxE6mUtQJKFnGfaROTKOt1lZbDiiX1kCixRv7Nw2Id_sDs"
		usdcMint     = "EPjFWdd5AufqSSqeM2qN1xzybapC8G4wEGGkZwyTDt1v"
	)

	asked := map[string]string{}
	p := serveProvider(t, func(w http.ResponseWriter, r *http.Request) {
		addr := r.URL.Query().Get("contract_addresses")
		asked[r.URL.Path] = addr
		_, _ = fmt.Fprintf(w, `{"%s": {"usd": 1.0}}`, addr)
	})

	assets := []*entity.Asset{
		contractAssetOn("ton-usdt", "ton", usdtRaw),
		contractAssetOn("sol-usdc", "solana", usdcMint),
	}
	assert.Len(t, p.Asked(assets), 2)

	got, err := p.FetchPrices(context.Background(), assets)
	require.NoError(t, err)

	assert.Equal(t, map[string]string{
		"/simple/token_price/the-open-network": usdtFriendly,
		"/simple/token_price/solana":           usdcMint,
	}, asked, "the mint as is, the jetton in the form CoinGecko lists it under")
	ids := []string{}
	for _, sp := range got {
		ids = append(ids, sp.AssetID)
	}
	assert.ElementsMatch(t, []string{"ton-usdt", "sol-usdc"}, ids)
}

// TestAsked_AgreesWithWhatIsSent: an address the platform cannot take is not
// asked about, so no miss is filed against an asset no request named.
func TestAsked_AgreesWithWhatIsSent(t *testing.T) {
	var calls int
	p := serveProvider(t, func(w http.ResponseWriter, _ *http.Request) {
		calls++
		_, _ = fmt.Fprint(w, `{}`)
	})

	unsendable := []*entity.Asset{
		contractAssetOn("ton-bad", "ton", "not-a-ton-address"),
		contractAssetOn("sol-evm", "solana", "0x"+strings.Repeat("ab", 20)),
		contractAssetOn("eth-b58", "eth", "EPjFWdd5AufqSSqeM2qN1xzybapC8G4wEGGkZwyTDt1v"),
	}
	assert.Empty(t, p.Asked(unsendable))

	got, err := p.FetchPrices(context.Background(), unsendable)
	require.NoError(t, err)
	assert.Empty(t, got)
	assert.Zero(t, calls)
}
