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

// fxgdExchangeAppraisal is the shape that made this task P1: MOEX publishes a
// recognised close for a fund nobody trades. Measured on the ISS history feed
// for FXGD, 2023-08-01..08 — CLOSE null, VALUE 0, NUMTRADES 0, LEGALCLOSEPRICE
// 93.55 — against a real last traded price of 37 roubles over the counter.
const fxgdExchangeAppraisal = `{
  "securities": {
    "columns": ["SECID","BOARDID","CURRENCYID","PREVPRICE","PREVDATE","PREVWAPRICE","PREVLEGALCLOSEPRICE"],
    "data": [["FXGD","TQTF","SUR",93.55,"2026-08-06",null,93.55]]
  },
  "marketdata": {
    "columns": ["SECID","BOARDID","LAST","VALTODAY","SYSTIME"],
    "data": [["FXGD","TQTF",null,0,"2026-08-07 23:50:46"]]
  }
}`

// fxgdOverTheCounterTrade is the same paper on MTQR, where it actually traded.
const fxgdOverTheCounterTrade = `{
  "securities": {
    "columns": ["SECID","BOARDID","CURRENCYID","PREVPRICE","PREVDATE","PREVWAPRICE"],
    "data": [["FXGD","MTQR","SUR",37,"2026-08-06",36.9]]
  },
  "marketdata": {
    "columns": ["SECID","BOARDID","LAST","VALTODAY","SYSTIME"],
    "data": [["FXGD","MTQR",37.2,412000,"2026-08-07 18:40:10"]]
  }
}`

// TestOverTheCounterTradeBeatsAnExchangeAppraisal is the whole point of reading
// a second engine. The exchange floor answers with a number 2.5x the truth, and
// it answers from a higher-ranked board, so nothing but "somebody actually paid
// this" can pick the right one.
func TestOverTheCounterTradeBeatsAnExchangeAppraisal(t *testing.T) {
	client, requests := serveByVenue(t, map[Venue]handlerSpec{
		VenueStockShares: {status: 200, body: fxgdExchangeAppraisal},
		VenueOTCShares:   {status: 200, body: fxgdOverTheCounterTrade},
	})

	prices, err := NewProvider(client).FetchPrices(context.Background(),
		[]*entity.Asset{listed("fxgd-1", "FXGD", entity.AssetTypeFund)})
	require.NoError(t, err)
	require.Len(t, prices, 1)

	got := prices[0]
	assert.Equal(t, "3720000000", got.Last.String(), "37.20 traded, not 93.55 appraised")
	assert.Equal(t, entity.PriceProvenanceTraded, got.Provenance)
	assert.True(t, got.Volume.Valid, "a traded print carries the turnover behind it")
	assert.Equal(t, "41200000000000", got.Volume.Decimal.String())

	assert.Len(t, *requests, len(venuesFor(entity.AssetTypeFund)),
		"every venue is asked before a winner is picked")
}

// TestAppraisedCloseIsStoredButNotAsAMarketPrice covers the case where the
// over-the-counter admission is gone and the recognised close is all that is
// left. The number is kept — it is real, and the catalogue should show it — but
// it carries the zero turnover of the session that produced it, which is what
// keeps it out of a total under ADR-009.
func TestAppraisedCloseIsStoredButNotAsAMarketPrice(t *testing.T) {
	client, _ := serveByVenue(t, map[Venue]handlerSpec{
		VenueStockShares: {status: 200, body: fxgdExchangeAppraisal},
	})

	prices, err := NewProvider(client).FetchPrices(context.Background(),
		[]*entity.Asset{listed("fxgd-1", "FXGD", entity.AssetTypeFund)})
	require.NoError(t, err)
	require.Len(t, prices, 1)

	got := prices[0]
	assert.Equal(t, "9355000000", got.Last.String())
	assert.Equal(t, entity.PriceProvenanceAppraised, got.Provenance)
	require.True(t, got.Volume.Valid, "zero turnover is a measurement here, not an absence")
	assert.True(t, got.Volume.Decimal.IsZero())
	assert.Equal(t, "2026-08-06T00:00:00+03:00", got.Timestamp.Format(time.RFC3339),
		"dated to the session it belongs to, not to the sweep")
}

// TestPreviousCloseWithoutEvidenceStaysUnclaimed: a board that publishes neither
// a weighted average nor a recognised close (SPEQ carries a previous price and
// neither, measured on ISS 2026-08-09) leaves no grounds for either claim.
// Inventing "traded" there would be the same error as inventing "appraised".
func TestPreviousCloseWithoutEvidenceStaysUnclaimed(t *testing.T) {
	body := `{
	  "securities": {
	    "columns": ["SECID","BOARDID","CURRENCYID","PREVPRICE","PREVDATE"],
	    "data": [["SBER","SPEQ","SUR",315.28,"2026-08-01"]]
	  },
	  "marketdata": {"columns": ["SECID","BOARDID","LAST","VALTODAY"], "data": []}
	}`

	prices := fetchWith(t, body, listed("sber-1", "SBER", entity.AssetTypeStock))
	require.Len(t, prices, 1)

	assert.Equal(t, entity.PriceProvenanceUnknown, prices[0].Provenance)
	assert.False(t, prices[0].Volume.Valid,
		"today's silence says nothing about the session that made this price")
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

	// One request per venue, and the ticker appears once in each: the same paper
	// is admitted in several places, so asking all of them is the point — asking
	// any of them twice for the same ticker is not.
	require.Len(t, *requests, len(venuesFor(entity.AssetTypeStock)))
	paths := map[string]bool{}
	for _, req := range *requests {
		assert.Equal(t, "SBER", req.URL.Query().Get("securities"))
		paths[req.URL.Path] = true
	}
	assert.Len(t, paths, len(*requests), "each venue is asked at its own path")
	assert.True(t, paths["/iss/engines/otc/markets/shares/securities.json"],
		"the over-the-counter market is one of them")
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

// TestForeignNameOnARussianBoardIsNotThatCompanysPrice: MTQR carries RM-suffixed
// foreign shares — AAPL-RM traded at 8149 roubles on 2026-08-07 — and that is a
// Russian over-the-counter quote for Apple, not Apple's price. Matching is by
// exact ticker, so an asset called AAPL takes nothing from it; the guard is here
// because a "helpful" suffix-stripping lookup would turn a $75 share into 8149
// of something.
func TestForeignNameOnARussianBoardIsNotThatCompanysPrice(t *testing.T) {
	body := `{
	  "securities": {
	    "columns": ["SECID","BOARDID","CURRENCYID","PREVPRICE","PREVDATE","PREVWAPRICE"],
	    "data": [["AAPL-RM","MTQR","SUR",7950,"2026-08-06",7782]]
	  },
	  "marketdata": {
	    "columns": ["SECID","BOARDID","LAST","VALTODAY","SYSTIME"],
	    "data": [["AAPL-RM","MTQR",8149,363133,"2026-08-07 18:39:59"]]
	  }
	}`

	assert.Empty(t, fetchWith(t, body, listed("aapl-1", "AAPL", entity.AssetTypeStock)),
		"a row for another ticker is not this asset's price")

	// The same response does price the instrument it actually describes.
	prices := fetchWith(t, body, listed("aaplrm-1", "AAPL-RM", entity.AssetTypeStock))
	require.Len(t, prices, 1)
	assert.Equal(t, entity.PriceProvenanceTraded, prices[0].Provenance)
}
