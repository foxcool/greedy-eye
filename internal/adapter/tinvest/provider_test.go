package tinvest

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/foxcool/greedy-eye/internal/entity"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// universe is the instrument catalogue a test server answers with. Two AAPLs on
// purpose: one on the SPB Exchange and one at a dealer desk, which is the shape
// that must refuse to bind.
const universe = `{"instruments":[
	{"figi":"BBG000B9XRY4","ticker":"AAPL","classCode":"SPBXM","currency":"usd",
	 "realExchange":"REAL_EXCHANGE_RTS","apiTradeAvailableFlag":true},
	{"figi":"BBG004730N88","ticker":"SBER","classCode":"TQBR","currency":"rub",
	 "realExchange":"REAL_EXCHANGE_MOEX","apiTradeAvailableFlag":true},
	{"figi":"BBG00BLOCKED0","ticker":"FROZEN","classCode":"SPBXM","currency":"usd",
	 "realExchange":"REAL_EXCHANGE_RTS","apiTradeAvailableFlag":false,"blockedTcaFlag":true}
]}`

// dealerDuplicate adds a second AAPL so the ticker resolves to two instruments
// on venues the adapter treats as one market.
const dealerDuplicate = `{"instruments":[
	{"figi":"BBG000DEALER1","ticker":"AAPL","classCode":"SPBXM","currency":"usd",
	 "realExchange":"REAL_EXCHANGE_RTS","apiTradeAvailableFlag":true}
]}`

type stubAPI struct {
	shares   string
	etfs     string
	prices   string
	statuses string
	calls    map[string]int
}

func newProviderWith(t *testing.T, api *stubAPI) *Provider {
	t.Helper()
	if api.calls == nil {
		api.calls = map[string]int{}
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		api.calls[r.URL.Path]++
		body := map[string]string{
			pathShares:          api.shares,
			pathEtfs:            api.etfs,
			pathLastPrices:      api.prices,
			pathTradingStatuses: api.statuses,
		}[r.URL.Path]
		if body == "" {
			body = `{}`
		}
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)

	c, err := NewClient(Config{Token: "test-token", BaseURL: srv.URL, Transport: srv.Client().Transport})
	require.NoError(t, err)
	return NewProvider(c)
}

func asset(symbol, market string, typ entity.AssetType, figi string) *entity.Asset {
	a := &entity.Asset{ID: "asset-" + symbol, Symbol: symbol, Market: market, Type: typ}
	if figi != "" {
		a.ExternalRefs = []entity.AssetExternalRef{{Source: RefSource, Ref: figi, Origin: entity.RefOriginAuto}}
	}
	return a
}

func TestSpeaksFor(t *testing.T) {
	p := newProviderWith(t, &stubAPI{})
	tests := []struct {
		name  string
		asset *entity.Asset
		want  bool
	}{
		{"spbex share", asset("AAPL", "spbex", entity.AssetTypeStock, ""), true},
		{"moex fund", asset("TMOS", "moex", entity.AssetTypeFund, ""), true},
		{"crypto is somebody else's", asset("BTC", "crypto", entity.AssetTypeCryptocurrency, ""), false},
		// Bonds quote as a percentage of nominal. Pricing them here would
		// publish a percentage as money.
		{"bond stays out", asset("SU26238", "moex", entity.AssetTypeBond, ""), false},
		{"no symbol", asset("", "spbex", entity.AssetTypeStock, ""), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, p.speaksFor(tt.asset))
		})
	}
}

func TestDiscoverRefsBindsExactlyOneMatch(t *testing.T) {
	p := newProviderWith(t, &stubAPI{shares: universe})

	refs, err := p.DiscoverRefs(context.Background(), []*entity.Asset{
		asset("AAPL", "spbex", entity.AssetTypeStock, ""),
		asset("SBER", "moex", entity.AssetTypeStock, ""),
	})
	require.NoError(t, err)
	require.Len(t, refs, 2)
	assert.Equal(t, RefSource, refs[0].Source)
	assert.Equal(t, "BBG000B9XRY4", refs[0].Ref)
	assert.Equal(t, entity.RefOriginAuto, refs[0].Origin)
	assert.Equal(t, "BBG004730N88", refs[1].Ref)
}

