package cbr

import (
	"bytes"
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/foxcool/greedy-eye/internal/entity"
	"github.com/shopspring/decimal"
)

// SeriesKeyRate names the Bank of Russia key rate for Latest.
const SeriesKeyRate = "key_rate"

// keyRatePath is the publisher's web service. The key rate has no plain XML
// feed like the daily rates; the service's KeyRate operation is the official
// machine-readable source, and answers a SOAP call only.
const keyRatePath = "/DailyInfoWebServ/DailyInfo.asmx"

// keyRateLookback is how far back the request reaches. The rate is quoted for
// every calendar day, so a few days always hold a row; the margin covers a
// publication gap rather than a rate that moves rarely.
const keyRateLookback = 14 * 24 * time.Hour

const keyRateEnvelope = `<?xml version="1.0" encoding="utf-8"?>` +
	`<soap:Envelope xmlns:soap="http://schemas.xmlsoap.org/soap/envelope/"><soap:Body>` +
	`<KeyRate xmlns="http://web.cbr.ru/"><fromDate>%s</fromDate><ToDate>%s</ToDate></KeyRate>` +
	`</soap:Body></soap:Envelope>`

// keyRateResponse picks the rows out of the dataset wrapped in the SOAP reply.
// The schema block that precedes the data is ignored.
type keyRateResponse struct {
	Rows []struct {
		Date string `xml:"DT"`
		Rate string `xml:"Rate"`
	} `xml:"Body>KeyRateResponse>KeyRateResult>diffgram>KeyRate>KR"`
}

// Latest returns the newest print of a named series. Only SeriesKeyRate is
// served; anything else is a caller's mistake and is refused rather than
// answered with a different number.
func (c *Client) Latest(ctx context.Context, series string) (entity.MacroObservation, error) {
	if series != SeriesKeyRate {
		return entity.MacroObservation{}, fmt.Errorf("cbr: unknown series %q", series)
	}

	now := time.Now().In(moscow)
	body := fmt.Sprintf(keyRateEnvelope,
		now.Add(-keyRateLookback).Format(time.DateOnly), now.Format(time.DateOnly))

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+keyRatePath, bytes.NewReader([]byte(body)))
	if err != nil {
		return entity.MacroObservation{}, fmt.Errorf("cbr: build request: %w", err)
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Content-Type", "text/xml; charset=utf-8")
	req.Header.Set("SOAPAction", `"http://web.cbr.ru/KeyRate"`)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return entity.MacroObservation{}, fmt.Errorf("cbr: request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return entity.MacroObservation{}, fmt.Errorf("cbr: unexpected status %d", resp.StatusCode)
	}

	var doc keyRateResponse
	dec := xml.NewDecoder(io.LimitReader(resp.Body, 1<<20))
	dec.CharsetReader = charsetReader
	if err := dec.Decode(&doc); err != nil {
		return entity.MacroObservation{}, fmt.Errorf("cbr: decode: %w", err)
	}

	// Rows are not relied on to arrive newest-first.
	var (
		best  entity.MacroObservation
		found bool
	)
	for _, row := range doc.Rows {
		date, err := time.Parse(time.RFC3339, strings.TrimSpace(row.Date))
		if err != nil {
			continue
		}
		rate, err := decimal.NewFromString(strings.TrimSpace(row.Rate))
		if err != nil {
			continue
		}
		if !found || date.After(best.Date) {
			best, found = entity.MacroObservation{Value: rate, Date: date}, true
		}
	}
	if !found {
		return entity.MacroObservation{}, fmt.Errorf("cbr: no key rate in the last %d days", int(keyRateLookback.Hours()/24))
	}
	return best, nil
}
