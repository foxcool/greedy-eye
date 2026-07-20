package solana

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

const testAddress = "CvC1oRhFemouyXTBSJp6NBdEUN1CqQEYRNFcUh46Cqv8"

// newTestSyncer points a syncer at a stub RPC dispatching on the JSON-RPC
// method, which is how one endpoint serves every call this adapter makes.
//
// Token accounts are served for the original token program only: a mint belongs
// to exactly one program on chain, so answering both with the same list would
// double every position and make the stub disagree with reality.
func newTestSyncer(t *testing.T, byMethod map[string]string) *WalletSyncerAdapter {
	t.Helper()
	return newTestSyncerFunc(t, func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var req struct {
			Method string `json:"method"`
			Params []any  `json:"params"`
		}
		_ = json.Unmarshal(body, &req)

		resp, ok := byMethod[req.Method]
		if !ok {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}

		if req.Method == "getTokenAccountsByOwner" {
			if filter, _ := req.Params[1].(map[string]any); filter["programId"] != tokenProgram {
				resp = emptyTokens
			}
		}

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(resp))
	})
}

func newTestSyncerFunc(t *testing.T, handler http.HandlerFunc) *WalletSyncerAdapter {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	client := NewClient(Config{APIKey: "test-key"})
	client.baseURL = srv.URL
	return NewWalletSyncer(client)
}

// emptyTokens is the response of a wallet holding no SPL positions, needed by
// every test that only cares about the native balance (both token programs are
// queried, and the stub answers both from the same body).
const emptyTokens = `{"jsonrpc":"2.0","id":"greedy-eye","result":{"value":[]}}`

// TestSyncWallet_NativeAndTokens pins the scaling on both sides: lamports pass
// through untouched at 9 decimals, and each SPL position keeps the decimals its
// own mint reports rather than a shared default.
func TestSyncWallet_NativeAndTokens(t *testing.T) {
	syncer := newTestSyncer(t, map[string]string{
		"getBalance": `{"result":{"value":2500000000}}`,
		"getTokenAccountsByOwner": `{"result":{"value":[
			{"account":{"data":{"parsed":{"info":{
				"mint":"EPjFWdd5AufqSSqeM2qN1xzybapC8G4wEGGkZwyTDt1v",
				"tokenAmount":{"amount":"1500000","decimals":6}}}}}}
		]}}`,
		"getAssetBatch": `{"result":[
			{"id":"EPjFWdd5AufqSSqeM2qN1xzybapC8G4wEGGkZwyTDt1v",
			 "burnt":false,
			 "content":{"metadata":{"symbol":"USDC","name":"USD Coin"}},
			 "token_info":{"symbol":"USDC"}}
		]}`,
	})

	balances, err := syncer.SyncWallet(context.Background(), testAddress, nil)
	require.NoError(t, err)
	require.Len(t, balances, 2)

	assert.Equal(t, "SOL", balances[0].Symbol)
	assert.Equal(t, "2500000000", balances[0].Amount, "lamports are already raw")
	assert.Equal(t, 9, balances[0].Decimals)
	assert.Empty(t, balances[0].ContractAddress, "native coins carry no contract")

	assert.Equal(t, "USDC", balances[1].Symbol)
	assert.Equal(t, "1500000", balances[1].Amount)
	assert.Equal(t, 6, balances[1].Decimals, "decimals come from the mint, not a default")
	assert.Equal(t, "EPjFWdd5AufqSSqeM2qN1xzybapC8G4wEGGkZwyTDt1v", balances[1].ContractAddress)
}

// TestSyncWallet_BothTokenProgramsQueried: Token-2022 is a separate program
// with its own accounts, so querying only the original one would silently miss
// every position minted under it.
func TestSyncWallet_BothTokenProgramsQueried(t *testing.T) {
	var programs []string
	syncer := newTestSyncerFunc(t, func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var req struct {
			Method string `json:"method"`
			Params []any  `json:"params"`
		}
		_ = json.Unmarshal(body, &req)

		w.Header().Set("Content-Type", "application/json")
		if req.Method == "getTokenAccountsByOwner" {
			filter, _ := req.Params[1].(map[string]any)
			programs = append(programs, filter["programId"].(string))
			_, _ = w.Write([]byte(emptyTokens))
			return
		}
		_, _ = w.Write([]byte(`{"result":{"value":0}}`))
	})

	_, err := syncer.SyncWallet(context.Background(), testAddress, nil)
	require.NoError(t, err)
	assert.Equal(t, []string{tokenProgram, token2022Program}, programs)
}