// The rule the whole binding layer exists for: two candidates is not a
// binding. Picking one silently prices a position as somebody else's paper and
// survives every later sweep.
func TestDiscoverRefsRefusesAmbiguousTicker(t *testing.T) {
	var both struct {
		Instruments []json.RawMessage `json:"instruments"`
	}
	require.NoError(t, json.Unmarshal([]byte(universe), &both))
	var extra struct {
		Instruments []json.RawMessage `json:"instruments"`
	}
	require.NoError(t, json.Unmarshal([]byte(dealerDuplicate), &extra))
	both.Instruments = append(both.Instruments, extra.Instruments...)
	merged, err := json.Marshal(both)
	require.NoError(t, err)

	p := newProviderWith(t, &stubAPI{shares: string(merged)})

	refs, err := p.DiscoverRefs(context.Background(), []*entity.Asset{
		asset("AAPL", "spbex", entity.AssetTypeStock, ""),
	})
	require.NoError(t, err)
	assert.Empty(t, refs)
}

func TestDiscoverRefsSkipsWhatIsAlreadyBound(t *testing.T) {
	api := &stubAPI{shares: universe}
	p := newProviderWith(t, api)

	refs, err := p.DiscoverRefs(context.Background(), []*entity.Asset{
		asset("AAPL", "spbex", entity.AssetTypeStock, "BBG000B9XRY4"),
	})
	require.NoError(t, err)
	assert.Empty(t, refs)
	assert.Zero(t, api.calls[pathShares], "a bound asset does not need the catalogue")
}

// A live market print: a trade made the number, so no turnover is claimed —
// none was measured, and marketdepth reads an absent volume as "no claim".
func TestFetchPricesTradedInstrumentClaimsNoVolume(t *testing.T) {
	p := newProviderWith(t, &stubAPI{
		shares: universe,
		prices: `{"lastPrices":[{"figi":"BBG000B9XRY4","price":{"units":"229","nano":250000000},
			"time":"2026-08-09T15:30:00Z","lastPriceType":"LAST_PRICE_EXCHANGE"}]}`,
		statuses: `{"tradingStatuses":[{"figi":"BBG000B9XRY4",
			"tradingStatus":"SECURITY_TRADING_STATUS_NORMAL_TRADING","apiTradeAvailableFlag":true}]}`,
	})

	prices, err := p.FetchPrices(context.Background(), []*entity.Asset{
		asset("AAPL", "spbex", entity.AssetTypeStock, "BBG000B9XRY4"),
	})
	require.NoError(t, err)
	require.Len(t, prices, 1)

	got := prices[0]
	assert.Equal(t, "USD", got.BaseSymbol, "the instrument's own currency, not the provider default")
	assert.Equal(t, entity.PriceProvenanceTraded, got.Provenance)
	assert.False(t, got.Volume.Valid, "no turnover was measured, so none is claimed")
	assert.Equal(t, "22925000000", got.Last.String())
	assert.Equal(t, time.Date(2026, 8, 9, 15, 30, 0, 0, time.UTC), got.Timestamp.UTC(),
		"dated by the exchange, never by the sweep")
}

