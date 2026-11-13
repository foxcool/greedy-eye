package cbr

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func serveKeyRate(t *testing.T, status int, fixture string) (*Client, *string) {
	t.Helper()
	body, err := os.ReadFile(filepath.Join("testdata", fixture))
	require.NoError(t, err)

	var sent string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, keyRatePath, r.URL.Path)
		assert.Equal(t, http.MethodPost, r.Method)
		assert.Equal(t, userAgent, r.Header.Get("User-Agent"))
		assert.Equal(t, `"http://web.cbr.ru/KeyRate"`, r.Header.Get("SOAPAction"))
		raw, _ := io.ReadAll(r.Body)
		sent = string(raw)
		w.WriteHeader(status)
		_, _ = w.Write(body)
	}))
	t.Cleanup(srv.Close)
	return NewClient(Config{BaseURL: srv.URL}), &sent
}

func TestLatestKeyRateReadsTheNewestPrint(t *testing.T) {
	c, sent := serveKeyRate(t, http.StatusOK, "keyrate_soap.xml")

	obs, err := c.Latest(context.Background(), SeriesKeyRate)
	require.NoError(t, err)

	assert.Equal(t, "14", obs.Value.String())
	// The newest row of the recorded response, as the publisher dates it.
	assert.Equal(t, "2026-11-13T00:00:00+03:00", obs.Date.Format("2006-01-02T15:04:05Z07:00"))
	assert.Contains(t, *sent, "<fromDate>")
	assert.Contains(t, *sent, "<ToDate>")
}

func TestLatestRejectsAnUnknownSeries(t *testing.T) {
	c, _ := serveKeyRate(t, http.StatusOK, "keyrate_soap.xml")

	_, err := c.Latest(context.Background(), "usd_rub")
	require.Error(t, err)
}

func TestLatestKeyRateServerErrorIsAnError(t *testing.T) {
	c, _ := serveKeyRate(t, http.StatusInternalServerError, "keyrate_soap.xml")

	_, err := c.Latest(context.Background(), SeriesKeyRate)
	require.Error(t, err)
}

func TestLatestKeyRateEmptyWindowIsAnError(t *testing.T) {
	c, _ := serveKeyRate(t, http.StatusOK, "keyrate_empty.xml")

	_, err := c.Latest(context.Background(), SeriesKeyRate)
	require.Error(t, err)
}
