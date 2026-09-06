package marketdata

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"
	apiv1 "github.com/foxcool/greedy-eye/api/v1"
	"github.com/foxcool/greedy-eye/internal/entity"
	"github.com/foxcool/greedy-eye/internal/middleware"
	"github.com/foxcool/greedy-eye/internal/scamfilter"
	"github.com/foxcool/greedy-eye/internal/store"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/timestamppb"
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

func (m *mockStore) SetAssetVerdict(ctx context.Context, assetID, verdict string, score *float64, signals map[string]float64, source string) (bool, error) {
	args := m.Called(ctx, assetID, verdict, score, signals, source)
	return args.Bool(0), args.Error(1)
}

// expectVerdict lets a FindOrCreateAsset test ignore the scoring side effect:
// every successful resolve scores the asset and persists the verdict, which is
// covered on its own elsewhere and is noise for identity-resolution tests.
func expectVerdict(s *mockStore) {
	s.On("SetAssetVerdict", mock.Anything, mock.Anything, mock.Anything,
		mock.Anything, mock.Anything, mock.Anything).Return(true, nil).Maybe()
	// Scoring asks the catalogue whether the ticker is already held; an
	// unclaimed ticker is the uninteresting default for these tests.
	s.On("FindTickerIncumbent", mock.Anything, mock.Anything).
		Return("", store.ErrNotFound).Maybe()
}

func (m *mockStore) FindTickerIncumbent(ctx context.Context, assetID string) (string, error) {
	args := m.Called(ctx, assetID)
	return args.String(0), args.Error(1)
}

func (m *mockStore) FindAssetIDByExternalRef(ctx context.Context, source, ref string) (string, error) {
	args := m.Called(ctx, source, ref)
	return args.String(0), args.Error(1)
}

func (m *mockStore) CreateAssetExternalRef(ctx context.Context, ref *entity.AssetExternalRef) (*entity.AssetExternalRef, error) {
	args := m.Called(ctx, ref)
	if v := args.Get(0); v != nil {
		return v.(*entity.AssetExternalRef), args.Error(1)
	}
	return nil, args.Error(1)
}

func (m *mockStore) ListAssets(ctx context.Context, opts ListAssetsOpts) ([]*entity.Asset, string, error) {
	args := m.Called(ctx, opts)
	if v := args.Get(0); v != nil {
		return v.([]*entity.Asset), args.String(1), args.Error(2)
	}
	return nil, args.String(1), args.Error(2)
}

func (m *mockStore) DeleteAssetExternalRef(ctx context.Context, assetID, id string) error {
	args := m.Called(ctx, assetID, id)
	return args.Error(0)
}

func (m *mockStore) ListAssetExternalRefs(ctx context.Context, assetIDs []string) ([]*entity.AssetExternalRef, error) {
	args := m.Called(ctx, assetIDs)
	if v := args.Get(0); v != nil {
		return v.([]*entity.AssetExternalRef), args.Error(1)
	}
	return nil, args.Error(1)
}

func (m *mockStore) CreateAssetRiskFlag(ctx context.Context, flag *entity.AssetRiskFlag) (*entity.AssetRiskFlag, error) {
	args := m.Called(ctx, flag)
	if v := args.Get(0); v != nil {
		return v.(*entity.AssetRiskFlag), args.Error(1)
	}
	return nil, args.Error(1)
}

func (m *mockStore) ListAssetRiskFlags(ctx context.Context, assetID string) ([]*entity.AssetRiskFlag, error) {
	args := m.Called(ctx, assetID)
	if v := args.Get(0); v != nil {
		return v.([]*entity.AssetRiskFlag), args.Error(1)
	}
	return nil, args.Error(1)
}

func (m *mockStore) DeleteAssetRiskFlag(ctx context.Context, assetID, id string) error {
	args := m.Called(ctx, assetID, id)
	return args.Error(0)
}

// expectRiskFlags lets a GetAsset test that is about something else ignore the
// axis-2 lookup, which the single-asset read always makes.
func expectRiskFlags(s *mockStore) {
	s.On("ListAssetRiskFlags", mock.Anything, mock.Anything).
		Return([]*entity.AssetRiskFlag{}, nil).Maybe()
}

// expectExternalRefs lets a pricing test ignore the ref lookup: the pricing
// path loads refs for every selected asset so providers can route contracts to
// the right chain, which is covered on its own and noise elsewhere.
func expectExternalRefs(s *mockStore) {
	s.On("ListAssetExternalRefs", mock.Anything, mock.Anything).
		Return([]*entity.AssetExternalRef{}, nil).Maybe()
}

func (m *mockStore) ListStalePricingTargets(ctx context.Context, opts StalePricingOpts) ([]*entity.Asset, error) {
	args := m.Called(ctx, opts)
	if v := args.Get(0); v != nil {
		return v.([]*entity.Asset), args.Error(1)
	}
	return nil, args.Error(1)
}

func (m *mockStore) RecordPriceAttempts(ctx context.Context, opts RecordAttemptsOpts) error {
	return m.Called(ctx, opts).Error(0)
}

func (m *mockStore) ResetPriceAttempts(ctx context.Context, sourceID string, at time.Time) (int64, error) {
	args := m.Called(ctx, sourceID, at)
	return args.Get(0).(int64), args.Error(1)
}

func (m *mockStore) SweepSchedule(ctx context.Context, opts SweepScheduleOpts) ([]*entity.SourceSchedule, error) {
	args := m.Called(ctx, opts)
	if v := args.Get(0); v != nil {
		return v.([]*entity.SourceSchedule), args.Error(1)
	}
	return nil, args.Error(1)
}

