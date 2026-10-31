package portfolio

import (
	"context"
	"errors"
	"testing"

	"connectrpc.com/connect"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	apiv1 "github.com/foxcool/greedy-eye/api/v1"
	"github.com/foxcool/greedy-eye/internal/entity"
)

func syncedRow(id, assetID, chain, amount string) *entity.Holding {
	return &entity.Holding{
		ID:        id,
		AssetID:   assetID,
		AccountID: testAccountID,
		Amount:    decimal.RequireFromString(amount),
		Decimals:  18,
		Chain:     chain,
		Source:    entity.SourceSync,
	}
}

// scopeHarness is a wallet on eth and arbitrum whose sync returns DAI on eth
// and whatever error the test names.
func scopeHarness(t *testing.T, rows []*entity.Holding, syncErr error, md *mockMDClient) (*apiv1.SyncAccountResponse, map[string]decimal.Decimal) {
	t.Helper()
	s, acct, updated := scopeStore(rows)
	ws := &mockWalletSyncer{}
	expectDAI(ws, syncErr)
	return scopeHarnessWith(t, s, acct, ws, md, updated)
}

func expectDAI(ws *mockWalletSyncer, syncErr error) {
	ws.On("SyncWallet", mock.Anything, "0xabc", []string{"eth", "arbitrum"}).Return([]entity.WalletBalance{
		{Symbol: "DAI", Amount: "200", Decimals: 18, ContractAddress: "0xdai", Chain: "eth"},
	}, syncErr)
}

func scopeStore(rows []*entity.Holding) (*mockStore, *entity.Account, map[string]decimal.Decimal) {
	acct := testAccount(testAccountID)
	acct.Type = entity.AccountTypeWallet
	acct.Data = map[string]string{"address": "0xabc", "chain": "eth,arbitrum"}

	s := &mockStore{}
	s.On("GetAccount", mock.Anything, testAccountID).Return(acct, nil)
	s.On("ListHoldings", mock.Anything, mock.Anything).Return(rows, "", nil)
	s.On("CreateHolding", mock.Anything, mock.Anything).Return(&entity.Holding{ID: "h-new"}, nil)
	s.On("RecordChainFailures", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil, nil).Maybe()
	updated := map[string]decimal.Decimal{}
	s.On("UpdateHolding", mock.Anything, mock.Anything, mock.Anything).
		Run(func(args mock.Arguments) {
			hld := args.Get(1).(*entity.Holding)
			updated[hld.ID] = hld.Amount
		}).
		Return(&entity.Holding{}, nil)
	return s, acct, updated
}

func scopeHarnessWith(t *testing.T, s *mockStore, _ *entity.Account, ws entity.WalletSyncer, md *mockMDClient, updated map[string]decimal.Decimal) (*apiv1.SyncAccountResponse, map[string]decimal.Decimal) {
	t.Helper()
	md.autoAsset = true
	md.On("FetchExternalPrices", mock.Anything, mock.Anything).
		Return(connect.NewResponse(&apiv1.FetchExternalPricesResponse{}), nil)

	h := newHandler(s).WithMarketDataClient(md).WithWalletSyncer(ws)
	resp, err := h.SyncAccount(ctxWithUser(testUserID), connect.NewRequest(&apiv1.SyncAccountRequest{AccountId: testAccountID}))
	require.NoError(t, err)
	return resp.Msg, updated
}

func ftwBoundOn(md *mockMDClient, chain string) {
	md.On("ListAssets", mock.Anything, mock.MatchedBy(func(r *connect.Request[apiv1.ListAssetsRequest]) bool {
		return r.Msg.GetQuery() == "0xFTW"
	})).Return(connect.NewResponse(&apiv1.ListAssetsResponse{Assets: []*apiv1.Asset{{Id: "asset-FTW"}}}), nil)
	md.On("GetAsset", mock.Anything, mock.Anything).Return(connect.NewResponse(&apiv1.Asset{
		Id:           "asset-FTW",
		ExternalRefs: []*apiv1.AssetExternalRef{{Source: entity.OnchainSource(chain), Ref: "0xftw"}},
	}), nil)
}

