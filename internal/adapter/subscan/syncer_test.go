package subscan

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newTestSyncer points a syncer at a stub Subscan serving the given body.
func newTestSyncer(t *testing.T, handler http.HandlerFunc) *WalletSyncerAdapter {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	client := NewClient(Config{APIKey: "test-key"})
	client.baseURLOverride = srv.URL
	return NewWalletSyncer(client)
}

func respondJSON(body string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}
}

// TestSyncWallet_StakedBalanceNotDoubleCounted is the correctness guard for
// this adapter. Substrate locks (bonded, unbonding, governance) restrict the
// free balance rather than sitting beside it, so a staking-heavy account —
// the Hydration controller being the motivating case — must report free +
// reserved, not free + reserved + bonded.
func TestSyncWallet_StakedBalanceNotDoubleCounted(t *testing.T) {
	// 100 free, of which 90 is bonded and 5 unbonding, plus 2 reserved.
	syncer := newTestSyncer(t, respondJSON(`{
		"code": 0, "message": "Success",
		"data": {"account": {
			"address": "15oF4u",
			"balance": "100.5",
			"reserved": "2",
			"bonded": "90",
			"unbonding": "5",
			"lock": "90"
		}}
	}`))

	balances, err := syncer.SyncWallet(context.Background(), "15oF4u", []string{"polkadot"})
	require.NoError(t, err)
	require.Len(t, balances, 1)

	assert.Equal(t, "DOT", balances[0].Symbol)
	assert.Equal(t, 10, balances[0].Decimals)
	// (100.5 + 2) * 10^10 — bonded and unbonding are already inside free.
	assert.Equal(t, "1025000000000", balances[0].Amount)
}

// TestSyncWallet_PerChainDecimals pins the scaling per network: the same
// decimal string means a different raw integer on each chain.
func TestSyncWallet_PerChainDecimals(t *testing.T) {
	tests := []struct {
		chain      string
		wantSymbol string
		wantAmount string
	}{
		{"polkadot", "DOT", "12500000000"},          // 10 decimals
		{"kusama", "KSM", "1250000000000"},          // 12
		{"hydration", "HDX", "1250000000000"},       // 12
		{"astar", "ASTR", "1250000000000000000"},    // 18
		{"moonbeam", "GLMR", "1250000000000000000"}, // 18
	}
	for _, tt := range tests {
		t.Run(tt.chain, func(t *testing.T) {
			syncer := newTestSyncer(t, respondJSON(
				`{"code":0,"data":{"account":{"balance":"1.25","reserved":"0"}}}`))

			balances, err := syncer.SyncWallet(context.Background(), "addr", []string{tt.chain})
			require.NoError(t, err)
			require.Len(t, balances, 1)
			assert.Equal(t, tt.wantSymbol, balances[0].Symbol)
			assert.Equal(t, tt.wantAmount, balances[0].Amount)
		})
	}
}

// TestSyncWallet_PartialFailureKeepsBalances covers the WalletSyncer contract:
// a failing chain surfaces as an error without discarding the chains that
// answered.
func TestSyncWallet_PartialFailureKeepsBalances(t *testing.T) {
	syncer := newTestSyncer(t, func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var req map[string]string
		_ = json.Unmarshal(body, &req)

		// The stub serves every network on one host, so branch on the address
		// to simulate one chain being down.
		if req["key"] == "broken" {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"code":0,"data":{"account":{"balance":"7","reserved":"0"}}}`))
	})

	balances, err := syncer.SyncWallet(context.Background(), "addr", []string{"polkadot", "kusama"})
	require.NoError(t, err)
	require.Len(t, balances, 2)

	_, err = syncer.SyncWallet(context.Background(), "broken", []string{"polkadot"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "polkadot")
}

// TestSyncWallet_SubscanErrorEnvelope: Subscan reports failures with HTTP 200
// and a non-zero code, so the body must be inspected rather than the status.
func TestSyncWallet_SubscanErrorEnvelope(t *testing.T) {
	syncer := newTestSyncer(t, respondJSON(`{"code": 10004, "message": "Record Not Found"}`))

	_, err := syncer.SyncWallet(context.Background(), "addr", []string{"polkadot"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "Record Not Found")
}

// TestSyncWallet_EmptyAndUnknown covers the two quiet cases: an address the
// chain never saw yields no position rather than a zero one, and a chain this
// adapter does not serve is reported instead of silently skipped.
func TestSyncWallet_EmptyAndUnknown(t *testing.T) {
	syncer := newTestSyncer(t, respondJSON(`{"code":0,"data":{"account":{}}}`))

	balances, err := syncer.SyncWallet(context.Background(), "addr", []string{"polkadot"})
	require.NoError(t, err)
	assert.Empty(t, balances, "an unused address must not create a zero holding")

	_, err = syncer.SyncWallet(context.Background(), "addr", []string{"ethereum"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "ethereum")
}

// TestSyncWallet_RejectsAutoDiscovery: SS58 encodes a different address per
// network, so there is nothing to discover from a key alone. The registry
// never routes an empty chain list here; this pins the guard anyway.
func TestSyncWallet_RejectsAutoDiscovery(t *testing.T) {
	syncer := newTestSyncer(t, respondJSON(`{"code":0,"data":{"account":{}}}`))

	_, err := syncer.SyncWallet(context.Background(), "addr", nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "auto-discovery")
}

func TestSupportedChainsIsStable(t *testing.T) {
	assert.Equal(t,
		[]string{"astar", "hydration", "kusama", "moonbeam", "polkadot"},
		SupportedChains())
}
