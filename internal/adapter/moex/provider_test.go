package moex

import (
	"context"
	"testing"
	"time"

	"github.com/foxcool/greedy-eye/internal/entity"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func listed(id, symbol string, t entity.AssetType) *entity.Asset {
	return &entity.Asset{ID: id, Symbol: symbol, Type: t, Market: MarketName}
}

// fetchWith runs the provider against a stub serving one body for every market.
func fetchWith(t *testing.T, body string, assets ...*entity.Asset) []entity.StoredPrice {
	t.Helper()
	client, _ := serveISS(t, body)
	prices, err := NewProvider(client).FetchPrices(context.Background(), assets)
	require.NoError(t, err)
	return prices
}

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

func TestSharePricedFromTheTradedBoard(t *testing.T) {
	prices := fetchWith(t, sharesJSON, listed("sber-1", "SBER", entity.AssetTypeStock))

	got := priceOf(t, prices, "sber-1")
	// 284.60 roubles, from TQBR — not 315.28 from the board that did not trade.
	assert.Equal(t, "28460000000", got.Last.String())
	assert.Equal(t, uint32(8), got.Decimals)
	assert.Equal(t, ProviderName, got.SourceID)
	assert.Empty(t, got.BaseAssetID, "the handler resolves the base asset UUID")
	assert.Equal(t, "2026-08-04T23:50:46+03:00", got.Timestamp.Format(time.RFC3339))
	assert.True(t, got.Volume.Valid, "a traded print carries the day's turnover")
	assert.Equal(t, "1496727706600000000", got.Volume.Decimal.String())
}

func TestFundIsPricedOnTheSameMarketAsShares(t *testing.T) {
	prices := fetchWith(t, sharesJSON, listed("sbmx-1", "SBMX", entity.AssetTypeFund))

	assert.Equal(t, "1617300000", priceOf(t, prices, "sbmx-1").Last.String())
}

func TestBondQuoteIsAPercentageOfNominal(t *testing.T) {
	prices := fetchWith(t, bondsJSON, listed("ofz-1", "SU26238RMFS4", entity.AssetTypeBond))

	// 56.75% of a 1000 rouble nominal is 567.50, not 56.75. Storing the quote
	// as roubles would understate the position seventeenfold.
	assert.Equal(t, "56750000000", priceOf(t, prices, "ofz-1").Last.String())
}

func TestBondWithoutARoubleNominalIsLeftUnpriced(t *testing.T) {
	// A eurobond: quoted as a percentage of a nominal denominated in dollars.
	// Publishing it under a rouble base would be wrong by the exchange rate.
	body := `{
	  "securities": {
	    "columns": ["SECID","BOARDID","CURRENCYID","PREVPRICE","PREVDATE","FACEVALUE","FACEUNIT"],
	    "data": [["RU000A0JWHA4","TQOD","SUR",98.5,"2026-08-01",1000,"USD"]]
	  },
	  "marketdata": {"columns": ["SECID","BOARDID","LAST","VALTODAY"], "data": []}
	}`

	assert.Empty(t, fetchWith(t, body, listed("euro-1", "RU000A0JWHA4", entity.AssetTypeBond)))
}

func TestQuoteInAnotherCurrencyIsLeftUnpriced(t *testing.T) {
	body := `{
	  "securities": {
	    "columns": ["SECID","BOARDID","CURRENCYID","PREVPRICE","PREVDATE","FACEVALUE","FACEUNIT"],
	    "data": [["USDSHARE","TQBR","USD",12.5,"2026-08-01",0,"SUR"]]
	  },
	  "marketdata": {"columns": ["SECID","BOARDID","LAST","VALTODAY"], "data": []}
	}`

	assert.Empty(t, fetchWith(t, body, listed("usd-1", "USDSHARE", entity.AssetTypeStock)))
}

func TestPreviousCloseIsDatedToItsOwnSessionAndCarriesNoVolume(t *testing.T) {
	// Outside trading hours the previous close is the honest current price. Its
	// timestamp belongs to that session, and today's zero turnover says nothing
	// about it — reporting the zero would gate every MOEX position as a thin
	// market on every weekend.
	body := `{
	  "securities": {
	    "columns": ["SECID","BOARDID","CURRENCYID","PREVPRICE","PREVDATE","FACEVALUE","FACEUNIT"],
	    "data": [["SBER","TQBR","SUR",281.39,"2026-08-01",3,"SUR"]]
	  },
	  "marketdata": {
	    "columns": ["SECID","BOARDID","LAST","VALTODAY","SYSTIME"],
	    "data": [["SBER","TQBR",null,0,"2026-08-03 10:00:00"]]
	  }
	}`

	got := priceOf(t, fetchWith(t, body, listed("sber-1", "SBER", entity.AssetTypeStock)), "sber-1")
	assert.Equal(t, "28139000000", got.Last.String())
	assert.Equal(t, "2026-08-01T00:00:00+03:00", got.Timestamp.Format(time.RFC3339))
	assert.False(t, got.Volume.Valid, "yesterday's price did not trade today")
}

func TestUntradedBoardsFallBackToTheRanking(t *testing.T) {
	// Nothing traded anywhere, so the main board decides rather than the order
	// ISS happened to answer in.
	body := `{
	  "securities": {
	    "columns": ["SECID","BOARDID","CURRENCYID","PREVPRICE","PREVDATE","FACEVALUE","FACEUNIT"],
	    "data": [
	      ["SBER","SPEQ","SUR",315.28,"2026-08-01",3,"SUR"],
	      ["SBER","TQBR","SUR",281.39,"2026-08-01",3,"SUR"]
	    ]
	  },
	  "marketdata": {"columns": ["SECID","BOARDID","LAST","VALTODAY"], "data": []}
	}`

	got := priceOf(t, fetchWith(t, body, listed("sber-1", "SBER", entity.AssetTypeStock)), "sber-1")
	assert.Equal(t, "28139000000", got.Last.String(), "TQBR outranks SPEQ")
}

func TestNothingOutsideMoexIsAsked(t *testing.T) {
	client, requests := serveISS(t, sharesJSON)

	prices, err := NewProvider(client).FetchPrices(context.Background(), []*entity.Asset{
		{ID: "btc-1", Symbol: "BTC", Type: entity.AssetTypeCryptocurrency, Market: entity.MarketCrypto},
		// Right venue, wrong instrument class: this provider prices no forex.
		{ID: "rub-1", Symbol: "RUB", Type: entity.AssetTypeForex, Market: MarketName},
		// A token that copied a MOEX ticker but lives on a contract market.
		{ID: "fake-1", Symbol: "SBER", Type: entity.AssetTypeStock, Market: entity.ContractMarket("base", "0xdead")},
	})

	require.NoError(t, err)
	assert.Empty(t, prices)
	assert.Empty(t, *requests, "a sweep with no MOEX listing must not spend a request")
}

func TestOneMarketFailingDoesNotCostTheOther(t *testing.T) {
	// Bonds answer an error, shares answer normally. The acceptance criterion
	// for this adapter is that a failed batch costs its own tickers only.
	client, _ := serveByMarket(t, map[string]handlerSpec{
		"shares": {status: 200, body: sharesJSON},
		"bonds":  {status: 500},
	})

	prices, err := NewProvider(client).FetchPrices(context.Background(), []*entity.Asset{
		listed("sber-1", "SBER", entity.AssetTypeStock),
		listed("ofz-1", "SU26238RMFS4", entity.AssetTypeBond),
	})

	require.NoError(t, err, "a failing market is reported in the log, not as a sweep failure")
	require.Len(t, prices, 1)
	assert.Equal(t, "sber-1", prices[0].AssetID)
}

func TestDuplicateTickersAreAskedOnce(t *testing.T) {
	client, requests := serveISS(t, sharesJSON)

	_, err := NewProvider(client).FetchPrices(context.Background(), []*entity.Asset{
		listed("sber-1", "SBER", entity.AssetTypeStock),
		listed("sber-2", "sber", entity.AssetTypeStock),
	})

	require.NoError(t, err)
	require.Len(t, *requests, 1)
	assert.Equal(t, "SBER", (*requests)[0].URL.Query().Get("securities"))
}

func TestProviderQuotesInRoubles(t *testing.T) {
	p := NewProvider(NewClient(Config{}))
	assert.Equal(t, "RUB", p.BaseAssetSymbol())
	assert.Equal(t, entity.AssetTypeForex, p.BaseAssetType())

	// Free and keyless: there is no plan allowance to divide into a per-sweep
	// portion, and saying so is not the same as saying the budget is zero.
	_, metered := p.AssetBudget(time.Now(), time.Hour)
	assert.False(t, metered)
}
