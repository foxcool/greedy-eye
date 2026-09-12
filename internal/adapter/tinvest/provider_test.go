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
	 "realExchange":"REAL_EXCHANGE_RTS","apiTradeAvailableFlag":true,
	 "tradingStatus":"SECURITY_TRADING_STATUS_NORMAL_TRADING"},
	{"figi":"BBG004730N88","ticker":"SBER","classCode":"TQBR","currency":"rub",
	 "realExchange":"REAL_EXCHANGE_MOEX","apiTradeAvailableFlag":true,
	 "tradingStatus":"SECURITY_TRADING_STATUS_DEALER_NORMAL_TRADING"},
	{"figi":"BBG00BLOCKED0","ticker":"FROZEN","classCode":"SPBXM","currency":"usd",
	 "realExchange":"REAL_EXCHANGE_RTS","apiTradeAvailableFlag":false,"blockedTcaFlag":true,
	 "tradingStatus":"SECURITY_TRADING_STATUS_NOT_AVAILABLE_FOR_TRADING"},
	{"figi":"BBG00FROZENFUND","ticker":"FROZENFUND","classCode":"TQTF","currency":"rub",
	 "realExchange":"REAL_EXCHANGE_MOEX","apiTradeAvailableFlag":true,"blockedTcaFlag":false,
	 "tradingStatus":"SECURITY_TRADING_STATUS_NOT_AVAILABLE_FOR_TRADING"},
	{"figi":"BBG00LATEOPENER","ticker":"LATEOPEN","classCode":"TQBR","currency":"rub",
	 "realExchange":"REAL_EXCHANGE_MOEX","apiTradeAvailableFlag":true,"blockedTcaFlag":false,
	 "tradingStatus":"SECURITY_TRADING_STATUS_NOT_AVAILABLE_FOR_TRADING"}
]}`

// testNow is the clock every price test is measured from. Fixed rather than
// taken from the wall, so a case that is about an age keeps meaning what it
// meant when it was written instead of drifting with the calendar.
var testNow = time.Date(2026, 9, 12, 4, 19, 0, 0, time.UTC)

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
	prov := NewProvider(c)
	prov.now = func() time.Time { return testNow }
	return prov
}

// at moves a provider's clock, for the cases whose whole point is an age.
func at(p *Provider, t time.Time) *Provider {
	p.now = func() time.Time { return t }
	return p
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

// personal-5be7, and the case this adapter got wrong for a month. A share whose
// exchange has shut for the night still has a market: it opens in the morning,
// the position is realisable, and the last print is a trade. Zeroing its
// turnover took every equity out of the total each evening and put it back each
// morning, with nothing bought or sold in between.
//
// The old rule demanded NORMAL_TRADING at the moment of asking, which is the
// session clock, not a fact about the paper.
func TestFetchPricesClosedSessionStillHasAMarket(t *testing.T) {
	p := newProviderWith(t, &stubAPI{
		shares: universe,
		prices: `{"lastPrices":[{"figi":"BBG004730N88","price":{"units":"310","nano":0},
			"time":"2026-09-11T20:49:47Z","lastPriceType":"LAST_PRICE_EXCHANGE"}]}`,
		statuses: `{"tradingStatuses":[{"figi":"BBG004730N88",
			"tradingStatus":"SECURITY_TRADING_STATUS_SESSION_CLOSE","apiTradeAvailableFlag":true}]}`,
	})

	prices, err := p.FetchPrices(context.Background(), []*entity.Asset{
		asset("SBER", "moex", entity.AssetTypeStock, "BBG004730N88"),
	})
	require.NoError(t, err)
	require.Len(t, prices, 1)

	got := prices[0]
	assert.Equal(t, entity.PriceProvenanceTraded, got.Provenance)
	assert.False(t, got.Volume.Valid,
		"a closed session is an age, not an absent market; the freshness axis dates the row")
}

// The row that made the old rule visibly wrong: for an instrument that traded
// this session, it claimed a trade produced the number AND that nothing had
// traded. Whichever way such a row was read, one half of it was false.
//
// NOT A GENERAL INVARIANT, and the name is careful about that: a frozen
// instrument legitimately carries provenance "traded" beside a turnover of zero,
// because its number did come from a trade and its market really has ended. See
// TestFetchPricesSeparatesAClosedNameFromAFrozenOne, which pins exactly that
// pair. The contradiction only exists while the paper is still trading.
func TestFetchPricesATradingNameNeverCarriesATradeWithNoTurnover(t *testing.T) {
	p := newProviderWith(t, &stubAPI{
		shares: universe,
		prices: `{"lastPrices":[{"figi":"BBG004730N88","price":{"units":"310","nano":0},
			"time":"2026-09-11T20:49:47Z","lastPriceType":"LAST_PRICE_EXCHANGE"}]}`,
		statuses: `{"tradingStatuses":[{"figi":"BBG004730N88",
			"tradingStatus":"SECURITY_TRADING_STATUS_CLOSING_AUCTION","apiTradeAvailableFlag":true}]}`,
	})

	prices, err := p.FetchPrices(context.Background(), []*entity.Asset{
		asset("SBER", "moex", entity.AssetTypeStock, "BBG004730N88"),
	})
	require.NoError(t, err)
	require.Len(t, prices, 1)

	got := prices[0]
	assert.Equal(t, entity.PriceProvenanceTraded, got.Provenance,
		"an exchange print is a trade whatever the session is doing")
	assert.False(t, got.Volume.Valid,
		"a turnover of zero beside this session's own trade is a row that cannot be read")
}

// An unanswered status is not a verdict. The status call is best-effort, and
// promoting its silence to "this cannot be traded" would empty a total on every
// hiccup of one endpoint — the same reasoning marketdepth applies to a volume no
// source reported. The catalogue's flags still speak, and they are what keeps
// sanctioned paper out.
func TestFetchPricesUnansweredStatusLeavesCatalogueToSpeak(t *testing.T) {
	p := newProviderWith(t, &stubAPI{
		shares: universe,
		prices: `{"lastPrices":[
			{"figi":"BBG000B9XRY4","price":{"units":"229","nano":0},
			 "time":"2026-08-09T15:30:00Z","lastPriceType":"LAST_PRICE_EXCHANGE"},
			{"figi":"BBG00BLOCKED0","price":{"units":"93","nano":550000000},
			 "time":"2022-03-01T07:00:00Z","lastPriceType":"LAST_PRICE_EXCHANGE"}]}`,
		statuses: `{"tradingStatuses":[]}`,
	})

	prices, err := p.FetchPrices(context.Background(), []*entity.Asset{
		asset("AAPL", "spbex", entity.AssetTypeStock, "BBG000B9XRY4"),
		asset("FROZEN", "spbex", entity.AssetTypeStock, "BBG00BLOCKED0"),
	})
	require.NoError(t, err)
	require.Len(t, prices, 2)

	byAsset := map[string]entity.StoredPrice{}
	for _, pr := range prices {
		byAsset[pr.AssetID] = pr
	}

	assert.False(t, byAsset["asset-AAPL"].Volume.Valid,
		"tradable paper is not condemned by one endpoint's silence")
	require.True(t, byAsset["asset-FROZEN"].Volume.Valid,
		"the catalogue says this cannot be traded, and that does not need confirming")
	assert.True(t, byAsset["asset-FROZEN"].Volume.Decimal.IsZero())
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

// The pair that decides the rule, and the reason neither half of it can stand
// alone. Both wear NOT_AVAILABLE_FOR_TRADING with clean flags at the same
// instant on the live API; what separates them is that one traded last night and
// the other has not traded since it was frozen years ago.
func TestFetchPricesSeparatesAClosedNameFromAFrozenOne(t *testing.T) {
	p := newProviderWith(t, &stubAPI{
		shares: universe,
		prices: `{"lastPrices":[
			{"figi":"BBG00LATEOPENER","price":{"units":"10","nano":225000000},
			 "time":"2026-09-11T20:44:01Z","lastPriceType":"LAST_PRICE_EXCHANGE"},
			{"figi":"BBG00FROZENFUND","price":{"units":"71","nano":570000000},
			 "time":"2022-02-25T20:48:28Z","lastPriceType":"LAST_PRICE_EXCHANGE"}]}`,
		statuses: `{"tradingStatuses":[
			{"figi":"BBG00LATEOPENER","tradingStatus":"SECURITY_TRADING_STATUS_NOT_AVAILABLE_FOR_TRADING",
			 "apiTradeAvailableFlag":true},
			{"figi":"BBG00FROZENFUND","tradingStatus":"SECURITY_TRADING_STATUS_NOT_AVAILABLE_FOR_TRADING",
			 "apiTradeAvailableFlag":true}]}`,
	})

	prices, err := p.FetchPrices(context.Background(), []*entity.Asset{
		asset("LATEOPEN", "moex", entity.AssetTypeStock, "BBG00LATEOPENER"),
		asset("FROZENFUND", "moex", entity.AssetTypeFund, "BBG00FROZENFUND"),
	})
	require.NoError(t, err)
	require.Len(t, prices, 2)

	byAsset := map[string]entity.StoredPrice{}
	for _, pr := range prices {
		byAsset[pr.AssetID] = pr
	}

	assert.False(t, byAsset["asset-LATEOPEN"].Volume.Valid,
		"shut for the night is not the absence of a market: it opens in the morning")
	require.True(t, byAsset["asset-FROZENFUND"].Volume.Valid,
		"frozen years ago: no session is going to reopen this one")
	assert.True(t, byAsset["asset-FROZENFUND"].Volume.Decimal.IsZero())
}

// The catalogue carries the same trading status as the live call, so a status
// call that answered nothing does not turn a frozen fund into a priced one.
func TestFetchPricesFallsBackToTheCatalogueStatus(t *testing.T) {
	p := newProviderWith(t, &stubAPI{
		shares: universe,
		prices: `{"lastPrices":[{"figi":"BBG00FROZENFUND","price":{"units":"71","nano":570000000},
			"time":"2022-02-25T20:48:28Z","lastPriceType":"LAST_PRICE_EXCHANGE"}]}`,
		statuses: `{"tradingStatuses":[]}`,
	})

	prices, err := p.FetchPrices(context.Background(), []*entity.Asset{
		asset("FROZENFUND", "moex", entity.AssetTypeFund, "BBG00FROZENFUND"),
	})
	require.NoError(t, err)
	require.Len(t, prices, 1)
	require.True(t, prices[0].Volume.Valid)
	assert.True(t, prices[0].Volume.Decimal.IsZero())
}

// An evening dealer session is trading. The rule this replaced allowed exactly
// one status constant, and a share in the evening session does not wear it —
// which is how a whole venue's worth of equity left the total each night.
func TestFetchPricesDealerSessionIsATradingSession(t *testing.T) {
	p := newProviderWith(t, &stubAPI{
		shares: universe,
		prices: `{"lastPrices":[{"figi":"BBG004730N88","price":{"units":"283","nano":320000000},
			"time":"2026-09-11T20:49:55Z","lastPriceType":"LAST_PRICE_EXCHANGE"}]}`,
		statuses: `{"tradingStatuses":[{"figi":"BBG004730N88",
			"tradingStatus":"SECURITY_TRADING_STATUS_DEALER_NORMAL_TRADING","apiTradeAvailableFlag":true}]}`,
	})

	prices, err := p.FetchPrices(context.Background(), []*entity.Asset{
		asset("SBER", "moex", entity.AssetTypeStock, "BBG004730N88"),
	})
	require.NoError(t, err)
	require.Len(t, prices, 1)
	assert.False(t, prices[0].Volume.Valid)
}

// The boundary the first attempt at this rule got wrong. pricefresh.DefaultMaxAge
// was borrowed on the grounds that two days clears a weekend — it clears a
// weekend of QUOTES, which a daily rate supplies, not a weekend of TRADES. MOEX
// runs from Friday's close near 20:50 UTC to Monday's opening auction near 07:00
// UTC, about 58 hours, and an instrument outside the extended sessions wears its
// refusal for the whole of it. A 48-hour horizon would have taken the position
// out of the total on Sunday evening and given it back on Monday morning.
func TestFetchPricesSurvivesAWholeWeekendShut(t *testing.T) {
	p := at(newProviderWith(t, &stubAPI{
		shares: universe,
		prices: `{"lastPrices":[{"figi":"BBG00LATEOPENER","price":{"units":"10","nano":225000000},
			"time":"2026-09-11T20:44:01Z","lastPriceType":"LAST_PRICE_EXCHANGE"}]}`,
		statuses: `{"tradingStatuses":[{"figi":"BBG00LATEOPENER",
			"tradingStatus":"SECURITY_TRADING_STATUS_NOT_AVAILABLE_FOR_TRADING",
			"apiTradeAvailableFlag":true}]}`,
	}), time.Date(2026, 9, 14, 6, 0, 0, 0, time.UTC)) // Monday, an hour before the open

	prices, err := p.FetchPrices(context.Background(), []*entity.Asset{
		asset("LATEOPEN", "moex", entity.AssetTypeStock, "BBG00LATEOPENER"),
	})
	require.NoError(t, err)
	require.Len(t, prices, 1)
	assert.False(t, prices[0].Volume.Valid,
		"57 hours shut over a weekend is a calendar, not the end of a market")
}

// The venue answers in a dealer dialect outside the main session, and it spells
// its refusal there too. Matching only the one constant that was measured would
// have let a 2022 print into the total on every such hour.
func TestFetchPricesReadsTheDealerDialectRefusal(t *testing.T) {
	p := newProviderWith(t, &stubAPI{
		shares: universe,
		prices: `{"lastPrices":[{"figi":"BBG00FROZENFUND","price":{"units":"71","nano":570000000},
			"time":"2022-02-25T20:48:28Z","lastPriceType":"LAST_PRICE_EXCHANGE"}]}`,
		statuses: `{"tradingStatuses":[{"figi":"BBG00FROZENFUND",
			"tradingStatus":"SECURITY_TRADING_STATUS_DEALER_NOT_AVAILABLE_FOR_TRADING",
			"apiTradeAvailableFlag":true}]}`,
	})

	prices, err := p.FetchPrices(context.Background(), []*entity.Asset{
		asset("FROZENFUND", "moex", entity.AssetTypeFund, "BBG00FROZENFUND"),
	})
	require.NoError(t, err)
	require.Len(t, prices, 1)
	require.True(t, prices[0].Volume.Valid)
	assert.True(t, prices[0].Volume.Decimal.IsZero())
}

// proto3 JSON omits an unspecified enum, so a record can answer about an
// instrument and say nothing about its mode. That must not read as permission:
// a weaker answer does not get to overrule the catalogue's refusal.
func TestFetchPricesDoesNotReadSilenceAsPermission(t *testing.T) {
	p := newProviderWith(t, &stubAPI{
		shares: universe,
		prices: `{"lastPrices":[{"figi":"BBG00FROZENFUND","price":{"units":"71","nano":570000000},
			"time":"2022-02-25T20:48:28Z","lastPriceType":"LAST_PRICE_EXCHANGE"}]}`,
		statuses: `{"tradingStatuses":[{"figi":"BBG00FROZENFUND","apiTradeAvailableFlag":true}]}`,
	})

	prices, err := p.FetchPrices(context.Background(), []*entity.Asset{
		asset("FROZENFUND", "moex", entity.AssetTypeFund, "BBG00FROZENFUND"),
	})
	require.NoError(t, err)
	require.Len(t, prices, 1)
	require.True(t, prices[0].Volume.Valid,
		"an unspecified mode is not a trading mode")
	assert.True(t, prices[0].Volume.Decimal.IsZero())
}

// The live record's own refusal to let the API trade an instrument is fresher
// than the catalogue's flags, which are a snapshot up to catalogTTL old. It
// counts as a refusal — and, like every refusal, only beside a print old enough
// to mean the market is gone rather than shut.
func TestFetchPricesReadsTheLiveApiRefusal(t *testing.T) {
	p := newProviderWith(t, &stubAPI{
		shares: universe,
		prices: `{"lastPrices":[{"figi":"BBG004730N88","price":{"units":"283","nano":320000000},
			"time":"2022-02-25T20:49:55Z","lastPriceType":"LAST_PRICE_EXCHANGE"}]}`,
		statuses: `{"tradingStatuses":[{"figi":"BBG004730N88",
			"tradingStatus":"SECURITY_TRADING_STATUS_DEALER_NORMAL_TRADING","apiTradeAvailableFlag":false}]}`,
	})

	prices, err := p.FetchPrices(context.Background(), []*entity.Asset{
		asset("SBER", "moex", entity.AssetTypeStock, "BBG004730N88"),
	})
	require.NoError(t, err)
	require.Len(t, prices, 1)
	require.True(t, prices[0].Volume.Valid,
		"the catalogue says tradable, the venue says otherwise, and nobody has traded it in four years")
	assert.True(t, prices[0].Volume.Decimal.IsZero())
}

// The deliberate cost of putting the flags inside the conjunction. Paper blocked
// today is still valued from the trade that really happened today,
// for as long as that trade counts as recent. What it cannot be exited for is a
// liquidity claim, and inventing a turnover of zero is the wrong instrument for
// making it (personal-dkae).
func TestFetchPricesKeepsFreshlyBlockedPaperInTheTotal(t *testing.T) {
	p := newProviderWith(t, &stubAPI{
		shares: universe,
		prices: `{"lastPrices":[{"figi":"BBG00BLOCKED0","price":{"units":"93","nano":550000000},
			"time":"2026-09-11T15:30:00Z","lastPriceType":"LAST_PRICE_EXCHANGE"}]}`,
		statuses: `{"tradingStatuses":[{"figi":"BBG00BLOCKED0",
			"tradingStatus":"SECURITY_TRADING_STATUS_NOT_AVAILABLE_FOR_TRADING"}]}`,
	})

	prices, err := p.FetchPrices(context.Background(), []*entity.Asset{
		asset("FROZEN", "spbex", entity.AssetTypeStock, "BBG00BLOCKED0"),
	})
	require.NoError(t, err)
	require.Len(t, prices, 1)
	assert.False(t, prices[0].Volume.Valid,
		"blocked with yesterday's trade behind it is not a market that has ended")
}

// The way back INTO the total, found by review after the first fix and closed by
// making refusal a disjunction. The venue answers about an instrument with a
// state that describes the trading day — a closed session, a break, an auction —
// and those are on the whitelist, correctly, because they say nothing about the
// paper. Letting the live answer REPLACE the catalogue's verdict therefore
// laundered a frozen fund: its own catalogue row says NOT_AVAILABLE_FOR_TRADING,
// and a Sunday sweep that got SESSION_CLOSE put a years-old print back into the
// sum at full value. That is personal-5be7 with the sign reversed, and by the
// ordering rule of this project it is the worse half: a total that discloses a
// gap can be read, one that overstates cannot.
func TestFetchPricesSessionStateDoesNotLaunderAFrozenFund(t *testing.T) {
	for _, mode := range []string{
		StatusSessionClose,
		StatusBreakInTrading,
		StatusDealerBreakInTrading,
		StatusClosingAuction,
		StatusOpeningPeriod,
	} {
		t.Run(mode, func(t *testing.T) {
			p := newProviderWith(t, &stubAPI{
				shares: universe,
				prices: `{"lastPrices":[{"figi":"BBG00FROZENFUND","price":{"units":"71","nano":570000000},
					"time":"2022-02-25T20:48:28Z","lastPriceType":"LAST_PRICE_EXCHANGE"}]}`,
				statuses: `{"tradingStatuses":[{"figi":"BBG00FROZENFUND",
					"tradingStatus":"` + mode + `","apiTradeAvailableFlag":true}]}`,
			})

			prices, err := p.FetchPrices(context.Background(), []*entity.Asset{
				asset("FROZENFUND", "moex", entity.AssetTypeFund, "BBG00FROZENFUND"),
			})
			require.NoError(t, err)
			require.Len(t, prices, 1)
			require.True(t, prices[0].Volume.Valid,
				"the catalogue's refusal is not withdrawn by a state describing the trading day")
			assert.True(t, prices[0].Volume.Decimal.IsZero())
		})
	}
}