// TestSyncAccount_RefusedTokenWithholdsOnlyItsPosition: an airdrop the reader
// refuses (no decimals) froze every sold position of darkfox.eth for six
// weeks. The refused token is still held and keeps its row; the sold WBTC goes.
func TestSyncAccount_RefusedTokenWithholdsOnlyItsPosition(t *testing.T) {
	sold := syncedRow("h-wbtc", "asset-WBTC", "eth", "1145109")
	refused := syncedRow("h-ftw", "asset-FTW", "arbitrum", "1000")
	md := &mockMDClient{}
	ftwBoundOn(md, "arbitrum")

	resp, updated := scopeHarness(t, []*entity.Holding{sold, refused},
		&entity.EntryError{Chain: "arbitrum", Ref: "0xFTW", Err: errors.New("no decimals reported")}, md)

	require.Len(t, resp.Errors, 1, "the refusal is still reported")
	assert.Equal(t, int32(1), resp.HoldingsZeroed)
	assert.True(t, updated["h-wbtc"].IsZero(), "a sold position goes despite an unrelated refusal")
	assert.NotContains(t, updated, "h-ftw", "the refused token is still held; zeroing it would lie in the minus")
}

// TestSyncAccount_RefusalOnOtherChainDoesNotProtect: the binding has to be on
// the refused chain. The same contract address elsewhere is another asset.
func TestSyncAccount_RefusalOnOtherChainDoesNotProtect(t *testing.T) {
	gone := syncedRow("h-ftw", "asset-FTW", "eth", "1000")
	md := &mockMDClient{}
	ftwBoundOn(md, "eth")

	resp, updated := scopeHarness(t, []*entity.Holding{gone},
		&entity.EntryError{Chain: "arbitrum", Ref: "0xFTW", Err: errors.New("no decimals reported")}, md)

	assert.Equal(t, int32(1), resp.HoldingsZeroed)
	assert.True(t, updated["h-ftw"].IsZero())
}

// TestSyncAccount_FailedChainWithholdsOnlyThatChain: a chain that failed
// reports nothing, which is indistinguishable from emptied — for that chain.
// The rest of the account answered and may shed what it no longer holds.
func TestSyncAccount_FailedChainWithholdsOnlyThatChain(t *testing.T) {
	sold := syncedRow("h-wbtc", "asset-WBTC", "eth", "1145109")
	dark := syncedRow("h-arb", "asset-ARB", "arbitrum", "50")
	legacy := syncedRow("h-old", "asset-OLD", "", "7")

	resp, updated := scopeHarness(t, []*entity.Holding{sold, dark, legacy},
		&entity.ChainError{Chain: "arbitrum", Err: errors.New("status 503")}, &mockMDClient{})

	assert.Equal(t, int32(1), resp.HoldingsZeroed)
	assert.True(t, updated["h-wbtc"].IsZero())
	assert.NotContains(t, updated, "h-arb", "a dark chain is not an emptied one")
	assert.NotContains(t, updated, "h-old", "a row with no chain could be on the dark one")
}

// TestSyncAccount_UnlocatedRefusalWithholdsAll: when the refused contract
// cannot be looked up, nothing says which row it stands for, so nothing goes.
func TestSyncAccount_UnlocatedRefusalWithholdsAll(t *testing.T) {
	sold := syncedRow("h-wbtc", "asset-WBTC", "eth", "1145109")
	md := &mockMDClient{}
	md.On("ListAssets", mock.Anything, mock.Anything).Return(nil, errors.New("marketdata down"))

	resp, updated := scopeHarness(t, []*entity.Holding{sold},
		&entity.EntryError{Chain: "arbitrum", Ref: "0xFTW", Err: errors.New("no decimals reported")}, md)

	assert.Equal(t, int32(0), resp.HoldingsZeroed)
	assert.NotContains(t, updated, "h-wbtc")
	assert.Len(t, resp.Errors, 2, "the refusal and the reason removal was withheld")
}