func (m *mockStore) PricingStatus(ctx context.Context, assetIDs []string) ([]*entity.AssetPricingStatus, error) {
	args := m.Called(ctx, assetIDs)
	if v := args.Get(0); v != nil {
		return v.([]*entity.AssetPricingStatus), args.Error(1)
	}
	return nil, args.Error(1)
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

// fakeContractResolver is a price provider that also confirms contract
// identity, the way the CoinGecko adapter does through its coin catalog.
type fakeContractResolver struct {
	listed map[string]listing // "<chain>/<address>" → the coin it belongs to
	err    error
	calls  int
}

// listing is what a catalogue says about one contract, for tests only.
type listing struct{ coinID, symbol string }

// listedAs describes a contract for tests that only care about the ticker it is
// published under. The coin id is derived from the symbol so that two contracts
// listed as the same ticker agree by default — the contested case is what a test
// has to state deliberately, not what it falls into by accident.
func listedAs(symbol string) listing {
	return listing{coinID: strings.ToLower(symbol) + "-coin", symbol: symbol}
}

func (f *fakeContractResolver) FetchPrices(context.Context, []*entity.Asset) ([]entity.StoredPrice, error) {
	return nil, nil
}
func (f *fakeContractResolver) BaseAssetSymbol() string         { return "USD" }
func (f *fakeContractResolver) BaseAssetType() entity.AssetType { return entity.AssetTypeForex }
func (f *fakeContractResolver) ResolveContract(_ context.Context, chain, address string) (string, string, bool, error) {
	f.calls++
	if f.err != nil {
		return "", "", false, f.err
	}
	l, ok := f.listed[strings.ToLower(chain+"/"+address)]
	return l.coinID, l.symbol, ok, nil
}

// failAfterFirstResolver answers the first lookup and fails every one after it,
// which is how a catalogue that goes down mid-decision behaves.
type failAfterFirstResolver struct {
	first listing
	err   error
	calls int
}

func (f *failAfterFirstResolver) FetchPrices(context.Context, []*entity.Asset) ([]entity.StoredPrice, error) {
	return nil, nil
}
func (f *failAfterFirstResolver) BaseAssetSymbol() string         { return "USD" }
func (f *failAfterFirstResolver) BaseAssetType() entity.AssetType { return entity.AssetTypeForex }
func (f *failAfterFirstResolver) ResolveContract(context.Context, string, string) (string, string, bool, error) {
	f.calls++
	if f.calls == 1 {
		return f.first.coinID, f.first.symbol, true, nil
	}
	return "", "", false, f.err
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
	// Not a UUID, so it resolves as a ticker — the path GetLatestPrice
	// has always taken and GetAsset now takes too.
	s.On("GetAssetBySymbol", mock.Anything, "id-x").Return(testAsset("id-x"), nil)
	h := newHandler(s)

	_, err := h.GetAsset(context.Background(), connect.NewRequest(&apiv1.GetAssetRequest{Id: "id-x"}))
	require.Error(t, err)
	assert.Equal(t, connect.CodeNotFound, connect.CodeOf(err))
}

func TestGetAsset_OK(t *testing.T) {
	s := &mockStore{}
	s.On("GetAsset", mock.Anything, "id-1").Return(testAsset("id-1"), nil)
	// Not a UUID, so it resolves as a ticker — the path GetLatestPrice
	// has always taken and GetAsset now takes too.
	s.On("GetAssetBySymbol", mock.Anything, "id-1").Return(testAsset("id-1"), nil)
	s.On("ListAssetExternalRefs", mock.Anything, []string{"id-1"}).Return(nil, nil)
	expectRiskFlags(s)
	h := newHandler(s)

	resp, err := h.GetAsset(context.Background(), connect.NewRequest(&apiv1.GetAssetRequest{Id: "id-1"}))
	require.NoError(t, err)
	assert.Equal(t, "id-1", resp.Msg.Id)
}

// TestGetAsset_CarriesTheBindings is the whole point of loading refs here: a
// card that shows a verdict without showing what the asset was bound through
// states a suspicion it cannot explain. It is also how a collapse is seen — two
// chains on one asset, which is what the AAVE row on prod looked like.
func TestGetAsset_CarriesTheBindings(t *testing.T) {
	s := &mockStore{}
	s.On("GetAsset", mock.Anything, "id-1").Return(testAsset("id-1"), nil)
	// Not a UUID, so it resolves as a ticker — the path GetLatestPrice
	// has always taken and GetAsset now takes too.
	s.On("GetAssetBySymbol", mock.Anything, "id-1").Return(testAsset("id-1"), nil)
	s.On("ListAssetExternalRefs", mock.Anything, []string{"id-1"}).Return([]*entity.AssetExternalRef{
		{ID: "ref-eth", AssetID: "id-1", Source: "onchain:ethereum", Ref: "0xreal", Origin: entity.RefOriginAuto},
		{ID: "ref-poly", AssetID: "id-1", Source: "onchain:polygon", Ref: "0xfake", Origin: entity.RefOriginAuto},
	}, nil)
	expectRiskFlags(s)
	h := newHandler(s)

	resp, err := h.GetAsset(context.Background(), connect.NewRequest(&apiv1.GetAssetRequest{Id: "id-1"}))
	require.NoError(t, err)
	require.Len(t, resp.Msg.ExternalRefs, 2)
	assert.Equal(t, "onchain:ethereum", resp.Msg.ExternalRefs[0].Source)
	assert.Equal(t, "0xfake", resp.Msg.ExternalRefs[1].Ref)
	assert.Equal(t, "ref-poly", resp.Msg.ExternalRefs[1].Id, "the id is what DeleteAssetExternalRef needs")
}

// TestGetAsset_SurvivesUnreadableBindings: the refs are context, not the asset.
func TestGetAsset_SurvivesUnreadableBindings(t *testing.T) {
	s := &mockStore{}
	s.On("GetAsset", mock.Anything, "id-1").Return(testAsset("id-1"), nil)
	// Not a UUID, so it resolves as a ticker — the path GetLatestPrice
	// has always taken and GetAsset now takes too.
	s.On("GetAssetBySymbol", mock.Anything, "id-1").Return(testAsset("id-1"), nil)
	s.On("ListAssetExternalRefs", mock.Anything, []string{"id-1"}).Return(nil, assert.AnError)
	expectRiskFlags(s)
	h := newHandler(s)

	resp, err := h.GetAsset(context.Background(), connect.NewRequest(&apiv1.GetAssetRequest{Id: "id-1"}))
	require.NoError(t, err)
	assert.Empty(t, resp.Msg.ExternalRefs)
}

// TestGetAsset_ResolvesATicker: the display currency is named by symbol in the
// valuation setting, and a valuation resolves it here once before pricing
// anything. GetLatestPrice and ListPriceHistory already accepted a ticker; this
// endpoint was the one that did not.
func TestGetAsset_ResolvesATicker(t *testing.T) {
	s := &mockStore{}
	s.On("GetAssetBySymbol", mock.Anything, "USD").Return(testAsset("id-usd"), nil)
	s.On("GetAsset", mock.Anything, "id-usd").Return(testAsset("id-usd"), nil)
	s.On("ListAssetExternalRefs", mock.Anything, []string{"id-usd"}).Return(nil, nil)
	expectRiskFlags(s)
	h := newHandler(s)

	resp, err := h.GetAsset(context.Background(), connect.NewRequest(&apiv1.GetAssetRequest{Id: "USD"}))
	require.NoError(t, err)
	assert.Equal(t, "id-usd", resp.Msg.Id, "the caller gets the id price rows are keyed by")
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
	expectVerdict(s)
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
	expectVerdict(s)
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
	expectVerdict(s)
	h := newHandler(s)

	resp, err := h.FindOrCreateAsset(context.Background(), connect.NewRequest(&apiv1.FindOrCreateAssetRequest{
		Symbol: "BTC",
	}))
	require.NoError(t, err)
	assert.False(t, resp.Msg.Created)
	require.NotNil(t, resp.Msg.Asset)
	assert.Equal(t, "id-1", resp.Msg.Asset.Id)
}

// TestFindOrCreateAsset_ResolvesByExternalRef: a bound contract wins over symbol
// matching, so a scam clone of a real ticker resolves to its own asset and the
// symbol identity is never even consulted.
func TestFindOrCreateAsset_ResolvesByExternalRef(t *testing.T) {
	s := &mockStore{}
	s.On("FindAssetIDByExternalRef", mock.Anything, "onchain:eth", "0xCAFE").Return("id-9", nil)
	s.On("GetAsset", mock.Anything, "id-9").Return(testAsset("id-9"), nil)
	expectVerdict(s)
	h := newHandler(s)

	source, ref := "onchain:eth", "0xCAFE"
	resp, err := h.FindOrCreateAsset(context.Background(), connect.NewRequest(&apiv1.FindOrCreateAssetRequest{
		Symbol:            "USDT",
		ExternalRefSource: &source,
		ExternalRef:       &ref,
	}))
	require.NoError(t, err)
	assert.False(t, resp.Msg.Created)
	require.NotNil(t, resp.Msg.Asset)
	assert.Equal(t, "id-9", resp.Msg.Asset.Id)
	s.AssertNotCalled(t, "FindAssetByIdentity")
	s.AssertNotCalled(t, "CreateAsset")
}

// TestFindOrCreateAsset_TickerCollisionScoresImpersonation: the catalogue, not
// the text, is what unmasks a lookalike. When the ticker is already held on this
// chain by an older listed asset with another contract, the verdict is
// impersonation and the sync path derives holdings.excluded from it.
func TestFindOrCreateAsset_TickerCollisionScoresImpersonation(t *testing.T) {
	s := &mockStore{}
	s.On("FindAssetIDByExternalRef", mock.Anything, "onchain:eth", "0x7f1ffe63").Return("id-fake", nil)
	s.On("GetAsset", mock.Anything, "id-fake").Return(testAsset("id-fake"), nil)
	s.On("FindTickerIncumbent", mock.Anything, "id-fake").Return("id-tether", nil)

	var gotVerdict string
	var gotSignals map[string]float64
	s.On("SetAssetVerdict", mock.Anything, "id-fake", mock.Anything, mock.Anything, mock.Anything, rescoreVerdictSource).
		Run(func(args mock.Arguments) {
			gotVerdict = args.String(2)
			gotSignals = args.Get(4).(map[string]float64)
		}).
		Return(true, nil)
	h := newHandler(s)

	source, ref := "onchain:eth", "0x7f1ffe63"
	resp, err := h.FindOrCreateAsset(context.Background(), connect.NewRequest(&apiv1.FindOrCreateAssetRequest{
		Symbol:            "USDT",
		ExternalRefSource: &source,
		ExternalRef:       &ref,
	}))
	require.NoError(t, err)
	assert.Equal(t, string(scamfilter.VerdictImpersonation), gotVerdict)
	assert.Contains(t, gotSignals, scamfilter.SignalTickerCollision)
	assert.Equal(t, string(scamfilter.VerdictImpersonation), resp.Msg.Asset.GetIdentityVerdict(),
		"the sync path reads the verdict off the response to quarantine the holding")
}

// TestFindOrCreateAsset_TickerIncumbentLookupFailureIsNotAVerdict: the signal is
// terminal, so a failed query must not condemn. Missing one impostor until the
// next rescore is the cheaper error.
func TestFindOrCreateAsset_TickerIncumbentLookupFailureIsNotAVerdict(t *testing.T) {
	s := &mockStore{}
	s.On("FindAssetIDByExternalRef", mock.Anything, "onchain:eth", "0xCAFE").Return("id-9", nil)
	s.On("GetAsset", mock.Anything, "id-9").Return(testAsset("id-9"), nil)
	s.On("FindTickerIncumbent", mock.Anything, "id-9").Return("", errors.New("connection reset"))

	var gotVerdict string
	s.On("SetAssetVerdict", mock.Anything, "id-9", mock.Anything, mock.Anything, mock.Anything, rescoreVerdictSource).
		Run(func(args mock.Arguments) { gotVerdict = args.String(2) }).
		Return(true, nil)
	h := newHandler(s)

	source, ref := "onchain:eth", "0xCAFE"
	_, err := h.FindOrCreateAsset(context.Background(), connect.NewRequest(&apiv1.FindOrCreateAssetRequest{
		Symbol:            "USDT",
		ExternalRefSource: &source,
		ExternalRef:       &ref,
	}))
	require.NoError(t, err)
	assert.Equal(t, string(scamfilter.VerdictLegit), gotVerdict)
}

// TestFindOrCreateAsset_RefResolvesWithoutSymbol: a provider that reports a
// balance with an empty symbol still names the token by address, and the
// catalogue already knows that address. Refusing it cost the caller twice — the
// position was dropped, and its account's snapshot counted as incomplete, which
// kept a sync from removing positions that really were gone (personal-bjvc).
func TestFindOrCreateAsset_RefResolvesWithoutSymbol(t *testing.T) {
	s := &mockStore{}
	s.On("FindAssetIDByExternalRef", mock.Anything, "onchain:eth", "0xCAFE").Return("id-9", nil)
	s.On("GetAsset", mock.Anything, "id-9").Return(testAsset("id-9"), nil)
	expectVerdict(s)
	h := newHandler(s)

	source, ref := "onchain:eth", "0xCAFE"
	resp, err := h.FindOrCreateAsset(context.Background(), connect.NewRequest(&apiv1.FindOrCreateAssetRequest{
		ExternalRefSource: &source,
		ExternalRef:       &ref,
	}))
	require.NoError(t, err)
	assert.Equal(t, "id-9", resp.Msg.Asset.Id)
	s.AssertNotCalled(t, "CreateAsset")
}

// TestFindOrCreateAsset_UnknownRefStillNeedsSymbol: past the ref lookup there is
// nothing left to identify the token with. Inventing an asset from an address
// alone would put a nameless row in the catalogue and in the portfolio.
func TestFindOrCreateAsset_UnknownRefStillNeedsSymbol(t *testing.T) {
	s := &mockStore{}
	s.On("FindAssetIDByExternalRef", mock.Anything, "onchain:eth", "0xNEW").Return("", store.ErrNotFound)
	h := newHandler(s)

	source, ref := "onchain:eth", "0xNEW"
	_, err := h.FindOrCreateAsset(context.Background(), connect.NewRequest(&apiv1.FindOrCreateAssetRequest{
		ExternalRefSource: &source,
		ExternalRef:       &ref,
	}))
	require.Error(t, err)
	assert.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))
	s.AssertNotCalled(t, "CreateAsset")
}

// TestFindOrCreateAsset_BindsRefOnIdentityMatch: an unbound contract the
// provider lists under the same ticker is the real token on another chain, so it
// resolves by symbol and gets bound — the next sync short-circuits on the ref
// and cross-chain contracts of one asset collapse together.
func TestFindOrCreateAsset_BindsRefOnIdentityMatch(t *testing.T) {
	s := &mockStore{}
	s.On("FindAssetIDByExternalRef", mock.Anything, "onchain:bsc", "0xBEEF").Return("", store.ErrNotFound)
	s.On("FindAssetByIdentity", mock.Anything, "USDC", "crypto", entity.AssetTypeCryptocurrency).
		Return(testAsset("id-1"), nil)
	s.On("CreateAssetExternalRef", mock.Anything, mock.MatchedBy(func(r *entity.AssetExternalRef) bool {
		return r.AssetID == "id-1" && r.Source == "onchain:bsc" && r.Ref == "0xBEEF" && r.Origin == entity.RefOriginAuto
	})).Return(&entity.AssetExternalRef{}, nil)
	// Nothing else is bound on bsc, so there is no rival to disagree with.
	s.On("ListAssetExternalRefs", mock.Anything, []string{"id-1"}).Return(nil, nil)
	expectVerdict(s)
	resolver := &fakeContractResolver{listed: map[string]listing{"bsc/0xbeef": listedAs("USDC")}}
	h := newHandler(s).WithProvider("coingecko", resolver)

	source, ref := "onchain:bsc", "0xBEEF"
	resp, err := h.FindOrCreateAsset(context.Background(), connect.NewRequest(&apiv1.FindOrCreateAssetRequest{
		Symbol:            "USDC",
		ExternalRefSource: &source,
		ExternalRef:       &ref,
	}))
	require.NoError(t, err)
	assert.False(t, resp.Msg.Created)
	assert.Equal(t, "id-1", resp.Msg.Asset.Id)
	assert.Equal(t, 1, resolver.calls, "an unbound contract must be confirmed before it claims a ticker")
	s.AssertExpectations(t)
}

// TestFindOrCreateAsset_BindsRefOnCreate: a brand-new confirmed contract creates
// the asset in the global market and binds the ref to it.
func TestFindOrCreateAsset_BindsRefOnCreate(t *testing.T) {
	s := &mockStore{}
	s.On("FindAssetIDByExternalRef", mock.Anything, "onchain:solana", "MintX").Return("", store.ErrNotFound)
	s.On("FindAssetByIdentity", mock.Anything, "WIF", "crypto", entity.AssetTypeCryptocurrency).
		Return(nil, store.ErrNotFound)
	// The contract is mirrored as a tag so coingecko can still price the token.
	s.On("CreateAsset", mock.Anything, mock.MatchedBy(func(a *entity.Asset) bool {
		return slices.Contains(a.Tags, "contract:MintX") && a.Market == entity.MarketCrypto
	})).Return(testAsset("id-2"), nil)
	s.On("CreateAssetExternalRef", mock.Anything, mock.MatchedBy(func(r *entity.AssetExternalRef) bool {
		return r.AssetID == "id-2" && r.Source == "onchain:solana" && r.Ref == "MintX"
	})).Return(&entity.AssetExternalRef{}, nil)
	expectVerdict(s)
	h := newHandler(s).WithProvider("coingecko",
		&fakeContractResolver{listed: map[string]listing{"solana/mintx": listedAs("WIF")}})

	source, ref := "onchain:solana", "MintX"
	resp, err := h.FindOrCreateAsset(context.Background(), connect.NewRequest(&apiv1.FindOrCreateAssetRequest{
		Symbol:            "WIF",
		ExternalRefSource: &source,
		ExternalRef:       &ref,
	}))
	require.NoError(t, err)
	assert.True(t, resp.Msg.Created)
	assert.Equal(t, "id-2", resp.Msg.Asset.Id)
	s.AssertExpectations(t)
}

