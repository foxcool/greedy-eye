package subscan

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/foxcool/greedy-eye/internal/entity"
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

	// The builtin USDT entry in this fixture was abbreviated to three fields
	// when it was pinned, and the one it lost is unique_id — which every
	// captured response carries on every entry, native included. A token whose
	// identity the response does not state is refused and NAMED, never guessed
	// at from its ticker: on a chain where anyone may register an asset, the
	// ticker is the claim under examination, not the evidence.
	require.ErrorContains(t, err, "USDT")
	require.Len(t, balances, 2, "the native coin is unaffected by a refused token")

	// The anchor is now the SUM: the position is split by liquidity, and the
	// total it adds up to is the figure verified against the manual baseline.
	assert.Equal(t, "34644639967302550", sumAmounts(t, balances))

	for _, b := range balances {
		assert.Equal(t, "HDX", b.Symbol)
		assert.Equal(t, 12, b.Decimals)
	}
	assert.Equal(t, entity.LiquidityLiquid, balances[0].Liquidity)
	assert.Equal(t, "628942544130414", balances[0].Amount, "34644.639967302550 - 34015.697423172136")
	assert.Equal(t, entity.LiquidityStaked, balances[1].Liquidity)
	assert.Equal(t, "34015697423172136", balances[1].Amount)
}

// sumAmounts adds the raw amounts of a split position back up. Every liquidity
// test checks this: a partition may move value between states, never create or
// destroy it.
func sumAmounts(t *testing.T, balances []entity.WalletBalance) string {
	t.Helper()
	sum := decimal.Zero
	for _, b := range balances {
		d, err := decimal.NewFromString(b.Amount)
		require.NoError(t, err)
		sum = sum.Add(d)
	}
	return sum.String()
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
	require.Len(t, balances, 2)

	assert.Equal(t, "6016092032275", sumAmounts(t, balances),
		"balance already contains the staking hold; adding reserved double-counts it")
	assert.Equal(t, 12, balances[0].Decimals)

	// The spendable remainder is not inferred — Subscan's own v2 endpoint
	// reports transferable_balance = 394224319384 for this exact account, which
	// is what balance minus the hold has to come out as.
	assert.Equal(t, entity.LiquidityLiquid, balances[0].Liquidity)
	assert.Equal(t, "394224319384", balances[0].Amount)
	assert.Equal(t, entity.LiquidityStaked, balances[1].Liquidity)
	assert.Equal(t, "5621867712891", balances[1].Amount)
}

// TestSyncWallet_ReservedAndBondedAreTheSamePlanck is the measurement that
// settled personal-3h4w, kept as a fixture.
//
// Live Kusama Asset Hub, 2026-08-02: balance 6.031593575767 KSM with reserved,
// bonded AND lock each reporting 5.637369256383 — one hold, stated three times.
// Subtracting reserved and bonded both would take 11.27 KSM out of 6.03 and
// drive the position negative, which is the amount corruption personal-qi0 was
// about. The split counts the hold once.
func TestSyncWallet_ReservedAndBondedAreTheSamePlanck(t *testing.T) {
	syncer := newTestSyncer(t, respondJSON(`{
		"code": 0, "message": "Success",
		"data": {"native": [{
			"symbol": "KSM", "unique_id": "KSM", "decimals": 12,
			"balance": "6031593575767",
			"lock": "5637369256383",
			"reserved": "5637369256383",
			"bonded": "5637369256383",
			"unbonding": "0"
		}]}
	}`))

	balances, err := syncer.SyncWallet(context.Background(), "EEg3jY", []string{"assethub-kusama"})
	require.NoError(t, err)
	require.Len(t, balances, 2)

	assert.Equal(t, "6031593575767", sumAmounts(t, balances), "the hold is counted once, not twice")
	assert.Equal(t, entity.LiquidityLiquid, balances[0].Liquidity)
	assert.Equal(t, "394224319384", balances[0].Amount)
	assert.Equal(t, entity.LiquidityStaked, balances[1].Liquidity)
	assert.Equal(t, "5637369256383", balances[1].Amount)
}

