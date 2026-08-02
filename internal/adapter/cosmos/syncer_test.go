package cosmos

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/foxcool/greedy-eye/internal/entity"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The verified pair: one key, two chains. Same payload, different prefix and
// checksum — which is exactly what re-encoding has to reproduce.
const (
	cosmosAddress = "cosmos14zta4gplkeym0dmlgnuxa2fmh7ymxtnlepxgxz"
	akashAddress  = "akash14zta4gplkeym0dmlgnuxa2fmh7ymxtnl56t0lc"
)

// newTestSyncer points a syncer at a stub LCD dispatching on the endpoint, which
// is how one server stands in for the four calls each chain takes.
func newTestSyncer(t *testing.T, byEndpoint map[string]string) *WalletSyncerAdapter {
	t.Helper()
	return newTestSyncerFunc(t, func(w http.ResponseWriter, r *http.Request) {
		for suffix, body := range byEndpoint {
			if strings.Contains(r.URL.Path, suffix) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(body))
				return
			}
		}
		w.WriteHeader(http.StatusNotFound)
	})
}

func newTestSyncerFunc(t *testing.T, handler http.HandlerFunc) *WalletSyncerAdapter {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	return NewWalletSyncer(NewClient(Config{BaseURL: srv.URL}))
}

// allFourPools is an account holding value in every pool at once.
var allFourPools = map[string]string{
	"/bank/v1beta1/balances/": `{"balances":[
		{"denom":"uatom","amount":"1000000"},
		{"denom":"ibc/27394FB092D2ECCD56123C74F36E4C1F926001CEADA9CA97EA622B25F41E5EB2","amount":"999999999"}
	]}`,
	"/staking/v1beta1/delegations/": `{"delegation_responses":[
		{"balance":{"denom":"uatom","amount":"5000000"}},
		{"balance":{"denom":"uatom","amount":"2000000"}}
	]}`,
	"/unbonding_delegations": `{"unbonding_responses":[
		{"entries":[{"balance":"300000"},{"balance":"200000"}]}
	]}`,
	"/distribution/v1beta1/delegators/": `{"total":[
		{"denom":"uatom","amount":"123456.789012345678900000"}
	]}`,
}

// TestSyncWallet_AllFourPoolsReported is the correctness guard for this
// adapter, and it runs opposite to the Substrate one. There, bonded tokens sit
// inside the free balance and adding them double-counts. Here delegated,
// unbonding and reward tokens have all left the bank balance, so omitting any
// of them reports a fraction of what the account holds.
//
// The pools are now reported separately rather than as one number: staked ATOM
// cannot be spent this month and bank ATOM can, which is the whole point of the
// liquidity axis. Nothing is lost and nothing is counted twice — the rows still
// add up to the same total.
func TestSyncWallet_AllFourPoolsReported(t *testing.T) {
	syncer := newTestSyncer(t, allFourPools)

	balances, err := syncer.SyncWallet(context.Background(), cosmosAddress, []string{"cosmos"})
	require.NoError(t, err)
	require.Len(t, balances, 3)

	byLiquidity := map[entity.Liquidity]entity.WalletBalance{}
	sum := decimal.Zero
	for _, b := range balances {
		assert.Equal(t, "ATOM", b.Symbol)
		assert.Equal(t, 6, b.Decimals)
		byLiquidity[b.Liquidity] = b
		sum = sum.Add(decimal.RequireFromString(b.Amount))
	}

	// 1000000 bank + 123456 rewards. The reward fraction is truncated: uatom is
	// the finest unit that settles. Rewards ride with the liquid pool — one
	// claim transaction away from spendable.
	assert.Equal(t, "1123456", byLiquidity[entity.LiquidityLiquid].Amount)
	assert.Equal(t, "7000000", byLiquidity[entity.LiquidityStaked].Amount)
	assert.Equal(t, "500000", byLiquidity[entity.LiquidityUnbonding].Amount)
	assert.Equal(t, "8623456", sum.String(), "the split must preserve the total")
}

// TestSyncWallet_EmptyPoolsAreNotRows: an account that only holds a bank
// balance reports one position, not three zero ones.
func TestSyncWallet_EmptyPoolsAreNotRows(t *testing.T) {
	syncer := newTestSyncer(t, map[string]string{
		"/bank/v1beta1/balances/": `{"balances":[{"denom":"uatom","amount":"4000000"}]}`,
	})

	balances, err := syncer.SyncWallet(context.Background(), cosmosAddress, []string{"cosmos"})
	require.NoError(t, err)
	require.Len(t, balances, 1)
	assert.Equal(t, entity.LiquidityLiquid, balances[0].Liquidity)
	assert.Equal(t, "4000000", balances[0].Amount)
}

// TestSyncWallet_IgnoresForeignDenoms: a bank balance also carries IBC vouchers
// of other chains' tokens. Counting them as the native denom would inflate the
// position with unrelated assets.
func TestSyncWallet_IgnoresForeignDenoms(t *testing.T) {
	syncer := newTestSyncer(t, map[string]string{
		"/bank/v1beta1/balances/": `{"balances":[
			{"denom":"ibc/27394FB092D2ECCD56123C74F36E4C1F926001CEADA9CA97EA622B25F41E5EB2","amount":"5000000"},
			{"denom":"uosmo","amount":"7000000"}
		]}`,
	})

	balances, err := syncer.SyncWallet(context.Background(), cosmosAddress, []string{"cosmos"})
	require.NoError(t, err)
	assert.Empty(t, balances, "only the chain's own denom counts")
}

