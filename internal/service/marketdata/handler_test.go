package marketdata

import (
	"context"
	"errors"
	"testing"
	"time"

	"connectrpc.com/connect"
	apiv1 "github.com/foxcool/greedy-eye/api/v1"
	"github.com/foxcool/greedy-eye/internal/entity"
	"github.com/foxcool/greedy-eye/internal/store"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"log/slog"
	"os"
)

// --- Mock Store ---

type mockStore struct {
	mock.Mock
}

func (m *mockStore) CreateAsset(ctx context.Context, asset *entity.Asset) (*entity.Asset, error) {
	args := m.Called(ctx, asset)
	if v := args.Get(0); v != nil {
		return v.(*entity.Asset), args.Error(1)
	}
	return nil, args.Error(1)
}

func (m *mockStore) GetAsset(ctx context.Context, id string) (*entity.Asset, error) {
	args := m.Called(ctx, id)
	if v := args.Get(0); v != nil {
		return v.(*entity.Asset), args.Error(1)
	}
	return nil, args.Error(1)
}

func (m *mockStore) GetAssetBySymbol(ctx context.Context, symbol string) (*entity.Asset, error) {
	args := m.Called(ctx, symbol)
	if v := args.Get(0); v != nil {
		return v.(*entity.Asset), args.Error(1)
	}
	return nil, args.Error(1)
}

func (m *mockStore) FindAssetByIdentity(ctx context.Context, symbol, market string, typ entity.AssetType) (*entity.Asset, error) {
	args := m.Called(ctx, symbol, market, typ)
	if v := args.Get(0); v != nil {
		return v.(*entity.Asset), args.Error(1)
	}
	return nil, args.Error(1)
}

func (m *mockStore) GetOrCreateAssetBySymbol(ctx context.Context, symbol, nameIfNew string, typeIfNew entity.AssetType) (*entity.Asset, error) {
	args := m.Called(ctx, symbol, nameIfNew, typeIfNew)
	if v := args.Get(0); v != nil {
		return v.(*entity.Asset), args.Error(1)
	}
	return nil, args.Error(1)
}

func (m *mockStore) UpdateAsset(ctx context.Context, asset *entity.Asset, fields []string) (*entity.Asset, error) {
	args := m.Called(ctx, asset, fields)
	if v := args.Get(0); v != nil {
		return v.(*entity.Asset), args.Error(1)
	}
	return nil, args.Error(1)
}

func (m *mockStore) DeleteAsset(ctx context.Context, id string) error {
	return m.Called(ctx, id).Error(0)
}

func (m *mockStore) ListAssets(ctx context.Context, opts ListAssetsOpts) ([]*entity.Asset, string, error) {
	args := m.Called(ctx, opts)
	if v := args.Get(0); v != nil {
		return v.([]*entity.Asset), args.String(1), args.Error(2)
	}
	return nil, args.String(1), args.Error(2)
}

func (m *mockStore) CreatePrice(ctx context.Context, price *entity.StoredPrice) (*entity.StoredPrice, error) {
	args := m.Called(ctx, price)
	if v := args.Get(0); v != nil {
		return v.(*entity.StoredPrice), args.Error(1)
	}
	return nil, args.Error(1)
}

func (m *mockStore) CreatePrices(ctx context.Context, prices []*entity.StoredPrice) (int, error) {
	args := m.Called(ctx, prices)
	return args.Int(0), args.Error(1)
}

func (m *mockStore) GetLatestPrice(ctx context.Context, assetID, baseAssetID, sourceID string) (*entity.StoredPrice, error) {
	args := m.Called(ctx, assetID, baseAssetID, sourceID)
	if v := args.Get(0); v != nil {
		return v.(*entity.StoredPrice), args.Error(1)
	}
	return nil, args.Error(1)
}

func (m *mockStore) ListPriceHistory(ctx context.Context, opts ListPriceHistoryOpts) ([]*entity.StoredPrice, string, error) {
	args := m.Called(ctx, opts)
	if v := args.Get(0); v != nil {
		return v.([]*entity.StoredPrice), args.String(1), args.Error(2)
	}
	return nil, args.String(1), args.Error(2)
}

