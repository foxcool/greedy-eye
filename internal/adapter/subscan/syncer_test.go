package subscan

import (
	"context"
	"encoding/json"
	"fmt"
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

// TestSyncWallet_HydrationControllerAnchor is the regression anchor for the
// whole adapter: the live Hydration controller, captured 2026-07-20, holding
// 34644.639967302550 HDX of which 34015.697423172136 is bonded. That figure
// was verified against the manual baseline before any of this existed, so any
// change that moves it has broken something.
//
// It also pins the balance model: bonded is a subset of balance, and USDT in
// the builtin array is not the native token and must be ignored here.
func TestSyncWallet_HydrationControllerAnchor(t *testing.T) {
	syncer := newTestSyncer(t, respondJSON(`{
		"code": 0, "message": "Success",
		"data": {
			"native": [{
				"symbol": "HDX", "unique_id": "HDX", "decimals": 12,
				"balance": "34644639967302550",
				"lock": "34015697423172136",
				"reserved": "0",
				"bonded": "34015697423172136",
				"unbonding": "0",
				"price": "0.00585337"
			}],
			"builtin": [{
				"symbol": "USDT", "decimals": 6, "balance": "156501335"
			}]
		}
	}`))

	balances, err := syncer.SyncWallet(context.Background(), "5Dsvsa", []string{"hydration"})
	require.NoError(t, err)
	require.Len(t, balances, 1, "only the native token is read; builtin assets are personal-feb.10")

	assert.Equal(t, "HDX", balances[0].Symbol)
	assert.Equal(t, 12, balances[0].Decimals)
	assert.Equal(t, "34644639967302550", balances[0].Amount)
}

// TestSyncWallet_ReservedIsInsideBalance is the guard for the bug that made
// this endpoint switch necessary (personal-feb.12).
//
// The fixture is the live Kusama Asset Hub controller: balance 6.016092032275
// KSM, of which a staking hold of 5.621867712891 is reserved. The old code
// read /api/v2/scan/search, where `balance` arrives as whole tokens and
// `reserved` as planck, and added the two — producing 5.62 trillion KSM
// against a supply of 15 million. Both halves of that mistake are covered
// here: the units are uniform, and reserved is never added.
func TestSyncWallet_ReservedIsInsideBalance(t *testing.T) {
	syncer := newTestSyncer(t, respondJSON(`{
		"code": 0, "message": "Success",
		"data": {"native": [{
			"symbol": "KSM", "unique_id": "KSM", "decimals": 12,
			"balance": "6016092032275",
			"lock": "5621867712891",
			"reserved": "5621867712891",
			"bonded": "5621867712891",
			"unbonding": "0"
		}]}
	}`))

	balances, err := syncer.SyncWallet(context.Background(), "EEg3jY", []string{"assethub-kusama"})
	require.NoError(t, err)
	require.Len(t, balances, 1)

	assert.Equal(t, "6016092032275", balances[0].Amount,
		"balance already contains the staking hold; adding reserved double-counts it")
	assert.Equal(t, 12, balances[0].Decimals)
}

// TestSyncWallet_DecimalsComeFromResponse pins the rule that replaced the
// per-network decimals table: the response states its own precision, and the
// adapter reports it verbatim. A chain whose token changed precision, or a
// network the table has wrong, can no longer produce a rescaled holding.
func TestSyncWallet_DecimalsComeFromResponse(t *testing.T) {
	tests := []struct {
		chain    string
		symbol   string
		decimals int
	}{
		{"polkadot", "DOT", 10},
		{"kusama", "KSM", 12},
		{"assethub-polkadot", "DOT", 10},
		{"assethub-kusama", "KSM", 12},
		{"hydration", "HDX", 12},
		{"astar", "ASTR", 18},
		{"moonbeam", "GLMR", 18},
	}
	for _, tt := range tests {
		t.Run(tt.chain, func(t *testing.T) {
			syncer := newTestSyncer(t, respondJSON(fmt.Sprintf(
				`{"code":0,"data":{"native":[{"symbol":%q,"decimals":%d,"balance":"1250000000000"}]}}`,
				tt.symbol, tt.decimals)))

			balances, err := syncer.SyncWallet(context.Background(), "addr", []string{tt.chain})
			require.NoError(t, err)
			require.Len(t, balances, 1)
			assert.Equal(t, tt.symbol, balances[0].Symbol)
			assert.Equal(t, tt.decimals, balances[0].Decimals)
			assert.Equal(t, "1250000000000", balances[0].Amount,
				"a raw planck figure is stored as it arrived, never shifted")
		})
	}
}

// TestSyncWallet_RefusesBalanceWithoutDecimals: a native entry carrying no
// precision cannot be read at all. Falling back to the per-network table would
// restore the guess this endpoint exists to remove, so the sync fails loudly
// instead of storing a number scaled by assumption.
func TestSyncWallet_RefusesBalanceWithoutDecimals(t *testing.T) {
	syncer := newTestSyncer(t, respondJSON(
		`{"code":0,"data":{"native":[{"symbol":"DOT","balance":"1250000000000"}]}}`))

	_, err := syncer.SyncWallet(context.Background(), "addr", []string{"polkadot"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "decimals")
}

// TestSyncWallet_RefusesUnparsableBalance: an unreadable balance must not
// degrade to zero. That would report a funded account as empty — the silent
// failure this package has already been bitten by twice.
func TestSyncWallet_RefusesUnparsableBalance(t *testing.T) {
	syncer := newTestSyncer(t, respondJSON(
		`{"code":0,"data":{"native":[{"symbol":"DOT","decimals":10,"balance":"1.2e+bogus"}]}}`))

	_, err := syncer.SyncWallet(context.Background(), "addr", []string{"polkadot"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unparsable balance")
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
		if req["address"] == "broken" {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"code":0,"data":{"native":[{"symbol":"DOT","decimals":10,"balance":"7000000000"}]}}`))
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
	syncer := newTestSyncer(t, respondJSON(`{"code":0,"data":{"native":null}}`))

	balances, err := syncer.SyncWallet(context.Background(), "addr", []string{"polkadot"})
	require.NoError(t, err)
	assert.Empty(t, balances, "an unused address must not create a zero holding")

	_, err = syncer.SyncWallet(context.Background(), "addr", []string{"ethereum"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "ethereum")
}

// TestSyncWallet_AutoDiscoverySweepsNetworks: with no chains named the adapter
// probes every network it knows and keeps only the ones holding a balance, so
// one account covers the whole ecosystem. SS58 is a re-encoding of a single
// public key, which is what makes sweeping meaningful.
func TestSyncWallet_AutoDiscoverySweepsNetworks(t *testing.T) {
	var probed int
	syncer := newTestSyncer(t, func(w http.ResponseWriter, _ *http.Request) {
		probed++
		w.Header().Set("Content-Type", "application/json")
		// Only the second network probed holds anything.
		if probed == 2 {
			_, _ = w.Write([]byte(`{"code":0,"data":{"native":[{"symbol":"DOT","decimals":10,"balance":"3000000000"}]}}`))
			return
		}
		_, _ = w.Write([]byte(`{"code":0,"data":{"native":null}}`))
	})

	balances, err := syncer.SyncWallet(context.Background(), "5Dsvsa", nil)
	require.NoError(t, err)

	assert.Equal(t, len(sweepChains()), probed, "every sweepable network must be probed")
	require.Len(t, balances, 1, "networks without a balance yield no position")
}

// TestAutoDiscoverySkipsEVMChains: Moonbeam is served by this adapter but
// identifies accounts by EVM H160, so an SS58 address can never resolve there.
// Sweeping it spent a request to collect a "Record Not Found" that surfaced as
// a sync error on every single run of every Substrate account.
func TestAutoDiscoverySkipsEVMChains(t *testing.T) {
	assert.NotContains(t, sweepChains(), "moonbeam")
	assert.Contains(t, SupportedChains(), "moonbeam",
		"naming the chain explicitly must still work")

	var probed []string
	syncer := newTestSyncer(t, func(w http.ResponseWriter, r *http.Request) {
		probed = append(probed, r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"code":0,"data":{"native":null}}`))
	})

	_, err := syncer.SyncWallet(context.Background(), "5Dsvsa", nil)
	require.NoError(t, err, "the sweep must not report an error for a plain empty account")
	assert.Len(t, probed, len(sweepChains()))
}

