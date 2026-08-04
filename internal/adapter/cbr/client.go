// Package cbr adapts the Bank of Russia daily rates feed to a price provider.
// The feed is the authoritative source for RUB cross rates, keyless and free,
// and it is what makes a RUB-quoted instrument convertible into the portfolio's
// quote currency at all.
package cbr

import (
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/shopspring/decimal"
	"golang.org/x/text/encoding/charmap"
)

// ProviderName is the canonical source identifier for Bank of Russia rates.
const ProviderName = "cbr"

const defaultBaseURL = "https://www.cbr.ru"

// dailyPath serves the rates set for a business day. Without a date parameter
// it answers with the set currently in force, which is the one to value
// today's positions at.
const dailyPath = "/scripts/XML_daily.asp"

// userAgent identifies the client by name. The feed answers 403 to Go's
// default agent string specifically — an empty one is accepted — so this is
// not politeness but the difference between a rate and an error.
const userAgent = "greedy-eye (+https://github.com/foxcool/greedy-eye)"

// moscow is the zone the feed's Date attribute is stated in. Fixed rather than
// loaded from tzdata: the offset has not changed since 2014 and a missing
// zoneinfo database in a scratch container must not turn a rate into an error.
var moscow = time.FixedZone("MSK", 3*60*60)

// Config holds client configuration. BaseURL selects the host and is the only
// knob: the feed is public and unauthenticated.
type Config struct {
	BaseURL string

	// Transport, when set, replaces the client's HTTP transport. The shared
	// provider rate budget (internal/adapter/ratelimit) is injected here.
	Transport http.RoundTripper
}

// Client talks to the Bank of Russia rates feed.
type Client struct {
	baseURL    string
	httpClient *http.Client
}

// NewClient creates a Bank of Russia client.
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

// Rates is one published set: the day it is in force for, and how many roubles
// one unit of each currency is worth.
type Rates struct {
	// Date is the business day the set applies to, at midnight Moscow time.
	// It is not the moment of the request: a set published on Friday is still
	// the current one on Sunday, and dating it "now" would claim a print that
	// never happened.
	Date time.Time
	// RUBPerUnit maps a currency's three-letter code to its value in roubles,
	// already divided by the feed's nominal.
	RUBPerUnit map[string]decimal.Decimal
}

// valCurs mirrors the feed's document element.
type valCurs struct {
	Date   string   `xml:"Date,attr"`
	Valute []valute `xml:"Valute"`
}

// valute is one currency's row. Value is quoted for Nominal units — 100
// Kazakhstani tenge, not one — and both fields use a comma decimal separator.
type valute struct {
	CharCode string `xml:"CharCode"`
	Nominal  string `xml:"Nominal"`
	Value    string `xml:"Value"`
}

// DailyRates fetches the rate set currently in force.
//
// Rows that do not parse are skipped rather than failing the set: one
// malformed currency must not cost the portfolio every other rate in the
// document. A set with no usable row at all is an error — that is the feed
// having changed shape, not a currency having a bad day.
func (c *Client) DailyRates(ctx context.Context) (*Rates, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+dailyPath, nil)
	if err != nil {
		return nil, fmt.Errorf("cbr: build request: %w", err)
	}
	req.Header.Set("User-Agent", userAgent)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("cbr: request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("cbr: unexpected status %d", resp.StatusCode)
	}

	var doc valCurs
	dec := xml.NewDecoder(resp.Body)
	// The document declares windows-1251 in its prolog. Only the currency
	// names need the codepage, but the decoder refuses the whole document
	// without a reader for it.
	dec.CharsetReader = charsetReader
	if err := dec.Decode(&doc); err != nil {
		return nil, fmt.Errorf("cbr: decode: %w", err)
	}

	date, err := time.ParseInLocation("02.01.2006", doc.Date, moscow)
	if err != nil {
		return nil, fmt.Errorf("cbr: parse date %q: %w", doc.Date, err)
	}

	rates := &Rates{Date: date, RUBPerUnit: make(map[string]decimal.Decimal, len(doc.Valute))}
	for _, v := range doc.Valute {
		code := strings.ToUpper(strings.TrimSpace(v.CharCode))
		if code == "" {
			continue
		}
		value, err := parseAmount(v.Value)
		if err != nil || !value.IsPositive() {
			continue
		}
		nominal, err := parseAmount(v.Nominal)
		if err != nil || !nominal.IsPositive() {
			continue
		}
		rates.RUBPerUnit[code] = value.Div(nominal)
	}

	if len(rates.RUBPerUnit) == 0 {
		return nil, fmt.Errorf("cbr: no usable rates in the set for %s", doc.Date)
	}
	return rates, nil
}

// parseAmount reads a number written with a comma decimal separator.
func parseAmount(s string) (decimal.Decimal, error) {
	return decimal.NewFromString(strings.ReplaceAll(strings.TrimSpace(s), ",", "."))
}

// charsetReader decodes the one legacy codepage this feed is published in.
// Anything else is refused rather than passed through as bytes: silently
// mis-decoding a currency code is how a rate ends up attached to the wrong
// currency.
func charsetReader(charset string, input io.Reader) (io.Reader, error) {
	switch strings.ToLower(charset) {
	case "windows-1251", "cp1251":
		return charmap.Windows1251.NewDecoder().Reader(input), nil
	case "utf-8", "utf8", "":
		return input, nil
	default:
		return nil, fmt.Errorf("cbr: unsupported charset %q", charset)
	}
}