func (m *mockStore) DeletePrice(ctx context.Context, id string) error {
	return m.Called(ctx, id).Error(0)
}

func (m *mockStore) DeletePrices(ctx context.Context, opts DeletePricesOpts) error {
	return m.Called(ctx, opts).Error(0)
}

// --- Helpers ---

func newHandler(s Store) *Handler {
	return NewHandler(s, slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError})))
}

func testAsset(id string) *entity.Asset {
	return &entity.Asset{
		ID:        id,
		Name:      "Bitcoin",
		Symbol:    "BTC",
		Type:      entity.AssetTypeCryptocurrency,
		Tags:      []string{"crypto"},
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
}

// --- Tests: CreateAsset ---

func TestCreateAsset_MissingAsset(t *testing.T) {
	h := newHandler(&mockStore{})
	_, err := h.CreateAsset(context.Background(), connect.NewRequest(&apiv1.CreateAssetRequest{}))
	require.Error(t, err)
	assert.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))
}

func TestCreateAsset_StoreError(t *testing.T) {
	s := &mockStore{}
	s.On("CreateAsset", mock.Anything, mock.Anything).Return(nil, errors.New("db error"))
	h := newHandler(s)

	_, err := h.CreateAsset(context.Background(), connect.NewRequest(&apiv1.CreateAssetRequest{
		Asset: &apiv1.Asset{Name: "Bitcoin"},
	}))
	require.Error(t, err)
	assert.Equal(t, connect.CodeInternal, connect.CodeOf(err))
}

func TestCreateAsset_OK(t *testing.T) {
	s := &mockStore{}
	s.On("CreateAsset", mock.Anything, mock.Anything).Return(testAsset("id-1"), nil)
	h := newHandler(s)

	resp, err := h.CreateAsset(context.Background(), connect.NewRequest(&apiv1.CreateAssetRequest{
		Asset: &apiv1.Asset{Name: "Bitcoin"},
	}))
	require.NoError(t, err)
	assert.Equal(t, "id-1", resp.Msg.Id)
	assert.Equal(t, "Bitcoin", resp.Msg.Name)
}

// --- Tests: GetAsset ---

func TestGetAsset_MissingID(t *testing.T) {
	h := newHandler(&mockStore{})
	_, err := h.GetAsset(context.Background(), connect.NewRequest(&apiv1.GetAssetRequest{}))
	require.Error(t, err)
	assert.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))
}

func TestGetAsset_NotFound(t *testing.T) {
	s := &mockStore{}
	s.On("GetAsset", mock.Anything, "id-x").Return(nil, store.ErrNotFound)
	h := newHandler(s)

	_, err := h.GetAsset(context.Background(), connect.NewRequest(&apiv1.GetAssetRequest{Id: "id-x"}))
	require.Error(t, err)
	assert.Equal(t, connect.CodeNotFound, connect.CodeOf(err))
}

func TestGetAsset_OK(t *testing.T) {
	s := &mockStore{}
	s.On("GetAsset", mock.Anything, "id-1").Return(testAsset("id-1"), nil)
	h := newHandler(s)

	resp, err := h.GetAsset(context.Background(), connect.NewRequest(&apiv1.GetAssetRequest{Id: "id-1"}))
	require.NoError(t, err)
	assert.Equal(t, "id-1", resp.Msg.Id)
}

// --- Tests: DeleteAsset ---

func TestDeleteAsset_MissingID(t *testing.T) {
	h := newHandler(&mockStore{})
	_, err := h.DeleteAsset(context.Background(), connect.NewRequest(&apiv1.DeleteAssetRequest{}))
	require.Error(t, err)
	assert.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))
}

func TestDeleteAsset_OK(t *testing.T) {
	s := &mockStore{}
	s.On("DeleteAsset", mock.Anything, "id-1").Return(nil)
	h := newHandler(s)

	_, err := h.DeleteAsset(context.Background(), connect.NewRequest(&apiv1.DeleteAssetRequest{Id: "id-1"}))
	require.NoError(t, err)
}

// --- Tests: ListAssets ---