// TestFindOrCreateAsset_UnlistedContractDoesNotClaimTicker is the regression for
// personal-c3b: a counterfeit carrying a well-known ticker must not resolve to
// the genuine asset. Its contract is listed nowhere, so it gets an asset of its
// own keyed by that contract — the genuine identity is never looked up, nothing
// is bound to it, and its price cannot leak onto the fake.
func TestFindOrCreateAsset_UnlistedContractDoesNotClaimTicker(t *testing.T) {
	const fake = "0x1280d5752daf7d2ff4f14b31e5c3b2d02cbc0e1f"
	s := &mockStore{}
	s.On("FindAssetIDByExternalRef", mock.Anything, "onchain:bsc", fake).Return("", store.ErrNotFound)
	s.On("FindAssetByIdentity", mock.Anything, "USDT", "onchain:bsc/"+fake, entity.AssetTypeCryptocurrency).
		Return(nil, store.ErrNotFound)
	s.On("CreateAsset", mock.Anything, mock.MatchedBy(func(a *entity.Asset) bool {
		return a.Symbol == "USDT" && a.Market == "onchain:bsc/"+fake
	})).Return(testAsset("id-fake"), nil)
	s.On("CreateAssetExternalRef", mock.Anything, mock.MatchedBy(func(r *entity.AssetExternalRef) bool {
		return r.AssetID == "id-fake" && r.Ref == fake
	})).Return(&entity.AssetExternalRef{}, nil)
	expectVerdict(s)
	// The catalog knows the real BSC-USD contract, not this one.
	h := newHandler(s).WithProvider("coingecko", &fakeContractResolver{
		listed: map[string]listing{"bsc/0x55d398326f99059ff775485246999027b3197955": listedAs("USDT")},
	})

	source, ref := "onchain:bsc", fake
	resp, err := h.FindOrCreateAsset(context.Background(), connect.NewRequest(&apiv1.FindOrCreateAssetRequest{
		Symbol:            "USDT",
		ExternalRefSource: &source,
		ExternalRef:       &ref,
	}))
	require.NoError(t, err)
	assert.True(t, resp.Msg.Created)
	s.AssertNotCalled(t, "FindAssetByIdentity", mock.Anything, "USDT", "crypto", entity.AssetTypeCryptocurrency)
	s.AssertExpectations(t)
}

// TestFindOrCreateAsset_ContractListedUnderAnotherSymbol: the contract exists but
// belongs to a different coin than the balance claims. Trusting the claimed
// ticker would attach the wrong price, so the token is isolated as well.
func TestFindOrCreateAsset_ContractListedUnderAnotherSymbol(t *testing.T) {
	s := &mockStore{}
	s.On("FindAssetIDByExternalRef", mock.Anything, "onchain:eth", "0xAAA").Return("", store.ErrNotFound)
	s.On("FindAssetByIdentity", mock.Anything, "USDT", "onchain:eth/0xaaa", entity.AssetTypeCryptocurrency).
		Return(nil, store.ErrNotFound)
	s.On("CreateAsset", mock.Anything, mock.MatchedBy(func(a *entity.Asset) bool {
		return a.Market == "onchain:eth/0xaaa"
	})).Return(testAsset("id-3"), nil)
	s.On("CreateAssetExternalRef", mock.Anything, mock.Anything).Return(&entity.AssetExternalRef{}, nil)
	expectVerdict(s)
	h := newHandler(s).WithProvider("coingecko",
		&fakeContractResolver{listed: map[string]listing{"eth/0xaaa": listedAs("USDC")}})

	source, ref := "onchain:eth", "0xAAA"
	_, err := h.FindOrCreateAsset(context.Background(), connect.NewRequest(&apiv1.FindOrCreateAssetRequest{
		Symbol:            "USDT",
		ExternalRefSource: &source,
		ExternalRef:       &ref,
	}))
	require.NoError(t, err)
	s.AssertExpectations(t)
}

// TestFindOrCreateAsset_IsolatesContractWithoutResolver: with no provider able to
// confirm identity, nothing may claim an existing ticker on a bare symbol match.
func TestFindOrCreateAsset_IsolatesContractWithoutResolver(t *testing.T) {
	s := &mockStore{}
	s.On("FindAssetIDByExternalRef", mock.Anything, "onchain:bsc", "0xBEEF").Return("", store.ErrNotFound)
	s.On("FindAssetByIdentity", mock.Anything, "USDC", "onchain:bsc/0xbeef", entity.AssetTypeCryptocurrency).
		Return(nil, store.ErrNotFound)
	s.On("CreateAsset", mock.Anything, mock.Anything).Return(testAsset("id-4"), nil)
	s.On("CreateAssetExternalRef", mock.Anything, mock.Anything).Return(&entity.AssetExternalRef{}, nil)
	expectVerdict(s)
	h := newHandler(s)

	source, ref := "onchain:bsc", "0xBEEF"
	_, err := h.FindOrCreateAsset(context.Background(), connect.NewRequest(&apiv1.FindOrCreateAssetRequest{
		Symbol:            "USDC",
		ExternalRefSource: &source,
		ExternalRef:       &ref,
	}))
	require.NoError(t, err)
	s.AssertExpectations(t)
}

// TestFindOrCreateAsset_ContractCatalogUnavailable: an unreachable provider is not
// evidence of anything. Failing the call keeps the sync loud and the catalog
// clean instead of scattering genuine tokens into per-contract rows for good.
func TestFindOrCreateAsset_ContractCatalogUnavailable(t *testing.T) {
	s := &mockStore{}
	s.On("FindAssetIDByExternalRef", mock.Anything, "onchain:eth", "0xCAFE").Return("", store.ErrNotFound)
	h := newHandler(s).WithProvider("coingecko",
		&fakeContractResolver{err: errors.New("coingecko unreachable")})

	source, ref := "onchain:eth", "0xCAFE"
	_, err := h.FindOrCreateAsset(context.Background(), connect.NewRequest(&apiv1.FindOrCreateAssetRequest{
		Symbol:            "USDT",
		ExternalRefSource: &source,
		ExternalRef:       &ref,
	}))
	require.Error(t, err)
	assert.Equal(t, connect.CodeUnavailable, connect.CodeOf(err))
	s.AssertNotCalled(t, "FindAssetByIdentity")
	s.AssertNotCalled(t, "CreateAsset")
}

// TestFindOrCreateAsset_SecondCoinOnOneChainDoesNotJoin is personal-dvgm. CAT on
// bsc is two coins: catcoin-cash and cat-inu. Both are listed, both under "CAT",
// so both pass the ticker test the guard had — and both landed on one asset,
// whose balance became the sum of two unrelated tokens priced as one. The asset
// already carries catcoin-cash's contract on bsc, so cat-inu's is refused the
// shared identity and gets a market of its own.
func TestFindOrCreateAsset_SecondCoinOnOneChainDoesNotJoin(t *testing.T) {
	const catCash, catInu = "0x59f4f336bf3d0c49dbfba4a74ebd2a6ace40539a", "0xaf8e0bce56615edf2810fab024c307de352a431f"
	s := &mockStore{}
	s.On("FindAssetIDByExternalRef", mock.Anything, "onchain:bsc", catInu).Return("", store.ErrNotFound)
	// The asset holding CAT in the global market is the coin the newcomer claims
	// to be; what it is already bound to on bsc is the question.
	s.On("FindAssetByIdentity", mock.Anything, "CAT", "crypto", entity.AssetTypeCryptocurrency).
		Return(testAsset("id-cat"), nil)
	s.On("ListAssetExternalRefs", mock.Anything, []string{"id-cat"}).Return([]*entity.AssetExternalRef{
		{AssetID: "id-cat", Source: "onchain:bsc", Ref: catCash},
	}, nil)
	// Refused: it lands on its own contract market instead.
	s.On("FindAssetByIdentity", mock.Anything, "CAT", "onchain:bsc/"+catInu, entity.AssetTypeCryptocurrency).
		Return(nil, store.ErrNotFound)
	s.On("CreateAsset", mock.Anything, mock.MatchedBy(func(a *entity.Asset) bool {
		return a.Symbol == "CAT" && a.Market == "onchain:bsc/"+catInu
	})).Return(testAsset("id-cat-inu"), nil)
	s.On("CreateAssetExternalRef", mock.Anything, mock.MatchedBy(func(r *entity.AssetExternalRef) bool {
		return r.AssetID == "id-cat-inu" && r.Source == "onchain:bsc" && r.Ref == catInu
	})).Return(&entity.AssetExternalRef{}, nil)
	expectVerdict(s)
	h := newHandler(s).WithProvider("coingecko", &fakeContractResolver{
		listed: map[string]listing{
			"bsc/" + catCash: {coinID: "catcoin-cash", symbol: "CAT"},
			"bsc/" + catInu:  {coinID: "cat-inu", symbol: "CAT"},
		},
	})

	source, ref := "onchain:bsc", catInu
	resp, err := h.FindOrCreateAsset(context.Background(), connect.NewRequest(&apiv1.FindOrCreateAssetRequest{
		Symbol:            "CAT",
		ExternalRefSource: &source,
		ExternalRef:       &ref,
	}))
	require.NoError(t, err)
	assert.True(t, resp.Msg.Created, "the second coin becomes its own asset")
	// The incumbent must be left exactly as it was: nothing bound to it.
	s.AssertNotCalled(t, "CreateAssetExternalRef", mock.Anything,
		mock.MatchedBy(func(r *entity.AssetExternalRef) bool { return r.AssetID == "id-cat" }))
	s.AssertExpectations(t)
}