// TestHandlesAddress routes auto-discovery. The same key is a different string
// on every network, so the prefix cannot be pinned — the checksum decides.
func TestHandlesAddress(t *testing.T) {
	tests := []struct {
		address string
		want    bool
	}{
		{"5DsvsaNbaA4JPXPRJHA2wWDMv4oJaWKQDbrUKEeELRfrto7Q", true}, // generic
		{"12pE1udfRwKmq4PwFvD35f3WmgnxGosYJ6axUXdatWhP5TUm", true}, // polkadot
		{"EPYXtiUCX5E9BCs4yy5qTaN4f5YPB8afyhDhtvBpDtMdvwF", true},  // kusama
		{"7KQnJQk4eGRZfktAKTdXsRoHVFoUjMYLBMVEEb9Z4PA6oiNJ", true}, // hydration
		// Moonbeam lives in this adapter but uses EVM addresses, so it can only
		// be reached by naming the chain.
		{"0x75304308839f839a553b60b5671bb2f043420167", false},
		{"EQCD39VS5jcptHL8vMjEXrzGaRcCVYto7HUn4bpAOg8xqB2N", false}, // TON: has 0/I/O
		{"", false},
	}
	for _, tt := range tests {
		assert.Equal(t, tt.want, HandlesAddress(tt.address), tt.address)
	}
}