// TestSyncWallet_UnreconcilableReserveStaysUnclassified: a reserve that is
// neither zero nor equal to bonded cannot be placed. It may be a deposit
// disjoint from the staking lock, or the same planck seen through another
// field, and the two answers differ in the direction that matters — guessing
// wrong overstates what can be spent. One unclassified row is the honest answer.
func TestSyncWallet_UnreconcilableReserveStaysUnclassified(t *testing.T) {
	syncer := newTestSyncer(t, respondJSON(`{
		"code": 0, "message": "Success",
		"data": {"native": [{
			"symbol": "DOT", "unique_id": "DOT", "decimals": 10,
			"balance": "1000000000000",
			"reserved": "10000000000",
			"bonded": "600000000000",
			"unbonding": "0"
		}]}
	}`))

	balances, err := syncer.SyncWallet(context.Background(), "16RUKR", []string{"polkadot"})
	require.NoError(t, err)
	require.Len(t, balances, 1)

	assert.Equal(t, "1000000000000", balances[0].Amount)
	assert.Equal(t, entity.LiquidityUnknown, balances[0].Liquidity,
		"an unknown liquidity is a gap; a wrong one is a false claim about available money")
}

// TestSyncWallet_FrozenPartsExceedingBalanceStayUnclassified: the same guard tzkt
// has. If bonded and unbonding overlap each other, the reconciliation above
// cannot see it, so the subtraction is what catches it.
func TestSyncWallet_FrozenPartsExceedingBalanceStayUnclassified(t *testing.T) {
	syncer := newTestSyncer(t, respondJSON(`{
		"code": 0, "message": "Success",
		"data": {"native": [{
			"symbol": "DOT", "unique_id": "DOT", "decimals": 10,
			"balance": "1000000000000",
			"reserved": "0",
			"bonded": "900000000000",
			"unbonding": "800000000000"
		}]}
	}`))

	balances, err := syncer.SyncWallet(context.Background(), "16RUKR", []string{"polkadot"})
	require.NoError(t, err)
	require.Len(t, balances, 1)

	assert.Equal(t, "1000000000000", balances[0].Amount)
	assert.Equal(t, entity.LiquidityUnknown, balances[0].Liquidity)
}

