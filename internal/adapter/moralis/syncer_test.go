package moralis

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestHandlesAddress guards auto-discovery routing: accounts created before
// chain routing existed carry only an address, so this predicate is the only
// thing that keeps them reaching the EVM syncer.
func TestHandlesAddress(t *testing.T) {
	tests := []struct {
		address string
		want    bool
	}{
		{"0x75304308839f839a553b60b5671bb2f043420167", true},
		{"0x93123E0394Ca6323611C910957553876A9629571", true}, // mixed-case checksum form
		{"5DsvsaNbaA4JPXPRJHA2wWDMv4oJaWKQDbrUKEeELRfrto7Q", false},
		{"EQCD39VS5jcptHL8vMjEXrzGaRcCVYto7HUn4bpAOg8xqB2N", false},
		{"0x7530", false}, // too short
		{"", false},
	}
	for _, tt := range tests {
		assert.Equal(t, tt.want, HandlesAddress(tt.address), tt.address)
	}
}

// TestWalletSyncer_TokenBalances_CarriesSpamSignals verifies the adapter no
// longer drops possible-spam/unverified tokens at the source: it returns every
// balance and carries the provider's spam and verification bits as scoring
// signals, so a scam clone syncs as its own quarantined asset downstream instead
// of vanishing (scam-filtering). The chain is stamped on every balance.
func TestWalletSyncer_TokenBalances_CarriesSpamSignals(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[
			{"token_address":"0xc944e90c64b2c07662a292be6244bdf05cda44a7","symbol":"GRT","name":"Graph Token","decimals":18,"balance":"1000","possible_spam":false,"verified_contract":true},
			{"token_address":"0xdeadbeef00000000000000000000000000000000","symbol":"SCAM","name":"Spam airdrop","decimals":6,"balance":"1","possible_spam":true,"verified_contract":false},
			{"token_address":"0x7f1ffe630000000000000000000000000000000","symbol":"USDT","name":"Fake USDT (unverified, not flagged as spam)","decimals":6,"balance":"4129545600000","possible_spam":false,"verified_contract":false}
		]`))
	}))
	defer srv.Close()

	client := NewClient(Config{APIKey: "test-api-key"})
	client.baseURL = srv.URL
	syncer := NewWalletSyncer(client)

	balances, err := syncer.tokenBalances(context.Background(), "eth", "0xabc")
	require.NoError(t, err)
	require.Len(t, balances, 3, "no balance is dropped at the source")

	bySymbol := map[string]int{}
	for i, b := range balances {
		bySymbol[b.Symbol] = i
		assert.Equal(t, "eth", b.Chain, b.Symbol)
		require.NotNil(t, b.ProviderSpam, b.Symbol)
		require.NotNil(t, b.ContractVerified, b.Symbol)
	}

	grt := balances[bySymbol["GRT"]]
	assert.False(t, *grt.ProviderSpam)
	assert.True(t, *grt.ContractVerified)

	scam := balances[bySymbol["SCAM"]]
	assert.True(t, *scam.ProviderSpam)
	assert.False(t, *scam.ContractVerified)

	fakeUSDT := balances[bySymbol["USDT"]]
	assert.False(t, *fakeUSDT.ProviderSpam)
	assert.False(t, *fakeUSDT.ContractVerified)
}
