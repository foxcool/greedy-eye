package cbr

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/foxcool/greedy-eye/internal/entity"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// serveRecordedSet answers every request with a set the feed published, and
// reports the date_req each request carried.
//
// daily_2024-02-17.xml is the set dated Saturday 17 February 2024 as the feed
// served it; the recorder re-encoded it to UTF-8 and rewrote the prolog to
// match, which the decoder accepts like the live codepage.
func serveRecordedSet(t *testing.T) (*Client, *[]string) {
	t.Helper()
	body, err := os.ReadFile(filepath.Join("testdata", "daily_2024-02-17.xml"))
	require.NoError(t, err)

	var asked []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, dailyPath, r.URL.Path)
		assert.Equal(t, userAgent, r.Header.Get("User-Agent"))
		asked = append(asked, r.URL.Query().Get("date_req"))
		w.Header().Set("Content-Type", "application/xml")
		_, _ = w.Write(body)
	}))
	t.Cleanup(srv.Close)
	return NewClient(Config{BaseURL: srv.URL}), &asked
}

func TestRatesOnAsksForTheDayAndReadsTheSetInForce(t *testing.T) {
	c, asked := serveRecordedSet(t)

	// A Sunday: no set is established for it, and the feed answers with the
	// one in force, dated the Saturday before.
	sunday := time.Date(2024, time.February, 18, 0, 0, 0, 0, time.UTC)
	rates, err := c.RatesOn(context.Background(), sunday)
	require.NoError(t, err)

	assert.Equal(t, []string{"18/02/2024"}, *asked)
	// Dated by the set, not by the request: the Sunday row would claim a
	// print that never happened.
	assert.Equal(t, "2024-02-17T00:00:00+03:00", rates.Date.Format(time.RFC3339))
	assert.Len(t, rates.RUBPerUnit, 43)
	assert.Equal(t, "92.5492", rates.RUBPerUnit["USD"].String())
	assert.Equal(t, "99.3523", rates.RUBPerUnit["EUR"].String())
	assert.Equal(t, "0.206555", rates.RUBPerUnit["KZT"].String(), "quoted per 100 tenge")
}

func TestRatesOnReadsTheCalendarDayNotTheInstant(t *testing.T) {
	c, asked := serveRecordedSet(t)

	// 23:30 on the 17th in UTC is already the 18th in Moscow. The caller
	// named the 17th; converting the instant would ask for the wrong day.
	late := time.Date(2024, time.February, 17, 23, 30, 0, 0, time.UTC)
	_, err := c.RatesOn(context.Background(), late)
	require.NoError(t, err)

	assert.Equal(t, []string{"17/02/2024"}, *asked)
}

func TestDailyRatesSendsNoDate(t *testing.T) {
	c, asked := serveRecordedSet(t)

	_, err := c.DailyRates(context.Background())
	require.NoError(t, err)

	assert.Equal(t, []string{""}, *asked, "the current set is asked for without date_req")
}

func TestFetchPricesOnDatesRowsByTheSetInForce(t *testing.T) {
	c, asked := serveRecordedSet(t)
	sunday := time.Date(2024, time.February, 18, 0, 0, 0, 0, time.UTC)

	prices, err := NewProvider(c).FetchPricesOn(context.Background(),
		[]*entity.Asset{currency("rub-1", "RUB"), currency("eur-1", "EUR"),
			{ID: "tok-1", Symbol: "EUR", Type: entity.AssetTypeCryptocurrency, Market: "crypto"}},
		sunday)
	require.NoError(t, err)

	assert.Equal(t, []string{"18/02/2024"}, *asked)
	require.Len(t, prices, 2, "a token named after a currency is not that currency")

	// An ordinary cbr row, the same shape the daily sweep writes, so a
	// backfilled day and a swept one are one series.
	rub := priceOf(t, prices, "rub-1")
	assert.Equal(t, ProviderName, rub.SourceID)
	assert.Equal(t, interval, rub.Interval)
	assert.Equal(t, uint32(8), rub.Decimals)
	// 1 / 92.5492 USD per rouble on the 17 February 2024 set.
	assert.Equal(t, "1080506", rub.Last.String())
	assert.Equal(t, "2024-02-17T00:00:00+03:00", rub.Timestamp.Format(time.RFC3339))

	// 99.3523 / 92.5492 USD per euro.
	assert.Equal(t, "107350793", priceOf(t, prices, "eur-1").Last.String())
}