// TestFindOrCreateAsset_BridgedDeploymentOnAnotherChainStillJoins is the
// counterexample that decided the shape of the rule, measured on dev 2026-09-06.
//
// A bridge's deployment is listed as a coin of its own: Base carries USDT as
// l2-standard-bridged-usdt-base, not as tether. It is still the asset's USDT —
// one token, several chains — which is exactly the collapse the guard exists to
// allow. So the ids are compared only among contracts on the SAME chain. An
// earlier draft compared against one id stored on the asset and isolated REAL
// Tether on Ethereum, because the bridged listing had been resolved first and
// "first" is only ever the sync's arrival order.
func TestFindOrCreateAsset_BridgedDeploymentOnAnotherChainStillJoins(t *testing.T) {
	const onEth, onBase = "0xdac17f958d2ee523a2206206994597c13d831ec7", "0xfde4c96c8593536e31f229ea8f37b2ada2699bb2"
	s := &mockStore{}
	s.On("FindAssetIDByExternalRef", mock.Anything, "onchain:eth", onEth).Return("", store.ErrNotFound)
	s.On("FindAssetByIdentity", mock.Anything, "USDT", "crypto", entity.AssetTypeCryptocurrency).
		Return(testAsset("id-usdt"), nil)
	// The asset's only other contract is on another chain, under another coin id.
	s.On("ListAssetExternalRefs", mock.Anything, []string{"id-usdt"}).Return([]*entity.AssetExternalRef{
		{AssetID: "id-usdt", Source: "onchain:base", Ref: onBase},
	}, nil)
	s.On("CreateAssetExternalRef", mock.Anything, mock.MatchedBy(func(r *entity.AssetExternalRef) bool {
		return r.AssetID == "id-usdt" && r.Source == "onchain:eth" && r.Ref == onEth
	})).Return(&entity.AssetExternalRef{}, nil)
	expectVerdict(s)
	h := newHandler(s).WithProvider("coingecko", &fakeContractResolver{
		listed: map[string]listing{
			"eth/" + onEth:   {coinID: "tether", symbol: "USDT"},
			"base/" + onBase: {coinID: "l2-standard-bridged-usdt-base", symbol: "USDT"},
		},
	})

	source, ref := "onchain:eth", onEth
	resp, err := h.FindOrCreateAsset(context.Background(), connect.NewRequest(&apiv1.FindOrCreateAssetRequest{
		Symbol:            "USDT",
		ExternalRefSource: &source,
		ExternalRef:       &ref,
	}))
	require.NoError(t, err)
	assert.False(t, resp.Msg.Created, "one token on several chains stays one asset")
	assert.Equal(t, "id-usdt", resp.Msg.Asset.Id)
	s.AssertNotCalled(t, "CreateAsset")
	s.AssertExpectations(t)
}

// TestFindOrCreateAsset_UnlistedSiblingIsNoObjection guards the LP exception.
// UNI-V2 and CAKE-LP give every pool its own contract under one symbol on one
// chain, so an asset legitimately spanning several contracts on ONE chain does
// exist. None of those pools is listed anywhere, and an unlisted sibling asserts
// nothing — silence is not disagreement, and the ticket forbids "fixing" them
// with a symbol heuristic.
func TestFindOrCreateAsset_UnlistedSiblingIsNoObjection(t *testing.T) {
	const pool, listedPool = "0xbb2b8038a1640196fbe3e38816f3e67cba72d940", "0xd3d2e2692501a5c9ca623199d38826e513033a17"
	s := &mockStore{}
	s.On("FindAssetIDByExternalRef", mock.Anything, "onchain:eth", pool).Return("", store.ErrNotFound)
	s.On("FindAssetByIdentity", mock.Anything, "UNI-V2", "crypto", entity.AssetTypeCryptocurrency).
		Return(testAsset("id-lp"), nil)
	s.On("ListAssetExternalRefs", mock.Anything, []string{"id-lp"}).Return([]*entity.AssetExternalRef{
		{AssetID: "id-lp", Source: "onchain:eth", Ref: listedPool},
	}, nil)
	s.On("CreateAssetExternalRef", mock.Anything, mock.MatchedBy(func(r *entity.AssetExternalRef) bool {
		return r.AssetID == "id-lp" && r.Source == "onchain:eth" && r.Ref == pool
	})).Return(&entity.AssetExternalRef{}, nil)
	expectVerdict(s)
	// The incoming pool is listed (so it gets past the ticker test); its sibling
	// is not, which is the ordinary shape for pools.
	h := newHandler(s).WithProvider("coingecko", &fakeContractResolver{
		listed: map[string]listing{"eth/" + pool: {coinID: "uniswap-v2", symbol: "UNI-V2"}},
	})

	source, ref := "onchain:eth", pool
	resp, err := h.FindOrCreateAsset(context.Background(), connect.NewRequest(&apiv1.FindOrCreateAssetRequest{
		Symbol:            "UNI-V2",
		ExternalRefSource: &source,
		ExternalRef:       &ref,
	}))
	require.NoError(t, err)
	assert.False(t, resp.Msg.Created)
	s.AssertExpectations(t)
}

// TestFindOrCreateAsset_HeldContractUnreachableFailsLoud: the sibling lookup is
// held to the rule the incoming contract already is. An unreachable catalogue is
// not evidence about anything, and reading it as "no rival" would let a
// contested contract in on the day the provider is down.
func TestFindOrCreateAsset_HeldContractUnreachableFailsLoud(t *testing.T) {
	s := &mockStore{}
	s.On("FindAssetIDByExternalRef", mock.Anything, "onchain:bsc", "0xNEW").Return("", store.ErrNotFound)
	s.On("FindAssetByIdentity", mock.Anything, "CAT", "crypto", entity.AssetTypeCryptocurrency).
		Return(testAsset("id-cat"), nil)
	s.On("ListAssetExternalRefs", mock.Anything, []string{"id-cat"}).Return([]*entity.AssetExternalRef{
		{AssetID: "id-cat", Source: "onchain:bsc", Ref: "0xOLD"},
	}, nil)
	h := newHandler(s).WithProvider("coingecko", &failAfterFirstResolver{
		first: listing{coinID: "catcoin-cash", symbol: "CAT"},
		err:   errors.New("coingecko unreachable"),
	})

	source, ref := "onchain:bsc", "0xNEW"
	_, err := h.FindOrCreateAsset(context.Background(), connect.NewRequest(&apiv1.FindOrCreateAssetRequest{
		Symbol:            "CAT",
		ExternalRefSource: &source,
		ExternalRef:       &ref,
	}))
	require.Error(t, err)
	assert.Equal(t, connect.CodeUnavailable, connect.CodeOf(err))
	s.AssertNotCalled(t, "CreateAsset")
}

func TestSetAssetVerdict_AdminSetsUserVerdict(t *testing.T) {
	s := &mockStore{}
	s.On("SetAssetVerdict", mock.Anything, "id-1", "scam",
		(*float64)(nil), map[string]float64(nil), "user:admin-1").Return(true, nil)
	s.On("GetAsset", mock.Anything, "id-1").Return(testAsset("id-1"), nil)
	h := newHandler(s)

	ctx := middleware.ContextWithUser(context.Background(), &entity.User{ID: "admin-1", Roles: []string{"admin"}})
	resp, err := h.SetAssetVerdict(ctx, connect.NewRequest(&apiv1.SetAssetVerdictRequest{
		AssetId: "id-1", Verdict: "scam",
	}))
	require.NoError(t, err)
	assert.Equal(t, "id-1", resp.Msg.Id)
	s.AssertExpectations(t)
}

func TestSetAssetVerdict_NonAdminDenied(t *testing.T) {
	h := newHandler(&mockStore{})
	ctx := middleware.ContextWithUser(context.Background(), &entity.User{ID: "u-1"})
	_, err := h.SetAssetVerdict(ctx, connect.NewRequest(&apiv1.SetAssetVerdictRequest{
		AssetId: "id-1", Verdict: "scam",
	}))
	assert.Equal(t, connect.CodePermissionDenied, connect.CodeOf(err))
}

func TestSetAssetVerdict_RejectsUnknownVerdict(t *testing.T) {
	h := newHandler(&mockStore{})
	ctx := middleware.ContextWithUser(context.Background(), &entity.User{ID: "admin-1", Roles: []string{"admin"}})
	for _, v := range []string{"unknown", "", "bogus"} {
		_, err := h.SetAssetVerdict(ctx, connect.NewRequest(&apiv1.SetAssetVerdictRequest{
			AssetId: "id-1", Verdict: v,
		}))
		assert.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err), "verdict %q", v)
	}
}

// --- Tests: asset risk flags (axis 2) ---

func TestAddAssetRiskFlag_AdminWritesFlag(t *testing.T) {
	review := time.Now().Add(48 * time.Hour).UTC().Truncate(time.Second)
	s := &mockStore{}
	s.On("CreateAssetRiskFlag", mock.Anything, mock.MatchedBy(func(f *entity.AssetRiskFlag) bool {
		return f.AssetID == "id-1" && f.Kind == "depeg" && f.ActionHint == "exit_soon" &&
			f.SetBy == "user:admin-1" && f.ReviewAt != nil && f.ReviewAt.Equal(review)
	})).Return(&entity.AssetRiskFlag{
		ID: "flag-1", AssetID: "id-1", Kind: "depeg", ActionHint: "exit_soon",
		ReviewAt: &review, SetBy: "user:admin-1", CreatedAt: time.Now(),
	}, nil)
	h := newHandler(s)

	hint := "exit_soon"
	resp, err := h.AddAssetRiskFlag(adminCtx(), connect.NewRequest(&apiv1.AddAssetRiskFlagRequest{
		AssetId: "id-1", Kind: "depeg", ActionHint: &hint, ReviewAt: timestamppb.New(review),
	}))
	require.NoError(t, err)
	assert.Equal(t, "flag-1", resp.Msg.Id)
	assert.Equal(t, "user:admin-1", resp.Msg.GetSetBy())
	s.AssertExpectations(t)
}

// An omitted action hint is "none": the flag says something happened without
// claiming to know what to do about it yet.
func TestAddAssetRiskFlag_DefaultsActionHintToNone(t *testing.T) {
	review := time.Now().Add(24 * time.Hour)
	s := &mockStore{}
	s.On("CreateAssetRiskFlag", mock.Anything, mock.MatchedBy(func(f *entity.AssetRiskFlag) bool {
		return f.ActionHint == "none"
	})).Return(&entity.AssetRiskFlag{ID: "flag-1", AssetID: "id-1", Kind: "exploit"}, nil)
	h := newHandler(s)

	_, err := h.AddAssetRiskFlag(adminCtx(), connect.NewRequest(&apiv1.AddAssetRiskFlagRequest{
		AssetId: "id-1", Kind: "exploit", ReviewAt: timestamppb.New(review),
	}))
	require.NoError(t, err)
	s.AssertExpectations(t)
}

func TestAddAssetRiskFlag_NonAdminDenied(t *testing.T) {
	h := newHandler(&mockStore{})
	ctx := middleware.ContextWithUser(context.Background(), &entity.User{ID: "u-1"})
	_, err := h.AddAssetRiskFlag(ctx, connect.NewRequest(&apiv1.AddAssetRiskFlagRequest{
		AssetId: "id-1", Kind: "depeg", ReviewAt: timestamppb.New(time.Now()),
	}))
	assert.Equal(t, connect.CodePermissionDenied, connect.CodeOf(err))
}

func TestAddAssetRiskFlag_RejectsUnknownKindAndHint(t *testing.T) {
	h := newHandler(&mockStore{})
	for _, kind := range []string{"", "rug", "scam"} {
		_, err := h.AddAssetRiskFlag(adminCtx(), connect.NewRequest(&apiv1.AddAssetRiskFlagRequest{
			AssetId: "id-1", Kind: kind, ReviewAt: timestamppb.New(time.Now()),
		}))
		assert.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err), "kind %q", kind)
	}

	bogus := "sell_everything"
	_, err := h.AddAssetRiskFlag(adminCtx(), connect.NewRequest(&apiv1.AddAssetRiskFlagRequest{
		AssetId: "id-1", Kind: "depeg", ActionHint: &bogus, ReviewAt: timestamppb.New(time.Now()),
	}))
	assert.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))
}

// The invariant that keeps axis 2 worth having: no review date, no flag.
func TestAddAssetRiskFlag_RequiresReviewDate(t *testing.T) {
	h := newHandler(&mockStore{})
	_, err := h.AddAssetRiskFlag(adminCtx(), connect.NewRequest(&apiv1.AddAssetRiskFlagRequest{
		AssetId: "id-1", Kind: "delisting",
	}))
	assert.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))
}

