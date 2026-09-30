package urlerr

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestStrip_KeyInURLDoesNotReachTheMessage: a transport failure against a URL
// carrying a key in its path and its query says what happened, never where.
func TestStrip_KeyInURLDoesNotReachTheMessage(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(200 * time.Millisecond)
	}))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL+"/v2/PATHSECRET/x?key=QUERYSECRET", nil)
	require.NoError(t, err)

	_, raw := srv.Client().Do(req)
	require.Error(t, raw)
	require.Contains(t, raw.Error(), "PATHSECRET", "the standard library quotes the URL, which is the leak")

	got := Strip(raw)
	assert.NotContains(t, got.Error(), "PATHSECRET")
	assert.NotContains(t, got.Error(), "QUERYSECRET")
	assert.True(t, errors.Is(got, context.DeadlineExceeded), "the cause survives")

	plain := errors.New("status 404")
	assert.Same(t, plain, Strip(plain))
	assert.Nil(t, Strip(nil))
}
