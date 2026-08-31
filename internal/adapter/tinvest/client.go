// Package tinvest adapts the T-Invest API to a price provider, covering the
// instruments a Russian broker quotes: SPB Exchange listings, which have no
// ISS-style open endpoint at all, and MOEX listings as a second opinion.
//
// Two things make this adapter unlike the other price sources.
//
// It needs a broker token, which is a personal credential rather than a service
// key — the account is a `broker` carrying `market_data`, resolved through
// internal/service/credentials like any other.
//
// And its TLS chain does not verify against a standard trust store: the host
// certificate is issued by the Russian Trusted Root CA, which no operating
// system or Go distribution ships. That root is NOT vendored here. It is
// supplied by whoever deploys the instance (config key tinvest.rootCAFile) and
// trusted for this client alone — which trust anchors a service accepts is an
// operator's decision, not a library's, and a state certificate authority
// checked into a source tree would make it for everyone downstream.
package tinvest

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/shopspring/decimal"
)

// ProviderName is the canonical source identifier for T-Invest prices.
const ProviderName = "tinvest"

// RefSource is the namespace this provider's instrument ids live in, as stored
// in asset_external_refs. The ref itself is a FIGI.
const RefSource = "tinvest"

const defaultBaseURL = "https://invest-public-api.tinkoff.ru/rest"

// The gRPC service paths the REST gateway exposes. Spelled in full because that
// is what the URL is: the gateway maps <fully.qualified.Service>/<Method> onto
// a POST with a JSON body.
const (
	pathShares          = "/tinkoff.public.invest.api.contract.v1.InstrumentsService/Shares"
	pathEtfs            = "/tinkoff.public.invest.api.contract.v1.InstrumentsService/Etfs"
	pathLastPrices      = "/tinkoff.public.invest.api.contract.v1.MarketDataService/GetLastPrices"
	pathTradingStatuses = "/tinkoff.public.invest.api.contract.v1.MarketDataService/GetTradingStatuses"
)

// instrumentStatusBase asks for the instruments available for trading through
// the API, rather than every instrument the broker has ever known. A universe
// that includes delisted paper would offer a FIGI for a ticker nobody can trade
// and make the binding look successful.
const instrumentStatusBase = "INSTRUMENT_STATUS_BASE"

// Config holds client configuration.
type Config struct {
	// Token is the broker's API token. Required: every method is authenticated.
	Token string

	// BaseURL selects the host. Empty means the production gateway.
	BaseURL string

	// RootCAPEM is the PEM the operator supplies to verify the API host.
	//
	// Required unless Transport is set, and deliberately without a default: the
	// certificate chain of *.tinkoff.ru terminates in the Russian Trusted Root
	// CA, which no standard trust store carries. Whether to trust it is a
	// deployment decision. Its official publication is gu-st.ru, and the chain
	// the host presents carries the same root — SHA256
	// D2:6D:2D:02:31:B7:C3:9F:92:CC:73:85:12:BA:54:10:35:19:E4:40:5D:68:B5:BD:70:3E:97:88:CA:8E:CF:31
	// as measured 2026-08-09, which is worth checking before installing it.
	RootCAPEM []byte

	// Transport, when set, replaces the client's HTTP transport entirely,
	// RootCAPEM included. The shared provider rate budget
	// (internal/adapter/ratelimit) is injected here.
	//
	// A transport supplied here is used as given: the caller owns its TLS
	// configuration, and silently rewriting it would be a surprising way for a
	// rate limiter to change who is trusted.
	Transport http.RoundTripper
}

// Client talks to the T-Invest REST gateway.
type Client struct {
	baseURL    string
	token      string
	httpClient *http.Client
}

// ErrNoRootCA reports that the operator has not supplied the certificate
// authority the API host's chain terminates in. It is a refusal to start, not a
// warning: without it every request fails inside TLS, which surfaces as an
// opaque handshake error at sweep time instead of a configuration problem at
// boot.
var ErrNoRootCA = errors.New(
	"tinvest: no root CA configured (tinvest.rootCAFile): the API host's chain " +
		"terminates in a CA no standard trust store carries")

