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

func TestWalletSyncer_TokenBalances_FiltersSpam(t *testing.T) {
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
	require.Len(t, balances, 1)
	assert.Equal(t, "GRT", balances[0].Symbol)
	assert.Equal(t, "1000", balances[0].Amount)
}
