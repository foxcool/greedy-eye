package esplora

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const testAddress = "bc1qar0srrr7xfkvy5l643lydnw9re59gtzzwf5mdq"

// newTestSyncer points a syncer at a stub Esplora serving the given body.
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

// TestSyncWallet_BalanceIsFundedMinusSpent is the correctness guard: Esplora
// reports lifetime totals, not a balance, so the holding is the difference.
// Reading funded_txo_sum alone would report every satoshi the address ever
// received as if it were still there.
func TestSyncWallet_BalanceIsFundedMinusSpent(t *testing.T) {
	syncer := newTestSyncer(t, respondJSON(`{
		"chain_stats": {"funded_txo_sum": 250000000, "spent_txo_sum": 100000000},
		"mempool_stats": {"funded_txo_sum": 999999999, "spent_txo_sum": 0}
	}`))

	balances, err := syncer.SyncWallet(context.Background(), testAddress, []string{Chain})
	require.NoError(t, err)
	require.Len(t, balances, 1)

	assert.Equal(t, "BTC", balances[0].Symbol)
	assert.Equal(t, 8, balances[0].Decimals)
	// 2.5 - 1.0 BTC in satoshis; the mempool figures are deliberately ignored.
	assert.Equal(t, "150000000", balances[0].Amount)
	assert.Empty(t, balances[0].ContractAddress, "native coins carry no contract")
}

// TestSyncWallet_UnconfirmedIgnored: mempool activity can be replaced or
// dropped, so it must not move the position on its own.
func TestSyncWallet_UnconfirmedIgnored(t *testing.T) {
	syncer := newTestSyncer(t, respondJSON(`{
		"chain_stats": {"funded_txo_sum": 0, "spent_txo_sum": 0},
		"mempool_stats": {"funded_txo_sum": 5000000, "spent_txo_sum": 0}
	}`))

	balances, err := syncer.SyncWallet(context.Background(), testAddress, nil)
	require.NoError(t, err)
	assert.Empty(t, balances)
}

// TestSyncWallet_EmptyAndUnknown covers the two quiet cases: an address the
// chain never saw yields no position rather than a zero one, and a chain this
// adapter does not serve is reported instead of silently skipped.
func TestSyncWallet_EmptyAndUnknown(t *testing.T) {
	syncer := newTestSyncer(t, respondJSON(`{"chain_stats":{}}`))

	balances, err := syncer.SyncWallet(context.Background(), testAddress, nil)
	require.NoError(t, err)
	assert.Empty(t, balances, "an unused address must not create a zero holding")

	_, err = syncer.SyncWallet(context.Background(), testAddress, []string{"ethereum"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "ethereum")
}

// TestSyncWallet_MalformedSumIsAnError: a sum that will not parse must fail
// loudly. Treating it as zero would erase a real balance and look like the
// wallet had been emptied.
func TestSyncWallet_MalformedSumIsAnError(t *testing.T) {
	syncer := newTestSyncer(t, respondJSON(
		`{"chain_stats":{"funded_txo_sum":"1e6","spent_txo_sum":0}}`))

	_, err := syncer.SyncWallet(context.Background(), testAddress, nil)
	require.Error(t, err)
}

func TestSyncWallet_HTTPError(t *testing.T) {
	syncer := newTestSyncer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	})

	_, err := syncer.SyncWallet(context.Background(), testAddress, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "503")
}

// TestHandlesAddress routes auto-discovery. Dash and Dogecoin are the negatives
// that matter: their addresses share the 25-byte base58 layout and differ only
// in the version byte, so a prefix-only rule would claim them here and report
// an empty wallet instead of routing them to Blockchair.
func TestHandlesAddress(t *testing.T) {
	tests := []struct {
		name    string
		address string
		want    bool
	}{
		{"bech32 p2wpkh", testAddress, true},
		{"legacy p2pkh", "1BvBMSEYstWetqTFn5Au4m4GFg7xJaNVN2", true},
		{"legacy p2sh", "3J98t1WpEZ73CNmQviecrnyiWrnqRhWNLy", true},
		{"dash", "XyAKZDSC5FnmJnBt2ZBhQ2G9dRrVpvBQhq", false},
		{"dogecoin", "DH5yaieqoZN36fDVciNyRueRGvGLR3mr7L", false},
		{"solana pubkey", "CvC1oRhFemouyXTBSJp6NBdEUN1CqQEYRNFcUh46Cqv8", false},
		{"ss58", "5DsvsaNbaA4JPXPRJHA2wWDMv4oJaWKQDbrUKEeELRfrto7Q", false},
		{"evm", "0x75304308839f839a553b60b5671bb2f043420167", false},
		{"cosmos bech32", "cosmos14zta4gplkeym0dmlgnuxa2fmh7ymxtnlepxgxz", false},
		{"empty", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, HandlesAddress(tt.address))
		})
	}
}

func TestSupportedChainsIsStable(t *testing.T) {
	assert.Equal(t, []string{"bitcoin"}, SupportedChains())
}