func TestDeleteAssetRiskFlag_AdminOnlyAndNamesBothSides(t *testing.T) {
	s := &mockStore{}
	s.On("DeleteAssetRiskFlag", mock.Anything, "id-1", "flag-1").Return(nil)
	h := newHandler(s)

	_, err := h.DeleteAssetRiskFlag(adminCtx(), connect.NewRequest(&apiv1.DeleteAssetRiskFlagRequest{
		AssetId: "id-1", Id: "flag-1",
	}))
	require.NoError(t, err)
	s.AssertExpectations(t)

	ctx := middleware.ContextWithUser(context.Background(), &entity.User{ID: "u-1"})
	_, err = h.DeleteAssetRiskFlag(ctx, connect.NewRequest(&apiv1.DeleteAssetRiskFlagRequest{
		AssetId: "id-1", Id: "flag-1",
	}))
	assert.Equal(t, connect.CodePermissionDenied, connect.CodeOf(err))
}

func TestGetAsset_AttachesRiskFlags(t *testing.T) {
	review := time.Now().Add(72 * time.Hour)
	s := &mockStore{}
	s.On("GetAsset", mock.Anything, "id-1").Return(testAsset("id-1"), nil)
	// Not a UUID, so it resolves as a ticker — the path GetLatestPrice
	// has always taken and GetAsset now takes too.
	s.On("GetAssetBySymbol", mock.Anything, "id-1").Return(testAsset("id-1"), nil)
	s.On("ListAssetExternalRefs", mock.Anything, []string{"id-1"}).Return(nil, nil)
	s.On("ListAssetRiskFlags", mock.Anything, "id-1").Return([]*entity.AssetRiskFlag{
		{ID: "flag-1", AssetID: "id-1", Kind: "frozen_transfers", ActionHint: "hold", ReviewAt: &review},
	}, nil)
	h := newHandler(s)

	resp, err := h.GetAsset(context.Background(), connect.NewRequest(&apiv1.GetAssetRequest{Id: "id-1"}))
	require.NoError(t, err)
	require.Len(t, resp.Msg.RiskFlags, 1)
	assert.Equal(t, "frozen_transfers", resp.Msg.RiskFlags[0].Kind)
	assert.Equal(t, "hold", resp.Msg.RiskFlags[0].GetActionHint())
	s.AssertExpectations(t)
}

// Best-effort, like the bindings: a flag lookup that fails costs the card its
// flags, never the asset. Nothing is miscounted by the omission because a flag
// never enters a number in the first place.
func TestGetAsset_RiskFlagFailureKeepsAsset(t *testing.T) {
	s := &mockStore{}
	s.On("GetAsset", mock.Anything, "id-1").Return(testAsset("id-1"), nil)
	// Not a UUID, so it resolves as a ticker — the path GetLatestPrice
	// has always taken and GetAsset now takes too.
	s.On("GetAssetBySymbol", mock.Anything, "id-1").Return(testAsset("id-1"), nil)
	s.On("ListAssetExternalRefs", mock.Anything, []string{"id-1"}).Return(nil, nil)
	s.On("ListAssetRiskFlags", mock.Anything, "id-1").Return(nil, assert.AnError)
	h := newHandler(s)

	resp, err := h.GetAsset(context.Background(), connect.NewRequest(&apiv1.GetAssetRequest{Id: "id-1"}))
	require.NoError(t, err)
	assert.Equal(t, "id-1", resp.Msg.Id)
	assert.Empty(t, resp.Msg.RiskFlags)
}

