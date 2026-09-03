package alchemy

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestHandlesAddress guards auto-discovery routing: accounts created before
// chain routing existed carry only an address, so this predicate is the only
// thing that keeps them reaching an EVM syncer.
func TestHandlesAddress(t *testing.T) {
	tests := []struct {
		address string
		want    bool
	}{
		{"0x75304308839f839a553b60b5671bb2f043420167", true},
		{"0x93123E0394Ca6323611C910957553876A9629571", true}, // mixed-case checksum form
		{"5DsvsaNbaA4JPXPRJHA2wWDMv4oJaWKQDbrUKEeELRfrto7Q", false},
		{"EQCD39VS5jcptHL8vMjEXrzGaRcCVYto7HUn4bpAOg8xqB2N", false},
		{"0x7530", false},
		{"", false},
	}
	for _, tt := range tests {
		assert.Equal(t, tt.want, HandlesAddress(tt.address), tt.address)
	}
}

// TestHexBalanceIsConverted pins the wire-format difference that makes this
// adapter's numbers different from the one it replaces: Moralis sent decimal
// strings, Alchemy sends hex quantities. Reading one as the other does not
// fail, it returns a different amount.
func TestHexBalanceIsConverted(t *testing.T) {
	tests := []struct {
		hex  string
		want string
		ok   bool
	}{
		{"0x02c68af0bb140000", "200000000000000000", true},
		{"0x0", "0", true},
		{"0x", "", false},
		{"", "", false},
		{"200000", "2097152", true}, // no prefix is still hex, not decimal
		{"0xnothex", "", false},
	}
	for _, tt := range tests {
		got, ok := hexToDecimal(tt.hex)
		assert.Equal(t, tt.ok, ok, tt.hex)
		assert.Equal(t, tt.want, got, tt.hex)
	}
}

// TestSyncWallet_NativeAndToken covers the ordinary answer: a native coin
// (tokenAddress null) and an ERC-20, on a slug that maps back to this build's
// own chain id. The chain matters as much as the amount — it is part of an
// asset's identity, so the same contract on two chains must not merge.
func TestSyncWallet_NativeAndToken(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, `{"data":{"tokens":[
			{"address":"0xabc","network":"base-mainnet","tokenAddress":null,"tokenBalance":"0x02c68af0bb140000","tokenMetadata":{"decimals":18,"name":"Ethereum","symbol":"ETH"},"error":null},
			{"address":"0xabc","network":"base-mainnet","tokenAddress":"0xc944e90c64b2c07662a292be6244bdf05cda44a7","tokenBalance":"0x3e8","tokenMetadata":{"decimals":6,"name":"USD Coin","symbol":"USDC"},"error":null}
		]}}`)
	}))
	defer srv.Close()

	balances, err := syncerAgainst(srv).SyncWallet(context.Background(), "0xabc", []string{"base"})
	require.NoError(t, err)
	require.Len(t, balances, 2)

	native := balances[0]
	assert.Equal(t, "ETH", native.Symbol)
	assert.Equal(t, "200000000000000000", native.Amount)
	assert.Equal(t, 18, native.Decimals)
	assert.Empty(t, native.ContractAddress, "a native coin has no contract")
	assert.Equal(t, "base", native.Chain, "the slug maps back to this build's chain id")

	token := balances[1]
	assert.Equal(t, "USDC", token.Symbol)
	assert.Equal(t, "1000", token.Amount)
	assert.Equal(t, 6, token.Decimals)
	assert.Equal(t, "0xc944e90c64b2c07662a292be6244bdf05cda44a7", token.ContractAddress)
	assert.Equal(t, "base", token.Chain)

	for _, b := range balances {
		assert.Nil(t, b.ProviderSpam, "Alchemy states nothing about spam; nil is that silence, false would be a claim")
		assert.Nil(t, b.ContractVerified)
	}
}

