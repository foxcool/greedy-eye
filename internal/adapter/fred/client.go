// Package fred reads economic series from FRED, the St. Louis Fed's data
// service. It carries what the portfolio has no market quote for — policy
// rates and the like — so a dashboard can show them beside the holdings.
//
// The service is free but keyed, so a client is built from a provider account
// like every other keyed adapter; no key lives in application config.
package fred

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/foxcool/greedy-eye/internal/entity"
	"github.com/shopspring/decimal"
)

// ProviderName is the account slug a FRED key is stored under.
const ProviderName = "fred"

const defaultBaseURL = "https://api.stlouisfed.org"

const observationsPath = "/fred/series/observations"

// recentWindow is how many of the newest rows are requested. A series can end
// in several unpublished days (value "."), so one row is not enough to find
// the latest print; a handful is.
const recentWindow = "10"

// Config holds client configuration.
type Config struct {
	APIKey string
	// BaseURL selects the host; it exists for tests replaying recorded
	// responses and is never read from account data.
	BaseURL string
	// Transport, when set, replaces the client's HTTP transport. The shared
	// provider rate budget (internal/adapter/ratelimit) is injected here.
	Transport http.RoundTripper
}

// Client talks to the FRED API.
type Client struct {
	apiKey     string
	baseURL    string
	httpClient *http.Client
}

// NewClient creates a FRED client.
func NewClient(cfg Config) *Client {
	baseURL := cfg.BaseURL
	if baseURL == "" {
		baseURL = defaultBaseURL
	}
	return &Client{
		apiKey:     cfg.APIKey,
		baseURL:    strings.TrimSuffix(baseURL, "/"),
		httpClient: &http.Client{Timeout: 30 * time.Second, Transport: cfg.Transport},
	}
}

type observationsResponse struct {
	Observations []struct {
		Date  string `json:"date"`
		Value string `json:"value"`
	} `json:"observations"`
	ErrorMessage string `json:"error_message"`
}

// Latest returns the newest observation of a series that carries a value,
// dated by the day FRED publishes it for.
func (c *Client) Latest(ctx context.Context, seriesID string) (entity.MacroObservation, error) {
	if c.apiKey == "" {
		return entity.MacroObservation{}, errors.New("fred: no API key")
	}

	q := url.Values{}
	q.Set("series_id", seriesID)
	q.Set("api_key", c.apiKey)
	q.Set("file_type", "json")
	q.Set("sort_order", "desc")
	q.Set("limit", recentWindow)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+observationsPath+"?"+q.Encode(), nil)
	if err != nil {
		return entity.MacroObservation{}, fmt.Errorf("fred: build request: %w", err)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		// The key travels in the query string, and *url.Error prints the whole
		// URL. Errors surface in logs and API responses, so only the cause is
		// kept.
		var uerr *url.Error
		if errors.As(err, &uerr) {
			err = uerr.Err
		}
		return entity.MacroObservation{}, fmt.Errorf("fred: request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	var doc observationsResponse
	decodeErr := json.NewDecoder(resp.Body).Decode(&doc)

	if resp.StatusCode != http.StatusOK {
		// FRED answers a bad key with 400 and a message naming the variable;
		// the message is the useful part and never contains the key itself.
		return entity.MacroObservation{}, fmt.Errorf("fred: status %d: %s", resp.StatusCode, doc.ErrorMessage)
	}
	if decodeErr != nil {
		return entity.MacroObservation{}, fmt.Errorf("fred: decode: %w", decodeErr)
	}

	for _, o := range doc.Observations {
		// "." is FRED's marker for a date with no value yet.
		if o.Value == "." {
			continue
		}
		value, err := decimal.NewFromString(o.Value)
		if err != nil {
			continue
		}
		date, err := time.Parse(time.DateOnly, o.Date)
		if err != nil {
			continue
		}
		return entity.MacroObservation{Value: value, Date: date}, nil
	}
	return entity.MacroObservation{}, fmt.Errorf("fred: series %s has no usable observation", seriesID)
}
