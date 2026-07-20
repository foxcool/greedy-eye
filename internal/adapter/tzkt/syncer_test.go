package tzkt

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const testAddress = "tz1eZwq8b5cvE2bPKokatLkVMzkxz24z3Don"

// newTestSyncer points a syncer at a stub TzKT serving the given body.
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

// TestSyncWallet_StakedBalanceNotDoubleCounted is the correctness guard for
// this adapter. TzKT's balance already covers spendable plus frozen funds, so
// staking freezes tokens in place rather than moving them aside.
//
// The numbers are a live baker's: balance minus stakedBalance came out exactly
// equal to its ownDelegatedBalance, which is what proves the staked amount is
// already inside. Adding the staking fields would have nearly doubled it.
func TestSyncWallet_StakedBalanceNotDoubleCounted(t *testing.T) {
	syncer := newTestSyncer(t, respondJSON(`{
		"balance": 20694662386884,
		"stakedBalance": 20687806127523,
		"unstakedBalance": 0,
		"ownDelegatedBalance": 6856259361
	}`))

	balances, err := syncer.SyncWallet(context.Background(), testAddress, []string{Chain})
	require.NoError(t, err)
	require.Len(t, balances, 1)

	assert.Equal(t, "XTZ", balances[0].Symbol)
	assert.Equal(t, 6, balances[0].Decimals)
	assert.Equal(t, "20694662386884", balances[0].Amount)
}

// TestSyncWallet_UnstakingNotDoubleCounted: tokens mid-unstake are still frozen
// and still inside balance. On a live account nearly the whole balance was
// unstaked, so adding the field would have doubled the position.
func TestSyncWallet_UnstakingNotDoubleCounted(t *testing.T) {
	syncer := newTestSyncer(t, respondJSON(`{
		"balance": 36355630031,
		"stakedBalance": 0,
		"unstakedBalance": 36349561940
	}`))

	balances, err := syncer.SyncWallet(context.Background(), testAddress, nil)
	require.NoError(t, err)
	require.Len(t, balances, 1)
	assert.Equal(t, "36355630031", balances[0].Amount)
}

// TestSyncWallet_EmptyAndUnknown covers the two quiet cases: an address the
// chain never saw yields no position rather than a zero one, and a chain this
// adapter does not serve is reported instead of silently skipped.
func TestSyncWallet_EmptyAndUnknown(t *testing.T) {
	syncer := newTestSyncer(t, respondJSON(`{"type":"empty","balance":0}`))

	balances, err := syncer.SyncWallet(context.Background(), testAddress, nil)
	require.NoError(t, err)
	assert.Empty(t, balances, "an unused address must not create a zero holding")

	_, err = syncer.SyncWallet(context.Background(), testAddress, []string{"ethereum"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "ethereum")
}

// TestSyncWallet_MissingStakingFields: TzKT omits the staking fields entirely
// for accounts that never staked, which must read as a plain balance.
func TestSyncWallet_MissingStakingFields(t *testing.T) {
	syncer := newTestSyncer(t, respondJSON(`{"balance": 341345}`))

	balances, err := syncer.SyncWallet(context.Background(), testAddress, nil)
	require.NoError(t, err)
	require.Len(t, balances, 1)
	assert.Equal(t, "341345", balances[0].Amount)
}

func TestSyncWallet_HTTPError(t *testing.T) {
	syncer := newTestSyncer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	})

	_, err := syncer.SyncWallet(context.Background(), testAddress, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "503")
}

// TestHandlesAddress routes auto-discovery. The Tezos prefixes are distinctive,
// so the negatives here are about not claiming the other base58 ecosystems.
func TestHandlesAddress(t *testing.T) {
	tests := []struct {
		name    string
		address string
		want    bool
	}{
		{"tz1 ed25519", testAddress, true},
		{"tz2 secp256k1", "tz2HgpqTbfj2adE1J9BHxTai9Mxt24FSMmdb", true},
		{"tz3 p256", "tz3bTdwZinP8U1JmSweNzVKhmwafqWmFWRfk", true},
		{"KT1 contract", "KT1TxqZ8QtKvLu3V3JH7Gx58n7Co8pgtpQU5", true},
		{"solana", "CvC1oRhFemouyXTBSJp6NBdEUN1CqQEYRNFcUh46Cqv8", false},
		{"ss58", "5DsvsaNbaA4JPXPRJHA2wWDMv4oJaWKQDbrUKEeELRfrto7Q", false},
		{"bitcoin legacy", "1BvBMSEYstWetqTFn5Au4m4GFg7xJaNVN2", false},
		{"evm", "0x75304308839f839a553b60b5671bb2f043420167", false},
		{"empty", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, HandlesAddress(tt.address))
		})
	}
}

func TestSupportedChainsIsStable(t *testing.T) {
	assert.Equal(t, []string{"tezos"}, SupportedChains())
}