// TestSyncWallet_RefusesUnscalableAmounts is the important one. A token whose
// decimals are unknown cannot be scaled, and emitting it with a zero scale
// reads its raw amount as whole units — the shape of the 10^12 inflation that
// Asset Hub balances once produced. Refusing it loudly costs a disclosed hole;
// admitting it costs a wrong number.
func TestSyncWallet_RefusesUnscalableAmounts(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, `{"data":{"tokens":[
			{"address":"0xabc","network":"eth-mainnet","tokenAddress":"0xdead000000000000000000000000000000000000","tokenBalance":"0x2710","tokenMetadata":{"decimals":null,"name":null,"symbol":null},"error":null},
			{"address":"0xabc","network":"eth-mainnet","tokenAddress":"0xbeef000000000000000000000000000000000000","tokenBalance":"0x2710","tokenMetadata":null,"error":"metadata unavailable"},
			{"address":"0xabc","network":"eth-mainnet","tokenAddress":"0xfeed000000000000000000000000000000000000","tokenBalance":"not-a-quantity","tokenMetadata":{"decimals":18,"name":"Broken","symbol":"BRK"},"error":null},
			{"address":"0xabc","network":"eth-mainnet","tokenAddress":"0xc0de000000000000000000000000000000000000","tokenBalance":"0x0","tokenMetadata":{"decimals":18,"name":"Zero","symbol":"ZERO"},"error":null},
			{"address":"0xabc","network":"eth-mainnet","tokenAddress":"0x900d000000000000000000000000000000000000","tokenBalance":"0x64","tokenMetadata":{"decimals":8,"name":"Good","symbol":"GOOD"},"error":null}
		]}}`)
	}))
	defer srv.Close()

	balances, err := syncerAgainst(srv).SyncWallet(context.Background(), "0xabc", []string{"eth"})

	require.Len(t, balances, 1, "only the row that can be scaled is emitted")
	assert.Equal(t, "GOOD", balances[0].Symbol)
	assert.Equal(t, "100", balances[0].Amount)

	require.Error(t, err, "every refusal is named; a hole must be disclosed, not inferred")
	msg := err.Error()
	assert.Contains(t, msg, "0xdead000000000000000000000000000000000000")
	assert.Contains(t, msg, "no decimals reported")
	assert.Contains(t, msg, "0xbeef000000000000000000000000000000000000")
	assert.Contains(t, msg, "metadata unavailable")
	assert.Contains(t, msg, "0xfeed000000000000000000000000000000000000")
	assert.Contains(t, msg, "not a hex quantity")
	assert.NotContains(t, msg, "0xc0de000000000000000000000000000000000000",
		"a zero balance is a fact the source stated, not a failure to report")
}

// TestSyncWallet_NetworkFailureDoesNotDiscardTheRest: a chain that fails is a
// hole to disclose, and the chains that answered are still true. Returning
// nothing because one of five networks broke is lying in the minus.
func TestSyncWallet_NetworkFailureDoesNotDiscardTheRest(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, `{"data":{"tokens":[
			{"address":"0xabc","network":"eth-mainnet","tokenAddress":null,"tokenBalance":"0x64","tokenMetadata":{"decimals":18,"name":"Ethereum","symbol":"ETH"},"error":null}
		]},"error":{"message":"Failed to fetch tokens on certain networks","partialErrors":[{"network":"base-mainnet","message":"Internal server error"}]}}`)
	}))
	defer srv.Close()

	balances, err := syncerAgainst(srv).SyncWallet(context.Background(), "0xabc", []string{"eth", "base"})

	require.Len(t, balances, 1, "what answered is returned")
	assert.Equal(t, "ETH", balances[0].Symbol)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "base-mainnet")
	assert.Contains(t, err.Error(), "Internal server error")
}

// TestSyncWallet_ChunksNetworks pins the API's cap of five networks per call:
// the ten chains this adapter supports must arrive as two requests, with every
// chain asked for exactly once. Exceeding the cap is rejected outright, so a
// regression here is a wallet that syncs nothing.
func TestSyncWallet_ChunksNetworks(t *testing.T) {
	var asked [][]string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var req tokensRequest
		require.NoError(t, json.Unmarshal(body, &req))
		require.Len(t, req.Addresses, 1)
		require.LessOrEqual(t, len(req.Addresses[0].Networks), maxNetworksPerCall)
		asked = append(asked, req.Addresses[0].Networks)
		writeJSON(w, `{"data":{"tokens":[]}}`)
	}))
	defer srv.Close()

	_, err := syncerAgainst(srv).SyncWallet(context.Background(), "0xabc", nil)
	require.NoError(t, err)

	var flat []string
	for _, batch := range asked {
		flat = append(flat, batch...)
	}
	assert.Len(t, asked, 2, "ten supported chains, five per call")
	assert.Len(t, flat, len(SupportedChains()), "every supported chain is asked for exactly once")
	for _, chain := range SupportedChains() {
		assert.Contains(t, flat, network[chain].slug, chain)
	}
}