// TestSyncAccount_MisspelledFailedChainWithholdsAll: a failed chain protects
// only rows recorded under exactly its name. Under any other spelling it would
// protect nothing and zero the rows it meant.
func TestSyncAccount_MisspelledFailedChainWithholdsAll(t *testing.T) {
	for _, name := range []string{"Arbitrum", "arb-mainnet"} {
		t.Run(name, func(t *testing.T) {
			held := syncedRow("h-arb", "asset-ARB", "arbitrum", "50")
			resp, updated := scopeHarness(t, []*entity.Holding{held},
				&entity.ChainError{Chain: name, Err: errors.New("status 503")}, &mockMDClient{})

			assert.Equal(t, int32(0), resp.HoldingsZeroed)
			assert.NotContains(t, updated, "h-arb")
		})
	}
}

// TestSyncAccount_UnboundRefusalProtectsRowsWithoutChain: a row written before
// positions carried a chain may be the refused token even when no asset is
// bound to its contract.
func TestSyncAccount_UnboundRefusalProtectsRowsWithoutChain(t *testing.T) {
	sold := syncedRow("h-wbtc", "asset-WBTC", "eth", "1145109")
	legacy := syncedRow("h-old", "asset-OLD", "", "7")
	md := &mockMDClient{}
	md.On("ListAssets", mock.Anything, mock.Anything).Return(connect.NewResponse(&apiv1.ListAssetsResponse{}), nil)

	resp, updated := scopeHarness(t, []*entity.Holding{sold, legacy},
		&entity.EntryError{Chain: "arbitrum", Ref: "0xFTW", Err: errors.New("no decimals reported")}, md)

	assert.Equal(t, int32(1), resp.HoldingsZeroed)
	assert.True(t, updated["h-wbtc"].IsZero())
	assert.NotContains(t, updated, "h-old")
}

// TestSyncAccount_RefusalLookupReadsEveryPage: the catalogue search also
// matches names, which an airdrop sets, so the bound asset can sit behind a
// page of spam that mentions its contract.
func TestSyncAccount_RefusalLookupReadsEveryPage(t *testing.T) {
	refused := syncedRow("h-ftw", "asset-FTW", "arbitrum", "1000")
	md := &mockMDClient{}
	md.On("ListAssets", mock.Anything, mock.MatchedBy(func(r *connect.Request[apiv1.ListAssetsRequest]) bool {
		return r.Msg.GetPageToken() == ""
	})).Return(connect.NewResponse(&apiv1.ListAssetsResponse{
		Assets:        []*apiv1.Asset{{Id: "asset-SPAM"}},
		NextPageToken: "p2",
	}), nil)
	md.On("ListAssets", mock.Anything, mock.MatchedBy(func(r *connect.Request[apiv1.ListAssetsRequest]) bool {
		return r.Msg.GetPageToken() == "p2"
	})).Return(connect.NewResponse(&apiv1.ListAssetsResponse{Assets: []*apiv1.Asset{{Id: "asset-FTW"}}}), nil)
	md.On("GetAsset", mock.Anything, mock.MatchedBy(func(r *connect.Request[apiv1.GetAssetRequest]) bool {
		return r.Msg.GetId() == "asset-SPAM"
	})).Return(connect.NewResponse(&apiv1.Asset{Id: "asset-SPAM"}), nil)
	md.On("GetAsset", mock.Anything, mock.MatchedBy(func(r *connect.Request[apiv1.GetAssetRequest]) bool {
		return r.Msg.GetId() == "asset-FTW"
	})).Return(connect.NewResponse(&apiv1.Asset{
		Id:           "asset-FTW",
		ExternalRefs: []*apiv1.AssetExternalRef{{Source: entity.OnchainSource("arbitrum"), Ref: "0xftw"}},
	}), nil)

	_, updated := scopeHarness(t, []*entity.Holding{refused},
		&entity.EntryError{Chain: "arbitrum", Ref: "0xFTW", Err: errors.New("no decimals reported")}, md)

	assert.NotContains(t, updated, "h-ftw", "the binding on page two still protects the row")
}