func TestListAssets_Empty(t *testing.T) {
	s := &mockStore{}
	s.On("ListAssets", mock.Anything, mock.Anything).Return([]*entity.Asset{}, "", nil)
	h := newHandler(s)

	resp, err := h.ListAssets(context.Background(), connect.NewRequest(&apiv1.ListAssetsRequest{}))
	require.NoError(t, err)
	assert.Empty(t, resp.Msg.Assets)
}

func TestListAssets_WithFilters(t *testing.T) {
	s := &mockStore{}
	pageSize := int32(5)
	pageToken := "tok"
	s.On("ListAssets", mock.Anything, ListAssetsOpts{
		Tags:      []string{"crypto"},
		PageSize:  5,
		PageToken: "tok",
	}).Return([]*entity.Asset{testAsset("a1"), testAsset("a2")}, "next", nil)
	h := newHandler(s)

	resp, err := h.ListAssets(context.Background(), connect.NewRequest(&apiv1.ListAssetsRequest{
		Tags:      []string{"crypto"},
		PageSize:  &pageSize,
		PageToken: &pageToken,
	}))
	require.NoError(t, err)
	assert.Len(t, resp.Msg.Assets, 2)
	assert.Equal(t, "next", resp.Msg.NextPageToken)
}

// --- Tests: UpdateAsset ---

func TestUpdateAsset_MissingID(t *testing.T) {
	h := newHandler(&mockStore{})
	_, err := h.UpdateAsset(context.Background(), connect.NewRequest(&apiv1.UpdateAssetRequest{
		Asset: &apiv1.Asset{},
	}))
	require.Error(t, err)
	assert.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))
}

func TestUpdateAsset_OK(t *testing.T) {
	s := &mockStore{}
	updated := testAsset("id-1")
	updated.Name = "Updated"
	s.On("UpdateAsset", mock.Anything, mock.Anything, []string(nil)).Return(updated, nil)
	h := newHandler(s)

	resp, err := h.UpdateAsset(context.Background(), connect.NewRequest(&apiv1.UpdateAssetRequest{
		Asset: &apiv1.Asset{Id: "id-1", Name: "Updated"},
	}))
	require.NoError(t, err)
	assert.Equal(t, "Updated", resp.Msg.Name)
}

// --- Tests: CreatePrice ---

func TestCreatePrice_MissingPrice(t *testing.T) {
	h := newHandler(&mockStore{})
	_, err := h.CreatePrice(context.Background(), connect.NewRequest(&apiv1.CreatePriceRequest{}))
	require.Error(t, err)
	assert.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))
}

func TestCreatePrice_OK(t *testing.T) {
	s := &mockStore{}
	now := time.Now()
	stored := &entity.StoredPrice{
		ID:          "p-1",
		AssetID:     "a-1",
		BaseAssetID: "usdt",
		SourceID:    "binance",
		Last:        decimal.NewFromInt(50000),
		Timestamp:   now,
	}
	s.On("CreatePrice", mock.Anything, mock.Anything).Return(stored, nil)
	h := newHandler(s)

	resp, err := h.CreatePrice(context.Background(), connect.NewRequest(&apiv1.CreatePriceRequest{
		Price: &apiv1.Price{AssetId: "a-1", BaseAssetId: "usdt"},
	}))
	require.NoError(t, err)
	assert.Equal(t, "p-1", resp.Msg.Id)
}

// --- Tests: GetLatestPrice ---

func TestGetLatestPrice_MissingFields(t *testing.T) {
	h := newHandler(&mockStore{})
	tests := []struct {
		name string
		req  *apiv1.GetLatestPriceRequest
	}{
		{"missing both", &apiv1.GetLatestPriceRequest{}},
		{"missing asset_id", &apiv1.GetLatestPriceRequest{BaseAssetId: "usdt"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := h.GetLatestPrice(context.Background(), connect.NewRequest(tt.req))
			require.Error(t, err)
			assert.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))
		})
	}
}