// TestHandlesAddress_RejectsForeignBase58 is the routing guard for the
// ecosystems that share this alphabet. Length alone cannot separate them —
// a Solana pubkey is 43-44 characters against SS58's 46-48 — and a wrong
// answer here is silent: the foreign chain reports an unknown account and the
// position drops to zero instead of erroring.
func TestHandlesAddress_RejectsForeignBase58(t *testing.T) {
	tests := []struct {
		name    string
		address string
	}{
		{"solana pubkey", "CvC1oRhFemouyXTBSJp6NBdEUN1CqQEYRNFcUh46Cqv8"},
		{"bitcoin legacy", "1BvBMSEYstWetqTFn5Au4m4GFg7xJaNVN2"},
		{"dash", "XyAKZDSC5FnmJnBt2ZBhQ2G9dRrVpvBQhq"},
		// Right shape, one character changed: exactly what a typo or a
		// truncated copy-paste looks like.
		{"corrupted checksum", "5DsvsaNbaA4JPXPRJHA2wWDMv4oJaWKQDbrUKEeELRfrto7R"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.False(t, HandlesAddress(tt.address))
		})
	}
}

func TestSupportedChainsIsStable(t *testing.T) {
	assert.Equal(t,
		[]string{
			"assethub-kusama", "assethub-polkadot", "astar",
			"hydration", "kusama", "moonbeam", "polkadot",
		},
		SupportedChains())
}

// countingTransport records how many requests actually left the client.
type countingTransport struct {
	base  http.RoundTripper
	calls int
}

func (t *countingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	t.calls++
	return t.base.RoundTrip(req)
}

// TestConfigTransportIsUsed guards the wiring that the shared rate budget
// depends on: the budget is an http.RoundTripper handed in through Config, so
// a client that quietly ignores it would sweep at full speed and trip the plan
// limit again (personal-o06).
func TestConfigTransportIsUsed(t *testing.T) {
	srv := httptest.NewServer(respondJSON(`{
		"code": 0, "message": "Success",
		"data": {"native": [{"symbol": "HDX", "decimals": 12, "balance": "1000000000000"}]}
	}`))
	t.Cleanup(srv.Close)

	tr := &countingTransport{base: http.DefaultTransport}
	client := NewClient(Config{APIKey: "test-key", Transport: tr})
	client.baseURLOverride = srv.URL

	_, err := NewWalletSyncer(client).SyncWallet(context.Background(), "5Dsvsa", []string{"hydration"})
	require.NoError(t, err)
	assert.Positive(t, tr.calls, "Config.Transport must reach the HTTP client")
}