// TestSyncWallet_UnsupportedChainIsNamed: an account configured for a chain
// this adapter cannot read must produce an error somebody can see, not a
// shorter answer. Fantom is the live case — Moralis read it, Alchemy does not.
func TestSyncWallet_UnsupportedChainIsNamed(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, `{"data":{"tokens":[
			{"address":"0xabc","network":"eth-mainnet","tokenAddress":null,"tokenBalance":"0x64","tokenMetadata":{"decimals":18,"name":"Ethereum","symbol":"ETH"},"error":null}
		]}}`)
	}))
	defer srv.Close()

	balances, err := syncerAgainst(srv).SyncWallet(context.Background(), "0xabc", []string{"eth", "fantom"})

	require.Len(t, balances, 1)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "fantom")
	assert.NotContains(t, SupportedChains(), "fantom")
}

// TestSyncWallet_PolygonAnswersUnderAnotherSlug is a fact measured against the
// live API on 2026-09-02 and written down nowhere else: polygon is ASKED for as
// "polygon-mainnet", which the API accepts, and comes back as "matic-mainnet".
// Without the alias every polygon balance is refused; with a "trim -mainnet"
// shortcut instead, they would have landed on a chain called "matic" — a second
// identity for every token already held there.
func TestSyncWallet_PolygonAnswersUnderAnotherSlug(t *testing.T) {
	var asked []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var req tokensRequest
		require.NoError(t, json.Unmarshal(body, &req))
		asked = append(asked, req.Addresses[0].Networks...)
		writeJSON(w, `{"data":{"tokens":[
			{"address":"0xabc","network":"matic-mainnet","tokenAddress":null,"tokenBalance":"0x64","tokenMetadata":{"decimals":18,"name":"Polygon","symbol":"POL"},"error":null}
		]}}`)
	}))
	defer srv.Close()

	balances, err := syncerAgainst(srv).SyncWallet(context.Background(), "0xabc", []string{"polygon"})
	require.NoError(t, err)
	assert.Equal(t, []string{"polygon-mainnet"}, asked, "the request keeps the documented slug")
	require.Len(t, balances, 1)
	assert.Equal(t, "polygon", balances[0].Chain, "the answer maps back to the chain this build already uses")
	assert.Equal(t, "POL", balances[0].Symbol)
}

// TestSyncWallet_UnknownNetworkIsCountedOnce: an unknown network is a fact
// about the network, not about each row on it. A wallet with ninety balances
// there produced ninety identical error lines, which is a report nobody reads.
func TestSyncWallet_UnknownNetworkIsCountedOnce(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, `{"data":{"tokens":[
			{"address":"0xabc","network":"shape-mainnet","tokenAddress":null,"tokenBalance":"0x64","tokenMetadata":{"decimals":18,"name":"Ethereum","symbol":"ETH"},"error":null},
			{"address":"0xabc","network":"shape-mainnet","tokenAddress":"0x1111000000000000000000000000000000000000","tokenBalance":"0x64","tokenMetadata":{"decimals":18,"name":"One","symbol":"ONE"},"error":null},
			{"address":"0xabc","network":"shape-mainnet","tokenAddress":"0x2222000000000000000000000000000000000000","tokenBalance":"0x64","tokenMetadata":{"decimals":18,"name":"Two","symbol":"TWO"},"error":null}
		]}}`)
	}))
	defer srv.Close()

	_, err := syncerAgainst(srv).SyncWallet(context.Background(), "0xabc", []string{"eth"})
	require.Error(t, err)
	assert.Equal(t, 1, strings.Count(err.Error(), "shape-mainnet"), "named once, not once per row")
	assert.Contains(t, err.Error(), "3 balance(s)")
}