// The reason this adapter needed care. The API reports no turnover, and an
// absent volume passes the ADR-009 gate — so a sanctioned share whose last
// trade was years ago would sail into the total at a price nobody can transact
// at. A halted instrument traded nothing today, and saying so is a measurement.
func TestFetchPricesHaltedInstrumentReportsZeroTurnover(t *testing.T) {
	p := newProviderWith(t, &stubAPI{
		shares: universe,
		prices: `{"lastPrices":[{"figi":"BBG00BLOCKED0","price":{"units":"93","nano":550000000},
			"time":"2022-03-01T07:00:00Z","lastPriceType":"LAST_PRICE_EXCHANGE"}]}`,
		statuses: `{"tradingStatuses":[{"figi":"BBG00BLOCKED0",
			"tradingStatus":"SECURITY_TRADING_STATUS_NOT_AVAILABLE_FOR_TRADING"}]}`,
	})

	prices, err := p.FetchPrices(context.Background(), []*entity.Asset{
		asset("FROZEN", "spbex", entity.AssetTypeStock, "BBG00BLOCKED0"),
	})
	require.NoError(t, err)
	require.Len(t, prices, 1)

	got := prices[0]
	require.True(t, got.Volume.Valid, "an instrument that cannot trade traded nothing, and that is measured")
	assert.True(t, got.Volume.Decimal.IsZero())
	// Still a trade, just an old one. Calling it an appraisal would misdescribe
	// it: its problems are age and the absence of a market today, and both are
	// reported by other means.
	assert.Equal(t, entity.PriceProvenanceTraded, got.Provenance)
	assert.Equal(t, 2022, got.Timestamp.UTC().Year())
}

// A market maker stated this number; no trade made it. That is the same claim
// MOEX's recognised close makes, and it carries the same provenance.
func TestFetchPricesDealerQuoteIsAppraised(t *testing.T) {
	p := newProviderWith(t, &stubAPI{
		shares: universe,
		prices: `{"lastPrices":[{"figi":"BBG000B9XRY4","price":{"units":"229","nano":0},
			"time":"2026-08-09T15:30:00Z","lastPriceType":"LAST_PRICE_DEALER"}]}`,
		statuses: `{"tradingStatuses":[{"figi":"BBG000B9XRY4",
			"tradingStatus":"SECURITY_TRADING_STATUS_NORMAL_TRADING","apiTradeAvailableFlag":true}]}`,
	})

	prices, err := p.FetchPrices(context.Background(), []*entity.Asset{
		asset("AAPL", "spbex", entity.AssetTypeStock, "BBG000B9XRY4"),
	})
	require.NoError(t, err)
	require.Len(t, prices, 1)
	assert.Equal(t, entity.PriceProvenanceAppraised, prices[0].Provenance)
	require.True(t, prices[0].Volume.Valid)
	assert.True(t, prices[0].Volume.Decimal.IsZero())
}

// The status call is the only thing that can say there is a market right now.
// When it fails, the conservative reading keeps the price out of the total
// rather than letting it in unmeasured.
func TestFetchPricesTreatsUnknownStatusAsNoMarket(t *testing.T) {
	p := newProviderWith(t, &stubAPI{
		shares: universe,
		prices: `{"lastPrices":[{"figi":"BBG000B9XRY4","price":{"units":"229","nano":0},
			"time":"2026-08-09T15:30:00Z","lastPriceType":"LAST_PRICE_EXCHANGE"}]}`,
		statuses: `{"tradingStatuses":[]}`,
	})

	prices, err := p.FetchPrices(context.Background(), []*entity.Asset{
		asset("AAPL", "spbex", entity.AssetTypeStock, "BBG000B9XRY4"),
	})
	require.NoError(t, err)
	require.Len(t, prices, 1)
	require.True(t, prices[0].Volume.Valid)
	assert.True(t, prices[0].Volume.Decimal.IsZero())
}

// Without the instrument there is no currency, and publishing a dollar figure
// as roubles is a hundredfold error rather than a rounding one.
func TestFetchPricesDropsInstrumentMissingFromCatalogue(t *testing.T) {
	// A universe that holds SOMETHING but not this instrument. An empty one
	// would test a different thing: a catalogue that answered with nothing at
	// all is a broken response, and ensure() now refuses it rather than
	// treating every instrument as unknown.
	p := newProviderWith(t, &stubAPI{
		shares: `{"instruments":[{"figi":"BBG004730N88","ticker":"GAZP","currency":"rub","lot":10,
			"realExchange":"REAL_EXCHANGE_MOEX","apiTradeAvailableFlag":true}]}`,
		prices: `{"lastPrices":[{"figi":"BBG000B9XRY4","price":{"units":"229","nano":0},
			"time":"2026-08-09T15:30:00Z","lastPriceType":"LAST_PRICE_EXCHANGE"}]}`,
		statuses: `{"tradingStatuses":[{"figi":"BBG000B9XRY4",
			"tradingStatus":"SECURITY_TRADING_STATUS_NORMAL_TRADING","apiTradeAvailableFlag":true}]}`,
	})

	prices, err := p.FetchPrices(context.Background(), []*entity.Asset{
		asset("AAPL", "spbex", entity.AssetTypeStock, "BBG000B9XRY4"),
	})
	require.NoError(t, err)
	assert.Empty(t, prices)
}

