package tinvest

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestQuotationDecimal(t *testing.T) {
	tests := []struct {
		name  string
		in    Quotation
		want  string
		valid bool
	}{
		{"whole units", Quotation{Units: "114", Nano: 0}, "114", true},
		{"units and nano", Quotation{Units: "114", Nano: 250000000}, "114.25", true},
		{"nano only", Quotation{Units: "0", Nano: 123456789}, "0.123456789", true},
		// Both parts carry the sign, so they add rather than cancel. Getting
		// this backwards turns -1.5 into -0.5.
		{"negative", Quotation{Units: "-1", Nano: -500000000}, "-1.5", true},
		{"absent units", Quotation{Nano: 500000000}, "0.5", true},
		{"garbage units", Quotation{Units: "не число"}, "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := tt.in.Decimal()
			require.Equal(t, tt.valid, ok)
			if tt.valid {
				assert.Equal(t, tt.want, got.String())
			}
		})
	}
}

// A client with neither a trust anchor nor a transport must refuse to exist.
// Building one anyway would move the failure to the first sweep, where a TLS
// handshake error says nothing about configuration.
func TestNewClientRequiresRootCA(t *testing.T) {
	_, err := NewClient(Config{Token: "t"})
	require.ErrorIs(t, err, ErrNoRootCA)

	_, err = TLSTransport(nil)
	require.ErrorIs(t, err, ErrNoRootCA)
}

// TestBaseURLDefaultsToProduction: the field is optional, and an instance that
// never sets it must keep talking to the broker exactly as before. This is the
// path every existing account is on, so a regression here is silent everywhere.
func TestBaseURLDefaultsToProduction(t *testing.T) {
	c, err := NewClient(Config{Token: "t", Transport: http.DefaultTransport})
	require.NoError(t, err)
	assert.Equal(t, defaultBaseURL, c.baseURL)

	// A trailing slash is the one thing normalised, because service paths are
	// appended with a leading one.
	c, err = NewClient(Config{Token: "t", BaseURL: "http://127.0.0.1:8081/rest/", Transport: http.DefaultTransport})
	require.NoError(t, err)
	assert.Equal(t, "http://127.0.0.1:8081/rest", c.baseURL)
}

// TestCheckBaseURLNamesWhatIsWrong: the value is typed by a person, so its
// rejection has to say which part is unusable. Concatenated onto a service path
// instead, a typo fails later as a connection error indistinguishable from the
// broker being down.
func TestCheckBaseURLNamesWhatIsWrong(t *testing.T) {
	assert.NoError(t, CheckBaseURL(""), "empty selects the production gateway")
	assert.NoError(t, CheckBaseURL("https://sandbox-invest-public-api.tbank.ru/rest"))
	assert.NoError(t, CheckBaseURL("http://localhost:8081"))

	for _, tc := range []struct{ raw, says string }{
		{"invest-public-api.tbank.ru/rest", "scheme"},
		{"ftp://example.invalid", "scheme"},
		{"https://", "no host"},
		{"http://127.0.0.1?x=1", "query"},
		{"http://127.0.0.1#frag", "query"},
	} {
		err := CheckBaseURL(tc.raw)
		require.ErrorIs(t, err, ErrBadBaseURL, "raw %q", tc.raw)
		assert.Contains(t, err.Error(), tc.raw, "the message must quote the value")
		assert.Contains(t, err.Error(), tc.says)
	}
}

// TestInsecureBaseURLOnlyForPlainHTTP guards the rule the registry reads to
// decide whether a trust anchor is required. Answering true for an https URL
// would drop TLS verification on the live gateway.
func TestInsecureBaseURLOnlyForPlainHTTP(t *testing.T) {
	assert.True(t, InsecureBaseURL("http://127.0.0.1:8081/rest"))
	assert.False(t, InsecureBaseURL("https://invest-public-api.tbank.ru/rest"))
	assert.False(t, InsecureBaseURL(""), "empty is the production gateway, which is https")
	assert.False(t, InsecureBaseURL("://nonsense"))
}

func TestTLSTransportRejectsUnusablePEM(t *testing.T) {
	_, err := TLSTransport([]byte("-----BEGIN CERTIFICATE-----\nnot a certificate\n-----END CERTIFICATE-----"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no usable certificate")
}

func TestClientSendsBearerToken(t *testing.T) {
	var gotAuth, gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotPath = r.URL.Path
		_ = json.NewEncoder(w).Encode(map[string]any{"lastPrices": []any{}})
	}))
	defer srv.Close()

	c := testClient(t, srv)
	_, err := c.LastPrices(context.Background(), []string{"BBG000B9XRY4"})
	require.NoError(t, err)
	assert.Equal(t, "Bearer test-token", gotAuth)
	assert.Equal(t, pathLastPrices, gotPath)
}

func TestClientRequiresToken(t *testing.T) {
	c, err := NewClient(Config{Transport: http.DefaultTransport})
	require.NoError(t, err)
	_, err = c.LastPrices(context.Background(), []string{"BBG000B9XRY4"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no API token")
}

// The error envelope is read for its message and never echoed whole: the token
// travels in the same request, and a verbose error is the classic way for one
// to reach a log.
func TestClientSurfacesAPIErrorWithoutEchoingTheRequest(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"code":16,"message":"Authentication token is missing or invalid","description":"40003"}`))
	}))
	defer srv.Close()

	c := testClient(t, srv)
	_, err := c.LastPrices(context.Background(), []string{"BBG000B9XRY4"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "Authentication token is missing or invalid")
	assert.NotContains(t, err.Error(), "test-token")
}

func TestClientEmptyBatchAsksNothing(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	c := testClient(t, srv)
	prices, err := c.LastPrices(context.Background(), nil)
	require.NoError(t, err)
	assert.Empty(t, prices)
	statuses, err := c.TradingStatuses(context.Background(), nil)
	require.NoError(t, err)
	assert.Empty(t, statuses)
	assert.Zero(t, calls, "an empty batch is not a request")
}

func TestClientReadsInstrumentUniverse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req map[string]string
		require.NoError(t, json.NewDecoder(r.Body).Decode(&req))
		assert.Equal(t, instrumentStatusBase, req["instrumentStatus"])
		_, _ = w.Write([]byte(`{"instruments":[{
			"figi":"BBG000B9XRY4","ticker":"AAPL","classCode":"SPBXM","currency":"usd",
			"realExchange":"REAL_EXCHANGE_RTS","apiTradeAvailableFlag":true}]}`))
	}))
	defer srv.Close()

	c := testClient(t, srv)
	shares, err := c.Shares(context.Background())
	require.NoError(t, err)
	require.Len(t, shares, 1)
	assert.Equal(t, "BBG000B9XRY4", shares[0].FIGI)
	assert.Equal(t, "usd", shares[0].Currency)
	assert.Equal(t, RealExchangeRTS, shares[0].RealExchange)
}

func testClient(t *testing.T, srv *httptest.Server) *Client {
	t.Helper()
	// A supplied transport owns TLS, which is what lets a test talk plain HTTP
	// without a trust anchor.
	c, err := NewClient(Config{Token: "test-token", BaseURL: srv.URL, Transport: srv.Client().Transport})
	require.NoError(t, err)
	return c
}