// TestSyncAccount_EndlessRefusalLookupWithholdsAll: a search that never ends
// cannot place the contract, so nothing is removed.
func TestSyncAccount_EndlessRefusalLookupWithholdsAll(t *testing.T) {
	sold := syncedRow("h-wbtc", "asset-WBTC", "eth", "1145109")
	md := &mockMDClient{}
	md.On("ListAssets", mock.Anything, mock.Anything).Return(connect.NewResponse(&apiv1.ListAssetsResponse{
		Assets:        []*apiv1.Asset{{Id: "asset-SPAM"}},
		NextPageToken: "more",
	}), nil)
	md.On("GetAsset", mock.Anything, mock.Anything).Return(connect.NewResponse(&apiv1.Asset{Id: "asset-SPAM"}), nil)

	resp, updated := scopeHarness(t, []*entity.Holding{sold},
		&entity.EntryError{Chain: "arbitrum", Ref: "0xFTW", Err: errors.New("no decimals reported")}, md)

	assert.Equal(t, int32(0), resp.HoldingsZeroed)
	assert.NotContains(t, updated, "h-wbtc")
	md.AssertNumberOfCalls(t, "ListAssets", refLookupPages)
}

func TestUnrecordedChain(t *testing.T) {
	rows := []*entity.Holding{{Chain: "arbitrum"}, {Chain: ""}}
	_, ok := unrecordedChain(map[string]string{"arbitrum": "503"}, rows)
	assert.False(t, ok)
	chain, ok := unrecordedChain(map[string]string{"Arbitrum": "503"}, rows)
	assert.True(t, ok)
	assert.Equal(t, "Arbitrum", chain)
	_, ok = unrecordedChain(nil, rows)
	assert.False(t, ok)
}

// confirmingSyncer is a wallet syncer that can also be asked about contracts.
type confirmingSyncer struct {
	*mockWalletSyncer
	balances map[string]string
	err      error
}

func (c *confirmingSyncer) ConfirmBalances(_ context.Context, _, _ string, contracts []string) (map[string]string, error) {
	if c.err != nil {
		return nil, c.err
	}
	out := map[string]string{}
	for _, contract := range contracts {
		if b, ok := c.balances[contract]; ok {
			out[contract] = b
		}
	}
	return out, nil
}

func boundOnEth(md *mockMDClient, assetID, contract string) {
	md.On("GetAsset", mock.Anything, mock.MatchedBy(func(r *connect.Request[apiv1.GetAssetRequest]) bool {
		return r.Msg.GetId() == assetID
	})).Return(connect.NewResponse(&apiv1.Asset{
		Id:           assetID,
		ExternalRefs: []*apiv1.AssetExternalRef{{Source: entity.OnchainSource("eth"), Ref: contract}},
	}), nil)
}

// reconfirmHarness syncs a wallet whose snapshot is incomplete (an unscoped
// error withholds every inferred removal) and whose syncer answers by contract.
func reconfirmHarness(t *testing.T, rows []*entity.Holding, cs *confirmingSyncer, md *mockMDClient) (*apiv1.SyncAccountResponse, map[string]decimal.Decimal) {
	t.Helper()
	s, acct, updated := scopeStore(rows)
	expectDAI(cs.mockWalletSyncer, errors.New("resolve asset BINA: provider quota exhausted"))
	return scopeHarnessWith(t, s, acct, cs, md, updated)
}

