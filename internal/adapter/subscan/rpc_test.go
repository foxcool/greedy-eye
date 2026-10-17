package subscan

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/foxcool/greedy-eye/internal/entity"
)

// The dot-controller address and what the Hydration node answered for it on
// 2026-10-17, read from the dev instance's account rather than retyped. Two
// independent nodes (rpc.hydradx.cloud, dwellir) returned the same bytes.
const (
	controllerAddress = "5DsvsaNbaA4JPXPRJHA2wWDMv4oJaWKQDbrUKEeELRfrto7Q"
	controllerKey     = "0x26aa394eea5630e07c48ae0c9558cef7b99d880ec681799c0cf30e8886371da91bc44224a96360994bb83778329cb805504094ffb9a9a925e3fb732a675f2906e26c0bb22cc2b47b6e8b6906447d0c7d"
	controllerAccount = "0x34000000010000000300000001000000fc62541ad5919900000000000000000000000000000000000000000000000000103b30813c5697000000000000000000574dc6b69b8e08000000000000000080"
)

// rpcStub answers the two methods the RPC path calls. storage maps a storage
// key to its value; a key absent from it answers null, as a node does for an
// account it has never seen.
type rpcStub struct {
	storage    map[string]string
	properties string
	requests   []*http.Request
}

func newRPCStub(t *testing.T, storage map[string]string) *httptest.Server {
	t.Helper()
	return newRPCStubWith(t, &rpcStub{storage: storage})
}

func newRPCStubWith(t *testing.T, stub *rpcStub) *httptest.Server {
	t.Helper()
	if stub.properties == "" {
		stub.properties = `{"tokenDecimals":12,"tokenSymbol":"HDX"}`
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		stub.requests = append(stub.requests, r)
		var req struct {
			Method string   `json:"method"`
			Params []string `json:"params"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		result := "null"
		switch req.Method {
		case "system_properties":
			result = stub.properties
		case "state_getStorage":
			if v, ok := stub.storage[req.Params[0]]; ok {
				result = `"` + v + `"`
			}
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":` + result + `}`))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func newRPCSyncer(t *testing.T, stub *rpcStub) *WalletSyncerAdapter {
	t.Helper()
	client := NewClient(Config{APIKey: "test-key"})
	client.baseURLOverride = "http://subscan.invalid" // any Subscan call fails the test
	client.rpcURLOverride = newRPCStubWith(t, stub).URL
	return NewWalletSyncer(client)
}

// TestSS58PublicKeyBuildsTheCapturedKey: the storage key is derived from the
// address, and a wrong derivation reads somebody else's empty account as this
// one's — a zero, not an error. Pinned against the key the node answered for.
func TestSS58PublicKeyBuildsTheCapturedKey(t *testing.T) {
	pub, err := ss58PublicKey(controllerAddress)
	require.NoError(t, err)
	assert.Equal(t, "504094ffb9a9a925e3fb732a675f2906e26c0bb22cc2b47b6e8b6906447d0c7d", hex.EncodeToString(pub))
	assert.Equal(t, controllerKey, "0x"+systemAccountPrefix+blake2128Concat(pub))
}

// TestSyncWallet_HydrationFromChainState is the live case of personal-feb.16:
// 43226.015892529916 HDX, 42597.539348757264 of it frozen by one lock. Subscan
// last reported 34644.64 on 2026-08-27; the seven weeks since are what the
// system did not see.
func TestSyncWallet_HydrationFromChainState(t *testing.T) {
	syncer := newRPCSyncer(t, &rpcStub{storage: map[string]string{controllerKey: controllerAccount}})

	balances, err := syncer.SyncWallet(context.Background(), controllerAddress, []string{"hydration"})
	require.NoError(t, err)
	require.Len(t, balances, 2)

	assert.Equal(t, "43226015892529916", sumAmounts(t, balances), "free + reserved, reserved being zero")
	for _, b := range balances {
		assert.Equal(t, "HDX", b.Symbol)
		assert.Equal(t, 12, b.Decimals)
		assert.Equal(t, "hydration", b.Chain)
	}
	assert.Equal(t, entity.LiquidityLiquid, balances[0].Liquidity)
	assert.Equal(t, "628476543772652", balances[0].Amount, "free - frozen")
	assert.Equal(t, entity.LiquidityUnknown, balances[1].Liquidity,
		"chain state says how much is locked, not what for")
	assert.Equal(t, "42597539348757264", balances[1].Amount)
}

// TestSyncWallet_HydrationUnknownAccount: a node answers null for an address it
// has never seen. That is an empty wallet, not a failure and not a zero row.
func TestSyncWallet_HydrationUnknownAccount(t *testing.T) {
	balances, err := newRPCSyncer(t, &rpcStub{}).SyncWallet(context.Background(), controllerAddress, []string{"hydration"})
	require.NoError(t, err)
	assert.Empty(t, balances)
}

// TestSyncWallet_HydrationDecimalsComeFromTheChain: the table's 12 is a
// fallback the RPC path never uses. A chain stating another precision wins,
// in either of the two shapes system_properties comes in.
func TestSyncWallet_HydrationDecimalsComeFromTheChain(t *testing.T) {
	for name, props := range map[string]string{
		"scalar": `{"tokenDecimals":10,"tokenSymbol":"HDX"}`,
		"array":  `{"tokenDecimals":[10,6],"tokenSymbol":["HDX","USDT"]}`,
	} {
		t.Run(name, func(t *testing.T) {
			syncer := newRPCSyncer(t, &rpcStub{storage: map[string]string{controllerKey: controllerAccount}, properties: props})
			balances, err := syncer.SyncWallet(context.Background(), controllerAddress, []string{"hydration"})
			require.NoError(t, err)
			require.NotEmpty(t, balances)
			assert.Equal(t, 10, balances[0].Decimals)
			assert.Equal(t, "HDX", balances[0].Symbol)
		})
	}
}

// TestSyncWallet_HydrationRefusesWhatItCannotRead: no stated precision, or a
// value shorter than the layout, is a chain error. Either would otherwise be
// read as a number, and a wrong one.
func TestSyncWallet_HydrationRefusesWhatItCannotRead(t *testing.T) {
	for name, stub := range map[string]*rpcStub{
		"no decimals":   {storage: map[string]string{controllerKey: controllerAccount}, properties: `{"tokenSymbol":"HDX"}`},
		"short account": {storage: map[string]string{controllerKey: controllerAccount[:66]}},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := newRPCSyncer(t, stub).SyncWallet(context.Background(), controllerAddress, []string{"hydration"})
			require.Error(t, err)
			assert.Contains(t, entity.FailedChains(err), "hydration")
		})
	}
}