// ListAssets must not carry flags: an empty list there means "not loaded", and
// loading them would put a per-asset query behind every catalogue render.
func TestListAssets_DoesNotLoadRiskFlags(t *testing.T) {
	s := &mockStore{}
	s.On("ListAssets", mock.Anything, mock.Anything).
		Return([]*entity.Asset{testAsset("id-1")}, "", nil)
	h := newHandler(s)

	resp, err := h.ListAssets(context.Background(), connect.NewRequest(&apiv1.ListAssetsRequest{}))
	require.NoError(t, err)
	require.Len(t, resp.Msg.Assets, 1)
	assert.Empty(t, resp.Msg.Assets[0].RiskFlags)
	s.AssertNotCalled(t, "ListAssetRiskFlags", mock.Anything, mock.Anything)
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

// --- Tests: FetchExternalPrices selection ---

// fakePriceProvider records what it was asked for and prices whatever it was
// told to. It optionally implements the budget interfaces, so one type covers
// both a plain provider and a metered one.
type fakePriceProvider struct {
	asked      [][]*entity.Asset
	prices     map[string]bool // asset IDs it will return a price for
	exempt     []string
	budget     int
	hasBudget  bool
	fetchError error
}

func (f *fakePriceProvider) FetchPrices(_ context.Context, assets []*entity.Asset) ([]entity.StoredPrice, error) {
	f.asked = append(f.asked, assets)
	if f.fetchError != nil {
		return nil, f.fetchError
	}
	var out []entity.StoredPrice
	for _, a := range assets {
		if f.prices[a.ID] {
			out = append(out, entity.StoredPrice{AssetID: a.ID, Last: decimal.NewFromInt(1)})
		}
	}
	return out, nil
}

func (f *fakePriceProvider) BaseAssetSymbol() string         { return "USD" }
func (f *fakePriceProvider) BaseAssetType() entity.AssetType { return entity.AssetTypeForex }
func (f *fakePriceProvider) BudgetExemptSymbols() []string   { return f.exempt }

func (f *fakePriceProvider) AssetBudget(_ time.Time, _ time.Duration) (int, bool) {
	return f.budget, f.hasBudget
}

// expectBaseAsset lets the handler resolve the quote currency.
// expectBaseAsset stubs the quote-currency resolution every sweep performs. The
// row carries market and type because the real store's does: a base is checked
// for being quotable before it is used, and a typeless row is not a shape the
// catalogue can produce.
func expectBaseAsset(s *mockStore) {
	s.On("GetOrCreateAssetBySymbol", mock.Anything, "USD", "USD", entity.AssetTypeForex).
		Return(&entity.Asset{ID: "usd-id", Symbol: "USD", Market: "forex", Type: entity.AssetTypeForex}, nil)
}

// --- Tests: per-row base currency and provider ref discovery ---

// perRowBaseProvider prices one asset in a currency of its own, the way a
// broker quotes a foreign share in dollars and a domestic one in roubles from
// the same response.
type perRowBaseProvider struct {
	rowBase map[string]string // asset ID → base symbol, "" for the provider default
}

func (p *perRowBaseProvider) FetchPrices(_ context.Context, assets []*entity.Asset) ([]entity.StoredPrice, error) {
	var out []entity.StoredPrice
	for _, a := range assets {
		out = append(out, entity.StoredPrice{
			AssetID:    a.ID,
			Last:       decimal.NewFromInt(1),
			BaseSymbol: p.rowBase[a.ID],
		})
	}
	return out, nil
}
func (p *perRowBaseProvider) BaseAssetSymbol() string         { return "USD" }
func (p *perRowBaseProvider) BaseAssetType() entity.AssetType { return entity.AssetTypeForex }

// A row naming its own base is stored against that base. Falling back to the
// provider default here would file a rouble price as dollars — a hundredfold
// error, not a rounding one.
func TestFetchExternalPrices_RowBaseOverridesProviderDefault(t *testing.T) {
	usd := testAsset("usd-quoted")
	rub := testAsset("rub-quoted")
	s := &mockStore{}
	expectBaseAsset(s)
	expectExternalRefs(s)
	s.On("GetOrCreateAssetBySymbol", mock.Anything, "RUB", "RUB", entity.AssetTypeForex).
		Return(&entity.Asset{ID: "rub-id", Symbol: "RUB", Market: "forex", Type: entity.AssetTypeForex}, nil)
	s.On("ListAssets", mock.Anything, mock.Anything).Return([]*entity.Asset{usd, rub}, "", nil)
	s.On("RecordPriceAttempts", mock.Anything, mock.Anything).Return(nil)

	var stored []*entity.StoredPrice
	s.On("CreatePrices", mock.Anything, mock.Anything).Return(2, nil).
		Run(func(args mock.Arguments) { stored = args.Get(1).([]*entity.StoredPrice) })

	p := &perRowBaseProvider{rowBase: map[string]string{"rub-quoted": "RUB"}}
	h := newHandler(s).WithProvider("broker", p)

	_, err := h.FetchExternalPrices(context.Background(), connect.NewRequest(
		&apiv1.FetchExternalPricesRequest{AssetIds: []string{"usd-quoted", "rub-quoted"}}))
	require.NoError(t, err)

	bases := map[string]string{}
	for _, row := range stored {
		bases[row.AssetID] = row.BaseAssetID
	}
	assert.Equal(t, map[string]string{"usd-quoted": "usd-id", "rub-quoted": "rub-id"}, bases)
}

// discoveringProvider learns an identifier for the assets it is given, the way
// a broker adapter resolves a ticker to a FIGI.
type discoveringProvider struct {
	fakePriceProvider
	refs []entity.AssetExternalRef
	err  error
	// sawRefs records what each asset carried by the time prices were asked
	// for, which is how the test checks the binding was attached in memory.
	sawRefs map[string]int
}

func (d *discoveringProvider) DiscoverRefs(_ context.Context, _ []*entity.Asset) ([]entity.AssetExternalRef, error) {
	return d.refs, d.err
}

func (d *discoveringProvider) FetchPrices(_ context.Context, assets []*entity.Asset) ([]entity.StoredPrice, error) {
	d.sawRefs = map[string]int{}
	var out []entity.StoredPrice
	for _, a := range assets {
		d.sawRefs[a.ID] = len(a.ExternalRefs)
		out = append(out, entity.StoredPrice{AssetID: a.ID, Last: decimal.NewFromInt(1)})
	}
	return out, nil
}

// A discovered binding is persisted, so it outlives the sweep, and attached in
// memory, so the same sweep can already price by it.
func TestFetchExternalPrices_PersistsDiscoveredRefs(t *testing.T) {
	a := testAsset("unbound-1")
	s := &mockStore{}
	expectBaseAsset(s)
	expectExternalRefs(s)
	s.On("ListAssets", mock.Anything, mock.Anything).Return([]*entity.Asset{a}, "", nil)
	s.On("RecordPriceAttempts", mock.Anything, mock.Anything).Return(nil)
	s.On("CreatePrices", mock.Anything, mock.Anything).Return(1, nil)
	s.On("CreateAssetExternalRef", mock.Anything, mock.MatchedBy(func(r *entity.AssetExternalRef) bool {
		return r.AssetID == "unbound-1" && r.Source == "broker" && r.Ref == "FIGI-1"
	})).Return(&entity.AssetExternalRef{
		ID: "ref-1", AssetID: "unbound-1", Source: "broker", Ref: "FIGI-1", Origin: entity.RefOriginAuto,
	}, nil)

	p := &discoveringProvider{refs: []entity.AssetExternalRef{
		{AssetID: "unbound-1", Source: "broker", Ref: "FIGI-1", Origin: entity.RefOriginAuto},
	}}
	h := newHandler(s).WithProvider("broker", p)

	_, err := h.FetchExternalPrices(context.Background(), connect.NewRequest(
		&apiv1.FetchExternalPricesRequest{AssetIds: []string{"unbound-1"}}))
	require.NoError(t, err)
	s.AssertExpectations(t)
	assert.Equal(t, 1, p.sawRefs["unbound-1"], "the binding is available to the sweep that made it")
}

// Re-discovering the same instrument every sweep is the steady state, not a
// failure: identity is stable once bound, so the store refuses the duplicate
// and the sweep carries on.
func TestFetchExternalPrices_DuplicateRefIsNotAnError(t *testing.T) {
	a := testAsset("bound-1")
	s := &mockStore{}
	expectBaseAsset(s)
	expectExternalRefs(s)
	s.On("ListAssets", mock.Anything, mock.Anything).Return([]*entity.Asset{a}, "", nil)
	s.On("RecordPriceAttempts", mock.Anything, mock.Anything).Return(nil)
	s.On("CreatePrices", mock.Anything, mock.Anything).Return(1, nil)
	s.On("CreateAssetExternalRef", mock.Anything, mock.Anything).
		Return(nil, fmt.Errorf("%w: already bound", store.ErrConstraint))

	p := &discoveringProvider{refs: []entity.AssetExternalRef{
		{AssetID: "bound-1", Source: "broker", Ref: "FIGI-1"},
	}}
	h := newHandler(s).WithProvider("broker", p)

	resp, err := h.FetchExternalPrices(context.Background(), connect.NewRequest(
		&apiv1.FetchExternalPricesRequest{AssetIds: []string{"bound-1"}}))
	require.NoError(t, err)
	assert.Empty(t, resp.Msg.Errors)
}

// A provider with no namespace of its own is never asked, and nothing is
// written on its behalf.
func TestFetchExternalPrices_PlainProviderDiscoversNothing(t *testing.T) {
	a := testAsset("plain-1")
	s := &mockStore{}
	expectBaseAsset(s)
	expectExternalRefs(s)
	s.On("ListAssets", mock.Anything, mock.Anything).Return([]*entity.Asset{a}, "", nil)
	s.On("RecordPriceAttempts", mock.Anything, mock.Anything).Return(nil)
	s.On("CreatePrices", mock.Anything, mock.Anything).Return(1, nil)

	h := newHandler(s).WithProvider("fake", &fakePriceProvider{prices: map[string]bool{"plain-1": true}})

	_, err := h.FetchExternalPrices(context.Background(), connect.NewRequest(
		&apiv1.FetchExternalPricesRequest{AssetIds: []string{"plain-1"}}))
	require.NoError(t, err)
	s.AssertNotCalled(t, "CreateAssetExternalRef", mock.Anything, mock.Anything)
}

// TestFetchExternalPrices_SweepSelectsDueAssets: an unattended sweep asks the
// store what is due for this source rather than re-pricing the catalogue, and
// records the outcome so the next sweep can skip what is fresh.
func TestFetchExternalPrices_SweepSelectsDueAssets(t *testing.T) {
	due := testAsset("due-1")
	s := &mockStore{}
	expectBaseAsset(s)
	expectExternalRefs(s)
	s.On("ListStalePricingTargets", mock.Anything, mock.MatchedBy(func(o StalePricingOpts) bool {
		return o.SourceID == "fake" && len(o.Symbols) == 0
	})).Return([]*entity.Asset{due}, nil)
	s.On("CreatePrices", mock.Anything, mock.Anything).Return(1, nil)
	s.On("RecordPriceAttempts", mock.Anything, mock.MatchedBy(func(o RecordAttemptsOpts) bool {
		return o.SourceID == "fake" && assert.ObjectsAreEqual([]string{"due-1"}, o.Priced) && len(o.Missed) == 0
	})).Return(nil)

	p := &fakePriceProvider{prices: map[string]bool{"due-1": true}}
	h := newHandler(s).WithProvider("fake", p)

	resp, err := h.FetchExternalPrices(context.Background(),
		connect.NewRequest(&apiv1.FetchExternalPricesRequest{}))
	require.NoError(t, err)
	assert.Equal(t, int32(1), resp.Msg.PricesFetched)
	s.AssertExpectations(t)
	s.AssertNotCalled(t, "ListAssets", mock.Anything, mock.Anything)
	require.Len(t, p.asked, 1)
	assert.Equal(t, []*entity.Asset{due}, p.asked[0])
}

// TestFetchExternalPrices_ExplicitIDsBypassSelection: naming asset ids is a
// deliberate reconciliation — it reads exactly those rows and skips both the
// freshness check and the per-sweep portion.
func TestFetchExternalPrices_ExplicitIDsBypassSelection(t *testing.T) {
	named := testAsset("named-1")
	s := &mockStore{}
	expectBaseAsset(s)
	expectExternalRefs(s)
	s.On("ListAssets", mock.Anything, mock.MatchedBy(func(o ListAssetsOpts) bool {
		return len(o.IDs) == 1 && o.IDs[0] == "named-1"
	})).Return([]*entity.Asset{named}, "", nil)
	s.On("CreatePrices", mock.Anything, mock.Anything).Return(1, nil)
	s.On("RecordPriceAttempts", mock.Anything, mock.Anything).Return(nil)

	p := &fakePriceProvider{prices: map[string]bool{"named-1": true}}
	h := newHandler(s).WithProvider("fake", p)

	_, err := h.FetchExternalPrices(context.Background(),
		connect.NewRequest(&apiv1.FetchExternalPricesRequest{AssetIds: []string{"named-1"}}))
	require.NoError(t, err)
	s.AssertExpectations(t)
	s.AssertNotCalled(t, "ListStalePricingTargets", mock.Anything, mock.Anything)
}

// TestFetchExternalPrices_BudgetCapsTheSweep: the portion a provider can afford
// becomes the store's limit, while the symbols it prices for free are selected
// separately and uncapped.
func TestFetchExternalPrices_BudgetCapsTheSweep(t *testing.T) {
	free := testAsset("free-1")
	paid := testAsset("paid-1")

	s := &mockStore{}
	expectBaseAsset(s)
	expectExternalRefs(s)
	s.On("ListStalePricingTargets", mock.Anything, mock.MatchedBy(func(o StalePricingOpts) bool {
		return len(o.Symbols) == 1 && o.Symbols[0] == "BTC" && o.Limit == 0
	})).Return([]*entity.Asset{free}, nil)
	s.On("ListStalePricingTargets", mock.Anything, mock.MatchedBy(func(o StalePricingOpts) bool {
		return len(o.ExcludeSymbols) == 1 && o.Limit == 60
	})).Return([]*entity.Asset{paid}, nil)
	s.On("CreatePrices", mock.Anything, mock.Anything).Return(2, nil)
	s.On("RecordPriceAttempts", mock.Anything, mock.Anything).Return(nil)

	p := &fakePriceProvider{
		exempt:    []string{"BTC"},
		budget:    60,
		hasBudget: true,
		prices:    map[string]bool{"free-1": true, "paid-1": true},
	}
	h := newHandler(s).WithProvider("fake", p).WithRefreshWindow(time.Hour)

	_, err := h.FetchExternalPrices(context.Background(),
		connect.NewRequest(&apiv1.FetchExternalPricesRequest{}))
	require.NoError(t, err)
	s.AssertExpectations(t)
	require.Len(t, p.asked, 1)
	assert.Len(t, p.asked[0], 2, "both tiers reach the provider in one call")
}

// TestFetchExternalPrices_ZeroBudgetStillPricesFreeTier: a budget of zero means
// "nothing outside the exempt set is worth asking for", and the curated batch
// still goes out — it costs one request whatever happens.
//
// It is deliberately NOT read as "this provider is spent". CBR returns zero
// permanently, because its entire feed is that one exempt document; treating
// the number as exhaustion switched off the FX leg, and without a RUB/USD rate
// every rouble-quoted position values at nothing.
func TestFetchExternalPrices_ZeroBudgetStillPricesFreeTier(t *testing.T) {
	free := testAsset("free-1")

	s := &mockStore{}
	expectBaseAsset(s)
	expectExternalRefs(s)
	s.On("ListStalePricingTargets", mock.Anything, mock.MatchedBy(func(o StalePricingOpts) bool {
		return len(o.Symbols) == 1
	})).Return([]*entity.Asset{free}, nil)
	s.On("CreatePrices", mock.Anything, mock.Anything).Return(1, nil)
	s.On("RecordPriceAttempts", mock.Anything, mock.Anything).Return(nil)

	p := &fakePriceProvider{
		exempt:    []string{"BTC"},
		budget:    0,
		hasBudget: true,
		prices:    map[string]bool{"free-1": true},
	}
	h := newHandler(s).WithProvider("fake", p).WithRefreshWindow(time.Hour)

	_, err := h.FetchExternalPrices(context.Background(),
		connect.NewRequest(&apiv1.FetchExternalPricesRequest{}))
	require.NoError(t, err)
	s.AssertNumberOfCalls(t, "ListStalePricingTargets", 1)
	require.Len(t, p.asked, 1, "the exempt document is still fetched")
}

// TestFetchExternalPrices_MissesAreRecorded: assets the provider did not price
// are recorded as misses, which is what backs them off out of the rotation.
func TestFetchExternalPrices_MissesAreRecorded(t *testing.T) {
	priced := testAsset("priced-1")
	unlisted := testAsset("unlisted-1")

	s := &mockStore{}
	expectBaseAsset(s)
	expectExternalRefs(s)
	s.On("ListStalePricingTargets", mock.Anything, mock.Anything).
		Return([]*entity.Asset{priced, unlisted}, nil)
	s.On("CreatePrices", mock.Anything, mock.Anything).Return(1, nil)
	s.On("RecordPriceAttempts", mock.Anything, mock.MatchedBy(func(o RecordAttemptsOpts) bool {
		return assert.ObjectsAreEqual([]string{"priced-1"}, o.Priced) &&
			assert.ObjectsAreEqual([]string{"unlisted-1"}, o.Missed) &&
			o.TTL == priceTTL && o.MaxBackoff == missBackoffCap
	})).Return(nil)

	p := &fakePriceProvider{prices: map[string]bool{"priced-1": true}}
	h := newHandler(s).WithProvider("fake", p)

	_, err := h.FetchExternalPrices(context.Background(),
		connect.NewRequest(&apiv1.FetchExternalPricesRequest{}))
	require.NoError(t, err)
	s.AssertExpectations(t)
}

// TestFetchExternalPrices_QuarantinedExcluded: scam and impersonation assets are
// already out of the portfolio sums, so pricing them spends quota for nothing.
func TestFetchExternalPrices_QuarantinedExcluded(t *testing.T) {
	s := &mockStore{}
	expectExternalRefs(s)
	s.On("ListStalePricingTargets", mock.Anything, mock.MatchedBy(func(o StalePricingOpts) bool {
		return assert.ObjectsAreEqual([]string{"scam", "impersonation"}, o.ExcludeVerdicts)
	})).Return([]*entity.Asset{}, nil)
	// An empty selection is explained rather than passed over in silence.
	s.On("SweepSchedule", mock.Anything, mock.Anything).
		Return([]*entity.SourceSchedule{{SourceID: "fake"}}, nil)

	h := newHandler(s).WithProvider("fake", &fakePriceProvider{})

	_, err := h.FetchExternalPrices(context.Background(),
		connect.NewRequest(&apiv1.FetchExternalPricesRequest{}))
	require.NoError(t, err)
	s.AssertExpectations(t)
}

// --- Tests: DeleteAssetExternalRef ---

func adminCtx() context.Context {
	return middleware.ContextWithUser(context.Background(), &entity.User{ID: "admin-1", Roles: []string{"admin"}})
}

func unbind(t *testing.T, h *Handler, ctx context.Context, assetID, id string) error {
	t.Helper()
	_, err := h.DeleteAssetExternalRef(ctx, connect.NewRequest(&apiv1.DeleteAssetExternalRefRequest{
		AssetId: assetID, Id: id,
	}))
	return err
}

func TestDeleteAssetExternalRef_AdminRemovesTheBinding(t *testing.T) {
	s := &mockStore{}
	s.On("DeleteAssetExternalRef", mock.Anything, "asset-1", "ref-poly").Return(nil)
	h := newHandler(s)

	require.NoError(t, unbind(t, h, adminCtx(), "asset-1", "ref-poly"))
	s.AssertExpectations(t)
}

func TestDeleteAssetExternalRef_IsAdminOnly(t *testing.T) {
	s := &mockStore{}
	h := newHandler(s)

	// A binding is catalogue identity, shared by everyone — undoing one is not
	// a personal preference.
	plain := middleware.ContextWithUser(context.Background(), &entity.User{ID: "user-1"})
	err := unbind(t, h, plain, "asset-1", "ref-poly")
	require.Error(t, err)
	assert.Equal(t, connect.CodePermissionDenied, connect.CodeOf(err))

	err = unbind(t, h, context.Background(), "asset-1", "ref-poly")
	require.Error(t, err)
	assert.Equal(t, connect.CodeUnauthenticated, connect.CodeOf(err))

	s.AssertNotCalled(t, "DeleteAssetExternalRef", mock.Anything, mock.Anything, mock.Anything)
}

func TestDeleteAssetExternalRef_NeedsBothSides(t *testing.T) {
	s := &mockStore{}
	h := newHandler(s)

	// The ref id alone would let a mistyped id detach a contract from an
	// unrelated asset, and this RPC exists to repair identity, not move it.
	for _, c := range []struct{ assetID, id string }{{"", "ref-1"}, {"asset-1", ""}, {"", ""}} {
		err := unbind(t, h, adminCtx(), c.assetID, c.id)
		require.Error(t, err)
		assert.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))
	}
	s.AssertNotCalled(t, "DeleteAssetExternalRef", mock.Anything, mock.Anything, mock.Anything)
}

