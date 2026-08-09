package moex

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// sharesJSON is the real ISS shape for the shares market, trimmed to what this
// adapter reads: SBER on two boards (one traded today, one not) and a fund.
const sharesJSON = `{
  "securities": {
    "columns": ["SECID","BOARDID","CURRENCYID","PREVPRICE","PREVDATE","FACEVALUE","FACEUNIT"],
    "data": [
      ["SBER","SPEQ","SUR",315.28,"2026-08-01",3,"SUR"],
      ["SBER","TQBR","SUR",281.39,"2026-08-01",3,"SUR"],
      ["SBMX","TQTF","SUR",16.15,"2026-08-01",0,"SUR"]
    ]
  },
  "marketdata": {
    "columns": ["SECID","BOARDID","LAST","VALTODAY","SYSTIME"],
    "data": [
      ["SBER","SPEQ",null,0,"2026-08-04 19:20:24"],
      ["SBER","TQBR",284.6,14967277066,"2026-08-04 23:50:46"],
      ["SBMX","TQTF",16.173,78138824,"2026-08-04 23:50:31"]
    ]
  }
}`

// bondsJSON is a federal bond quoted as a percentage of a 1000 rouble nominal.
const bondsJSON = `{
  "securities": {
    "columns": ["SECID","BOARDID","CURRENCYID","PREVPRICE","PREVDATE","FACEVALUE","FACEUNIT","ACCRUEDINT"],
    "data": [["SU26238RMFS4","TQOB","SUR",56.4,"2026-08-01",1000,"SUR",13.23]]
  },
  "marketdata": {
    "columns": ["SECID","BOARDID","LAST","VALTODAY","SYSTIME"],
    "data": [["SU26238RMFS4","TQOB",56.75,412000000,"2026-08-04 23:49:59"]]
  }
}`

// serveISS stands up a stub answering every market with the same body and
// recording what was asked for.
func serveISS(t *testing.T, body string) (*Client, *[]*http.Request) {
	t.Helper()
	var seen []*http.Request

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = append(seen, r.Clone(context.Background()))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)

	return NewClient(Config{BaseURL: srv.URL}), &seen
}

// handlerSpec is one market's canned answer.
type handlerSpec struct {
	status int
	body   string
}

// serveByMarket answers each ISS market differently, which is how a partial
// failure is reproduced: one market up, another down.
func serveByMarket(t *testing.T, byMarket map[string]handlerSpec) (*Client, *[]*http.Request) {
	t.Helper()
	var seen []*http.Request

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = append(seen, r.Clone(context.Background()))
		for market, spec := range byMarket {
			if !strings.Contains(r.URL.Path, "/markets/"+market+"/") {
				continue
			}
			w.WriteHeader(spec.status)
			_, _ = w.Write([]byte(spec.body))
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	t.Cleanup(srv.Close)

	return NewClient(Config{BaseURL: srv.URL}), &seen
}

// serveByVenue answers each (engine, market) pair differently. Distinguishing
// engines is the point: the same security can be described by both, and the
// interesting cases are exactly the ones where the two disagree.
func serveByVenue(t *testing.T, byVenue map[Venue]handlerSpec) (*Client, *[]*http.Request) {
	t.Helper()
	var seen []*http.Request

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = append(seen, r.Clone(context.Background()))
		for venue, spec := range byVenue {
			prefix := "/iss/engines/" + string(venue.Engine) + "/markets/" + string(venue.Market) + "/"
			if !strings.HasPrefix(r.URL.Path, prefix) {
				continue
			}
			w.WriteHeader(spec.status)
			_, _ = w.Write([]byte(spec.body))
			return
		}
		// A venue with no canned answer behaves like a venue the security is not
		// admitted to: a valid, empty response.
		_, _ = w.Write([]byte(`{"securities":{"columns":[],"data":[]},"marketdata":{"columns":[],"data":[]}}`))
	}))
	t.Cleanup(srv.Close)

	return NewClient(Config{BaseURL: srv.URL}), &seen
}

func TestQuotesJoinsBothBlocks(t *testing.T) {
	client, requests := serveISS(t, sharesJSON)

	quotes, err := client.Quotes(context.Background(), VenueStockShares, []string{"SBER", "SBMX"})
	require.NoError(t, err)
	require.Len(t, quotes, 3, "one row per security and board")

	require.Len(t, *requests, 1)
	req := (*requests)[0]
	assert.Equal(t, "/iss/engines/stock/markets/shares/securities.json", req.URL.Path)
	assert.Equal(t, "SBER,SBMX", req.URL.Query().Get("securities"))
	assert.Equal(t, "securities,marketdata", req.URL.Query().Get("iss.only"))
	assert.Equal(t, userAgent, req.Header.Get("User-Agent"))

	byBoard := map[string]Quote{}
	for _, q := range quotes {
		if q.SecID == "SBER" {
			byBoard[q.Board] = q
		}
	}

	traded := byBoard["TQBR"]
	assert.Equal(t, "284.6", traded.Last.Decimal.String())
	assert.Equal(t, "14967277066", traded.ValToday.Decimal.String())
	assert.Equal(t, "2026-08-04T23:50:46+03:00", traded.SysTime.Format(time.RFC3339))
	assert.Equal(t, "SUR", traded.Currency)

	// The other board is described but did not trade: a null last price is not
	// a price of zero.
	quiet := byBoard["SPEQ"]
	assert.False(t, quiet.Last.Valid, "null LAST must not become 0")
	assert.Equal(t, "315.28", quiet.Prev.Decimal.String())
	assert.Equal(t, "2026-08-01T00:00:00+03:00", quiet.PrevDate.Format(time.RFC3339))
}

func TestQuotesReadsTheBondNominal(t *testing.T) {
	client, _ := serveISS(t, bondsJSON)

	quotes, err := client.Quotes(context.Background(), VenueStockBonds, []string{"SU26238RMFS4"})
	require.NoError(t, err)
	require.Len(t, quotes, 1)

	assert.Equal(t, "56.75", quotes[0].Last.Decimal.String(), "the quote is a percentage")
	assert.Equal(t, "1000", quotes[0].FaceValue.Decimal.String())
	assert.Equal(t, "SUR", quotes[0].FaceUnit)
}

func TestQuotesAsksNothingForAnEmptyList(t *testing.T) {
	client, requests := serveISS(t, sharesJSON)

	quotes, err := client.Quotes(context.Background(), VenueStockShares, nil)
	require.NoError(t, err)
	assert.Empty(t, quotes)
	assert.Empty(t, *requests)
}

func TestQuotesRejectsABadStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	}))
	t.Cleanup(srv.Close)

	_, err := NewClient(Config{BaseURL: srv.URL}).
		Quotes(context.Background(), VenueStockShares, []string{"SBER"})
	require.Error(t, err)
}

func TestQuotesSurvivesAShortRow(t *testing.T) {
	// ISS has added and removed columns before. A row shorter than its header
	// must cost that row's missing fields, not the whole response.
	body := `{
	  "securities": {
	    "columns": ["SECID","BOARDID","CURRENCYID","PREVPRICE"],
	    "data": [["SBER","TQBR"]]
	  },
	  "marketdata": {"columns": ["SECID","BOARDID","LAST"], "data": []}
	}`
	client, _ := serveISS(t, body)

	quotes, err := client.Quotes(context.Background(), VenueStockShares, []string{"SBER"})
	require.NoError(t, err)
	require.Len(t, quotes, 1)
	assert.Equal(t, "SBER", quotes[0].SecID)
	assert.False(t, quotes[0].Prev.Valid)
}