func TestGetLatestPrice_OK(t *testing.T) {
	const baseUUID = "00000000-0000-0000-0000-000000000001"
	const assetUUID = "00000000-0000-0000-0000-0000000000a1"
	s := &mockStore{}
	s.On("GetLatestPrice", mock.Anything, assetUUID, baseUUID, "").Return(&entity.StoredPrice{
		ID: "p-1", AssetID: assetUUID, BaseAssetID: baseUUID,
	}, nil)
	h := newHandler(s)

	resp, err := h.GetLatestPrice(context.Background(), connect.NewRequest(&apiv1.GetLatestPriceRequest{
		AssetId: assetUUID, BaseAssetId: baseUUID,
	}))
	require.NoError(t, err)
	assert.Equal(t, "p-1", resp.Msg.Id)
}

// TestGetLatestPrice_AnyBase verifies that omitting base_asset_id returns the asset's
// latest price in whatever base it trades against (used for portfolio cross-rate valuation).
func TestGetLatestPrice_AnyBase(t *testing.T) {
	const tradedBase = "00000000-0000-0000-0000-0000000000aa"
	const assetUUID = "00000000-0000-0000-0000-0000000000a1"
	s := &mockStore{}
	// Empty base_asset_id → store called with empty base ("any pair").
	s.On("GetLatestPrice", mock.Anything, assetUUID, "", "").Return(&entity.StoredPrice{
		ID: "p-9", AssetID: assetUUID, BaseAssetID: tradedBase,
	}, nil)
	h := newHandler(s)

	resp, err := h.GetLatestPrice(context.Background(), connect.NewRequest(&apiv1.GetLatestPriceRequest{
		AssetId: assetUUID, // no BaseAssetId
	}))
	require.NoError(t, err)
	assert.Equal(t, "p-9", resp.Msg.Id)
	assert.Equal(t, tradedBase, resp.Msg.GetBaseAssetId())
}

func TestGetLatestPrice_SymbolResolved(t *testing.T) {
	const resolvedUUID = "00000000-0000-0000-0000-000000000002"
	const assetUUID = "00000000-0000-0000-0000-0000000000a1"
	s := &mockStore{}
	// Handler passes the symbol through verbatim; the real store normalizes case.
	s.On("GetAssetBySymbol", mock.Anything, "usd").Return(&entity.Asset{ID: resolvedUUID}, nil)
	s.On("GetLatestPrice", mock.Anything, assetUUID, resolvedUUID, "").Return(&entity.StoredPrice{
		ID: "p-2", AssetID: assetUUID, BaseAssetID: resolvedUUID,
	}, nil)
	h := newHandler(s)

	resp, err := h.GetLatestPrice(context.Background(), connect.NewRequest(&apiv1.GetLatestPriceRequest{
		AssetId: assetUUID, BaseAssetId: "usd", // lowercase symbol, not UUID
	}))
	require.NoError(t, err)
	assert.Equal(t, "p-2", resp.Msg.Id)
}

func TestGetLatestPrice_SymbolNotFound(t *testing.T) {
	const assetUUID = "00000000-0000-0000-0000-0000000000a1"
	s := &mockStore{}
	s.On("GetAssetBySymbol", mock.Anything, "unknown").Return(nil, store.ErrNotFound)
	h := newHandler(s)

	_, err := h.GetLatestPrice(context.Background(), connect.NewRequest(&apiv1.GetLatestPriceRequest{
		AssetId: assetUUID, BaseAssetId: "unknown",
	}))
	require.Error(t, err)
	assert.Equal(t, connect.CodeNotFound, connect.CodeOf(err))
}

// --- Tests: Stubs return Unimplemented ---

func TestStubs_ReturnUnimplemented(t *testing.T) {
	h := newHandler(&mockStore{})
	ctx := context.Background()

	_, err := h.EnrichAssetData(ctx, connect.NewRequest(&apiv1.EnrichAssetDataRequest{}))
	assert.Equal(t, connect.CodeUnimplemented, connect.CodeOf(err))

	_, err = h.FindSimilarAssets(ctx, connect.NewRequest(&apiv1.FindSimilarAssetsRequest{}))
	assert.Equal(t, connect.CodeUnimplemented, connect.CodeOf(err))

	_, err = h.FetchExternalPrices(ctx, connect.NewRequest(&apiv1.FetchExternalPricesRequest{}))
	assert.Equal(t, connect.CodeUnimplemented, connect.CodeOf(err))
}