// ErrBadBaseURL reports that the configured host is not a URL this client can
// send a request to. It exists so the failure reads as configuration: without
// it a typo is concatenated onto a service path and handed to net/http, which
// answers either a generic "build request" error or, worse, a connection
// failure indistinguishable from the broker being down.
var ErrBadBaseURL = errors.New("tinvest: base URL is not usable")

// NewClient creates a T-Invest client. It fails when the base URL is not one
// requests can be built from, or when the operator has supplied neither a trust
// anchor nor a transport of their own.
func NewClient(cfg Config) (*Client, error) {
	if err := CheckBaseURL(cfg.BaseURL); err != nil {
		return nil, err
	}
	baseURL := cfg.BaseURL
	if baseURL == "" {
		baseURL = defaultBaseURL
	}

	transport := cfg.Transport
	if transport == nil {
		var err error
		if transport, err = TLSTransport(cfg.RootCAPEM); err != nil {
			return nil, err
		}
	}

	return &Client{
		baseURL:    strings.TrimSuffix(baseURL, "/"),
		token:      cfg.Token,
		httpClient: &http.Client{Timeout: 30 * time.Second, Transport: transport},
	}, nil
}

// CheckBaseURL rejects a host requests cannot be built from, naming what is
// wrong with it. An empty value is valid and selects the production gateway.
//
// Exported so the provider registry can ask BEFORE it builds a TLS transport.
// The other order reports the wrong fault: a typo in the URL, on an account
// with no trust anchor, comes back as "no root CA configured" — telling the
// operator to fix a field they did not touch.
//
// A path is allowed and not stripped: the gateway serves under /rest, which is
// why defaultBaseURL carries one, and service paths are appended to whatever is
// given. Query and fragment are refused instead of ignored — appending a
// service path after "?x=1" produces a URL that neither fails nor works, and
// silently dropping the part the operator typed is its own kind of lie.
func CheckBaseURL(raw string) error {
	if raw == "" {
		return nil
	}
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("%w: %q: %w", ErrBadBaseURL, raw, err)
	}
	switch {
	case u.Scheme != "http" && u.Scheme != "https":
		// http is deliberate: it is what lets an instance point at a local
		// server replaying captured responses, which is the only way to
		// exercise the mapping without the live broker. The sandbox is not an
		// alternative — it answers different methods entirely.
		return fmt.Errorf("%w: %q: scheme must be http or https, got %q", ErrBadBaseURL, raw, u.Scheme)
	case u.Host == "":
		return fmt.Errorf("%w: %q: no host", ErrBadBaseURL, raw)
	case u.RawQuery != "" || u.Fragment != "":
		return fmt.Errorf("%w: %q: a query or fragment cannot carry a service path", ErrBadBaseURL, raw)
	}
	return nil
}

// InsecureBaseURL reports whether a base URL is served without TLS, and so has
// no certificate for a trust anchor to verify.
//
// Exported because the caller that decides whether to demand a root CA is the
// provider registry, which holds the account: asking for an anchor to check a
// plaintext connection would make the local-replay case impossible, and
// answering that question inside NewClient would hide it from the place the
// account's fields are read.
func InsecureBaseURL(raw string) bool {
	if raw == "" {
		return false
	}
	u, err := url.Parse(raw)
	return err == nil && u.Scheme == "http"
}

// TLSTransport builds the transport that verifies the API host against the
// operator's anchor.
//
// Exported because the rate limiter wraps a base transport rather than
// replacing it: composing them anywhere else would mean passing a ready
// Transport into Config, where it silently takes precedence over RootCAPEM and
// the connection quietly falls back to the system trust store. Building the
// verified transport first and letting the limiter wrap it keeps one place
// responsible for who is trusted.
func TLSTransport(rootCAPEM []byte) (http.RoundTripper, error) {
	if len(rootCAPEM) == 0 {
		return nil, ErrNoRootCA
	}
	pool, err := rootPool(rootCAPEM)
	if err != nil {
		return nil, err
	}
	t := http.DefaultTransport.(*http.Transport).Clone()
	t.TLSClientConfig = &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}
	return t, nil
}

