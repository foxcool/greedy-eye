package fred

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// serve stands up a stub of the observations endpoint that replies with a
// recorded body. The returned counter is how many requests reached it.
func serve(t *testing.T, status int, fixture string) (*Client, *int, *http.Request) {
	t.Helper()
	body, err := os.ReadFile(filepath.Join("testdata", fixture))
	require.NoError(t, err)

	var calls int
	var last http.Request
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		last = *r.Clone(context.Background())
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write(body)
	}))
	t.Cleanup(srv.Close)

	return NewClient(Config{APIKey: "secret-key", BaseURL: srv.URL}), &calls, &last
}

func TestLatestReadsNewestPublishedObservation(t *testing.T) {
	c, calls, req := serve(t, http.StatusOK, "dff_observations.json")

	obs, err := c.Latest(context.Background(), "DFF")
	require.NoError(t, err)

	// The newest row is "." — FRED's marker for a day with no print yet. The
	// value must come from the latest day that has one, and carry that day.
	assert.Equal(t, "3.88", obs.Value.String())
	assert.Equal(t, "2026-11-12", obs.Date.Format("2006-01-02"))

	assert.Equal(t, 1, *calls)
	assert.Equal(t, "/fred/series/observations", req.URL.Path)
	q := req.URL.Query()
	assert.Equal(t, "DFF", q.Get("series_id"))
	assert.Equal(t, "secret-key", q.Get("api_key"))
	assert.Equal(t, "json", q.Get("file_type"))
	assert.Equal(t, "desc", q.Get("sort_order"))
}

func TestLatestWithoutUsableObservationFails(t *testing.T) {
	c, _, _ := serve(t, http.StatusOK, "no_usable_observation.json")

	_, err := c.Latest(context.Background(), "DFF")
	require.Error(t, err)
}

func TestLatestRefusedKeyIsAnErrorWithoutTheKey(t *testing.T) {
	c, _, _ := serve(t, http.StatusBadRequest, "bad_key.json")

	_, err := c.Latest(context.Background(), "DFF")
	require.Error(t, err)
	assert.NotContains(t, err.Error(), "secret-key")
}

func TestLatestTransportErrorDoesNotLeakTheKey(t *testing.T) {
	// Nothing listens here, so the HTTP client fails with a *url.Error whose
	// text embeds the full request URL — api_key included.
	c := NewClient(Config{APIKey: "secret-key", BaseURL: "http://127.0.0.1:1"})

	_, err := c.Latest(context.Background(), "DFF")
	require.Error(t, err)
	assert.NotContains(t, err.Error(), "secret-key")
}

func TestLatestRequiresAKey(t *testing.T) {
	_, err := NewClient(Config{}).Latest(context.Background(), "DFF")
	require.Error(t, err)
}