// TestSyncWallet_UnbondingIsItsOwnState: value on its way out of staking is not
// locked — it becomes spendable when the era ends, with no further decision.
func TestSyncWallet_UnbondingIsItsOwnState(t *testing.T) {
	syncer := newTestSyncer(t, respondJSON(`{
		"code": 0, "message": "Success",
		"data": {"native": [{
			"symbol": "DOT", "unique_id": "DOT", "decimals": 10,
			"balance": "1000000000000",
			"reserved": "0",
			"bonded": "600000000000",
			"unbonding": "150000000000"
		}]}
	}`))

	balances, err := syncer.SyncWallet(context.Background(), "16RUKR", []string{"polkadot"})
	require.NoError(t, err)
	require.Len(t, balances, 3)

	assert.Equal(t, "1000000000000", sumAmounts(t, balances))
	assert.Equal(t, entity.LiquidityLiquid, balances[0].Liquidity)
	assert.Equal(t, "250000000000", balances[0].Amount)
	assert.Equal(t, entity.LiquidityStaked, balances[1].Liquidity)
	assert.Equal(t, "600000000000", balances[1].Amount)
	assert.Equal(t, entity.LiquidityUnbonding, balances[2].Liquidity)
	assert.Equal(t, "150000000000", balances[2].Amount)
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

// TestSyncWallet_AssetHubAssetsAreHoldings is the live capture that closes
// personal-feb.10: Polkadot Asset Hub, the dot-controller address, 2026-09-12,
// taken off the dev instance through its own credential rather than retyped.
//
// It carries the two shapes pallet-assets comes in — a locally registered asset
// (DED, id 30) and one teleported from a parachain (MYTH, from 3369) — and they
// differ in precision, which is the whole reason the entry states its own.
// Reading MYTH at DED's ten decimals would report 5.7 billion MYTH.
func TestSyncWallet_AssetHubAssetsAreHoldings(t *testing.T) {
	syncer := newTestSyncer(t, respondJSON(`{
		"code": 0, "message": "Success",
		"data": {
			"native": [{
				"symbol": "DOT", "unique_id": "DOT", "decimals": 10,
				"balance": "83568869912",
				"lock": "0", "reserved": "0", "bonded": "0", "unbonding": "0"
			}],
			"assets": [
				{
					"symbol": "DED", "unique_id": "standard_assets/30", "decimals": 10,
					"balance": "895736192688", "lock": "0", "asset_id": "30"
				},
				{
					"symbol": "MYTH",
					"unique_id": "standard_foreign_assets/6212dc295daf309533f0f5873ec3f3e62d9dba33",
					"decimals": 18, "balance": "57000000000000000000",
					"asset_id": "6212dc295daf309533f0f5873ec3f3e62d9dba33"
				}
			]
		}
	}`))

	balances, err := syncer.SyncWallet(context.Background(), "5Dsvsa", []string{"assethub-polkadot"})
	require.NoError(t, err)
	require.Len(t, balances, 3, "the chain's own coin plus its two pallet assets")

	assert.Equal(t, "DOT", balances[0].Symbol)
	assert.Equal(t, "83568869912", balances[0].Amount)
	assert.Empty(t, balances[0].ContractAddress, "a native coin has no contract to confirm")

	ded := balances[1]
	assert.Equal(t, "DED", ded.Symbol)
	assert.Equal(t, "895736192688", ded.Amount)
	assert.Equal(t, 10, ded.Decimals, "89.5736192688 DED")
	assert.Equal(t, "standard_assets/30", ded.ContractAddress)
	assert.Equal(t, "assethub-polkadot", ded.Chain)
	assert.Equal(t, entity.LiquidityLiquid, ded.Liquidity, "nothing is locked")

	myth := balances[2]
	assert.Equal(t, "MYTH", myth.Symbol)
	assert.Equal(t, "57000000000000000000", myth.Amount)
	assert.Equal(t, 18, myth.Decimals, "57 MYTH, not 5.7bn")
	assert.Equal(t, "standard_foreign_assets/6212dc295daf309533f0f5873ec3f3e62d9dba33", myth.ContractAddress)
}

// TestSyncWallet_TokenWithoutDecimalsIsRefusedNotGuessed covers the hazard this
// whole issue was written around: absent precision read as zero turns a
// position into 10^decimals times itself. An entry that does not say how to
// read its number is refused and named, and the rest of the chain is untouched.
//
// The refusal is per ENTRY on purpose. Registration on an Asset Hub is
// permissionless, so a malformed entry is something a stranger can put into
// this account's response; failing the chain on it would hand that stranger a
// way to stop DOT from syncing.
func TestSyncWallet_TokenWithoutDecimalsIsRefusedNotGuessed(t *testing.T) {
	syncer := newTestSyncer(t, respondJSON(`{
		"code": 0, "message": "Success",
		"data": {
			"native": [{
				"symbol": "DOT", "unique_id": "DOT", "decimals": 10,
				"balance": "83568869912", "reserved": "0", "bonded": "0", "unbonding": "0"
			}],
			"assets": [
				{"symbol": "JUNK", "unique_id": "standard_assets/999", "balance": "1000000000000000000"},
				{"symbol": "DED", "unique_id": "standard_assets/30", "decimals": 10, "balance": "895736192688"}
			]
		}
	}`))

	balances, err := syncer.SyncWallet(context.Background(), "5Dsvsa", []string{"assethub-polkadot"})
	require.ErrorContains(t, err, "JUNK")
	require.ErrorContains(t, err, "decimals")
	require.Len(t, balances, 2, "the native coin and the well-formed asset both survive")
	assert.Equal(t, "DOT", balances[0].Symbol)
	assert.Equal(t, "DED", balances[1].Symbol)
}

// TestSyncWallet_ZeroDecimalsIsAPrecisionNotAnAbsence is the other half of the
// same guard. Zero decimals is legitimate — an asset may be denominated in
// whole units — so the parser must separate "says zero" from "says nothing",
// which is why the field is read as a pointer.
func TestSyncWallet_ZeroDecimalsIsAPrecisionNotAnAbsence(t *testing.T) {
	syncer := newTestSyncer(t, respondJSON(`{
		"code": 0, "message": "Success",
		"data": {
			"native": [{
				"symbol": "DOT", "unique_id": "DOT", "decimals": 10,
				"balance": "83568869912", "reserved": "0", "bonded": "0", "unbonding": "0"
			}],
			"assets": [
				{"symbol": "TICKET", "unique_id": "standard_assets/7", "decimals": 0, "balance": "3"}
			]
		}
	}`))

	balances, err := syncer.SyncWallet(context.Background(), "5Dsvsa", []string{"assethub-polkadot"})
	require.NoError(t, err)
	require.Len(t, balances, 2)
	assert.Equal(t, "TICKET", balances[1].Symbol)
	assert.Equal(t, "3", balances[1].Amount)
	assert.Equal(t, 0, balances[1].Decimals, "three whole tickets")
}

// TestSyncWallet_OneIdentityIsOnePosition: downstream, holdings merge per
// (asset, chain), so two rows carrying one identity are SUMMED rather than
// shown twice. A response that names the same asset in two groups must
// therefore produce one position, and say that it did not produce two.
func TestSyncWallet_OneIdentityIsOnePosition(t *testing.T) {
	syncer := newTestSyncer(t, respondJSON(`{
		"code": 0, "message": "Success",
		"data": {
			"native": [{
				"symbol": "HDX", "unique_id": "HDX", "decimals": 12,
				"balance": "1000000000000", "reserved": "0", "bonded": "0", "unbonding": "0"
			}],
			"builtin": [
				{"symbol": "USDT", "unique_id": "builtin/10", "decimals": 6, "balance": "156501335"}
			],
			"assets": [
				{"symbol": "USDT", "unique_id": "builtin/10", "decimals": 6, "balance": "156501335"}
			]
		}
	}`))

	balances, err := syncer.SyncWallet(context.Background(), "5Dsvsa", []string{"hydration"})
	require.ErrorContains(t, err, "twice")
	require.Len(t, balances, 2, "HDX and one USDT")
	assert.Equal(t, "156501335", balances[1].Amount, "156.501335 USDT, counted once")
}

// TestSyncWallet_TokensSurviveAnEmptyNativeBalance: an account can hold a
// pallet asset having spent the last of the chain's own coin, and on the Asset
// Hubs that is the likely case rather than the exotic one. "No native balance"
// must not be read as "nothing here".
func TestSyncWallet_TokensSurviveAnEmptyNativeBalance(t *testing.T) {
	syncer := newTestSyncer(t, respondJSON(`{
		"code": 0, "message": "Success",
		"data": {
			"native": [],
			"assets": [
				{"symbol": "RMRK", "unique_id": "standard_assets/8", "decimals": 10, "balance": "10995332256"}
			]
		}
	}`))

	balances, err := syncer.SyncWallet(context.Background(), "EEg3jY", []string{"assethub-kusama"})
	require.NoError(t, err)
	require.Len(t, balances, 1)
	assert.Equal(t, "RMRK", balances[0].Symbol)
	assert.Equal(t, "10995332256", balances[0].Amount)
	assert.Equal(t, "assethub-kusama", balances[0].Chain)
}

// TestSyncWallet_ASpentAssetIsNotAPosition: an asset the account once held
// stays in the response at zero. Emitting it would put a row nobody holds into
// the catalogue, and every such row is one more thing the unpriced tail has to
// explain.
func TestSyncWallet_ASpentAssetIsNotAPosition(t *testing.T) {
	syncer := newTestSyncer(t, respondJSON(`{
		"code": 0, "message": "Success",
		"data": {
			"native": [{
				"symbol": "DOT", "unique_id": "DOT", "decimals": 10,
				"balance": "83568869912", "reserved": "0", "bonded": "0", "unbonding": "0"
			}],
			"assets": [
				{"symbol": "GONE", "unique_id": "standard_assets/11", "decimals": 10, "balance": "0"}
			]
		}
	}`))

	balances, err := syncer.SyncWallet(context.Background(), "5Dsvsa", []string{"assethub-polkadot"})
	require.NoError(t, err)
	require.Len(t, balances, 1)
	assert.Equal(t, "DOT", balances[0].Symbol)
}

// TestSyncWallet_ALockedTokenIsNotClaimedSpendable: no captured response has
// carried a non-zero token lock, so whether it is a subset of balance (as the
// native coin's is) or something beside it has not been measured. The position
// is reported whole and unclassified — the same refusal splitLiquidity makes,
// for the same reason: overstating spendable money is the one error that
// matters here.
func TestSyncWallet_ALockedTokenIsNotClaimedSpendable(t *testing.T) {
	syncer := newTestSyncer(t, respondJSON(`{
		"code": 0, "message": "Success",
		"data": {
			"native": [],
			"assets": [
				{"symbol": "DED", "unique_id": "standard_assets/30", "decimals": 10,
				 "balance": "895736192688", "lock": "100000000000"}
			]
		}
	}`))

	balances, err := syncer.SyncWallet(context.Background(), "5Dsvsa", []string{"assethub-polkadot"})
	require.NoError(t, err)
	require.Len(t, balances, 1, "not split into spendable and frozen")
	assert.Equal(t, "895736192688", balances[0].Amount)
	assert.Equal(t, entity.LiquidityUnknown, balances[0].Liquidity)
}

// TestSyncWallet_AnUnreadableLockIsNotAZeroLock closes the hole the adjacent
// personal-feb.13 pointed at, one layer over from where that ticket looks.
//
// A lock is read with the parser that returns zero on failure, and the liquidity
// rule asked whether the lock was zero. So a lock nobody could parse arrived as
// "nothing frozen" — a claim about spendable money made out of a string that was
// never read, and made in the one direction that overstates it. The entry is
// still a position; only its partition is withheld, and the withholding is named.
func TestSyncWallet_AnUnreadableLockIsNotAZeroLock(t *testing.T) {
	syncer := newTestSyncer(t, respondJSON(`{
		"code": 0, "message": "Success",
		"data": {
			"native": [],
			"assets": [
				{"symbol": "DED", "unique_id": "standard_assets/30", "decimals": 10,
				 "balance": "895736192688", "lock": "not-a-number"}
			]
		}
	}`))

	balances, err := syncer.SyncWallet(context.Background(), "5Dsvsa", []string{"assethub-polkadot"})
	require.ErrorContains(t, err, "unreadable lock")
	require.Len(t, balances, 1, "the position is reported; only its partition is withheld")
	assert.Equal(t, "895736192688", balances[0].Amount)
	assert.Equal(t, entity.LiquidityUnknown, balances[0].Liquidity)
}

// TestSyncWallet_AnAbsentLockIsAZeroLock is the other side of that line, and it
// is measured rather than assumed: in one live Polkadot Asset Hub response DED
// carries "lock": "0" and MYTH carries no lock field at all. Subscan omits a
// component an account does not use, so absent is zero — and reading it as
// unknown would drop every token into an unstated liquidity.
func TestSyncWallet_AnAbsentLockIsAZeroLock(t *testing.T) {
	syncer := newTestSyncer(t, respondJSON(`{
		"code": 0, "message": "Success",
		"data": {
			"native": [],
			"assets": [
				{"symbol": "MYTH",
				 "unique_id": "standard_foreign_assets/6212dc295daf309533f0f5873ec3f3e62d9dba33",
				 "decimals": 18, "balance": "57000000000000000000"}
			]
		}
	}`))

	balances, err := syncer.SyncWallet(context.Background(), "5Dsvsa", []string{"assethub-polkadot"})
	require.NoError(t, err)
	require.Len(t, balances, 1)
	assert.Equal(t, entity.LiquidityLiquid, balances[0].Liquidity)
}