// --- Tests: FindOrCreateAsset ---

func TestFindOrCreateAsset_FindsExisting(t *testing.T) {
	s := &mockStore{}
	s.On("FindAssetByIdentity", mock.Anything, "BTC", "crypto", entity.AssetTypeCryptocurrency).
		Return(testAsset("id-1"), nil)
	h := newHandler(s)

	resp, err := h.FindOrCreateAsset(context.Background(), connect.NewRequest(&apiv1.FindOrCreateAssetRequest{
		Symbol: "btc", // normalized before lookup; type/market default to crypto
	}))
	require.NoError(t, err)
	assert.False(t, resp.Msg.Created)
	require.NotNil(t, resp.Msg.Asset)
	assert.Equal(t, "id-1", resp.Msg.Asset.Id)
	s.AssertNotCalled(t, "CreateAsset")
}

func TestFindOrCreateAsset_DryRunWouldCreate(t *testing.T) {
	s := &mockStore{}
	s.On("FindAssetByIdentity", mock.Anything, "NEW", "crypto", entity.AssetTypeCryptocurrency).
		Return(nil, store.ErrNotFound)
	h := newHandler(s)

	resp, err := h.FindOrCreateAsset(context.Background(), connect.NewRequest(&apiv1.FindOrCreateAssetRequest{
		Symbol: "NEW",
		DryRun: true,
	}))
	require.NoError(t, err)
	assert.True(t, resp.Msg.Created)
	assert.Nil(t, resp.Msg.Asset)
	s.AssertNotCalled(t, "CreateAsset")
}

func TestFindOrCreateAsset_Creates(t *testing.T) {
	s := &mockStore{}
	s.On("FindAssetByIdentity", mock.Anything, "NEW", "crypto", entity.AssetTypeCryptocurrency).
		Return(nil, store.ErrNotFound)
	s.On("CreateAsset", mock.Anything, mock.MatchedBy(func(a *entity.Asset) bool {
		return a.Symbol == "NEW" && a.Name == "New Token" && a.Market == "crypto" && a.Type == entity.AssetTypeCryptocurrency
	})).Return(testAsset("id-2"), nil)
	h := newHandler(s)

	name := "New Token"
	resp, err := h.FindOrCreateAsset(context.Background(), connect.NewRequest(&apiv1.FindOrCreateAssetRequest{
		Symbol: "NEW",
		Name:   &name,
	}))
	require.NoError(t, err)
	assert.True(t, resp.Msg.Created)
	require.NotNil(t, resp.Msg.Asset)
	s.AssertExpectations(t)
}

// TestFindOrCreateAsset_LosesCreationRace verifies a concurrent insert is
// resolved by reading back the winner instead of surfacing the constraint.
func TestFindOrCreateAsset_LosesCreationRace(t *testing.T) {
	s := &mockStore{}
	s.On("FindAssetByIdentity", mock.Anything, "BTC", "crypto", entity.AssetTypeCryptocurrency).
		Return(nil, store.ErrNotFound).Once()
	s.On("CreateAsset", mock.Anything, mock.Anything).Return(nil, store.ErrConstraint)
	s.On("FindAssetByIdentity", mock.Anything, "BTC", "crypto", entity.AssetTypeCryptocurrency).
		Return(testAsset("id-1"), nil).Once()
	h := newHandler(s)

	resp, err := h.FindOrCreateAsset(context.Background(), connect.NewRequest(&apiv1.FindOrCreateAssetRequest{
		Symbol: "BTC",
	}))
	require.NoError(t, err)
	assert.False(t, resp.Msg.Created)
	require.NotNil(t, resp.Msg.Asset)
	assert.Equal(t, "id-1", resp.Msg.Asset.Id)
}

func TestFindOrCreateAsset_StockRequiresMarket(t *testing.T) {
	h := newHandler(&mockStore{})

	_, err := h.FindOrCreateAsset(context.Background(), connect.NewRequest(&apiv1.FindOrCreateAssetRequest{
		Symbol: "AAPL",
		Type:   apiv1.AssetType_ASSET_TYPE_STOCK,
	}))
	require.Error(t, err)
	assert.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))
}
