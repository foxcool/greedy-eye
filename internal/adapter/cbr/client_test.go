package cbr

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/text/encoding/charmap"
)

// dailyXML is the feed's real shape, trimmed to four rows: a plain 1:1
// currency, one quoted per 100 units, the USD leg every conversion needs, and
// a name carrying Cyrillic so the codepage is actually exercised.
const dailyXML = `<?xml version="1.0" encoding="windows-1251"?>` +
	`<ValCurs Date="04.08.2026" name="Foreign Currency Market">` +
	`<Valute ID="R01239"><NumCode>978</NumCode><CharCode>EUR</CharCode><Nominal>1</Nominal>` +
	`<Name>Евро</Name><Value>91,2345</Value><VunitRate>91,2345</VunitRate></Valute>` +
	`<Valute ID="R01335"><NumCode>398</NumCode><CharCode>KZT</CharCode><Nominal>100</Nominal>` +
	`<Name>Казахстанских тенге</Name><Value>14,5000</Value><VunitRate>0,145</VunitRate></Valute>` +
	`<Valute ID="R01235"><NumCode>840</NumCode><CharCode>USD</CharCode><Nominal>1</Nominal>` +
	`<Name>Доллар США</Name><Value>78,5000</Value><VunitRate>78,5</VunitRate></Valute>` +
	`</ValCurs>`

// serveWindows1251 stands up a stub feed publishing body in the codepage the
// real one uses, so the decoder is tested against bytes rather than a string
// that happens to be valid UTF-8.
func serveWindows1251(t *testing.T, body string) *Client {
	t.Helper()
	encoded, err := charmap.Windows1251.NewEncoder().String(body)
	require.NoError(t, err)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, dailyPath, r.URL.Path)
		// The live feed answers 403 to Go's default agent string, so sending a
		// named one is load-bearing rather than cosmetic.
		assert.Equal(t, userAgent, r.Header.Get("User-Agent"))
		w.Header().Set("Content-Type", "application/xml")
		_, _ = w.Write([]byte(encoded))
	}))
	t.Cleanup(srv.Close)

	return NewClient(Config{BaseURL: srv.URL})
}

func TestDailyRatesParsesTheSet(t *testing.T) {
	rates, err := serveWindows1251(t, dailyXML).DailyRates(context.Background())
	require.NoError(t, err)

	// The set is dated in Moscow time. Read as UTC it would slide back a day,
	// which is a whole publication behind for anything comparing freshness.
	assert.Equal(t, "2026-08-04T00:00:00+03:00", rates.Date.Format(time.RFC3339))
	assert.Len(t, rates.RUBPerUnit, 3)
}

func TestDailyRatesDividesByNominal(t *testing.T) {
	rates, err := serveWindows1251(t, dailyXML).DailyRates(context.Background())
	require.NoError(t, err)

	// 14.50 roubles buys 100 tenge, so one tenge is 0.145 roubles. Storing the
	// quoted 14.50 would overstate the position a hundredfold.
	assert.Equal(t, "0.145", rates.RUBPerUnit["KZT"].String())
	assert.Equal(t, "91.2345", rates.RUBPerUnit["EUR"].String())
	assert.Equal(t, "78.5", rates.RUBPerUnit["USD"].String())
}

func TestDailyRatesSkipsUnusableRowsButKeepsTheSet(t *testing.T) {
	body := `<?xml version="1.0" encoding="windows-1251"?>` +
		`<ValCurs Date="04.08.2026" name="Foreign Currency Market">` +
		`<Valute ID="R01235"><CharCode>USD</CharCode><Nominal>1</Nominal><Value>78,5000</Value></Valute>` +
		`<Valute ID="R01239"><CharCode>EUR</CharCode><Nominal>1</Nominal><Value>not a number</Value></Valute>` +
		`<Valute ID="R01000"><CharCode>ZZZ</CharCode><Nominal>0</Nominal><Value>1,0000</Value></Valute>` +
		`<Valute ID="R01001"><CharCode>YYY</CharCode><Nominal>1</Nominal><Value>-3,0000</Value></Valute>` +
		`</ValCurs>`

	rates, err := serveWindows1251(t, body).DailyRates(context.Background())
	require.NoError(t, err)

	assert.Equal(t, "78.5", rates.RUBPerUnit["USD"].String())
	assert.NotContains(t, rates.RUBPerUnit, "EUR", "an unparseable value is not a rate")
	assert.NotContains(t, rates.RUBPerUnit, "ZZZ", "dividing by a zero nominal is not a rate")
	assert.NotContains(t, rates.RUBPerUnit, "YYY", "a negative rate is the feed failing to say")
}

func TestDailyRatesRejectsAnEmptySet(t *testing.T) {
	body := `<?xml version="1.0" encoding="windows-1251"?>` +
		`<ValCurs Date="04.08.2026" name="Foreign Currency Market"></ValCurs>`

	_, err := serveWindows1251(t, body).DailyRates(context.Background())
	require.Error(t, err, "a set with no usable row is the feed having changed shape")
}

func TestDailyRatesRejectsABadStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	t.Cleanup(srv.Close)

	_, err := NewClient(Config{BaseURL: srv.URL}).DailyRates(context.Background())
	require.Error(t, err)
}
