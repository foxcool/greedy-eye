package gateio

import (
	"context"
	"crypto/hmac"
	"crypto/sha512"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Distinctive enough to search for. A one-character secret made the leak test
// below vacuous — "s" is a substring of the request path itself.
const (
	testKey    = "gk-4f2ab8"
	testSecret = "gs-9c1be7"
)

// newTestSyncer points a syncer at a stub Gate.io. The clock is frozen so a
// signature computed by hand in a test is reproducible.
func newTestSyncer(t *testing.T, handler http.HandlerFunc) *ExchangeSyncer {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	client := NewClient(Config{APIKey: testKey, APISecret: testSecret, BaseURL: srv.URL})
	client.now = func() time.Time { return time.Unix(1757000000, 0) }
	return NewExchangeSyncer(client)
}

func TestSyncExchange_SumsAvailableAndLocked(t *testing.T) {
	syncer := newTestSyncer(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`[
			{"currency":"BTC","available":"0.50000000","locked":"0.10000000"},
			{"currency":"DOGE","available":"1234.56789012","locked":"0"},
			{"currency":"USDT","available":"0","locked":"0"}
		]`))
	})

	balances, err := syncer.SyncExchange(context.Background())
	require.NoError(t, err)
	require.Len(t, balances, 2, "a currency with nothing in it is not a position")

	bySymbol := map[string]string{}
	for _, b := range balances {
		assert.Equal(t, balanceDecimals, b.Decimals)
		bySymbol[b.Symbol] = b.Amount
	}
	assert.Equal(t, "60000000", bySymbol["BTC"], "(0.5 + 0.1) * 10^8")
	assert.Equal(t, "123456789012", bySymbol["DOGE"], "1234.56789012 * 10^8, exact")
}

// A currency that never had an order comes back without the field. That is
// zero; an amount that is present and unreadable is not.
// TestSyncExchange_LockedIsDisjointAnchor is the live proof of the balance
// model — the only observation that tells the two readings apart.
//
// Dev's account holds MEW against an unfilled GTC sell order for 50009.1 placed
// 2025-11-02. The sync of 2026-09-05 reported 50009.336898, leaving 0.236898
// available beside the order. That total is reachable only if the pools are
// disjoint: were locked a subset, the API would have returned 50009.336898 as
// available and this code would have published 100018.436898.
//
// The amounts below are reconstructed from that total and that order, not
// captured from the wire.
func TestSyncExchange_LockedIsDisjointAnchor(t *testing.T) {
	syncer := newTestSyncer(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`[{"currency":"MEW","available":"0.236898","locked":"50009.1"}]`))
	})

	balances, err := syncer.SyncExchange(context.Background())
	require.NoError(t, err)
	require.Len(t, balances, 1)
	assert.Equal(t, "5000933689800", balances[0].Amount,
		"50009.336898 MEW: what the live account reported on 2026-09-05")
	assert.NotEqual(t, "10001843689800", balances[0].Amount,
		"the doubling a subset reading would produce")
}

func TestSyncExchange_MissingLockedIsZero(t *testing.T) {
	syncer := newTestSyncer(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`[{"currency":"ETH","available":"2.5"}]`))
	})

	balances, err := syncer.SyncExchange(context.Background())
	require.NoError(t, err)
	require.Len(t, balances, 1)
	assert.Equal(t, "250000000", balances[0].Amount)
}

func TestSyncExchange_UnreadableAmountIsAnError(t *testing.T) {
	syncer := newTestSyncer(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`[{"currency":"BTC","available":"lots","locked":"0"}]`))
	})

	_, err := syncer.SyncExchange(context.Background())
	require.Error(t, err, "an amount with no scale must not be read as zero")
	assert.Contains(t, err.Error(), "BTC")
}

func TestSyncExchange_PropagatesAPIError(t *testing.T) {
	syncer := newTestSyncer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"label":"INVALID_SIGNATURE","message":"Signature mismatch"}`))
	})

	_, err := syncer.SyncExchange(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "INVALID_SIGNATURE")
}

func TestSyncExchange_RefusesWithoutCredentials(t *testing.T) {
	client := NewClient(Config{BaseURL: "http://127.0.0.1:1"})
	_, err := NewExchangeSyncer(client).SyncExchange(context.Background())
	require.Error(t, err, "an unsigned request would be refused by the venue anyway; fail before spending it")
	assert.Contains(t, err.Error(), "api key and secret")
}

// Pinned against an independently computed HMAC, not against the client's own
// output — which would only ever agree with itself.
func TestSign_PayloadShape(t *testing.T) {
	var got http.Header
	syncer := newTestSyncer(t, func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Clone()
		_, _ = w.Write([]byte(`[]`))
	})

	_, err := syncer.SyncExchange(context.Background())
	require.NoError(t, err)

	const ts = "1757000000"
	emptyBody := sha512.Sum512(nil)
	want := "GET\n" + accountsPath + "\n\n" + hex.EncodeToString(emptyBody[:]) + "\n" + ts

	mac := hmac.New(sha512.New, []byte(testSecret))
	mac.Write([]byte(want))

	assert.Equal(t, testKey, got.Get("KEY"))
	assert.Equal(t, ts, got.Get("Timestamp"))
	assert.Equal(t, hex.EncodeToString(mac.Sum(nil)), got.Get("SIGN"))
}

// The secret is only ever an HMAC input: it must reach the wire nowhere else.
func TestSign_SecretNeverLeavesTheProcess(t *testing.T) {
	var (
		gotURL    string
		gotHeader http.Header
	)
	syncer := newTestSyncer(t, func(w http.ResponseWriter, r *http.Request) {
		gotURL, gotHeader = r.URL.String(), r.Header.Clone()
		_, _ = w.Write([]byte(`[]`))
	})

	_, err := syncer.SyncExchange(context.Background())
	require.NoError(t, err)

	assert.NotContains(t, gotURL, testSecret)
	for name, values := range gotHeader {
		for _, v := range values {
			assert.NotContains(t, v, testSecret, "secret leaked in header %s", name)
		}
	}
}