// TestRPCRequestsCarryNoSubscanKey: the node is a third party's. The Subscan
// key must not travel there, and the request must go through the RPC budget,
// not Subscan's.
func TestRPCRequestsCarryNoSubscanKey(t *testing.T) {
	stub := &rpcStub{storage: map[string]string{controllerKey: controllerAccount}}
	srv := newRPCStubWith(t, stub)

	subscanTr := &countingTransport{base: http.DefaultTransport}
	rpcTr := &countingTransport{base: http.DefaultTransport}
	client := NewClient(Config{APIKey: "test-key", Transport: subscanTr, RPCTransport: rpcTr})
	client.rpcURLOverride = srv.URL

	_, err := NewWalletSyncer(client).SyncWallet(context.Background(), controllerAddress, []string{"hydration"})
	require.NoError(t, err)

	assert.Zero(t, subscanTr.calls, "Subscan's budget pays nothing for a chain node")
	assert.Equal(t, 2, rpcTr.calls)
	for _, r := range stub.requests {
		assert.Empty(t, r.Header.Get("X-API-Key"))
		assert.NotEmpty(t, r.Header.Get("User-Agent"), "the public node answers 403 without one")
	}
}

// TestStateBalances_AReserveIsNotGuessedAt: with anything reserved, or a floor
// above free, the position stays whole and unpartitioned.
func TestStateBalances_AReserveIsNotGuessedAt(t *testing.T) {
	net := networks["hydration"]
	whole := func(s NativeState) []entity.WalletBalance { return stateBalances("hydration", net, s) }

	got := whole(NativeState{Free: d(t, "100"), Reserved: d(t, "5"), Frozen: d(t, "10"), Decimals: 12, Symbol: "HDX"})
	require.Len(t, got, 1)
	assert.Equal(t, entity.LiquidityUnknown, got[0].Liquidity)
	assert.Equal(t, "105", got[0].Amount)

	got = whole(NativeState{Free: d(t, "100"), Reserved: d(t, "0"), Frozen: d(t, "150"), Decimals: 12, Symbol: "HDX"})
	require.Len(t, got, 1)
	assert.Equal(t, "100", got[0].Amount)

	got = whole(NativeState{Free: d(t, "100"), Reserved: d(t, "0"), Frozen: d(t, "0"), Decimals: 12, Symbol: "HDX"})
	require.Len(t, got, 1)
	assert.Equal(t, entity.LiquidityLiquid, got[0].Liquidity, "nothing frozen is exactly liquid")
}

func d(t *testing.T, s string) decimal.Decimal {
	t.Helper()
	v, err := decimal.NewFromString(s)
	require.NoError(t, err)
	return v
}

// TestSyncWalletAnswered_AnEmptyChainAnswered: a chain that answered with
// nothing held is reported as answered — the only evidence that ends a run of
// failures on an address holding nothing there (personal-uvbn). A chain that
// failed is not.
func TestSyncWalletAnswered_AnEmptyChainAnswered(t *testing.T) {
	client := NewClient(Config{APIKey: "test-key"})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "too many", http.StatusTooManyRequests)
	}))
	t.Cleanup(srv.Close)
	client.baseURLOverride = srv.URL
	client.rpcURLOverride = newRPCStub(t, nil).URL

	balances, answered, err := NewWalletSyncer(client).SyncWalletAnswered(
		context.Background(), controllerAddress, []string{"hydration", "polkadot"})
	assert.Empty(t, balances)
	assert.Equal(t, []string{"hydration"}, answered, "empty hydration answered; polkadot refused")
	assert.Contains(t, entity.FailedChains(err), "polkadot")
}