// TestSyncWallet_UnknownNetworkInResponse: a slug this build does not know must
// not be turned into a chain name by trimming "-mainnet". Chain is identity, and
// a guessed one merges or splits assets silently.
func TestSyncWallet_UnknownNetworkInResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, `{"data":{"tokens":[
			{"address":"0xabc","network":"shape-mainnet","tokenAddress":null,"tokenBalance":"0x64","tokenMetadata":{"decimals":18,"name":"Ethereum","symbol":"ETH"},"error":null}
		]}}`)
	}))
	defer srv.Close()

	balances, err := syncerAgainst(srv).SyncWallet(context.Background(), "0xabc", []string{"eth"})

	assert.Empty(t, balances)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "shape-mainnet")
}

// TestSyncWallet_FollowsPagination: a page key that is ignored truncates a
// wallet's tail while the answer still looks complete — the same failure as a
// valuation reading only its first page of holdings.
func TestSyncWallet_FollowsPagination(t *testing.T) {
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var req tokensRequest
		require.NoError(t, json.Unmarshal(body, &req))
		calls++
		if req.PageKey == "" {
			writeJSON(w, `{"data":{"tokens":[
				{"address":"0xabc","network":"eth-mainnet","tokenAddress":"0x1111000000000000000000000000000000000000","tokenBalance":"0x1","tokenMetadata":{"decimals":18,"name":"One","symbol":"ONE"},"error":null}
			],"pageKey":"page-2"}}`)
			return
		}
		assert.Equal(t, "page-2", req.PageKey)
		writeJSON(w, `{"data":{"tokens":[
			{"address":"0xabc","network":"eth-mainnet","tokenAddress":"0x2222000000000000000000000000000000000000","tokenBalance":"0x2","tokenMetadata":{"decimals":18,"name":"Two","symbol":"TWO"},"error":null}
		]}}`)
	}))
	defer srv.Close()

	balances, err := syncerAgainst(srv).SyncWallet(context.Background(), "0xabc", []string{"eth"})
	require.NoError(t, err)
	assert.Equal(t, 2, calls)
	require.Len(t, balances, 2, "the tail is read, not assumed absent")
	assert.Equal(t, "ONE", balances[0].Symbol)
	assert.Equal(t, "TWO", balances[1].Symbol)
}

// TestClient_RejectsTooManyNetworks: the cap is the API's, and asking past it
// fails the whole call. Chunking is what keeps that from happening, so the
// client states the rule rather than trusting its caller.
func TestClient_RejectsTooManyNetworks(t *testing.T) {
	client := NewClient(Config{APIKey: "k"})
	_, _, err := client.TokensByAddress(context.Background(), "0xabc",
		[]string{"a", "b", "c", "d", "e", "f"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "the API accepts 5")
}

// TestClient_KeyStaysOutOfErrors: the key is a path segment for this API, so an
// error naming the URL would put a live credential into a sync error, a log
// line and the response a user reads.
func TestClient_KeyStaysOutOfErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	client := NewClient(Config{APIKey: "secret-key-value"})
	client.baseURL = srv.URL
	_, _, err := client.TokensByAddress(context.Background(), "0xabc", []string{"eth-mainnet"})

	require.Error(t, err)
	assert.NotContains(t, err.Error(), "secret-key-value")
	assert.Contains(t, err.Error(), "401")
}

// TestSyncWallet_HttpFailureIsNotSilent: a transport failure must not read as
// "this wallet holds nothing".
func TestSyncWallet_HttpFailureIsNotSilent(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	balances, err := syncerAgainst(srv).SyncWallet(context.Background(), "0xabc", []string{"eth"})
	assert.Empty(t, balances)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "500")
}

func syncerAgainst(srv *httptest.Server) *WalletSyncerAdapter {
	client := NewClient(Config{APIKey: "test-key"})
	client.baseURL = srv.URL
	return NewWalletSyncer(client)
}

func writeJSON(w http.ResponseWriter, body string) {
	w.Header().Set("Content-Type", "application/json")
	_, _ = io.WriteString(w, strings.TrimSpace(body))
}
