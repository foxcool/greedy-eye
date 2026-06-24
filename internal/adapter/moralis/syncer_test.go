package moralis

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

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
