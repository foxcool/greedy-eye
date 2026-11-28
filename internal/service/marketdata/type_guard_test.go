package marketdata

import (
	"context"

	"testing"

	"connectrpc.com/connect"
	apiv1 "github.com/foxcool/greedy-eye/api/v1"
	"github.com/foxcool/greedy-eye/internal/entity"
	"github.com/foxcool/greedy-eye/internal/middleware"
	"github.com/foxcool/greedy-eye/internal/store"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// Type is a classification, not identity: a creation path that declares
// another type for a known (symbol, market) takes the existing row with its own
// type instead of minting a twin (ADR-006).

func TestFindOrCreateAsset_AnotherTypeTakesTheExistingRow(t *testing.T) {
	s := &mockStore{}
	s.On("FindAssetByIdentity", mock.Anything, "X", "moex", entity.AssetTypeFund).
		Return(nil, store.ErrNotFound)
	stock := &entity.Asset{ID: "id-stock", Symbol: "X", Market: "moex", Type: entity.AssetTypeStock}
	// An explicit venue is its own namespace: X on spbex is not asked about.
	s.On("FindAssetsInNamespace", mock.Anything, "X", []string{"moex"}).
		Return([]*entity.Asset{stock}, nil)
	expectVerdict(s)
	h := newHandler(s)

	moex := "moex"
	for _, dryRun := range []bool{true, false} {
		resp, err := h.FindOrCreateAsset(syncContext(), connect.NewRequest(&apiv1.FindOrCreateAssetRequest{
			Symbol: "X", Market: &moex, Type: apiv1.AssetType_ASSET_TYPE_FUND, DryRun: dryRun,
		}))
		require.NoError(t, err, "dry_run=%v", dryRun)
		assert.False(t, resp.Msg.Created, "dry_run=%v", dryRun)
		if !dryRun {
			assert.Equal(t, "id-stock", resp.Msg.Asset.GetId())
			assert.Equal(t, apiv1.AssetType_ASSET_TYPE_STOCK, resp.Msg.Asset.GetType(), "the row keeps its own type")
		}
	}
	s.AssertNotCalled(t, "CreateAsset", mock.Anything, mock.Anything)
	s.AssertNotCalled(t, "UpdateAsset", mock.Anything, mock.Anything, mock.Anything)
}

// The derived market spans crypto and forex, and several candidates resolve to
// the oldest instead of failing the caller.
func TestFindOrCreateAsset_SeveralCandidatesTakeTheOldest(t *testing.T) {
	s := &mockStore{}
	s.On("FindAssetByIdentity", mock.Anything, "USDT", "crypto", entity.AssetTypeCryptocurrency).
		Return(nil, store.ErrNotFound)
	s.On("FindAssetsInNamespace", mock.Anything, "USDT", []string{"crypto", "forex"}).
		Return([]*entity.Asset{
			{ID: "id-old", Symbol: "USDT", Market: "forex", Type: entity.AssetTypeForex},
			{ID: "id-new", Symbol: "USDT", Market: "crypto", Type: entity.AssetTypeStock},
		}, nil)
	expectVerdict(s)
	h := newHandler(s)

	resp, err := h.FindOrCreateAsset(syncContext(), connect.NewRequest(&apiv1.FindOrCreateAssetRequest{Symbol: "USDT"}))
	require.NoError(t, err)
	assert.False(t, resp.Msg.Created)
	assert.Equal(t, "id-old", resp.Msg.Asset.GetId())
	s.AssertNotCalled(t, "CreateAsset", mock.Anything, mock.Anything)
}

// A non-admin import resolves the existing row too: the gate is on creating.
func TestFindOrCreateAsset_AnotherTypeNeedsNoCreateRight(t *testing.T) {
	s := &mockStore{}
	s.On("FindAssetByIdentity", mock.Anything, "USDT", "forex", entity.AssetTypeForex).
		Return(nil, store.ErrNotFound)
	s.On("FindAssetsInNamespace", mock.Anything, "USDT", []string{"crypto", "forex"}).
		Return([]*entity.Asset{{ID: "id-usdt", Symbol: "USDT", Market: "crypto", Type: entity.AssetTypeCryptocurrency}}, nil)
	expectVerdict(s)
	h := newHandler(s)

	resp, err := h.FindOrCreateAsset(middleware.ContextWithUser(context.Background(), &entity.User{ID: "user-1"}), connect.NewRequest(&apiv1.FindOrCreateAssetRequest{
		Symbol: "USDT", Type: apiv1.AssetType_ASSET_TYPE_FOREX,
	}))
	require.NoError(t, err)
	assert.Equal(t, "id-usdt", resp.Msg.Asset.GetId())
}

// A vouched contract does not join a fiat row of the same ticker: binding an
// address to a currency would price the token as that currency.
func TestFindOrCreateAsset_ContractDoesNotAdoptForex(t *testing.T) {
	s := &mockStore{}
	s.On("FindAssetIDByExternalRef", mock.Anything, "onchain:eth", "0xfeed").Return("", store.ErrNotFound)
	s.On("FindAssetByIdentity", mock.Anything, "USD", "crypto", entity.AssetTypeCryptocurrency).
		Return(nil, store.ErrNotFound)
	s.On("FindAssetsInNamespace", mock.Anything, "USD", []string{"crypto", "forex"}).
		Return([]*entity.Asset{{ID: "id-fiat", Symbol: "USD", Market: "forex", Type: entity.AssetTypeForex}}, nil)
	s.On("CreateAsset", mock.Anything, mock.MatchedBy(func(a *entity.Asset) bool {
		return a.Symbol == "USD" && a.Type == entity.AssetTypeCryptocurrency
	})).Return(&entity.Asset{ID: "id-token", Symbol: "USD", Market: "crypto", Type: entity.AssetTypeCryptocurrency}, nil)
	s.On("CreateAssetExternalRef", mock.Anything, mock.MatchedBy(func(r *entity.AssetExternalRef) bool {
		return r.AssetID == "id-token"
	})).Return(&entity.AssetExternalRef{}, nil)
	expectVerdict(s)
	h := newHandler(s).WithProvider("coingecko", &fakeContractResolver{
		listed: map[string]listing{"eth/0xfeed": listedAs("USD")},
	})

	source, ref := "onchain:eth", "0xfeed"
	resp, err := h.FindOrCreateAsset(syncContext(), connect.NewRequest(&apiv1.FindOrCreateAssetRequest{
		Symbol: "USD", ExternalRefSource: &source, ExternalRef: &ref,
	}))
	require.NoError(t, err)
	assert.True(t, resp.Msg.Created)
	assert.Equal(t, "id-token", resp.Msg.Asset.GetId())
	s.AssertNotCalled(t, "CreateAssetExternalRef", mock.Anything, mock.MatchedBy(func(r *entity.AssetExternalRef) bool {
		return r.AssetID == "id-fiat"
	}))
}

func TestFindOrCreateAsset_NamespaceLookupErrorIsReturned(t *testing.T) {
	s := &mockStore{}
	s.On("FindAssetByIdentity", mock.Anything, "BTC", "crypto", entity.AssetTypeCryptocurrency).
		Return(nil, store.ErrNotFound)
	s.On("FindAssetsInNamespace", mock.Anything, "BTC", []string{"crypto", "forex"}).
		Return(nil, assert.AnError)
	h := newHandler(s)

	_, err := h.FindOrCreateAsset(syncContext(), connect.NewRequest(&apiv1.FindOrCreateAssetRequest{Symbol: "BTC"}))
	require.Error(t, err, "a failed lookup must not fall through to minting a twin")
	s.AssertNotCalled(t, "CreateAsset", mock.Anything, mock.Anything)
}