// rootPool returns the system pool plus the operator's anchor.
//
// The addition is scoped to this client. Putting a certificate authority into
// the process-wide pool would make every outbound connection of the service —
// every other provider, every webhook — accept certificates from it, and that
// is a far larger claim than "this one broker API can be verified".
func rootPool(pem []byte) (*x509.CertPool, error) {
	pool, err := x509.SystemCertPool()
	if err != nil || pool == nil {
		pool = x509.NewCertPool()
	}
	if !pool.AppendCertsFromPEM(pem) {
		return nil, fmt.Errorf("tinvest: root CA file contains no usable certificate")
	}
	return pool, nil
}

// Quotation is the API's money type: an integer part and a billionths part,
// either of which may be negative.
type Quotation struct {
	Units string `json:"units"` // int64 over JSON, so a string
	Nano  int32  `json:"nano"`
}

// Decimal converts a quotation to an exact decimal. No float is involved: this
// is money, and 0.1 is not representable in binary floating point.
func (q Quotation) Decimal() (decimal.Decimal, bool) {
	units := decimal.Zero
	if q.Units != "" {
		parsed, err := decimal.NewFromString(q.Units)
		if err != nil {
			return decimal.Zero, false
		}
		units = parsed
	}
	// Units and nano carry the same sign, so adding is correct for negatives:
	// -1.5 arrives as units -1, nano -500000000.
	return units.Add(decimal.New(int64(q.Nano), -9)), true
}

// Instrument is one tradable security, flattened from the Share and Etf
// messages, which agree on every field this adapter reads.
type Instrument struct {
	FIGI string `json:"figi"`
	// UID is the API's own identifier, stable across a FIGI reassignment.
	UID       string `json:"uid"`
	Ticker    string `json:"ticker"`
	ClassCode string `json:"classCode"`
	// Currency is what the instrument is quoted in, lowercase in the API
	// ("usd", "rub"). SPB quotes foreign shares in dollars and domestic paper in
	// roubles, so this varies within a single response.
	Currency string `json:"currency"`
	// Exchange is the trading section, e.g. "SPB_MORNING", "MOEX".
	Exchange string `json:"exchange"`
	// RealExchange is where settlement actually happens, and it is the field
	// that answers "which of our markets is this?". The section in Exchange
	// varies by session (SPB_MORNING, MOEX_EVENING_WEEKEND) and the class code
	// varies by instrument segment, while this enum has four members and names
	// the venue outright.
	RealExchange string `json:"realExchange"`
	// TradingStatus is the instrument's current trading mode.
	TradingStatus string `json:"tradingStatus"`
	// APITradeAvailableFlag reports whether the instrument can be traded through
	// the API at all. False covers blocked and delisted paper.
	APITradeAvailableFlag bool `json:"apiTradeAvailableFlag"`
	// BlockedTCAFlag marks an instrument blocked by the broker.
	BlockedTCAFlag bool `json:"blockedTcaFlag"`
}

// LastPrice is the most recent price of one instrument.
type LastPrice struct {
	FIGI          string    `json:"figi"`
	Price         Quotation `json:"price"`
	Time          time.Time `json:"time"`
	Ticker        string    `json:"ticker"`
	ClassCode     string    `json:"classCode"`
	InstrumentUID string    `json:"instrumentUid"`
	// LastPriceType tells an exchange print from a dealer quote.
	LastPriceType string `json:"lastPriceType"`
}

