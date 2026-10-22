package portfolio

import (
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

	ws := &mockWalletSyncer{}
	ws.On("SyncWallet", mock.Anything, "0xabc", []string{"eth", "arbitrum"}).Return([]entity.WalletBalance{
		{Symbol: "DAI", Amount: "200", Decimals: 18, ContractAddress: "0xdai", Chain: "eth"},
	}, syncErr)

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