// TestSyncWallet_ReencodesPerChain pins the mechanic that makes a single
// account cover the ecosystem: each zone's LCD accepts only its own prefix, so
// the address must be rewritten before the request rather than sent verbatim.
func TestSyncWallet_ReencodesPerChain(t *testing.T) {
	var asked []string
	syncer := newTestSyncerFunc(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/bank/v1beta1/balances/") {
			parts := strings.Split(r.URL.Path, "/")
			asked = append(asked, parts[len(parts)-1])
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{}`))
	})

	_, err := syncer.SyncWallet(context.Background(), cosmosAddress, []string{"cosmos", "akash"})
	require.NoError(t, err)
	assert.Equal(t, []string{cosmosAddress, akashAddress}, asked)
}

// TestSyncWallet_AutoDiscoverySweepsZones: with no chains named the adapter
// probes every zone it knows and keeps the ones holding a balance.
func TestSyncWallet_AutoDiscoverySweepsZones(t *testing.T) {
	var chainsProbed []string
	syncer := newTestSyncerFunc(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/bank/v1beta1/balances/") {
			chainsProbed = append(chainsProbed, strings.Split(r.URL.Path, "/")[1])
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{}`))
	})

	_, err := syncer.SyncWallet(context.Background(), cosmosAddress, nil)
	require.NoError(t, err)
	assert.Len(t, chainsProbed, len(SupportedChains()), "every known zone must be probed")
}

// TestSyncWallet_PartialFailureKeepsBalances covers the WalletSyncer contract:
// a failing zone surfaces as an error without discarding the zones that
// answered.
func TestSyncWallet_PartialFailureKeepsBalances(t *testing.T) {
	syncer := newTestSyncerFunc(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/akash") {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if strings.Contains(r.URL.Path, "/bank/v1beta1/balances/") {
			_, _ = w.Write([]byte(`{"balances":[{"denom":"uatom","amount":"4000000"}]}`))
			return
		}
		_, _ = w.Write([]byte(`{}`))
	})

	balances, err := syncer.SyncWallet(context.Background(), cosmosAddress, []string{"cosmos", "akash"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "akash")
	require.Len(t, balances, 1, "the reachable zone still reports")
	assert.Equal(t, "ATOM", balances[0].Symbol)
}

// TestSyncWallet_EmptyAndUnknown covers the two quiet cases: an unused address
// yields no position rather than a zero one — including when the LCD answers
// 404 instead of an empty list — and an unserved chain is reported.
func TestSyncWallet_EmptyAndUnknown(t *testing.T) {
	syncer := newTestSyncerFunc(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})

	balances, err := syncer.SyncWallet(context.Background(), cosmosAddress, []string{"cosmos"})
	require.NoError(t, err)
	assert.Empty(t, balances, "an unused address must not create a zero holding")

	_, err = syncer.SyncWallet(context.Background(), cosmosAddress, []string{"ethereum"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "ethereum")
}

// TestReencode pins the verified pair. The payload is shared; only the prefix
// and the checksum change, and getting the checksum wrong yields an address
// that looks right and belongs to nobody.
func TestReencode(t *testing.T) {
	got, err := reencode(cosmosAddress, "akash")
	require.NoError(t, err)
	assert.Equal(t, akashAddress, got)

	back, err := reencode(akashAddress, "cosmos")
	require.NoError(t, err)
	assert.Equal(t, cosmosAddress, back)
}

func TestDecodeBech32RejectsCorruption(t *testing.T) {
	// One character changed: the checksum no longer holds.
	_, _, err := decodeBech32("cosmos14zta4gplkeym0dmlgnuxa2fmh7ymxtnlepxgxy")
	assert.ErrorIs(t, err, errBech32Checksum)

	for _, bad := range []string{"", "cosmos", "nosep", "cosmos1b", "COSMOS14zta4gplkeym0dmlgnuxa2fmh7ymxtnlepxgxz"} {
		_, _, err := decodeBech32(bad)
		assert.Error(t, err, bad)
	}
}

// TestHandlesAddress routes auto-discovery. Bitcoin's SegWit addresses are
// bech32 too, so the prefix — not the encoding — is what decides.
func TestHandlesAddress(t *testing.T) {
	tests := []struct {
		name    string
		address string
		want    bool
	}{
		{"cosmos hub", cosmosAddress, true},
		{"akash", akashAddress, true},
		{"osmosis", "osmo14zta4gplkeym0dmlgnuxa2fmh7ymxtnl364css", true},
		{"unserved zone", "juno14zta4gplkeym0dmlgnuxa2fmh7ymxtnl0n9np7", false},
		{"bitcoin bech32", "bc1qar0srrr7xfkvy5l643lydnw9re59gtzzwf5mdq", false},
		{"ss58", "5DsvsaNbaA4JPXPRJHA2wWDMv4oJaWKQDbrUKEeELRfrto7Q", false},
		{"solana", "CvC1oRhFemouyXTBSJp6NBdEUN1CqQEYRNFcUh46Cqv8", false},
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
	assert.Equal(t, []string{"akash", "cosmos", "osmosis"}, SupportedChains())
}
