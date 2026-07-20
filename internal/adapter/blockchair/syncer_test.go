package blockchair

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	dashAddress = "XyAKZDSC5FnmJnBt2ZBhQ2G9dRrVpvBQhq"
	dogeAddress = "DH5yaieqoZN36fDVciNyRueRGvGLR3mr7L"
)

// newTestSyncer points a syncer at a stub Blockchair serving the given body.
func newTestSyncer(t *testing.T, handler http.HandlerFunc) *WalletSyncerAdapter {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	return NewWalletSyncer(NewClient(Config{BaseURL: srv.URL}))
}

func respondJSON(body string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}
}

// dashboardFor renders the API's per-address envelope, which keys the result by
// the address that was asked for.
func dashboardFor(address string, balance int64) string {
	return fmt.Sprintf(`{"data":{%q:{"address":{"balance":%d}}},"context":{"code":200}}`,
		address, balance)
}

// TestSyncWallet_PerChainDecimals pins the scaling and the symbol per chain.
func TestSyncWallet_PerChainDecimals(t *testing.T) {
	tests := []struct {
		chain      string
		address    string
		wantSymbol string
	}{
		{"dash", dashAddress, "DASH"},
		{"dogecoin", dogeAddress, "DOGE"},
	}
	for _, tt := range tests {
		t.Run(tt.chain, func(t *testing.T) {
			syncer := newTestSyncer(t, respondJSON(dashboardFor(tt.address, 125000000)))

			balances, err := syncer.SyncWallet(context.Background(), tt.address, []string{tt.chain})
			require.NoError(t, err)
			require.Len(t, balances, 1)

			assert.Equal(t, tt.wantSymbol, balances[0].Symbol)
			assert.Equal(t, 8, balances[0].Decimals)
			assert.Equal(t, "125000000", balances[0].Amount, "the smallest unit is already raw")
			assert.Empty(t, balances[0].ContractAddress)
		})
	}
}

// TestSyncWallet_AutoDiscoveryPicksOneChain: an address is valid on exactly one
// of these chains, so discovery resolves it from the version byte instead of
// probing every chain and collecting "never seen" answers.
func TestSyncWallet_AutoDiscoveryPicksOneChain(t *testing.T) {
	var asked []string
	syncer := newTestSyncer(t, func(w http.ResponseWriter, r *http.Request) {
		asked = append(asked, strings.Split(r.URL.Path, "/")[1])
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(dashboardFor(dogeAddress, 5000000000)))
	})

	balances, err := syncer.SyncWallet(context.Background(), dogeAddress, nil)
	require.NoError(t, err)

	assert.Equal(t, []string{"dogecoin"}, asked, "only the address's own chain is asked")
	require.Len(t, balances, 1)
	assert.Equal(t, "DOGE", balances[0].Symbol)
}

// TestSyncWallet_ErrorEnvelope: Blockchair reports rate limiting as a body
// error, and the reason is worth surfacing — a blacklisted IP looks nothing
// like an empty wallet and must not be mistaken for one.
func TestSyncWallet_ErrorEnvelope(t *testing.T) {
	syncer := newTestSyncer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(430)
		_, _ = w.Write([]byte(
			`{"data":null,"context":{"code":430,"error":"Your IP address is temporary blacklisted"}}`))
	})

	_, err := syncer.SyncWallet(context.Background(), dashAddress, []string{"dash"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "blacklisted")
}

// TestSyncWallet_EmptyAndUnknown covers the two quiet cases: an address the
// chain never saw yields no position rather than a zero one, and a chain this
// adapter does not serve is reported instead of silently skipped.
func TestSyncWallet_EmptyAndUnknown(t *testing.T) {
	syncer := newTestSyncer(t, respondJSON(
		`{"data":{"`+dashAddress+`":{"address":null}},"context":{"code":200}}`))

	balances, err := syncer.SyncWallet(context.Background(), dashAddress, []string{"dash"})
	require.NoError(t, err)
	assert.Empty(t, balances, "an unused address must not create a zero holding")

	_, err = syncer.SyncWallet(context.Background(), dashAddress, []string{"bitcoin"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "bitcoin")
}

// TestSyncWallet_PartialFailureKeepsBalances covers the WalletSyncer contract.
func TestSyncWallet_PartialFailureKeepsBalances(t *testing.T) {
	syncer := newTestSyncer(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/dash") {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(dashboardFor(dogeAddress, 7000000000)))
	})

	balances, err := syncer.SyncWallet(context.Background(), dogeAddress, []string{"dash", "dogecoin"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "dash")
	require.Len(t, balances, 1)
	assert.Equal(t, "DOGE", balances[0].Symbol)
}

// TestHandlesAddress routes auto-discovery. Bitcoin is the negative that
// matters: its addresses share this exact layout and differ only in the version
// byte, so claiming them here would resolve a real wallet against the wrong
// chain and report it as empty.
func TestHandlesAddress(t *testing.T) {
	tests := []struct {
		name    string
		address string
		want    bool
	}{
		{"dash", dashAddress, true},
		{"dogecoin", dogeAddress, true},
		{"bitcoin legacy p2pkh", "1BvBMSEYstWetqTFn5Au4m4GFg7xJaNVN2", false},
		{"bitcoin legacy p2sh", "3J98t1WpEZ73CNmQviecrnyiWrnqRhWNLy", false},
		{"bitcoin bech32", "bc1qar0srrr7xfkvy5l643lydnw9re59gtzzwf5mdq", false},
		{"solana", "CvC1oRhFemouyXTBSJp6NBdEUN1CqQEYRNFcUh46Cqv8", false},
		{"ss58", "5DsvsaNbaA4JPXPRJHA2wWDMv4oJaWKQDbrUKEeELRfrto7Q", false},
		{"tezos", "tz1eZwq8b5cvE2bPKokatLkVMzkxz24z3Don", false},
		{"empty", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, HandlesAddress(tt.address))
		})
	}
}

func TestSupportedChainsIsStable(t *testing.T) {
	assert.Equal(t, []string{"dash", "dogecoin"}, SupportedChains())
}
