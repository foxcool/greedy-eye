// Package moex adapts the MOEX ISS public API to a price provider, covering
// the Russian exchange's shares, exchange-traded funds and bonds. The API is
// keyless and free; quotes are delayed by about 15 minutes, which is well
// inside what a portfolio valuation needs.
package moex

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/shopspring/decimal"
)

// ProviderName is the canonical source identifier for MOEX prices.
const ProviderName = "moex"

const defaultBaseURL = "https://iss.moex.com"

// userAgent names the client. The CBR feed refuses Go's default agent string
// outright (see internal/adapter/cbr); ISS does not, but a public API run as a
// courtesy deserves a caller it can identify and throttle by name.
const userAgent = "greedy-eye (+https://github.com/foxcool/greedy-eye)"

// Market is an ISS market inside the "stock" engine. Shares and exchange-traded
// funds share one market — a fund's primary board (TQTF, TQIF) lives under
// "shares" — so two markets cover everything this adapter prices.
type Market string

const (
	MarketShares Market = "shares"
	MarketBonds  Market = "bonds"
)

// moscow is the zone ISS states its timestamps in. Fixed rather than loaded
// from tzdata: a container with no zoneinfo database must not turn a quote into
// an error.
var moscow = time.FixedZone("MSK", 3*60*60)

// Config holds client configuration. BaseURL selects the host and is the only
// knob: the API is public and unauthenticated.
type Config struct {
	BaseURL string

	// Transport, when set, replaces the client's HTTP transport. The shared
	// provider rate budget (internal/adapter/ratelimit) is injected here.
	Transport http.RoundTripper
}

// Client talks to the MOEX ISS API.
type Client struct {
	baseURL    string
	httpClient *http.Client
}

// NewClient creates a MOEX ISS client.
func NewClient(cfg Config) *Client {
	baseURL := cfg.BaseURL
	if baseURL == "" {
		baseURL = defaultBaseURL
	}
	return &Client{
		baseURL:    strings.TrimSuffix(baseURL, "/"),
		httpClient: &http.Client{Timeout: 30 * time.Second, Transport: cfg.Transport},
	}
}

// Quote is one security on one board, joined from the two blocks ISS answers
// with: the static description and the day's trading.
type Quote struct {
	SecID string
	Board string
	// Currency the instrument is quoted in ("SUR" for roubles).
	Currency string
	// Last is the most recent trade of the current session; unset outside
	// trading hours or when the security did not trade today.
	Last decimal.NullDecimal
	// Prev is the previous session's close, with the date it belongs to.
	Prev     decimal.NullDecimal
	PrevDate time.Time
	// FaceValue and FaceUnit describe the nominal. Bonds are quoted as a
	// percentage of it, so for them the pair is what turns a quote into money.
	FaceValue decimal.NullDecimal
	FaceUnit  string
	// ValToday is today's turnover in the quote currency. Zero means the
	// security has not traded today, which is not the same as a market with no
	// depth — see the provider.
	ValToday decimal.NullDecimal
	// SysTime is when ISS last updated the row.
	SysTime time.Time
}

// issBlock is the shape every ISS block answers in: column names once, rows as
// positional arrays.
type issBlock struct {
	Columns []string            `json:"columns"`
	Data    [][]json.RawMessage `json:"data"`
}

// row indexes one data row by column name.
func (b issBlock) row(i int) map[string]json.RawMessage {
	out := make(map[string]json.RawMessage, len(b.Columns))
	for j, name := range b.Columns {
		if j < len(b.Data[i]) {
			out[name] = b.Data[i][j]
		}
	}
	return out
}

type issResponse struct {
	Securities issBlock `json:"securities"`
	MarketData issBlock `json:"marketdata"`
}

// Quotes returns every (security, board) row ISS has for the given tickers on
// one market.
//
// Board selection is deliberately left to the caller: ISS answers with every
// board a security is admitted to, and which one carries the meaningful price
// is a judgement about trading, not about parsing.
func (c *Client) Quotes(ctx context.Context, market Market, secIDs []string) ([]Quote, error) {
	if len(secIDs) == 0 {
		return nil, nil
	}

	endpoint := fmt.Sprintf("%s/iss/engines/stock/markets/%s/securities.json", c.baseURL, market)
	query := url.Values{
		"iss.meta":   {"off"},
		"iss.only":   {"securities,marketdata"},
		"securities": {strings.Join(secIDs, ",")},
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint+"?"+query.Encode(), nil)
	if err != nil {
		return nil, fmt.Errorf("moex: build request: %w", err)
	}
	req.Header.Set("User-Agent", userAgent)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("moex: request %s: %w", market, err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("moex: %s answered %d", market, resp.StatusCode)
	}

	var doc issResponse
	if err := json.NewDecoder(resp.Body).Decode(&doc); err != nil {
		return nil, fmt.Errorf("moex: decode %s: %w", market, err)
	}
	return join(doc), nil
}

// join merges the two blocks on (SECID, BOARDID). The description block is the
// spine: a board ISS describes but reports no trading for is still a real
// listing carrying a previous close.
func join(doc issResponse) []Quote {
	type key struct{ sec, board string }

	trading := make(map[key]map[string]json.RawMessage, len(doc.MarketData.Data))
	for i := range doc.MarketData.Data {
		r := doc.MarketData.row(i)
		trading[key{str(r["SECID"]), str(r["BOARDID"])}] = r
	}

	quotes := make([]Quote, 0, len(doc.Securities.Data))
	for i := range doc.Securities.Data {
		s := doc.Securities.row(i)
		q := Quote{
			SecID:     str(s["SECID"]),
			Board:     str(s["BOARDID"]),
			Currency:  str(s["CURRENCYID"]),
			Prev:      num(s["PREVPRICE"]),
			PrevDate:  day(str(s["PREVDATE"])),
			FaceValue: num(s["FACEVALUE"]),
			FaceUnit:  str(s["FACEUNIT"]),
		}
		if q.SecID == "" {
			continue
		}
		if m, ok := trading[key{q.SecID, q.Board}]; ok {
			q.Last = num(m["LAST"])
			q.ValToday = num(m["VALTODAY"])
			q.SysTime = stamp(str(m["SYSTIME"]))
		}
		quotes = append(quotes, q)
	}
	return quotes
}

// str reads a JSON string, treating null and any non-string as empty.
func str(raw json.RawMessage) string {
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return ""
	}
	return s
}

// num reads a JSON number. ISS writes null for "no value", which is a different
// claim from zero: a security that did not trade has no last price, while one
// that traded at zero would be a broken feed.
func num(raw json.RawMessage) decimal.NullDecimal {
	if len(raw) == 0 || string(raw) == "null" {
		return decimal.NullDecimal{}
	}
	var f json.Number
	if err := json.Unmarshal(raw, &f); err != nil {
		return decimal.NullDecimal{}
	}
	d, err := decimal.NewFromString(f.String())
	if err != nil {
		return decimal.NullDecimal{}
	}
	return decimal.NullDecimal{Decimal: d, Valid: true}
}

// day parses an ISS date ("2026-08-04") as midnight Moscow time.
func day(s string) time.Time {
	t, err := time.ParseInLocation(time.DateOnly, s, moscow)
	if err != nil {
		return time.Time{}
	}
	return t
}

// stamp parses an ISS timestamp ("2026-08-04 23:50:46") in Moscow time.
func stamp(s string) time.Time {
	t, err := time.ParseInLocation(time.DateTime, s, moscow)
	if err != nil {
		return time.Time{}
	}
	return t
}