func TestDeleteAssetExternalRef_UnknownBindingIsNotFound(t *testing.T) {
	s := &mockStore{}
	s.On("DeleteAssetExternalRef", mock.Anything, "asset-1", "ref-x").
		Return(fmt.Errorf("%w: external ref", store.ErrNotFound))
	h := newHandler(s)

	err := unbind(t, h, adminCtx(), "asset-1", "ref-x")
	require.Error(t, err)
	assert.Equal(t, connect.CodeNotFound, connect.CodeOf(err))
}

// stuckProvider is a provider that reports its own credential as unable to
// carry work, the way CoinGecko does once its share of the plan is spent.
type stuckProvider struct {
	fakePriceProvider
	reason string
}

func (s *stuckProvider) Unusable() (string, bool) { return s.reason, true }

// TestFetchExternalPrices_SkipsUnusableProvider: a provider whose share is spent
// or which is pausing after refusals is not asked at all — not even for target
// selection. Handing it a list costs one refusal per asset, and recordAttempts
// files each refusal against the ASSET, so a token's own back-off would grow
// because of an outage it has nothing to do with.
func TestFetchExternalPrices_SkipsUnusableProvider(t *testing.T) {
	s := &mockStore{}
	expectBaseAsset(s)
	expectExternalRefs(s)
	s.On("ListStalePricingTargets", mock.Anything, mock.Anything).Return([]*entity.Asset{testAsset("a1")}, nil)
	s.On("RecordPriceAttempts", mock.Anything, mock.Anything).Return(nil)
	s.On("CreatePrices", mock.Anything, mock.Anything).Return(1, nil)

	spent := &stuckProvider{reason: "share spent"}
	working := &fakePriceProvider{}
	h := newHandler(s).
		WithProvider("coingecko", spent).
		WithProvider("binance", working)

	_, err := h.FetchExternalPrices(context.Background(), connect.NewRequest(&apiv1.FetchExternalPricesRequest{}))
	require.NoError(t, err)

	assert.Empty(t, spent.asked, "the unusable provider is never asked")
	assert.NotEmpty(t, working.asked, "and the others are unaffected")
}

// TestFetchExternalPrices_ReconciliationIgnoresHealth: naming assets is a person
// waiting for an answer, not background work. It is not sized by the background
// share and must not be silenced by it — the request may well be the operator
// checking whether the provider is back.
func TestFetchExternalPrices_ReconciliationIgnoresHealth(t *testing.T) {
	a := testAsset("a1")
	s := &mockStore{}
	expectBaseAsset(s)
	expectExternalRefs(s)
	s.On("ListAssets", mock.Anything, mock.Anything).Return([]*entity.Asset{a}, "", nil)
	s.On("RecordPriceAttempts", mock.Anything, mock.Anything).Return(nil)
	s.On("CreatePrices", mock.Anything, mock.Anything).Return(1, nil)

	p := &stuckProvider{reason: "share spent"}
	h := newHandler(s).WithProvider("coingecko", p)

	_, err := h.FetchExternalPrices(context.Background(), connect.NewRequest(
		&apiv1.FetchExternalPricesRequest{AssetIds: []string{"a1"}}))
	require.NoError(t, err)
	assert.NotEmpty(t, p.asked, "an explicitly named asset is still asked for")
}

// --- Tests: an empty sweep says which silence it is (personal-2du9) ---

// A sweep that selected nothing because the provider's plan is spent must say
// so. Before this, the run logged the same "fetched=0" as a sweep with nothing
// to ask, and the two states are not remotely the same: one is health, the
// other is a source that has stopped contributing.
func TestFetchExternalPrices_BudgetExhaustedIsNamed(t *testing.T) {
	s := &mockStore{}
	expectExternalRefs(s)
	// Exempt symbols are selected uncapped; the budgeted pass never runs,
	// because the budget yields nothing.
	s.On("ListStalePricingTargets", mock.Anything, mock.Anything).
		Return([]*entity.Asset{}, nil).Maybe()

	h := newHandler(s).WithProvider("metered", &fakePriceProvider{budget: 0, hasBudget: true})
	h.refreshWindow = time.Hour

	resp, err := h.FetchExternalPrices(context.Background(),
		connect.NewRequest(&apiv1.FetchExternalPricesRequest{}))
	require.NoError(t, err)
	assert.Equal(t, "budget_exhausted", resp.Msg.GetIdleSources()["metered"])
	// The schedule is not consulted on this path: the budget already explains
	// the emptiness, and asking would spend a query to learn nothing.
	s.AssertNotCalled(t, "SweepSchedule", mock.Anything, mock.Anything)
}

// The other empty sweep: the budget is fine, the schedule is not. Everything
// this source could be asked about is deferred, which reads identically to
// "nothing due" until the attempt log is consulted.
func TestFetchExternalPrices_AllDeferredIsNamed(t *testing.T) {
	s := &mockStore{}
	expectExternalRefs(s)
	s.On("ListStalePricingTargets", mock.Anything, mock.Anything).
		Return([]*entity.Asset{}, nil)
	s.On("SweepSchedule", mock.Anything, mock.MatchedBy(func(o SweepScheduleOpts) bool {
		return assert.ObjectsAreEqual([]string{"fake"}, o.SourceIDs)
	})).Return([]*entity.SourceSchedule{{SourceID: "fake", Deferred: 12}}, nil)

	h := newHandler(s).WithProvider("fake", &fakePriceProvider{})

	resp, err := h.FetchExternalPrices(context.Background(),
		connect.NewRequest(&apiv1.FetchExternalPricesRequest{}))
	require.NoError(t, err)
	assert.Equal(t, "all_deferred", resp.Msg.GetIdleSources()["fake"])
}

// The reassuring case has to stay distinguishable from the two above, or the
// field is just noise: nothing deferred means nothing was due.
func TestFetchExternalPrices_NothingDueIsNamed(t *testing.T) {
	s := &mockStore{}
	expectExternalRefs(s)
	s.On("ListStalePricingTargets", mock.Anything, mock.Anything).
		Return([]*entity.Asset{}, nil)
	s.On("SweepSchedule", mock.Anything, mock.Anything).
		Return([]*entity.SourceSchedule{{SourceID: "fake", DueNow: 0, Deferred: 0}}, nil)

	h := newHandler(s).WithProvider("fake", &fakePriceProvider{})

	resp, err := h.FetchExternalPrices(context.Background(),
		connect.NewRequest(&apiv1.FetchExternalPricesRequest{}))
	require.NoError(t, err)
	assert.Equal(t, "nothing_due", resp.Msg.GetIdleSources()["fake"])
}

// A sweep that found work reports no idle source at all — and pays nothing for
// the explanation, which only the empty path asks for.
func TestFetchExternalPrices_WorkingSweepReportsNoIdleSource(t *testing.T) {
	s := &mockStore{}
	expectExternalRefs(s)
	expectBaseAsset(s)
	asset := &entity.Asset{ID: "asset-1", Symbol: "BTC", Market: entity.MarketCrypto}
	s.On("ListStalePricingTargets", mock.Anything, mock.Anything).
		Return([]*entity.Asset{asset}, nil)
	s.On("RecordPriceAttempts", mock.Anything, mock.Anything).Return(nil).Maybe()
	s.On("CreatePrices", mock.Anything, mock.Anything).Return(1, nil).Maybe()

	h := newHandler(s).WithProvider("fake", &fakePriceProvider{prices: map[string]bool{"asset-1": true}})

	resp, err := h.FetchExternalPrices(context.Background(),
		connect.NewRequest(&apiv1.FetchExternalPricesRequest{}))
	require.NoError(t, err)
	assert.Empty(t, resp.Msg.GetIdleSources())
	s.AssertNotCalled(t, "SweepSchedule", mock.Anything, mock.Anything)
}

// --- Tests: GetSweepSchedule ---

// The RPC reports every source the instance can price with, so a single frozen
// source is visible without having to name it first.
func TestGetSweepSchedule_ReportsEverySource(t *testing.T) {
	s := &mockStore{}
	s.On("SweepSchedule", mock.Anything, mock.MatchedBy(func(o SweepScheduleOpts) bool {
		return assert.ObjectsAreEqual([]string{"a", "b"}, o.SourceIDs) &&
			assert.ObjectsAreEqual(quarantineVerdicts, o.ExcludeVerdicts)
	})).Return([]*entity.SourceSchedule{
		{SourceID: "a", DueNow: 3},
		{SourceID: "b", Deferred: 7, MaxMisses: 9},
	}, nil)

	h := newHandler(s).
		WithProvider("a", &fakePriceProvider{}).
		WithProvider("b", &fakePriceProvider{})

	resp, err := h.GetSweepSchedule(context.Background(),
		connect.NewRequest(&apiv1.GetSweepScheduleRequest{}))
	require.NoError(t, err)
	require.Len(t, resp.Msg.GetSources(), 2)
	assert.Equal(t, uint32(3), resp.Msg.GetSources()[0].GetDueNow())
	assert.Equal(t, uint32(7), resp.Msg.GetSources()[1].GetDeferred())
	assert.Equal(t, uint32(9), resp.Msg.GetSources()[1].GetMaxMisses())
}

func TestResetSweepSchedule_ForgivesNamedSourcesOnly(t *testing.T) {
	s := &mockStore{}
	s.On("ResetPriceAttempts", mock.Anything, "a", mock.Anything).Return(int64(204), nil)

	h := newHandler(s).
		WithProvider("a", &fakePriceProvider{}).
		WithProvider("b", &fakePriceProvider{})

	resp, err := h.ResetSweepSchedule(context.Background(),
		connect.NewRequest(&apiv1.ResetSweepScheduleRequest{SourceIds: []string{"a"}}))
	require.NoError(t, err)
	assert.Equal(t, map[string]uint32{"a": 204}, resp.Msg.GetAssetsFreed())
	s.AssertNotCalled(t, "ResetPriceAttempts", mock.Anything, "b", mock.Anything)
}