// TestSyncAccount_ConfirmedZeroRemovesDespiteIncompleteSnapshot: MKR and
// stkAAVE on main were 0 on-chain for a day while an unrelated failure kept the
// gate shut. Asked by contract, the chain says 0, and that is enough.
func TestSyncAccount_ConfirmedZeroRemovesDespiteIncompleteSnapshot(t *testing.T) {
	mkr := syncedRow("h-mkr", "asset-MKR", "eth", "284583021768310634")
	unbound := syncedRow("h-old", "asset-OLD", "eth", "7")
	md := &mockMDClient{}
	boundOnEth(md, "asset-MKR", "0x9F8F72aA9304c8B593d555F12eF6589cC3A579A2")
	md.On("GetAsset", mock.Anything, mock.Anything).Return(connect.NewResponse(&apiv1.Asset{Id: "asset-OLD"}), nil)
	cs := &confirmingSyncer{mockWalletSyncer: &mockWalletSyncer{}, balances: map[string]string{
		"0x9f8f72aa9304c8b593d555f12ef6589cc3a579a2": "0",
	}}

	resp, updated := reconfirmHarness(t, []*entity.Holding{mkr, unbound}, cs, md)

	assert.Equal(t, int32(1), resp.HoldingsZeroed)
	assert.True(t, updated["h-mkr"].IsZero(), "an explicit zero is a statement, not an inference")
	assert.NotContains(t, updated, "h-old", "no contract to ask about: left to the snapshot's completeness")
}

// TestSyncAccount_ConfirmedNonZeroKeepsRow: the listing dropped a balance the
// chain still holds. Its last amount is nearer the truth than a zero.
func TestSyncAccount_ConfirmedNonZeroKeepsRow(t *testing.T) {
	mkr := syncedRow("h-mkr", "asset-MKR", "eth", "284583021768310634")
	md := &mockMDClient{}
	boundOnEth(md, "asset-MKR", "0x9f8f72aa9304c8b593d555f12ef6589cc3a579a2")
	cs := &confirmingSyncer{mockWalletSyncer: &mockWalletSyncer{}, balances: map[string]string{
		"0x9f8f72aa9304c8b593d555f12ef6589cc3a579a2": "1000",
	}}

	resp, updated := reconfirmHarness(t, []*entity.Holding{mkr}, cs, md)

	assert.Equal(t, int32(0), resp.HoldingsZeroed)
	assert.NotContains(t, updated, "h-mkr")
}

// TestSyncAccount_FailedReconfirmationKeepsRow: a confirmation that could not
// be read says nothing, so the incomplete snapshot still withholds the row.
func TestSyncAccount_FailedReconfirmationKeepsRow(t *testing.T) {
	mkr := syncedRow("h-mkr", "asset-MKR", "eth", "284583021768310634")
	md := &mockMDClient{}
	boundOnEth(md, "asset-MKR", "0x9f8f72aa9304c8b593d555f12ef6589cc3a579a2")
	cs := &confirmingSyncer{mockWalletSyncer: &mockWalletSyncer{}, err: errors.New("status 429")}

	resp, updated := reconfirmHarness(t, []*entity.Holding{mkr}, cs, md)

	assert.Equal(t, int32(0), resp.HoldingsZeroed)
	assert.NotContains(t, updated, "h-mkr")
}

const unnamedContract = "0xAbCdEf0123456789aBcDeF0123456789abcd1234"

func unnamedTokenHarness(t *testing.T, rows []*entity.Holding, tokens ...entity.WalletBalance) (*apiv1.SyncAccountResponse, *mockStore) {
	t.Helper()
	s, _, _ := scopeStore(rows)
	ws := &mockWalletSyncer{}
	ws.On("SyncWallet", mock.Anything, "0xabc", []string{"eth", "arbitrum"}).Return(tokens, nil)
	md := &mockMDClient{autoAsset: true}
	md.On("FetchExternalPrices", mock.Anything, mock.Anything).
		Return(connect.NewResponse(&apiv1.FetchExternalPricesResponse{}), nil)

	h := newHandler(s).WithMarketDataClient(md).WithWalletSyncer(ws)
	resp, err := h.SyncAccount(ctxWithUser(testUserID), connect.NewRequest(&apiv1.SyncAccountRequest{AccountId: testAccountID}))
	require.NoError(t, err)
	return resp.Msg, s
}