// TestSyncWallet_DropsJunk covers the floor filter. Each case is a position
// that exists on chain but is not a priceable fungible balance.
func TestSyncWallet_DropsJunk(t *testing.T) {
	syncer := newTestSyncer(t, map[string]string{
		"getBalance": `{"result":{"value":0}}`,
		"getTokenAccountsByOwner": `{"result":{"value":[
			{"account":{"data":{"parsed":{"info":{"mint":"zero",
				"tokenAmount":{"amount":"0","decimals":6}}}}}},
			{"account":{"data":{"parsed":{"info":{"mint":"burnt",
				"tokenAmount":{"amount":"100","decimals":6}}}}}},
			{"account":{"data":{"parsed":{"info":{"mint":"nosymbol",
				"tokenAmount":{"amount":"100","decimals":6}}}}}},
			{"account":{"data":{"parsed":{"info":{"mint":"nft",
				"tokenAmount":{"amount":"1","decimals":0}}}}}},
			{"account":{"data":{"parsed":{"info":{"mint":"unknown",
				"tokenAmount":{"amount":"100","decimals":6}}}}}},
			{"account":{"data":{"parsed":{"info":{"mint":"good",
				"tokenAmount":{"amount":"100","decimals":6}}}}}}
		]}}`,
		"getAssetBatch": `{"result":[
			{"id":"burnt","burnt":true,"token_info":{"symbol":"DEAD"}},
			{"id":"nosymbol","content":{"metadata":{"name":"No Ticker"}}},
			{"id":"nft","content":{"metadata":{"symbol":"COOLNFT"}}},
			{"id":"good","token_info":{"symbol":"BONK"},
			 "content":{"metadata":{"name":"Bonk"}}}
		]}`,
	})

	balances, err := syncer.SyncWallet(context.Background(), testAddress, nil)
	require.NoError(t, err)

	require.Len(t, balances, 1, "only the priceable fungible position survives")
	assert.Equal(t, "BONK", balances[0].Symbol)
}

// TestSyncWallet_PartialFailureKeepsNative covers the WalletSyncer contract:
// SOL is usually the largest position on the account, so a token-side failure
// must not discard it.
func TestSyncWallet_PartialFailureKeepsNative(t *testing.T) {
	t.Run("token listing fails", func(t *testing.T) {
		syncer := newTestSyncer(t, map[string]string{
			"getBalance": `{"result":{"value":7000000000}}`,
			// getTokenAccountsByOwner absent → stub answers 500.
		})

		balances, err := syncer.SyncWallet(context.Background(), testAddress, nil)
		require.Error(t, err)
		require.Len(t, balances, 1)
		assert.Equal(t, "SOL", balances[0].Symbol)
	})

	t.Run("metadata fails", func(t *testing.T) {
		syncer := newTestSyncer(t, map[string]string{
			"getBalance": `{"result":{"value":7000000000}}`,
			"getTokenAccountsByOwner": `{"result":{"value":[
				{"account":{"data":{"parsed":{"info":{"mint":"m",
					"tokenAmount":{"amount":"5","decimals":6}}}}}}
			]}}`,
		})

		balances, err := syncer.SyncWallet(context.Background(), testAddress, nil)
		require.Error(t, err)
		require.Len(t, balances, 1)
		assert.Equal(t, "SOL", balances[0].Symbol)
	})
}

// TestSyncWallet_RPCErrorEnvelope: JSON-RPC reports failures with HTTP 200 and
// an error member, so the body must be inspected rather than the status.
func TestSyncWallet_RPCErrorEnvelope(t *testing.T) {
	syncer := newTestSyncer(t, map[string]string{
		"getBalance": `{"jsonrpc":"2.0","error":{"code":-32602,"message":"Invalid param"},"id":"x"}`,
	})

	_, err := syncer.SyncWallet(context.Background(), testAddress, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "Invalid param")
}

// TestSyncWallet_EmptyAndUnknown covers the two quiet cases: an unused address
// yields no position rather than a zero one, and a chain this adapter does not
// serve is reported instead of silently skipped.
func TestSyncWallet_EmptyAndUnknown(t *testing.T) {
	syncer := newTestSyncer(t, map[string]string{
		"getBalance":              `{"result":{"value":0}}`,
		"getTokenAccountsByOwner": emptyTokens,
	})

	balances, err := syncer.SyncWallet(context.Background(), testAddress, nil)
	require.NoError(t, err)
	assert.Empty(t, balances, "an unused address must not create a zero holding")

	_, err = syncer.SyncWallet(context.Background(), testAddress, []string{"ethereum"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "ethereum")
}

// TestHandlesAddress routes auto-discovery. The negatives matter more than the
// positives here: SS58 shares this alphabet and sits two characters away, and a
// wrong answer is silent — the foreign chain reports an unknown account and the
// position drops to zero.
func TestHandlesAddress(t *testing.T) {
	tests := []struct {
		name    string
		address string
		want    bool
	}{
		{"solana pubkey", testAddress, true},
		{"solana wrapped sol mint", "So11111111111111111111111111111111111111112", true},
		{"ss58 generic", "5DsvsaNbaA4JPXPRJHA2wWDMv4oJaWKQDbrUKEeELRfrto7Q", false},
		{"ss58 polkadot", "12pE1udfRwKmq4PwFvD35f3WmgnxGosYJ6axUXdatWhP5TUm", false},
		{"evm", "0x75304308839f839a553b60b5671bb2f043420167", false},
		{"ton", "EQCD39VS5jcptHL8vMjEXrzGaRcCVYto7HUn4bpAOg8xqB2N", false},
		{"bitcoin legacy", "1BvBMSEYstWetqTFn5Au4m4GFg7xJaNVN2", false},
		{"empty", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, HandlesAddress(tt.address))
		})
	}
}

func TestSupportedChainsIsStable(t *testing.T) {
	assert.Equal(t, []string{"solana"}, SupportedChains())
}
