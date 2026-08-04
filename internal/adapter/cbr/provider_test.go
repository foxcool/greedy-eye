package cbr

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/foxcool/greedy-eye/internal/entity"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// currency builds a catalogue row of the kind the feed may speak for.
func currency(id, symbol string) *entity.Asset {
	return &entity.Asset{ID: id, Symbol: symbol, Type: entity.AssetTypeForex, Market: "forex"}
}

// fetch runs the provider against the shared fixture set.
func fetch(t *testing.T, assets ...*entity.Asset) []entity.StoredPrice {
	t.Helper()
	prices, err := NewProvider(serveWindows1251(t, dailyXML)).FetchPrices(context.Background(), assets)
	require.NoError(t, err)
	return prices
}

// priceOf finds the stored price for an asset id.
func priceOf(t *testing.T, prices []entity.StoredPrice, assetID string) entity.StoredPrice {
	t.Helper()
	for _, p := range prices {
		if p.AssetID == assetID {
			return p
		}
	}
	t.Fatalf("no price stored for asset %s", assetID)
	return entity.StoredPrice{}
}

func TestFetchPricesQuotesTheRoubleInUSD(t *testing.T) {
	prices := fetch(t, currency("rub-1", "RUB"))

	// 78.50 roubles to the dollar, so one rouble is 1/78.5 = 0.01273885 USD,
	// scaled by 8 decimals. This row is the whole point of the provider: a
	// moex position priced in roubles reaches a USD total through it.
	got := priceOf(t, prices, "rub-1")
	assert.Equal(t, "1273885", got.Last.String())
	assert.Equal(t, uint32(8), got.Decimals)
	assert.Equal(t, ProviderName, got.SourceID)
	assert.Empty(t, got.BaseAssetID, "the handler resolves the base asset UUID")
}

func TestFetchPricesConvertsThroughTheUSDLeg(t *testing.T) {
	prices := fetch(t, currency("eur-1", "EUR"), currency("kzt-1", "KZT"))

	// 91.2345 roubles to the euro against 78.5 to the dollar: 1.16222...
	assert.Equal(t, "116222293", priceOf(t, prices, "eur-1").Last.String())
	// 0.145 roubles to the tenge: 0.00184713...
	assert.Equal(t, "184713", priceOf(t, prices, "kzt-1").Last.String())
}

func TestFetchPricesDatesTheSetNotTheRequest(t *testing.T) {
	prices := fetch(t, currency("eur-1", "EUR"))

	// A Friday set fetched on Sunday is two days old. Timestamping it "now"
	// would hide staleness behind the sweep's own punctuality.
	assert.Equal(t, "2026-08-04T00:00:00+03:00", priceOf(t, prices, "eur-1").Timestamp.Format(time.RFC3339))
}

func TestFetchPricesLeavesTheQuoteCurrencyAlone(t *testing.T) {
	assert.Empty(t, fetch(t, currency("usd-1", "USD")),
		"the base does not need quoting against itself")
}

func TestFetchPricesIgnoresNonCurrencies(t *testing.T) {
	// A token that copied a currency's ticker. Pricing it from the feed is the
	// counterfeit-USDT failure with a different ticker.
	token := &entity.Asset{
		ID: "fake-1", Symbol: "EUR",
		Type:   entity.AssetTypeCryptocurrency,
		Market: entity.ContractMarket("base", "0xdeadbeef"),
	}
	// Same trick, one step further: typed forex to reach the rate anyway.
	typedForex := &entity.Asset{
		ID: "fake-2", Symbol: "EUR",
		Type:   entity.AssetTypeForex,
		Market: entity.ContractMarket("bsc", "0xfeedface"),
	}

	assert.Empty(t, fetch(t, token, typedForex))
}

func TestFetchPricesSkipsCurrenciesTheSetDoesNotCarry(t *testing.T) {
	prices := fetch(t, currency("gbp-1", "GBP"), currency("eur-1", "EUR"))

	require.Len(t, prices, 1, "an absent currency is a miss, not a guess")
	assert.Equal(t, "eur-1", prices[0].AssetID)
}

func TestFetchPricesAsksNothingWithoutCandidates(t *testing.T) {
	var called bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)

	p := NewProvider(NewClient(Config{BaseURL: srv.URL}))
	prices, err := p.FetchPrices(context.Background(), []*entity.Asset{
		{ID: "btc-1", Symbol: "BTC", Type: entity.AssetTypeCryptocurrency, Market: entity.MarketCrypto},
	})

	require.NoError(t, err)
	assert.Empty(t, prices)
	assert.False(t, called, "a sweep carrying no currency must not spend a request")
}

func TestFetchPricesFailsWithoutTheUSDLeg(t *testing.T) {
	body := `<?xml version="1.0" encoding="windows-1251"?>` +
		`<ValCurs Date="04.08.2026" name="Foreign Currency Market">` +
		`<Valute ID="R01239"><CharCode>EUR</CharCode><Nominal>1</Nominal><Value>91,2345</Value></Valute>` +
		`</ValCurs>`

	_, err := NewProvider(serveWindows1251(t, body)).
		FetchPrices(context.Background(), []*entity.Asset{currency("eur-1", "EUR")})

	require.Error(t, err, "without the dollar rate there is nothing to publish, not even the rouble")
}

func TestFetchPricesDropsARateThatUnderflowsTheScale(t *testing.T) {
	// One unit worth less than 1e-8 USD rounds to zero, and a zero price reads
	// as a counted position worth nothing rather than an unpriced one.
	body := `<?xml version="1.0" encoding="windows-1251"?>` +
		`<ValCurs Date="04.08.2026" name="Foreign Currency Market">` +
		`<Valute ID="R01235"><CharCode>USD</CharCode><Nominal>1</Nominal><Value>78,5000</Value></Valute>` +
		`<Valute ID="R01700"><CharCode>ZWL</CharCode><Nominal>100000000</Nominal><Value>0,0001</Value></Valute>` +
		`</ValCurs>`

	prices, err := NewProvider(serveWindows1251(t, body)).
		FetchPrices(context.Background(), []*entity.Asset{currency("zwl-1", "ZWL")})

	require.NoError(t, err)
	assert.Empty(t, prices)
}

func TestBudgetExemptCoversEverythingThisProviderPrices(t *testing.T) {
	p := NewProvider(NewClient(Config{}))

	exempt := make(map[string]bool, len(p.BudgetExemptSymbols()))
	for _, s := range p.BudgetExemptSymbols() {
		exempt[s] = true
	}

	assert.True(t, exempt["RUB"], "the rouble is the reason this provider exists")
	assert.True(t, exempt["EUR"])
	assert.False(t, exempt["USD"], "the quote side would come back unpriced every sweep")

	// One document covers the whole list, so there is no per-asset allowance
	// to hand out: anything outside it is not priceable here at any budget.
	n, ok := p.AssetBudget(time.Now(), time.Hour)
	assert.True(t, ok)
	assert.Zero(t, n)
}

func TestProviderQuotesInUSD(t *testing.T) {
	p := NewProvider(NewClient(Config{}))
	assert.Equal(t, "USD", p.BaseAssetSymbol())
	assert.Equal(t, entity.AssetTypeForex, p.BaseAssetType())
}