// An undated print cannot be dated "now" without claiming a freshness nobody
// measured, so it is not published at all.
func TestFetchPricesDropsUndatedPrint(t *testing.T) {
	p := newProviderWith(t, &stubAPI{
		shares:   universe,
		prices:   `{"lastPrices":[{"figi":"BBG000B9XRY4","price":{"units":"229","nano":0}}]}`,
		statuses: `{"tradingStatuses":[]}`,
	})

	prices, err := p.FetchPrices(context.Background(), []*entity.Asset{
		asset("AAPL", "spbex", entity.AssetTypeStock, "BBG000B9XRY4"),
	})
	require.NoError(t, err)
	assert.Empty(t, prices)
}

// An unbound asset is not priced by ticker as a fallback: that is exactly the
// guess the binding step exists to avoid.
func TestFetchPricesSkipsUnboundAsset(t *testing.T) {
	api := &stubAPI{shares: universe}
	p := newProviderWith(t, api)

	prices, err := p.FetchPrices(context.Background(), []*entity.Asset{
		asset("AAPL", "spbex", entity.AssetTypeStock, ""),
	})
	require.NoError(t, err)
	assert.Empty(t, prices)
	assert.Zero(t, api.calls[pathLastPrices])
}

// A rouble instrument and a dollar instrument come out of one response with
// their own bases. One base per provider cannot describe that.
func TestFetchPricesCarriesPerInstrumentCurrency(t *testing.T) {
	p := newProviderWith(t, &stubAPI{
		shares: universe,
		prices: `{"lastPrices":[
			{"figi":"BBG000B9XRY4","price":{"units":"229","nano":0},"time":"2026-08-09T15:30:00Z","lastPriceType":"LAST_PRICE_EXCHANGE"},
			{"figi":"BBG004730N88","price":{"units":"311","nano":40000000},"time":"2026-08-09T15:30:00Z","lastPriceType":"LAST_PRICE_EXCHANGE"}]}`,
		statuses: `{"tradingStatuses":[
			{"figi":"BBG000B9XRY4","tradingStatus":"SECURITY_TRADING_STATUS_NORMAL_TRADING","apiTradeAvailableFlag":true},
			{"figi":"BBG004730N88","tradingStatus":"SECURITY_TRADING_STATUS_NORMAL_TRADING","apiTradeAvailableFlag":true}]}`,
	})

	prices, err := p.FetchPrices(context.Background(), []*entity.Asset{
		asset("AAPL", "spbex", entity.AssetTypeStock, "BBG000B9XRY4"),
		asset("SBER", "moex", entity.AssetTypeStock, "BBG004730N88"),
	})
	require.NoError(t, err)
	require.Len(t, prices, 2)

	bases := map[string]string{}
	for _, p := range prices {
		bases[p.AssetID] = p.BaseSymbol
	}
	assert.Equal(t, map[string]string{"asset-AAPL": "USD", "asset-SBER": "RUB"}, bases)
}

// The universe is fetched once and reused: a per-asset lookup would put a
// request behind every position of every sweep.
func TestCatalogueIsFetchedOncePerTTL(t *testing.T) {
	api := &stubAPI{shares: universe}
	p := newProviderWith(t, api)

	for range 3 {
		_, err := p.DiscoverRefs(context.Background(), []*entity.Asset{
			asset("AAPL", "spbex", entity.AssetTypeStock, ""),
		})
		require.NoError(t, err)
	}
	assert.Equal(t, 1, api.calls[pathShares])
	assert.Equal(t, 1, api.calls[pathEtfs])
}
