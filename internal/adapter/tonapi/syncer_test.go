package tonapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newTestSyncer points a syncer at a stub serving the account and jettons
// endpoints. Either body may be empty to make that endpoint fail.
func newTestSyncer(t *testing.T, accountBody, jettonsBody string) *WalletSyncerAdapter {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body := accountBody
		if strings.HasSuffix(r.URL.Path, "/jettons") {
			body = jettonsBody
		}
		if body == "" {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)

	client := NewClient(Config{})
	client.baseURL = srv.URL
	return NewWalletSyncer(client)
}

// TestSyncWallet_NativeAndJettons covers the happy path. Balances arrive as
// raw integers already, so they pass through untouched — the failure mode to
// guard against is a large nanoton balance losing precision via float64.
func TestSyncWallet_NativeAndJettons(t *testing.T) {
	syncer := newTestSyncer(t,
		`{"balance":1592537944127325,"status":"active"}`,
		`{"balances":[
			{"balance":"4280000000000","wallet_address":{"is_scam":false},
			 "jetton":{"address":"0:e968","name":"Tether USD","symbol":"USDT","decimals":6,"verification":"whitelist"}},
			{"balance":"91000000000","wallet_address":{"is_scam":false},
			 "jetton":{"address":"0:aaaa","name":"Tonstakers TON","symbol":"tsTON","decimals":9,"verification":"whitelist"}}
		]}`)

	balances, err := syncer.SyncWallet(context.Background(), "EQCD39", []string{"ton"})
	require.NoError(t, err)
	require.Len(t, balances, 3)

	assert.Equal(t, "TON", balances[0].Symbol)
	assert.Equal(t, "1592537944127325", balances[0].Amount, "nanoton balance must survive exactly")
	assert.Equal(t, 9, balances[0].Decimals)

	assert.Equal(t, "USDT", balances[1].Symbol)
	assert.Equal(t, 6, balances[1].Decimals)
	assert.Equal(t, "0:e968", balances[1].ContractAddress)

	// Liquid staking needs no special handling: tsTON is an ordinary jetton.
	assert.Equal(t, "tsTON", balances[2].Symbol)
}

// TestSyncWallet_DropsFlaggedJettons pins the floor of scam filtering. The
// third fixture is copied from a live response: airdrop spam carrying a
// phishing domain in the ticker that tonapi flags neither way — it passes
// through until catalogue-wide scoring (personal-6yn) exists.
func TestSyncWallet_DropsFlaggedJettons(t *testing.T) {
	syncer := newTestSyncer(t,
		`{"balance":0}`,
		`{"balances":[
			{"balance":"1","wallet_address":{"is_scam":false},
			 "jetton":{"symbol":"GRAM Unlock at gramunlock.org","decimals":9,"verification":"blacklist"}},
			{"balance":"2","wallet_address":{"is_scam":true},
			 "jetton":{"symbol":"FAKE","decimals":9,"verification":"none"}},
			{"balance":"3","wallet_address":{"is_scam":false},
			 "jetton":{"symbol":"TONNEL 💎 Unlock at TONNEL.ME","decimals":9,"verification":"none"}}
		]}`)

	balances, err := syncer.SyncWallet(context.Background(), "EQCD39", []string{"ton"})
	require.NoError(t, err)

	require.Len(t, balances, 1, "blacklisted and scam-flagged jettons are dropped")
	assert.Contains(t, balances[0].Symbol, "TONNEL",
		"unflagged spam still gets through: this is the known gap personal-6yn closes")
}

// TestSyncWallet_JettonFailureKeepsNative: the native balance is the largest
// position on most accounts, so a jetton listing error must not discard it.
func TestSyncWallet_JettonFailureKeepsNative(t *testing.T) {
	syncer := newTestSyncer(t, `{"balance":5000000000}`, "")

	balances, err := syncer.SyncWallet(context.Background(), "EQCD39", []string{"ton"})
	require.Error(t, err)
	require.Len(t, balances, 1)
	assert.Equal(t, "TON", balances[0].Symbol)
	assert.Equal(t, "5000000000", balances[0].Amount)
}

// TestSyncWallet_EmptyAccount: an unused address yields no position rather
// than a zero one.
func TestSyncWallet_EmptyAccount(t *testing.T) {
	syncer := newTestSyncer(t, `{"balance":0,"status":"nonexist"}`, `{"balances":[]}`)

	balances, err := syncer.SyncWallet(context.Background(), "EQCD39", []string{"ton"})
	require.NoError(t, err)
	assert.Empty(t, balances)
}

// TestHandlesAddress routes auto-discovery. TON's user-facing form is
// base64url, which admits characters SS58 forbids — that is what keeps the
// two ecosystems from claiming each other's accounts.
func TestHandlesAddress(t *testing.T) {
	tests := []struct {
		address string
		want    bool
	}{
		{"EQCD39VS5jcptHL8vMjEXrzGaRcCVYto7HUn4bpAOg8xqB2N", true},                   // bounceable
		{"UQAoM6ZQhF_1uK7zhbzs9uAcYcnJT10QMKrsFI04l3jL0iQb", true},                   // non-bounceable, has _
		{"0:83dfd552e63729b472fcbcc8c45ebcc6691702558b68ec7527e1ba403a0f31a8", true}, // raw
		{"5DsvsaNbaA4JPXPRJHA2wWDMv4oJaWKQDbrUKEeELRfrto7Q", false},                  // SS58
		{"0x75304308839f839a553b60b5671bb2f043420167", false},                        // EVM
		{"", false},
	}
	for _, tt := range tests {
		assert.Equal(t, tt.want, HandlesAddress(tt.address), tt.address)
	}
}

func TestSyncWallet_RejectsForeignChain(t *testing.T) {
	syncer := newTestSyncer(t, `{"balance":1}`, `{"balances":[]}`)

	_, err := syncer.SyncWallet(context.Background(), "EQCD39", []string{"polkadot"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "polkadot")
}