// TradingStatus is one instrument's trading mode at the moment of asking.
type TradingStatus struct {
	FIGI                  string `json:"figi"`
	TradingStatus         string `json:"tradingStatus"`
	APITradeAvailableFlag bool   `json:"apiTradeAvailableFlag"`
	InstrumentUID         string `json:"instrumentUid"`
}

// Trading status values this adapter distinguishes. The enum has more members
// (opening auction, closing auction, break); everything that is not normal
// trading is treated alike, because none of the others is a market either.
const (
	StatusNormalTrading = "SECURITY_TRADING_STATUS_NORMAL_TRADING"
)

// LastPriceExchange is the price type produced by a trade on the exchange, as
// opposed to LAST_PRICE_DEALER, which a market maker states.
const LastPriceExchange = "LAST_PRICE_EXCHANGE"

// RealExchange values, the settlement venue behind an instrument. RTS is the
// SPB Exchange under its historical name.
const (
	RealExchangeMOEX = "REAL_EXCHANGE_MOEX"
	RealExchangeRTS  = "REAL_EXCHANGE_RTS"
)

// Shares lists the tradable shares universe.
func (c *Client) Shares(ctx context.Context) ([]Instrument, error) {
	return c.instruments(ctx, pathShares)
}

// Etfs lists the tradable exchange-traded funds universe.
func (c *Client) Etfs(ctx context.Context) ([]Instrument, error) {
	return c.instruments(ctx, pathEtfs)
}

func (c *Client) instruments(ctx context.Context, path string) ([]Instrument, error) {
	var out struct {
		Instruments []Instrument `json:"instruments"`
	}
	req := map[string]string{"instrumentStatus": instrumentStatusBase}
	if err := c.post(ctx, path, req, &out); err != nil {
		return nil, err
	}
	return out.Instruments, nil
}

// LastPrices returns the last price of each instrument named, by FIGI.
//
// Instruments the API does not answer for are simply absent — asking for a FIGI
// it does not know is not an error, and treating it as one would cost the batch
// every price it did return.
func (c *Client) LastPrices(ctx context.Context, figis []string) ([]LastPrice, error) {
	if len(figis) == 0 {
		return nil, nil
	}
	var out struct {
		LastPrices []LastPrice `json:"lastPrices"`
	}
	if err := c.post(ctx, pathLastPrices, map[string]any{"instrumentId": figis}, &out); err != nil {
		return nil, err
	}
	return out.LastPrices, nil
}

// TradingStatuses reports whether each instrument is currently trading.
func (c *Client) TradingStatuses(ctx context.Context, figis []string) ([]TradingStatus, error) {
	if len(figis) == 0 {
		return nil, nil
	}
	var out struct {
		TradingStatuses []TradingStatus `json:"tradingStatuses"`
	}
	if err := c.post(ctx, pathTradingStatuses, map[string]any{"instrumentId": figis}, &out); err != nil {
		return nil, err
	}
	return out.TradingStatuses, nil
}

// apiError is the gateway's error envelope, e.g.
// {"code":16,"message":"Authentication token is missing or invalid","description":"40003"}.
type apiError struct {
	Code        int    `json:"code"`
	Message     string `json:"message"`
	Description string `json:"description"`
}

func (c *Client) post(ctx context.Context, path string, payload, out any) error {
	if c.token == "" {
		return fmt.Errorf("tinvest: no API token configured")
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("tinvest: encode request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+path, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("tinvest: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.token)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("tinvest: %s: %w", path, err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		// The envelope is read for its message, never echoed whole: the token
		// is in the request, and a verbose error is the classic way for one to
		// reach a log.
		var e apiError
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<10))
		if json.Unmarshal(raw, &e) == nil && e.Message != "" {
			return fmt.Errorf("tinvest: %s: status %d: %s", path, resp.StatusCode, e.Message)
		}
		return fmt.Errorf("tinvest: %s: unexpected status %d", path, resp.StatusCode)
	}

	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("tinvest: decode %s response: %w", path, err)
	}
	return nil
}