// TestSyncAccount_TokenWithoutSymbolBecomesHolding: a provider that reports a
// balance with no symbol still names the token by its contract. The position
// must reach the account under a label derived from that contract rather than
// vanish from the snapshot.
func TestSyncAccount_TokenWithoutSymbolBecomesHolding(t *testing.T) {
	resp, s := unnamedTokenHarness(t, nil,
		entity.WalletBalance{Amount: "5", Decimals: 18, ContractAddress: unnamedContract, Chain: "eth"})

	assert.Empty(t, resp.Errors)
	assert.Zero(t, resp.PositionsSkipped)
	assert.Equal(t, int32(1), resp.HoldingsUpserted)
	s.AssertCalled(t, "CreateHolding", mock.Anything, mock.MatchedBy(func(h *entity.Holding) bool {
		return h.AssetID == "asset-0XABCD..1234" && h.Chain == "eth"
	}))
}

// TestSyncAccount_UnidentifiableBalanceIsCounted: no symbol and no contract
// leaves nothing to name the position by. It is counted in the response so it
// does not disappear unseen, and it does not withhold removal — a sold
// position still goes.
func TestSyncAccount_UnidentifiableBalanceIsCounted(t *testing.T) {
	sold := syncedRow("h-sold", "asset-SOLD", "eth", "1")
	resp, s := unnamedTokenHarness(t, []*entity.Holding{sold},
		entity.WalletBalance{Amount: "5", Decimals: 18, Chain: "eth"},
		entity.WalletBalance{Symbol: "DAI", Amount: "200", Decimals: 18, ContractAddress: "0xdai", Chain: "eth"})

	assert.Equal(t, int32(1), resp.PositionsSkipped)
	assert.Empty(t, resp.Errors)
	assert.Equal(t, int32(1), resp.HoldingsZeroed, "a skipped balance must not freeze removal of sold positions")
	s.AssertNotCalled(t, "CreateHolding", mock.Anything, mock.MatchedBy(func(h *entity.Holding) bool {
		return h.AssetID != "asset-DAI"
	}))
}

func TestContractDisplaySymbol(t *testing.T) {
	cases := []struct {
		name, source, ref, want string
		ok                      bool
	}{
		{"evm address is shortened", "onchain:eth", unnamedContract, "0xAbCd..1234", true},
		{"short ref kept whole", "onchain:ton", "1984", "1984", true},
		{"broker namespace is not labelled", "tinvest", unnamedContract, "", false},
		{"no ref", "onchain:eth", " ", "", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, ok := contractDisplaySymbol(c.source, c.ref)
			assert.Equal(t, c.ok, ok)
			assert.Equal(t, c.want, got)
		})
	}
}

// TestSyncAccount_EmptySnapshotNamesItsCause: a provider that returned nothing
// and one whose every balance was unidentifiable both leave an empty snapshot,
// and both withhold removal. The message has to name which, because the fix for
// one is on the provider side and for the other on the token side.
func TestSyncAccount_EmptySnapshotNamesItsCause(t *testing.T) {
	cases := []struct {
		name        string
		tokens      []entity.WalletBalance
		wantSkipped int32
		wantMsg     string
		notMsg      string
	}{
		{
			name:    "provider returned nothing",
			tokens:  []entity.WalletBalance{},
			wantMsg: "provider returned no positions",
			notMsg:  "skipped",
		},
		{
			name: "everything it returned was skipped",
			tokens: []entity.WalletBalance{
				{Amount: "5", Decimals: 18, Chain: "eth"},
				{Amount: "7", Decimals: 18, Chain: "arbitrum"},
			},
			wantSkipped: 2,
			wantMsg:     "2 skipped as unidentifiable",
			notMsg:      "returned no positions",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			held := syncedRow("h-held", "asset-HELD", "eth", "1")
			resp, s := unnamedTokenHarness(t, []*entity.Holding{held}, tc.tokens...)

			assert.Equal(t, tc.wantSkipped, resp.PositionsSkipped)
			assert.Zero(t, resp.HoldingsZeroed, "removal is withheld whatever emptied the snapshot")
			require.Len(t, resp.Errors, 1)
			assert.Contains(t, resp.Errors[0], tc.wantMsg)
			assert.NotContains(t, resp.Errors[0], tc.notMsg)
			assert.Contains(t, resp.Errors[0], "1 synced holding(s) left untouched")
			s.AssertNotCalled(t, "UpdateHolding", mock.Anything, mock.Anything, mock.Anything)
		})
	}
}