// An empty list is rejected rather than read as "every source": clearing the
// whole instance's schedule is a bigger statement than clearing one source's,
// and it should not be what a caller gets for omitting a field.
func TestResetSweepSchedule_RequiresNamedSources(t *testing.T) {
	s := &mockStore{}
	h := newHandler(s).WithProvider("a", &fakePriceProvider{})

	_, err := h.ResetSweepSchedule(context.Background(),
		connect.NewRequest(&apiv1.ResetSweepScheduleRequest{}))
	require.Error(t, err)
	assert.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))
	s.AssertNotCalled(t, "ResetPriceAttempts", mock.Anything, mock.Anything, mock.Anything)
}

// A misspelled source must not report a confident zero: "freed nothing" and
// "no such provider" are the two readings an operator most needs to tell apart,
// since the first sends them looking for the problem somewhere else.
func TestResetSweepSchedule_UnknownSourceIsNotAQuietZero(t *testing.T) {
	s := &mockStore{}
	h := newHandler(s).WithProvider("a", &fakePriceProvider{})

	_, err := h.ResetSweepSchedule(context.Background(),
		connect.NewRequest(&apiv1.ResetSweepScheduleRequest{SourceIds: []string{"moex"}}))
	require.Error(t, err)
	assert.Equal(t, connect.CodeNotFound, connect.CodeOf(err))
	s.AssertNotCalled(t, "ResetPriceAttempts", mock.Anything, mock.Anything, mock.Anything)
}

// soonest_due is absent rather than zero when nothing is deferred: a zero
// timestamp renders as a date in 1970, which reads as a fact instead of as the
// absence of one.
func TestGetSweepSchedule_AbsentTimestampsStayAbsent(t *testing.T) {
	s := &mockStore{}
	s.On("SweepSchedule", mock.Anything, mock.Anything).
		Return([]*entity.SourceSchedule{{SourceID: "a", DueNow: 1}}, nil)

	h := newHandler(s).WithProvider("a", &fakePriceProvider{})

	resp, err := h.GetSweepSchedule(context.Background(),
		connect.NewRequest(&apiv1.GetSweepScheduleRequest{}))
	require.NoError(t, err)
	require.Len(t, resp.Msg.GetSources(), 1)
	assert.Nil(t, resp.Msg.GetSources()[0].SoonestDue)
	assert.Nil(t, resp.Msg.GetSources()[0].LatestDeferred)
}

// An unknown source is NotFound rather than an empty list: an empty answer to
// "how is source X doing" reads as "fine".
func TestGetSweepSchedule_UnknownSourceIsNotFound(t *testing.T) {
	s := &mockStore{}
	h := newHandler(s).WithProvider("a", &fakePriceProvider{})

	name := "nosuch"
	_, err := h.GetSweepSchedule(context.Background(),
		connect.NewRequest(&apiv1.GetSweepScheduleRequest{SourceId: &name}))
	require.Error(t, err)
	assert.Equal(t, connect.CodeNotFound, connect.CodeOf(err))
}

// --- Tests: an attempt is only recorded where a request was made (personal-edtu) ---

// selectiveProvider prices only the assets it is told to speak for, and reports
// that subset through Asked — the way every real adapter now does.
type selectiveProvider struct {
	fakePriceProvider
	speaksFor map[string]bool
}

func (s *selectiveProvider) Asked(assets []*entity.Asset) []*entity.Asset {
	out := make([]*entity.Asset, 0, len(assets))
	for _, a := range assets {
		if s.speaksFor[a.ID] {
			out = append(out, a)
		}
	}
	return out
}

// The sweep selects per source but cannot know what a source covers, so a
// provider is handed the whole due list. Recording the part it never requested
// as misses files one source's silence against assets it does not cover: on dev
// that was 575 miss rows of crypto against MOEX and 533 against CBR, each miss
// doubling a back-off until the queue froze at the week-long ceiling.
func TestFetchExternalPrices_UnaskedAssetsAreNotRecordedAsMisses(t *testing.T) {
	s := &mockStore{}
	expectExternalRefs(s)
	expectBaseAsset(s)

	covered := &entity.Asset{ID: "asset-covered", Symbol: "BTC", Market: entity.MarketCrypto}
	foreign := &entity.Asset{ID: "asset-foreign", Symbol: "SBER", Market: "moex"}
	s.On("ListStalePricingTargets", mock.Anything, mock.Anything).
		Return([]*entity.Asset{covered, foreign}, nil)
	s.On("CreatePrices", mock.Anything, mock.Anything).Return(1, nil).Maybe()

	var recorded RecordAttemptsOpts
	s.On("RecordPriceAttempts", mock.Anything, mock.Anything).
		Run(func(args mock.Arguments) { recorded = args.Get(1).(RecordAttemptsOpts) }).
		Return(nil)

	provider := &selectiveProvider{
		fakePriceProvider: fakePriceProvider{prices: map[string]bool{"asset-covered": true}},
		speaksFor:         map[string]bool{"asset-covered": true},
	}
	h := newHandler(s).WithProvider("fake", provider)

	_, err := h.FetchExternalPrices(context.Background(),
		connect.NewRequest(&apiv1.FetchExternalPricesRequest{}))
	require.NoError(t, err)

	assert.Equal(t, []string{"asset-covered"}, recorded.Priced)
	assert.Empty(t, recorded.Missed,
		"an asset this provider never requested must not carry its silence")
}

// A provider that speaks for everything it is handed keeps the old behaviour:
// a genuine miss is still a miss, and it is what drains an unlistable asset out
// of the rotation.
func TestFetchExternalPrices_GenuineMissIsStillRecorded(t *testing.T) {
	s := &mockStore{}
	expectExternalRefs(s)
	expectBaseAsset(s)

	priced := &entity.Asset{ID: "asset-priced", Symbol: "BTC", Market: entity.MarketCrypto}
	silent := &entity.Asset{ID: "asset-silent", Symbol: "XYZ", Market: entity.MarketCrypto}
	s.On("ListStalePricingTargets", mock.Anything, mock.Anything).
		Return([]*entity.Asset{priced, silent}, nil)
	s.On("CreatePrices", mock.Anything, mock.Anything).Return(1, nil).Maybe()

	var recorded RecordAttemptsOpts
	s.On("RecordPriceAttempts", mock.Anything, mock.Anything).
		Run(func(args mock.Arguments) { recorded = args.Get(1).(RecordAttemptsOpts) }).
		Return(nil)

	provider := &selectiveProvider{
		fakePriceProvider: fakePriceProvider{prices: map[string]bool{"asset-priced": true}},
		speaksFor:         map[string]bool{"asset-priced": true, "asset-silent": true},
	}
	h := newHandler(s).WithProvider("fake", provider)

	_, err := h.FetchExternalPrices(context.Background(),
		connect.NewRequest(&apiv1.FetchExternalPricesRequest{}))
	require.NoError(t, err)

	assert.Equal(t, []string{"asset-priced"}, recorded.Priced)
	assert.Equal(t, []string{"asset-silent"}, recorded.Missed,
		"asked and unanswered is a real miss about that asset")
}

// A transport failure is evidence about the SOURCE, not about any asset in the
// batch. Binance answers 400 for the whole request when one symbol is not a
// tradable pair; crediting every asset in it with a miss doubled the back-off of
// assets it would happily have priced, and the schedule they were pushed into
// outlived the fix that brought the source back (personal-7994).
func TestFetchExternalPrices_TransportFailureRecordsNoMisses(t *testing.T) {
	s := &mockStore{}
	expectExternalRefs(s)
	expectBaseAsset(s)

	asset := &entity.Asset{ID: "asset-1", Symbol: "BTC", Market: entity.MarketCrypto}
	s.On("ListStalePricingTargets", mock.Anything, mock.Anything).
		Return([]*entity.Asset{asset}, nil)

	provider := &fakePriceProvider{fetchError: errors.New("unexpected status 400 from Binance")}
	h := newHandler(s).WithProvider("fake", provider)

	resp, err := h.FetchExternalPrices(context.Background(),
		connect.NewRequest(&apiv1.FetchExternalPricesRequest{}))
	require.NoError(t, err)
	assert.NotEmpty(t, resp.Msg.GetErrors(), "the failure is still reported")
	s.AssertNotCalled(t, "RecordPriceAttempts", mock.Anything, mock.Anything)
}

// TestQuotableBase pins which resolved rows may denominate a provider's prices.
// The guard runs once per sweep per provider, before anything is fetched, so a
// row it lets through becomes the denominator of that source's whole batch.
func TestQuotableBase(t *testing.T) {
	tests := []struct {
		name    string
		asset   *entity.Asset
		wantErr string
		reason  string
	}{
		{
			name:   "a forex currency is the ordinary case",
			asset:  &entity.Asset{Symbol: "USD", Market: "forex", Type: entity.AssetTypeForex},
			reason: "what CoinGecko, CBR, MOEX and T-Invest all quote in",
		},
		{
			name:   "a crypto quote currency is equally ordinary",
			asset:  &entity.Asset{Symbol: "USDT", Market: entity.MarketCrypto, Type: entity.AssetTypeCryptocurrency},
			reason: "Binance quotes in USDT, and that is a real pair, not a display convention",
		},
		{
			name: "a contract market cannot denominate anything",
			asset: &entity.Asset{
				Symbol: "USD",
				Market: entity.ContractMarket("base", "0x306fb9107924a5e1ce254ef4522f6085d903e784"),
				Type:   entity.AssetTypeCryptocurrency,
			},
			wantErr: "cannot be a quote currency",
			reason:  "dev 2026-08-04: syncing a whale wallet minted a counterfeit \"US Dollar\" token",
		},
		{
			name:    "a fund share is not a currency",
			asset:   &entity.Asset{Symbol: "FXUS", Market: "moex", Type: entity.AssetTypeFund},
			wantErr: "cannot be a quote currency",
			reason:  "a price is a ratio between an instrument and a currency",
		},
		{
			name:    "a stock is not a currency either",
			asset:   &entity.Asset{Symbol: "AAPL", Market: "nasdaq", Type: entity.AssetTypeStock},
			wantErr: "cannot be a quote currency",
			reason:  "same reason, and the one a misconfigured provider is likeliest to hit",
		},
		{
			name:    "nothing resolved at all",
			asset:   nil,
			wantErr: "no asset resolved",
			reason:  "a nil row must not read as an acceptable base",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := quotableBase(tt.asset)
			if tt.wantErr == "" {
				require.NoError(t, err, tt.reason)
				return
			}
			require.Error(t, err, tt.reason)
			assert.ErrorIs(t, err, store.ErrInvalidArgument,
				"a refused base is a bad argument, not a transport failure")
			assert.Contains(t, err.Error(), tt.wantErr, tt.reason)
		})
	}
}

// TestQuotableBaseNamesTheOffendingRow pins that the refusal is diagnosable.
// The twin cost two months precisely because the failure it caused named a
// symbol and nothing about which row answered for it.
func TestQuotableBaseNamesTheOffendingRow(t *testing.T) {
	market := entity.ContractMarket("bsc", "0x037499ebb453c6c84f1888c783ef8b75a257bd29")
	err := quotableBase(&entity.Asset{Symbol: "USD", Market: market, Type: entity.AssetTypeCryptocurrency})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "USD", "the ticker that was asked for")
	assert.Contains(t, err.Error(), market, "and the row that answered")
}
