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
	"github.com/foxcool/greedy-eye/internal/spend"
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
	// Re-scoring a known asset asks whether anything ever priced it; a listed
	// asset is the uninteresting default.
	expectListed(s)
}

func expectListed(s *mockStore) {
	s.On("GetLatestPrice", mock.Anything, mock.Anything, "", "").
		Return(&entity.StoredPrice{}, nil).Maybe()
}

func (m *mockStore) CountCoveredTargets(ctx context.Context, opts StalePricingOpts) (int, error) {
	args := m.Called(ctx, opts)
	return args.Int(0), args.Error(1)
}

func (m *mockStore) FreshTradedQuotes(ctx context.Context, excludeSource string, since time.Time) ([]*entity.StoredPrice, error) {
	args := m.Called(ctx, excludeSource, since)
	if v := args.Get(0); v != nil {
		return v.([]*entity.StoredPrice), args.Error(1)
	}
	return nil, args.Error(1)
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
	s.On("ContestedAssets", mock.Anything, mock.Anything, mock.Anything).
		Return(map[string]bool{}, nil).Maybe()
}

func (m *mockStore) ContestedAssets(ctx context.Context, assetIDs []string, excludeVerdicts []string) (map[string]bool, error) {
	args := m.Called(ctx, assetIDs, excludeVerdicts)
	if v := args.Get(0); v != nil {
		return v.(map[string]bool), args.Error(1)
	}
	return nil, args.Error(1)
}

// testBinding admits what testAsset builds (no market) alongside the real
// crypto and contract markets, so a fake provider is handed what the
// pre-binding tests always handed it.
var testBinding = entity.PriceBinding{Markets: []string{"", entity.MarketCrypto}, ContractMarkets: true}

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

func (m *mockStore) DeferUnaskedPricing(ctx context.Context, opts DeferUnaskedOpts) error {
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
func (f *fakeContractResolver) Binding() entity.PriceBinding    { return testBinding }
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
func (f *failAfterFirstResolver) Binding() entity.PriceBinding    { return testBinding }
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
	_, err := h.CreateAsset(adminContext(), connect.NewRequest(&apiv1.CreateAssetRequest{}))
	require.Error(t, err)
	assert.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))
}

func TestCreateAsset_StoreError(t *testing.T) {
	s := &mockStore{}
	s.On("CreateAsset", mock.Anything, mock.Anything).Return(nil, errors.New("db error"))
	h := newHandler(s)

	_, err := h.CreateAsset(adminContext(), connect.NewRequest(&apiv1.CreateAssetRequest{
		Asset: &apiv1.Asset{Name: "Bitcoin"},
	}))
	require.Error(t, err)
	assert.Equal(t, connect.CodeInternal, connect.CodeOf(err))
}

func TestCreateAsset_OK(t *testing.T) {
	s := &mockStore{}
	s.On("CreateAsset", mock.Anything, mock.Anything).Return(testAsset("id-1"), nil)
	h := newHandler(s)

	resp, err := h.CreateAsset(adminContext(), connect.NewRequest(&apiv1.CreateAssetRequest{
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
	_, err := h.DeleteAsset(adminCtx(), connect.NewRequest(&apiv1.DeleteAssetRequest{}))
	require.Error(t, err)
	assert.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))
}

func TestDeleteAsset_OK(t *testing.T) {
	s := &mockStore{}
	s.On("DeleteAsset", mock.Anything, "id-1").Return(nil)
	h := newHandler(s)

	_, err := h.DeleteAsset(adminCtx(), connect.NewRequest(&apiv1.DeleteAssetRequest{Id: "id-1"}))
	require.NoError(t, err)
}

func TestDeleteAsset_IsAdminOnly(t *testing.T) {
	s := &mockStore{}
	h := newHandler(s)
	req := connect.NewRequest(&apiv1.DeleteAssetRequest{Id: "id-1"})

	// The row is global: deleting it removes it for every user who holds it.
	plain := middleware.ContextWithUser(context.Background(), &entity.User{ID: "user-1"})
	_, err := h.DeleteAsset(plain, req)
	require.Error(t, err)
	assert.Equal(t, connect.CodePermissionDenied, connect.CodeOf(err))

	_, err = h.DeleteAsset(context.Background(), req)
	require.Error(t, err)
	assert.Equal(t, connect.CodeUnauthenticated, connect.CodeOf(err))

	s.AssertNotCalled(t, "DeleteAsset", mock.Anything, mock.Anything)
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

// TestListAssets_IDsAndQueryReachTheStore: the two reads that replace paging
// the catalogue into the browser (personal-1asm), with bounds on both.
func TestListAssets_IDsAndQueryReachTheStore(t *testing.T) {
	s := &mockStore{}
	id := "019a0000-0000-7000-8000-000000000001"
	q := "  usdt "
	s.On("ListAssets", mock.Anything, ListAssetsOpts{IDs: []string{id}, Query: "usdt"}). // query arrives trimmed
												Return([]*entity.Asset{testAsset("a1")}, "", nil).Once()
	h := newHandler(s)

	_, err := h.ListAssets(context.Background(), connect.NewRequest(&apiv1.ListAssetsRequest{Ids: []string{id}, Query: &q}))
	require.NoError(t, err)
	s.AssertExpectations(t)

	tooMany := make([]string, maxListAssetIDs+1)
	for i := range tooMany {
		tooMany[i] = id
	}
	long := strings.Repeat("x", maxAssetQueryLen+1)
	for name, req := range map[string]*apiv1.ListAssetsRequest{
		"too many ids": {Ids: tooMany},
		"not a uuid":   {Ids: []string{"1; drop table"}},
		"long query":   {Query: &long},
	} {
		_, err := h.ListAssets(context.Background(), connect.NewRequest(req))
		require.Error(t, err, name)
		assert.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err), name)
	}
}

// --- Tests: UpdateAsset ---

func TestUpdateAsset_MissingID(t *testing.T) {
	h := newHandler(&mockStore{})
	_, err := h.UpdateAsset(adminCtx(), connect.NewRequest(&apiv1.UpdateAssetRequest{
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

	resp, err := h.UpdateAsset(adminCtx(), connect.NewRequest(&apiv1.UpdateAssetRequest{
		Asset: &apiv1.Asset{Id: "id-1", Name: "Updated"},
	}))
	require.NoError(t, err)
	assert.Equal(t, "Updated", resp.Msg.Name)
}

func TestUpdateAsset_IsAdminOnly(t *testing.T) {
	s := &mockStore{}
	h := newHandler(s)
	req := connect.NewRequest(&apiv1.UpdateAssetRequest{
		Asset: &apiv1.Asset{Id: "id-1", Name: "Renamed by a stranger"},
	})

	// One user renaming an asset renames it for everyone who holds it.
	plain := middleware.ContextWithUser(context.Background(), &entity.User{ID: "user-1"})
	_, err := h.UpdateAsset(plain, req)
	require.Error(t, err)
	assert.Equal(t, connect.CodePermissionDenied, connect.CodeOf(err))

	_, err = h.UpdateAsset(context.Background(), req)
	require.Error(t, err)
	assert.Equal(t, connect.CodeUnauthenticated, connect.CodeOf(err))

	s.AssertNotCalled(t, "UpdateAsset", mock.Anything, mock.Anything, mock.Anything)
}

// --- Tests: CreatePrice ---

// syncContext is how wallet and broker sync reach FindOrCreateAsset: in-process,
// with the authority to bind the refs their providers reported.
func syncContext() context.Context {
	return middleware.WithSyncAuthority(context.Background())
}

func adminContext() context.Context {
	return middleware.ContextWithUser(context.Background(), &entity.User{ID: "admin-1", Roles: []string{"admin"}})
}

func TestCreatePrice_MissingPrice(t *testing.T) {
	h := newHandler(&mockStore{})
	_, err := h.CreatePrice(adminContext(), connect.NewRequest(&apiv1.CreatePriceRequest{}))
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

	resp, err := h.CreatePrice(adminContext(), connect.NewRequest(&apiv1.CreatePriceRequest{
		Price: &apiv1.Price{AssetId: "a-1", BaseAssetId: "usdt"},
	}))
	require.NoError(t, err)
	assert.Equal(t, "p-1", resp.Msg.Id)
}

// A price row values every holder of the asset and decides which claimant of
// a contested ticker is the incumbent, so no ordinary user may write or delete
// one — and the store is never reached to find out.
func TestPriceWrites_AreAdminOnly(t *testing.T) {
	user := middleware.ContextWithUser(context.Background(), &entity.User{ID: "user-1"})
	asset := "a-1"
	h := newHandler(&mockStore{}) // no expectations: any store call panics

	calls := map[string]func() error{
		"CreatePrice": func() error {
			_, err := h.CreatePrice(user, connect.NewRequest(&apiv1.CreatePriceRequest{
				Price: &apiv1.Price{AssetId: asset, BaseAssetId: "usd"}}))
			return err
		},
		"CreatePrices": func() error {
			_, err := h.CreatePrices(user, connect.NewRequest(&apiv1.CreatePricesRequest{
				Prices: []*apiv1.Price{{AssetId: asset, BaseAssetId: "usd"}}}))
			return err
		},
		"DeletePrice": func() error {
			_, err := h.DeletePrice(user, connect.NewRequest(&apiv1.DeletePriceRequest{Id: "p-1"}))
			return err
		},
		"DeletePrices": func() error {
			_, err := h.DeletePrices(user, connect.NewRequest(&apiv1.DeletePricesRequest{AssetId: &asset}))
			return err
		},
	}
	for name, call := range calls {
		t.Run(name, func(t *testing.T) {
			assert.Equal(t, connect.CodePermissionDenied, connect.CodeOf(call()))
		})
	}
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
}

// An instance with no price provider says so rather than reporting a fetch
// that priced nothing.
func TestFetchExternalPrices_NoProvidersIsUnimplemented(t *testing.T) {
	h := newHandler(&mockStore{})

	_, err := h.FetchExternalPrices(syncContext(), connect.NewRequest(&apiv1.FetchExternalPricesRequest{}))
	assert.Equal(t, connect.CodeUnimplemented, connect.CodeOf(err))
}

// --- Tests: FindOrCreateAsset ---

func TestFindOrCreateAsset_FindsExisting(t *testing.T) {
	s := &mockStore{}
	s.On("FindAssetByIdentity", mock.Anything, "BTC", "crypto", entity.AssetTypeCryptocurrency).
		Return(testAsset("id-1"), nil)
	expectVerdict(s)
	h := newHandler(s)

	resp, err := h.FindOrCreateAsset(syncContext(), connect.NewRequest(&apiv1.FindOrCreateAssetRequest{
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

	resp, err := h.FindOrCreateAsset(syncContext(), connect.NewRequest(&apiv1.FindOrCreateAssetRequest{
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
	resp, err := h.FindOrCreateAsset(syncContext(), connect.NewRequest(&apiv1.FindOrCreateAssetRequest{
		Symbol: "NEW",
		Name:   &name,
	}))
	require.NoError(t, err)
	assert.True(t, resp.Msg.Created)
	require.NotNil(t, resp.Msg.Asset)
	s.AssertExpectations(t)
}

// TestFindOrCreateAsset_NulByteKeepsThePosition is personal-1vuy: a token whose
// name carries a NUL byte used to fail the insert (SQLSTATE 22021), dropping the
// balance and marking the snapshot incomplete. It lands now, with the byte
// marked, and is condemned by the invisible-rune signal rather than lost.
func TestFindOrCreateAsset_NulByteKeepsThePosition(t *testing.T) {
	s := &mockStore{}
	s.On("FindAssetByIdentity", mock.Anything, "GTPS\uFFFD", "crypto", entity.AssetTypeCryptocurrency).
		Return(nil, store.ErrNotFound)
	stored := &entity.Asset{ID: "id-nul", Symbol: "GTPS\uFFFD", Name: "Get\uFFFDPaid", Market: "crypto", Type: entity.AssetTypeCryptocurrency}
	s.On("CreateAsset", mock.Anything, mock.MatchedBy(func(a *entity.Asset) bool {
		return a.Symbol == stored.Symbol && a.Name == stored.Name
	})).Return(stored, nil)
	s.On("FindTickerIncumbent", mock.Anything, "id-nul").Return("", store.ErrNotFound)
	s.On("SetAssetVerdict", mock.Anything, "id-nul", "scam", mock.Anything,
		mock.MatchedBy(func(sig map[string]float64) bool { return sig["invisible_unicode"] == 1 }),
		mock.Anything).Return(true, nil)
	h := newHandler(s)

	name := "Get\x00Paid"
	resp, err := h.FindOrCreateAsset(syncContext(), connect.NewRequest(&apiv1.FindOrCreateAssetRequest{
		Symbol: "gtps\x00",
		Name:   &name,
	}))
	require.NoError(t, err)
	assert.True(t, resp.Msg.Created)
	assert.Equal(t, "scam", resp.Msg.Asset.GetIdentityVerdict())
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

	resp, err := h.FindOrCreateAsset(syncContext(), connect.NewRequest(&apiv1.FindOrCreateAssetRequest{
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
	resp, err := h.FindOrCreateAsset(syncContext(), connect.NewRequest(&apiv1.FindOrCreateAssetRequest{
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
	expectListed(s)
	h := newHandler(s)

	source, ref := "onchain:eth", "0x7f1ffe63"
	resp, err := h.FindOrCreateAsset(syncContext(), connect.NewRequest(&apiv1.FindOrCreateAssetRequest{
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
	expectListed(s)
	h := newHandler(s)

	source, ref := "onchain:eth", "0xCAFE"
	_, err := h.FindOrCreateAsset(syncContext(), connect.NewRequest(&apiv1.FindOrCreateAssetRequest{
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
	resp, err := h.FindOrCreateAsset(syncContext(), connect.NewRequest(&apiv1.FindOrCreateAssetRequest{
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
	_, err := h.FindOrCreateAsset(syncContext(), connect.NewRequest(&apiv1.FindOrCreateAssetRequest{
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
	resp, err := h.FindOrCreateAsset(syncContext(), connect.NewRequest(&apiv1.FindOrCreateAssetRequest{
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

// TestFindOrCreateAsset_DryRunDoesNotBindRef: a dry run is a plan with no
// writes, so a ref that resolves an existing asset by identity is reported, not
// bound. The mock has no CreateAssetExternalRef expectation: a call panics.
func TestFindOrCreateAsset_DryRunDoesNotBindRef(t *testing.T) {
	s := &mockStore{}
	s.On("FindAssetIDByExternalRef", mock.Anything, "onchain:bsc", "0xBEEF").Return("", store.ErrNotFound)
	s.On("FindAssetByIdentity", mock.Anything, "USDC", "crypto", entity.AssetTypeCryptocurrency).
		Return(testAsset("id-1"), nil)
	s.On("ListAssetExternalRefs", mock.Anything, []string{"id-1"}).Return(nil, nil)
	resolver := &fakeContractResolver{listed: map[string]listing{"bsc/0xbeef": listedAs("USDC")}}
	h := newHandler(s).WithProvider("coingecko", resolver)

	source, ref := "onchain:bsc", "0xBEEF"
	resp, err := h.FindOrCreateAsset(adminContext(), connect.NewRequest(&apiv1.FindOrCreateAssetRequest{
		Symbol:            "USDC",
		ExternalRefSource: &source,
		ExternalRef:       &ref,
		DryRun:            true,
	}))
	require.NoError(t, err)
	assert.False(t, resp.Msg.Created)
	assert.Equal(t, "id-1", resp.Msg.Asset.Id)
	s.AssertNotCalled(t, "CreateAssetExternalRef", mock.Anything, mock.Anything)
	s.AssertNotCalled(t, "SetAssetVerdict", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything)
}

// TestFindOrCreateAsset_RefToMissingAssetFallsThrough: a ref whose asset is gone
// is no identity, so resolution continues by symbol.
func TestFindOrCreateAsset_RefToMissingAssetFallsThrough(t *testing.T) {
	s := &mockStore{}
	s.On("FindAssetIDByExternalRef", mock.Anything, "coingecko", "usd-coin").Return("id-gone", nil)
	s.On("GetAsset", mock.Anything, "id-gone").Return(nil, store.ErrNotFound)
	s.On("FindAssetByIdentity", mock.Anything, "USDC", "crypto", entity.AssetTypeCryptocurrency).
		Return(testAsset("id-1"), nil)
	s.On("CreateAssetExternalRef", mock.Anything, mock.Anything).Return(nil, store.ErrConstraint)
	expectVerdict(s)
	h := newHandler(s)

	source, ref := "coingecko", "usd-coin"
	resp, err := h.FindOrCreateAsset(syncContext(), connect.NewRequest(&apiv1.FindOrCreateAssetRequest{
		Symbol:            "USDC",
		ExternalRefSource: &source,
		ExternalRef:       &ref,
	}))
	require.NoError(t, err)
	assert.Equal(t, "id-1", resp.Msg.Asset.Id)
}

// TestFindOrCreateAsset_RefLookupErrorIsReturned: a bound ref IS the identity.
// Falling through to the symbol on a transient read error would land the
// position on whatever row the symbol names, e.g. the global one instead of the
// contract's own.
func TestFindOrCreateAsset_RefLookupErrorIsReturned(t *testing.T) {
	s := &mockStore{}
	s.On("FindAssetIDByExternalRef", mock.Anything, "onchain:eth", "0xCAFE").Return("id-9", nil)
	s.On("GetAsset", mock.Anything, "id-9").Return(nil, errors.New("connection reset"))
	h := newHandler(s)

	source, ref := "onchain:eth", "0xCAFE"
	_, err := h.FindOrCreateAsset(syncContext(), connect.NewRequest(&apiv1.FindOrCreateAssetRequest{
		Symbol:            "USDT",
		ExternalRefSource: &source,
		ExternalRef:       &ref,
	}))
	require.Error(t, err)
	assert.Equal(t, connect.CodeInternal, connect.CodeOf(err))
	s.AssertNotCalled(t, "FindAssetByIdentity", mock.Anything, mock.Anything, mock.Anything, mock.Anything)
	s.AssertNotCalled(t, "CreateAssetExternalRef", mock.Anything, mock.Anything)
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
	resp, err := h.FindOrCreateAsset(syncContext(), connect.NewRequest(&apiv1.FindOrCreateAssetRequest{
		Symbol:            "WIF",
		ExternalRefSource: &source,
		ExternalRef:       &ref,
	}))
	require.NoError(t, err)
	assert.True(t, resp.Msg.Created)
	assert.Equal(t, "id-2", resp.Msg.Asset.Id)
	s.AssertExpectations(t)
}

// TestFindOrCreateAsset_ContractTagFollowsEntityRule: the "contract:" tag is
// what admits an asset to the contract price path, so it must be written for
// exactly the sources marketForContract isolates (entity.ChainFromOnchainSource),
// however the source is spelled, and for no other.
func TestFindOrCreateAsset_ContractTagFollowsEntityRule(t *testing.T) {
	cases := []struct {
		name    string
		source  string
		wantTag bool
	}{
		{"canonical", "onchain:eth", true},
		{"mixed case", "OnChain:ETH", true},
		{"chainless", "onchain:", false},
		{"other namespace", "coingecko", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := &mockStore{}
			s.On("FindAssetIDByExternalRef", mock.Anything, tc.source, "0xCAFE").Return("", store.ErrNotFound)
			s.On("FindAssetByIdentity", mock.Anything, "TKN", mock.Anything, entity.AssetTypeCryptocurrency).
				Return(nil, store.ErrNotFound)
			var created *entity.Asset
			s.On("CreateAsset", mock.Anything, mock.MatchedBy(func(a *entity.Asset) bool {
				created = a
				return true
			})).Return(testAsset("id-7"), nil)
			s.On("CreateAssetExternalRef", mock.Anything, mock.Anything).Return(&entity.AssetExternalRef{}, nil)
			expectVerdict(s)
			h := newHandler(s)

			source, ref := tc.source, "0xCAFE"
			_, err := h.FindOrCreateAsset(syncContext(), connect.NewRequest(&apiv1.FindOrCreateAssetRequest{
				Symbol:            "TKN",
				ExternalRefSource: &source,
				ExternalRef:       &ref,
			}))
			require.NoError(t, err)
			require.NotNil(t, created)
			assert.Equal(t, tc.wantTag, slices.Contains(created.Tags, "contract:0xCAFE"))
		})
	}
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
	resp, err := h.FindOrCreateAsset(syncContext(), connect.NewRequest(&apiv1.FindOrCreateAssetRequest{
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
	_, err := h.FindOrCreateAsset(syncContext(), connect.NewRequest(&apiv1.FindOrCreateAssetRequest{
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
	_, err := h.FindOrCreateAsset(syncContext(), connect.NewRequest(&apiv1.FindOrCreateAssetRequest{
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
	_, err := h.FindOrCreateAsset(syncContext(), connect.NewRequest(&apiv1.FindOrCreateAssetRequest{
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
	resp, err := h.FindOrCreateAsset(syncContext(), connect.NewRequest(&apiv1.FindOrCreateAssetRequest{
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
	resp, err := h.FindOrCreateAsset(syncContext(), connect.NewRequest(&apiv1.FindOrCreateAssetRequest{
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
	resp, err := h.FindOrCreateAsset(syncContext(), connect.NewRequest(&apiv1.FindOrCreateAssetRequest{
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
	_, err := h.FindOrCreateAsset(syncContext(), connect.NewRequest(&apiv1.FindOrCreateAssetRequest{
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

	_, err := h.FindOrCreateAsset(syncContext(), connect.NewRequest(&apiv1.FindOrCreateAssetRequest{
		Symbol: "AAPL",
		Type:   apiv1.AssetType_ASSET_TYPE_STOCK,
	}))
	require.Error(t, err)
	assert.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))
}

// --- Tests: who may add a catalogue row (personal-a483) ---

func TestCreateAsset_IsAdminOnly(t *testing.T) {
	s := &mockStore{}
	h := newHandler(s)
	// A contract: tag admits the asset to the contract price path, so the
	// caller's tags are as much a catalogue write as the row itself.
	req := connect.NewRequest(&apiv1.CreateAssetRequest{
		Asset: &apiv1.Asset{Name: "Tether", Tags: []string{"contract:0xdac17f958d2ee523a2206206994597c13d831ec7"}},
	})

	plain := middleware.ContextWithUser(context.Background(), &entity.User{ID: "user-1"})
	_, err := h.CreateAsset(plain, req)
	require.Error(t, err)
	assert.Equal(t, connect.CodePermissionDenied, connect.CodeOf(err))

	_, err = h.CreateAsset(context.Background(), req)
	require.Error(t, err)
	assert.Equal(t, connect.CodeUnauthenticated, connect.CodeOf(err))

	s.AssertNotCalled(t, "CreateAsset", mock.Anything, mock.Anything)
}

// TestFindOrCreateAsset_PreSquatIsRefused is the case personal-a483 was filed
// for. A ticker nobody has priced has no incumbent, so a row minted ahead of
// the real one would keep the real holder contested. The squat is refused —
// in the plan as well as on commit — and the holder's sync then creates the
// only claimant there is.
func TestFindOrCreateAsset_PreSquatIsRefused(t *testing.T) {
	s := &mockStore{}
	s.On("FindAssetByIdentity", mock.Anything, "NEWIPO", "moex", entity.AssetTypeFund).
		Return(nil, store.ErrNotFound)
	s.On("FindAssetByIdentity", mock.Anything, "NEWIPO", "moex", entity.AssetTypeStock).
		Return(nil, store.ErrNotFound)
	s.On("CreateAsset", mock.Anything, mock.MatchedBy(func(a *entity.Asset) bool {
		return a.Symbol == "NEWIPO" && a.Type == entity.AssetTypeStock
	})).Return(&entity.Asset{ID: "id-real", Symbol: "NEWIPO", Market: "moex", Type: entity.AssetTypeStock}, nil)
	expectVerdict(s)
	h := newHandler(s)

	moex := "moex"
	squat := func(dryRun bool) *connect.Request[apiv1.FindOrCreateAssetRequest] {
		return connect.NewRequest(&apiv1.FindOrCreateAssetRequest{
			Symbol: "NEWIPO", Market: &moex, Type: apiv1.AssetType_ASSET_TYPE_FUND, DryRun: dryRun,
		})
	}
	stranger := middleware.ContextWithUser(context.Background(), &entity.User{ID: "user-2"})

	for _, dryRun := range []bool{true, false} {
		_, err := h.FindOrCreateAsset(stranger, squat(dryRun))
		require.Error(t, err, "dry_run=%v", dryRun)
		assert.Equal(t, connect.CodePermissionDenied, connect.CodeOf(err), "dry_run=%v", dryRun)
	}
	s.AssertNotCalled(t, "CreateAsset", mock.Anything, mock.MatchedBy(func(a *entity.Asset) bool {
		return a.Type == entity.AssetTypeFund
	}))

	resp, err := h.FindOrCreateAsset(syncContext(), connect.NewRequest(&apiv1.FindOrCreateAssetRequest{
		Symbol: "NEWIPO", Market: &moex, Type: apiv1.AssetType_ASSET_TYPE_STOCK,
	}))
	require.NoError(t, err)
	assert.True(t, resp.Msg.Created)
	assert.Equal(t, "id-real", resp.Msg.Asset.GetId())
}

// TestFindOrCreateAsset_NonAdminStillFindsExisting: the gate is on creating a
// row, not on resolving one — a manual import names rows that exist.
func TestFindOrCreateAsset_NonAdminStillFindsExisting(t *testing.T) {
	s := &mockStore{}
	s.On("FindAssetByIdentity", mock.Anything, "BTC", "crypto", entity.AssetTypeCryptocurrency).
		Return(testAsset("id-1"), nil)
	expectVerdict(s)
	h := newHandler(s)

	user := middleware.ContextWithUser(context.Background(), &entity.User{ID: "user-1"})
	for _, dryRun := range []bool{true, false} {
		resp, err := h.FindOrCreateAsset(user, connect.NewRequest(&apiv1.FindOrCreateAssetRequest{
			Symbol: "BTC", DryRun: dryRun,
		}))
		require.NoError(t, err, "dry_run=%v", dryRun)
		assert.False(t, resp.Msg.Created)
		assert.Equal(t, "id-1", resp.Msg.Asset.GetId())
	}
	s.AssertNotCalled(t, "CreateAsset", mock.Anything, mock.Anything)
}

// TestFindOrCreateAsset_AdminCreates: the row is scored too — creating it is
// intake, nobody holds it yet, so judging it now overrides no one (personal-ztoc).
func TestFindOrCreateAsset_AdminCreates(t *testing.T) {
	s := &mockStore{}
	s.On("FindAssetByIdentity", mock.Anything, "NEW", "crypto", entity.AssetTypeCryptocurrency).
		Return(nil, store.ErrNotFound)
	s.On("CreateAsset", mock.Anything, mock.Anything).Return(testAsset("id-2"), nil)
	s.On("FindTickerIncumbent", mock.Anything, mock.Anything).Return("", store.ErrNotFound).Maybe()
	s.On("SetAssetVerdict", mock.Anything, "id-2", mock.Anything, mock.Anything, mock.Anything,
		rescoreVerdictSource).Return(true, nil).Once()
	h := newHandler(s)

	resp, err := h.FindOrCreateAsset(adminContext(), connect.NewRequest(&apiv1.FindOrCreateAssetRequest{Symbol: "NEW"}))
	require.NoError(t, err)
	assert.True(t, resp.Msg.Created)
	s.AssertExpectations(t)
}

// --- Tests: who may write a shared asset's verdict (personal-ztoc) ---

// TestFindOrCreateAsset_ProviderFlagsAreSyncOnly: provider_spam plus an
// unverified contract is most of the way to a scam verdict, and that verdict
// excludes the asset for every holder. A caller who heard nothing from a
// provider is refused before anything is looked up — in the plan as well.
func TestFindOrCreateAsset_ProviderFlagsAreSyncOnly(t *testing.T) {
	s := &mockStore{}
	h := newHandler(s)

	spam, verified := true, false
	callers := map[string]context.Context{
		"user":  middleware.ContextWithUser(context.Background(), &entity.User{ID: "user-1"}),
		"admin": adminContext(),
		"anon":  context.Background(),
	}
	flags := map[string]func(dryRun bool) *apiv1.FindOrCreateAssetRequest{
		"provider_spam": func(dryRun bool) *apiv1.FindOrCreateAssetRequest {
			return &apiv1.FindOrCreateAssetRequest{Symbol: "BTC", ProviderSpam: &spam, DryRun: dryRun}
		},
		"contract_verified": func(dryRun bool) *apiv1.FindOrCreateAssetRequest {
			return &apiv1.FindOrCreateAssetRequest{Symbol: "BTC", ContractVerified: &verified, DryRun: dryRun}
		},
	}
	for who, ctx := range callers {
		for flag, msg := range flags {
			for _, dryRun := range []bool{true, false} {
				_, err := h.FindOrCreateAsset(ctx, connect.NewRequest(msg(dryRun)))
				require.Error(t, err, "%s %s dry_run=%v", who, flag, dryRun)
				assert.Equal(t, connect.CodePermissionDenied, connect.CodeOf(err), "%s %s dry_run=%v", who, flag, dryRun)
			}
		}
	}
	s.AssertNotCalled(t, "FindAssetByIdentity", mock.Anything, mock.Anything, mock.Anything, mock.Anything)
	s.AssertNotCalled(t, "SetAssetVerdict", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything)
}

// TestFindOrCreateAsset_SyncScoresWithProviderFlags: the sync is who the flags
// are for, and they still reach the score.
func TestFindOrCreateAsset_SyncScoresWithProviderFlags(t *testing.T) {
	s := &mockStore{}
	s.On("FindAssetByIdentity", mock.Anything, "BTC", "crypto", entity.AssetTypeCryptocurrency).
		Return(testAsset("id-1"), nil)
	s.On("FindTickerIncumbent", mock.Anything, mock.Anything).Return("", store.ErrNotFound).Maybe()
	s.On("SetAssetVerdict", mock.Anything, "id-1", mock.Anything, mock.Anything,
		mock.MatchedBy(func(sig map[string]float64) bool {
			_, spam := sig[scamfilter.SignalProviderSpam]
			_, unverified := sig[scamfilter.SignalUnverified]
			return spam && unverified
		}), rescoreVerdictSource).Return(true, nil).Once()
	expectListed(s)
	h := newHandler(s)

	spam, verified := true, false
	_, err := h.FindOrCreateAsset(syncContext(), connect.NewRequest(&apiv1.FindOrCreateAssetRequest{
		Symbol: "BTC", ProviderSpam: &spam, ContractVerified: &verified,
	}))
	require.NoError(t, err)
	s.AssertExpectations(t)
}

// TestFindOrCreateAsset_OnlySyncRescoresAnExistingRow: a manual import, an MCP
// lookup or an admin resolving a row that exists gets the verdict that stands
// and writes none — re-scoring without the provider's flags would wipe what the
// sync judged from them. The sync's own dry run writes nothing either.
func TestFindOrCreateAsset_OnlySyncRescoresAnExistingRow(t *testing.T) {
	stored := testAsset("id-1")
	stored.IdentityVerdict = string(scamfilter.VerdictScam)

	cases := []struct {
		name   string
		ctx    context.Context
		dryRun bool
	}{
		{"user", middleware.ContextWithUser(context.Background(), &entity.User{ID: "user-1"}), false},
		{"user dry run", middleware.ContextWithUser(context.Background(), &entity.User{ID: "user-1"}), true},
		{"admin", adminContext(), false},
		{"admin dry run", adminContext(), true},
		{"sync dry run", syncContext(), true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := &mockStore{}
			s.On("FindAssetByIdentity", mock.Anything, "BTC", "crypto", entity.AssetTypeCryptocurrency).
				Return(stored, nil)
			h := newHandler(s)

			resp, err := h.FindOrCreateAsset(tc.ctx, connect.NewRequest(&apiv1.FindOrCreateAssetRequest{
				Symbol: "BTC", DryRun: tc.dryRun,
			}))
			require.NoError(t, err)
			assert.Equal(t, string(scamfilter.VerdictScam), resp.Msg.Asset.GetIdentityVerdict())
			s.AssertNotCalled(t, "SetAssetVerdict", mock.Anything, mock.Anything, mock.Anything,
				mock.Anything, mock.Anything, mock.Anything)
		})
	}
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
func (f *fakePriceProvider) Binding() entity.PriceBinding    { return testBinding }
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
func (p *perRowBaseProvider) Binding() entity.PriceBinding    { return testBinding }

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

	_, err := h.FetchExternalPrices(syncContext(), connect.NewRequest(
		&apiv1.FetchExternalPricesRequest{AssetIds: []string{"usd-quoted", "rub-quoted"}}))
	require.NoError(t, err)

	bases := map[string]string{}
	for _, row := range stored {
		bases[row.AssetID] = row.BaseAssetID
	}
	assert.Equal(t, map[string]string{"usd-quoted": "usd-id", "rub-quoted": "rub-id"}, bases)
}

// stockBaseProvider names a base of a type that can never be a quote currency.
type stockBaseProvider struct{ perRowBaseProvider }

func (p *stockBaseProvider) BaseAssetType() entity.AssetType { return entity.AssetTypeStock }

// A base refused by its type must not reach the catalogue: the create would
// leave a row nobody can use, and each sweep would add the same refusal again.
func TestFetchExternalPrices_UnquotableBaseCreatesNoRow(t *testing.T) {
	a := testAsset("a-1")
	s := &mockStore{}
	expectExternalRefs(s)
	s.On("ListAssets", mock.Anything, mock.Anything).Return([]*entity.Asset{a}, "", nil)

	h := newHandler(s).WithProvider("broker", &stockBaseProvider{})
	resp, err := h.FetchExternalPrices(syncContext(), connect.NewRequest(
		&apiv1.FetchExternalPricesRequest{AssetIds: []string{"a-1"}}))
	require.NoError(t, err)

	s.AssertNotCalled(t, "GetOrCreateAssetBySymbol", mock.Anything, mock.Anything, mock.Anything, mock.Anything)
	require.Len(t, resp.Msg.Errors, 1)
	assert.Contains(t, resp.Msg.Errors[0], "cannot be a quote currency")
}

// A row-level base the catalogue answers with an unquotable row is refused once
// per sweep: the same bad symbol on every row costs one lookup and one error.
func TestFetchExternalPrices_RefusedRowBaseIsRememberedWithinSweep(t *testing.T) {
	ids := []string{"r-1", "r-2", "r-3"}
	var assets []*entity.Asset
	rowBase := map[string]string{}
	for _, id := range ids {
		assets = append(assets, testAsset(id))
		rowBase[id] = "BAD"
	}
	s := &mockStore{}
	expectBaseAsset(s)
	expectExternalRefs(s)
	s.On("GetOrCreateAssetBySymbol", mock.Anything, "BAD", "BAD", entity.AssetTypeForex).Once().
		Return(&entity.Asset{ID: "bad-id", Symbol: "BAD", Market: entity.ContractMarket("bsc", "0x1"), Type: entity.AssetTypeForex}, nil)
	s.On("ListAssets", mock.Anything, mock.Anything).Return(assets, "", nil)
	s.On("RecordPriceAttempts", mock.Anything, mock.Anything).Return(nil)

	h := newHandler(s).WithProvider("broker", &perRowBaseProvider{rowBase: rowBase})
	resp, err := h.FetchExternalPrices(syncContext(), connect.NewRequest(
		&apiv1.FetchExternalPricesRequest{AssetIds: ids}))
	require.NoError(t, err)

	s.AssertNumberOfCalls(t, "GetOrCreateAssetBySymbol", 2) // USD once, BAD once
	require.Len(t, resp.Msg.Errors, 1)
	assert.Contains(t, resp.Msg.Errors[0], "BAD")
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

	_, err := h.FetchExternalPrices(syncContext(), connect.NewRequest(
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

	resp, err := h.FetchExternalPrices(syncContext(), connect.NewRequest(
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

	_, err := h.FetchExternalPrices(syncContext(), connect.NewRequest(
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

	resp, err := h.FetchExternalPrices(syncContext(),
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

	_, err := h.FetchExternalPrices(syncContext(),
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
	s.On("FreshTradedQuotes", mock.Anything, mock.Anything, mock.Anything).Return(nil, nil).Maybe()
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

	_, err := h.FetchExternalPrices(syncContext(),
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

	_, err := h.FetchExternalPrices(syncContext(),
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

	_, err := h.FetchExternalPrices(syncContext(),
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

	_, err := h.FetchExternalPrices(syncContext(),
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

	_, err := h.FetchExternalPrices(syncContext(), connect.NewRequest(&apiv1.FetchExternalPricesRequest{}))
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

	_, err := h.FetchExternalPrices(syncContext(), connect.NewRequest(
		&apiv1.FetchExternalPricesRequest{AssetIds: []string{"a1"}}))
	require.NoError(t, err)
	assert.NotEmpty(t, p.asked, "an explicitly named asset is still asked for")
}

// --- Tests: several keys for one provider ---

// keySource hands out a fixed registry with several keys per slug, the way the
// credentials resolver does for a user with two accounts of one provider.
type keySource map[string][]PriceProvider

func (k keySource) PriceProvidersFor(context.Context, string) (map[string][]PriceProvider, error) {
	return k, nil
}

// errSpentMidFetch fails the way a key does when its plan runs out during the
// request: the rate limiter refuses it, and the adapter wraps the refusal.
var errSpentMidFetch = fmt.Errorf("coingecko native lookup: do request: %w", spend.ErrQuotaExhausted)

// TestFetchExternalPrices_SpentKeyHandsTheSweepToTheNext: with two CoinGecko
// accounts, the first running out ended CoinGecko's part of the sweep, and the
// second account's whole plan sat unused until the first one's period rolled
// over. The next key now takes the same due assets, and nothing is recorded
// against them for the refusal.
func TestFetchExternalPrices_SpentKeyHandsTheSweepToTheNext(t *testing.T) {
	s := &mockStore{}
	expectBaseAsset(s)
	expectExternalRefs(s)
	s.On("ListStalePricingTargets", mock.Anything, mock.Anything).Return([]*entity.Asset{testAsset("a1")}, nil)
	s.On("RecordPriceAttempts", mock.Anything, mock.MatchedBy(func(o RecordAttemptsOpts) bool {
		return o.SourceID == "coingecko" && assert.ObjectsAreEqual([]string{"a1"}, o.Priced) && len(o.Missed) == 0
	})).Return(nil).Once()
	s.On("CreatePrices", mock.Anything, mock.Anything).Return(1, nil)

	first := &fakePriceProvider{fetchError: errSpentMidFetch}
	second := &fakePriceProvider{prices: map[string]bool{"a1": true}}
	h := newHandler(s).WithProviderSource(keySource{"coingecko": {first, second}})

	resp, err := h.FetchExternalPrices(syncContext(), connect.NewRequest(&apiv1.FetchExternalPricesRequest{}))
	require.NoError(t, err)

	assert.Len(t, first.asked, 1)
	require.Len(t, second.asked, 1, "the next key takes over")
	assert.Equal(t, "a1", second.asked[0][0].ID)
	assert.Equal(t, int32(1), resp.Msg.PricesFetched)
	assert.Empty(t, resp.Msg.Errors, "a spent key with another behind it is not a failure")
	s.AssertExpectations(t)
}

// TestFetchExternalPrices_UnusableKeyIsPassedForTheNext: a key already spent
// before the sweep is not asked, and the one behind it is.
func TestFetchExternalPrices_UnusableKeyIsPassedForTheNext(t *testing.T) {
	s := &mockStore{}
	expectBaseAsset(s)
	expectExternalRefs(s)
	s.On("ListStalePricingTargets", mock.Anything, mock.Anything).Return([]*entity.Asset{testAsset("a1")}, nil)
	s.On("RecordPriceAttempts", mock.Anything, mock.Anything).Return(nil)
	s.On("CreatePrices", mock.Anything, mock.Anything).Return(1, nil)

	spent := &stuckProvider{reason: "share spent"}
	next := &fakePriceProvider{prices: map[string]bool{"a1": true}}
	h := newHandler(s).WithProviderSource(keySource{"coingecko": {spent, next}})

	resp, err := h.FetchExternalPrices(backgroundCtx(), connect.NewRequest(
		&apiv1.FetchExternalPricesRequest{AssetIds: []string{"a1"}}))
	require.NoError(t, err)

	assert.Empty(t, spent.asked)
	assert.NotEmpty(t, next.asked)
	assert.Empty(t, resp.Msg.DeferredAssets, "a source with a usable key defers nothing")
}

// TestFetchExternalPrices_OtherFailureDoesNotMoveToTheNextKey: only a spent
// plan hands over. A rejected request is the source's answer, and asking the
// next key would spend its plan on the same rejection.
func TestFetchExternalPrices_OtherFailureDoesNotMoveToTheNextKey(t *testing.T) {
	s := &mockStore{}
	expectBaseAsset(s)
	expectExternalRefs(s)
	s.On("ListStalePricingTargets", mock.Anything, mock.Anything).Return([]*entity.Asset{testAsset("a1")}, nil)

	first := &fakePriceProvider{fetchError: errors.New("unexpected status 400 from CoinGecko")}
	second := &fakePriceProvider{prices: map[string]bool{"a1": true}}
	h := newHandler(s).WithProviderSource(keySource{"coingecko": {first, second}})

	resp, err := h.FetchExternalPrices(syncContext(), connect.NewRequest(&apiv1.FetchExternalPricesRequest{}))
	require.NoError(t, err)

	assert.Empty(t, second.asked)
	assert.NotEmpty(t, resp.Msg.Errors)
}

// TestFetchExternalPrices_LoneSpentKeyEndsTheSource: with one account nothing
// changes — the refusal is reported and filed against no asset.
func TestFetchExternalPrices_LoneSpentKeyEndsTheSource(t *testing.T) {
	s := &mockStore{}
	expectBaseAsset(s)
	expectExternalRefs(s)
	s.On("ListStalePricingTargets", mock.Anything, mock.Anything).Return([]*entity.Asset{testAsset("a1")}, nil)

	only := &fakePriceProvider{fetchError: errSpentMidFetch}
	h := newHandler(s).WithProviderSource(keySource{"coingecko": {only}})

	resp, err := h.FetchExternalPrices(syncContext(), connect.NewRequest(&apiv1.FetchExternalPricesRequest{}))
	require.NoError(t, err)

	assert.Len(t, only.asked, 1)
	require.Len(t, resp.Msg.Errors, 1)
	assert.Contains(t, resp.Msg.Errors[0], "quota exhausted")
	s.AssertNotCalled(t, "RecordPriceAttempts", mock.Anything, mock.Anything)
}

// A key refused mid-fetch with only spent keys behind it ends the source with
// the refusal, as one key would — a second spent account must not turn the
// error into a clean-looking sweep.
func TestFetchExternalPrices_MidFetchRefusalSurvivesSpentKeysBehind(t *testing.T) {
	s := &mockStore{}
	expectBaseAsset(s)
	expectExternalRefs(s)
	s.On("ListStalePricingTargets", mock.Anything, mock.Anything).Return([]*entity.Asset{testAsset("a1")}, nil)

	first := &fakePriceProvider{fetchError: errSpentMidFetch}
	spent := &stuckProvider{reason: "share spent"}
	h := newHandler(s).WithProviderSource(keySource{"coingecko": {first, spent}})

	resp, err := h.FetchExternalPrices(syncContext(), connect.NewRequest(&apiv1.FetchExternalPricesRequest{}))
	require.NoError(t, err)

	require.Len(t, resp.Msg.Errors, 1)
	assert.Contains(t, resp.Msg.Errors[0], "quota exhausted")
	s.AssertNotCalled(t, "RecordPriceAttempts", mock.Anything, mock.Anything)
}

// A key refused mid-fetch had already counted the named assets it left over;
// the key that takes over and asks for all of them must not report those.
func TestFetchExternalPrices_TakeoverKeyReplacesDeferredCount(t *testing.T) {
	s := &mockStore{}
	expectBaseAsset(s)
	expectExternalRefs(s)
	s.On("ListStalePricingTargets", mock.Anything, mock.Anything).Return([]*entity.Asset{testAsset("a1")}, nil).Once()
	s.On("ListStalePricingTargets", mock.Anything, mock.Anything).Return([]*entity.Asset{testAsset("a1"), testAsset("a2")}, nil)
	s.On("RecordPriceAttempts", mock.Anything, mock.Anything).Return(nil)
	s.On("CreatePrices", mock.Anything, mock.Anything).Return(2, nil)

	first := &fakePriceProvider{fetchError: errSpentMidFetch}
	second := &fakePriceProvider{prices: map[string]bool{"a1": true, "a2": true}}
	h := newHandler(s).WithProviderSource(keySource{"coingecko": {first, second}})

	resp, err := h.FetchExternalPrices(backgroundCtx(), connect.NewRequest(
		&apiv1.FetchExternalPricesRequest{AssetIds: []string{"a1", "a2"}}))
	require.NoError(t, err)

	require.NotEmpty(t, second.asked)
	assert.Empty(t, resp.Msg.DeferredAssets, "the key that answered left nothing over")
}

// partialProvider prices what it can and then fails, the way an adapter
// returns the batches it got before its key was refused.
type partialProvider struct {
	fakePriceProvider
	err error
}

func (p *partialProvider) FetchPrices(ctx context.Context, assets []*entity.Asset) ([]entity.StoredPrice, error) {
	out, _ := p.fakePriceProvider.FetchPrices(ctx, assets)
	return out, p.err
}

func idsOf(assets []*entity.Asset) []string {
	out := make([]string, len(assets))
	for i, a := range assets {
		out[i] = a.ID
	}
	return out
}

// attemptsFor matches the attempt record of one source naming exactly these
// priced assets and no miss.
func attemptsFor(priced ...string) any {
	return mock.MatchedBy(func(o RecordAttemptsOpts) bool {
		return assert.ObjectsAreEqual(priced, o.Priced) && len(o.Missed) == 0
	})
}

// storedIDs collects the asset ids of every CreatePrices call.
func storedIDs(s *mockStore) []string {
	var out []string
	for _, c := range s.Calls {
		if c.Method != "CreatePrices" {
			continue
		}
		for _, p := range c.Arguments.Get(1).([]*entity.StoredPrice) {
			out = append(out, p.AssetID)
		}
	}
	return out
}

// TestFetchExternalPrices_PartialFetchBeforeRefusalIsKept: a key refused after
// pricing part of its batch had those prices thrown away, and the next key
// selected and paid for the same assets again. What the first key priced is
// now kept and recorded, the next key is asked only for the rest, and nothing
// is filed as a miss.
func TestFetchExternalPrices_PartialFetchBeforeRefusalIsKept(t *testing.T) {
	s := &mockStore{}
	expectBaseAsset(s)
	expectExternalRefs(s)
	s.On("ListStalePricingTargets", mock.Anything, mock.Anything).
		Return([]*entity.Asset{testAsset("a1"), testAsset("a2"), testAsset("a3")}, nil)
	s.On("RecordPriceAttempts", mock.Anything, attemptsFor("a1")).Return(nil).Once()
	s.On("RecordPriceAttempts", mock.Anything, attemptsFor("a2", "a3")).Return(nil).Once()
	s.On("CreatePrices", mock.Anything, mock.Anything).Return(3, nil)

	first := &partialProvider{fakePriceProvider{prices: map[string]bool{"a1": true}}, errSpentMidFetch}
	second := &fakePriceProvider{prices: map[string]bool{"a1": true, "a2": true, "a3": true}}
	h := newHandler(s).WithProviderSource(keySource{"coingecko": {first, second}})

	resp, err := h.FetchExternalPrices(syncContext(), connect.NewRequest(&apiv1.FetchExternalPricesRequest{}))
	require.NoError(t, err)

	require.Len(t, second.asked, 1)
	assert.Equal(t, []string{"a2", "a3"}, idsOf(second.asked[0]), "the next key is asked only for the rest")
	assert.ElementsMatch(t, []string{"a1", "a2", "a3"}, storedIDs(s), "every asset priced once")
	assert.Equal(t, int32(3), resp.Msg.PricesFetched)
	assert.Empty(t, resp.Msg.Errors)
	s.AssertExpectations(t)
}

// TestFetchExternalPrices_LastKeyRefusedMidFetchKeepsItsPrices: with no key
// behind it, the refused key's prices are still stored and the rest is
// reported as unpriced rather than dropped with the batch.
func TestFetchExternalPrices_LastKeyRefusedMidFetchKeepsItsPrices(t *testing.T) {
	s := &mockStore{}
	expectBaseAsset(s)
	expectExternalRefs(s)
	s.On("ListStalePricingTargets", mock.Anything, mock.Anything).
		Return([]*entity.Asset{testAsset("a1"), testAsset("a2"), testAsset("a3")}, nil)
	s.On("RecordPriceAttempts", mock.Anything, attemptsFor("a1")).Return(nil).Once()
	s.On("CreatePrices", mock.Anything, mock.Anything).Return(1, nil)

	only := &partialProvider{fakePriceProvider{prices: map[string]bool{"a1": true}}, errSpentMidFetch}
	h := newHandler(s).WithProviderSource(keySource{"coingecko": {only}})

	resp, err := h.FetchExternalPrices(syncContext(), connect.NewRequest(&apiv1.FetchExternalPricesRequest{}))
	require.NoError(t, err)

	assert.Equal(t, []string{"a1"}, storedIDs(s))
	assert.Equal(t, int32(1), resp.Msg.PricesStored)
	require.Len(t, resp.Msg.Errors, 1)
	assert.Contains(t, resp.Msg.Errors[0], "quota exhausted")
	assert.Contains(t, resp.Msg.Errors[0], "2 of 3 assets left unpriced")
	s.AssertExpectations(t)
}

// A key refused mid-fetch with only spent keys behind it still keeps what it
// priced, and the refusal names what is left.
func TestFetchExternalPrices_PartialRefusalBeforeSpentKeysIsKept(t *testing.T) {
	s := &mockStore{}
	expectBaseAsset(s)
	expectExternalRefs(s)
	s.On("ListStalePricingTargets", mock.Anything, mock.Anything).
		Return([]*entity.Asset{testAsset("a1"), testAsset("a2")}, nil)
	s.On("RecordPriceAttempts", mock.Anything, attemptsFor("a1")).Return(nil).Once()
	s.On("CreatePrices", mock.Anything, mock.Anything).Return(1, nil)

	first := &partialProvider{fakePriceProvider{prices: map[string]bool{"a1": true}}, errSpentMidFetch}
	spent := &stuckProvider{reason: "share spent"}
	h := newHandler(s).WithProviderSource(keySource{"coingecko": {first, spent}})

	resp, err := h.FetchExternalPrices(syncContext(), connect.NewRequest(&apiv1.FetchExternalPricesRequest{}))
	require.NoError(t, err)

	assert.Equal(t, []string{"a1"}, storedIDs(s))
	require.Len(t, resp.Msg.Errors, 1)
	assert.Contains(t, resp.Msg.Errors[0], "1 of 2 assets left unpriced")
	assert.NotContains(t, resp.Msg.IdleSources, "coingecko", "a source that priced something was not idle")
	s.AssertExpectations(t)
}

// A key that selects nothing after an earlier one priced part of the run does
// not make the source idle: it contributed.
func TestFetchExternalPrices_NothingLeftAfterRefusalIsNotIdle(t *testing.T) {
	s := &mockStore{}
	expectBaseAsset(s)
	expectExternalRefs(s)
	s.On("ListStalePricingTargets", mock.Anything, mock.Anything).
		Return([]*entity.Asset{testAsset("a1"), testAsset("a2")}, nil).Once()
	s.On("ListStalePricingTargets", mock.Anything, mock.Anything).Return([]*entity.Asset{}, nil)
	s.On("SweepSchedule", mock.Anything, mock.Anything).Return([]*entity.SourceSchedule{{SourceID: "coingecko"}}, nil)
	s.On("RecordPriceAttempts", mock.Anything, mock.Anything).Return(nil)
	s.On("CreatePrices", mock.Anything, mock.Anything).Return(1, nil)

	first := &partialProvider{fakePriceProvider{prices: map[string]bool{"a1": true}}, errSpentMidFetch}
	second := &fakePriceProvider{}
	h := newHandler(s).WithProviderSource(keySource{"coingecko": {first, second}})

	resp, err := h.FetchExternalPrices(syncContext(), connect.NewRequest(&apiv1.FetchExternalPricesRequest{}))
	require.NoError(t, err)

	assert.Empty(t, second.asked)
	assert.Equal(t, []string{"a1"}, storedIDs(s))
	assert.Empty(t, resp.Msg.IdleSources)
}

// Background work naming its assets: what the refused key priced is fresh, so
// the next key's selection no longer holds it, and it must not be counted as
// left to the sweep.
func TestFetchExternalPrices_PricedBeforeRefusalIsNotDeferred(t *testing.T) {
	s := &mockStore{}
	expectBaseAsset(s)
	expectExternalRefs(s)
	s.On("ListStalePricingTargets", mock.Anything, mock.Anything).
		Return([]*entity.Asset{testAsset("a1"), testAsset("a2"), testAsset("a3")}, nil).Once()
	s.On("ListStalePricingTargets", mock.Anything, mock.Anything).
		Return([]*entity.Asset{testAsset("a2"), testAsset("a3")}, nil)
	s.On("RecordPriceAttempts", mock.Anything, mock.Anything).Return(nil)
	s.On("CreatePrices", mock.Anything, mock.Anything).Return(3, nil)

	first := &partialProvider{fakePriceProvider{prices: map[string]bool{"a1": true}}, errSpentMidFetch}
	second := &fakePriceProvider{prices: map[string]bool{"a2": true, "a3": true}}
	h := newHandler(s).WithProviderSource(keySource{"coingecko": {first, second}})

	resp, err := h.FetchExternalPrices(backgroundCtx(), connect.NewRequest(
		&apiv1.FetchExternalPricesRequest{AssetIds: []string{"a1", "a2", "a3"}}))
	require.NoError(t, err)

	assert.ElementsMatch(t, []string{"a1", "a2", "a3"}, storedIDs(s))
	assert.Empty(t, resp.Msg.DeferredAssets)
	assert.Empty(t, resp.Msg.IdleSources)
}

// errRejected fails the way a key does once its owner revoked it.
var errRejected = fmt.Errorf("coingecko native lookup: unexpected status 401 from CoinGecko: %w", spend.ErrCredentialRejected)

// TestFetchExternalPrices_RejectedKeyHandsTheSweepToTheNext: a revoked first
// key ended the source for every run, and the account behind it never priced
// anything. A key the provider refuses is now passed over like a spent one.
func TestFetchExternalPrices_RejectedKeyHandsTheSweepToTheNext(t *testing.T) {
	s := &mockStore{}
	expectBaseAsset(s)
	expectExternalRefs(s)
	s.On("ListStalePricingTargets", mock.Anything, mock.Anything).Return([]*entity.Asset{testAsset("a1")}, nil)
	s.On("RecordPriceAttempts", mock.Anything, attemptsFor("a1")).Return(nil).Once()
	s.On("CreatePrices", mock.Anything, mock.Anything).Return(1, nil)

	revoked := &fakePriceProvider{fetchError: errRejected}
	next := &fakePriceProvider{prices: map[string]bool{"a1": true}}
	h := newHandler(s).WithProviderSource(keySource{"coingecko": {revoked, next}})

	resp, err := h.FetchExternalPrices(syncContext(), connect.NewRequest(&apiv1.FetchExternalPricesRequest{}))
	require.NoError(t, err)

	require.Len(t, next.asked, 1, "the next key takes over in the same run")
	assert.Equal(t, int32(1), resp.Msg.PricesFetched)
	assert.Empty(t, resp.Msg.Errors)
	s.AssertExpectations(t)
}

// TestFetchExternalPrices_OutageDoesNotBurnTheNextKey: a failure that says
// nothing about the key (a 5xx, a timeout) is the source's answer for the run.
// Asking every key would spend each one's allowance on the same outage.
func TestFetchExternalPrices_OutageDoesNotBurnTheNextKey(t *testing.T) {
	for name, outage := range map[string]error{
		"5xx":     errors.New("coingecko native lookup: unexpected status 503 from CoinGecko"),
		"timeout": fmt.Errorf("coingecko native lookup: do request: %w", context.DeadlineExceeded),
	} {
		t.Run(name, func(t *testing.T) {
			s := &mockStore{}
			expectBaseAsset(s)
			expectExternalRefs(s)
			s.On("ListStalePricingTargets", mock.Anything, mock.Anything).Return([]*entity.Asset{testAsset("a1")}, nil)

			first := &fakePriceProvider{fetchError: outage}
			second := &fakePriceProvider{prices: map[string]bool{"a1": true}}
			h := newHandler(s).WithProviderSource(keySource{"coingecko": {first, second}})

			resp, err := h.FetchExternalPrices(syncContext(), connect.NewRequest(&apiv1.FetchExternalPricesRequest{}))
			require.NoError(t, err)

			assert.Empty(t, second.asked)
			require.Len(t, resp.Msg.Errors, 1)
			s.AssertNotCalled(t, "RecordPriceAttempts", mock.Anything, mock.Anything)
		})
	}
}

// TestGetSweepSchedule_NamesASkipOnlyWhenEveryKeyIsSpent: the schedule asks
// what the sweep would do, and the sweep moves past a spent key.
func TestGetSweepSchedule_NamesASkipOnlyWhenEveryKeyIsSpent(t *testing.T) {
	s := &mockStore{}
	s.On("SweepSchedule", mock.Anything, mock.Anything).Return([]*entity.SourceSchedule{{SourceID: "coingecko"}}, nil)

	spent := &stuckProvider{reason: "share spent"}
	h := newHandler(s).WithProviderSource(keySource{"coingecko": {spent, &fakePriceProvider{}}})
	resp, err := h.GetSweepSchedule(syncContext(), connect.NewRequest(&apiv1.GetSweepScheduleRequest{}))
	require.NoError(t, err)
	require.Len(t, resp.Msg.Sources, 1)
	assert.Nil(t, resp.Msg.Sources[0].SkipReason)

	h = newHandler(s).WithProviderSource(keySource{"coingecko": {spent, &stuckProvider{reason: "paused"}}})
	resp, err = h.GetSweepSchedule(syncContext(), connect.NewRequest(&apiv1.GetSweepScheduleRequest{}))
	require.NoError(t, err)
	require.Len(t, resp.Msg.Sources, 1)
	assert.Equal(t, "paused", resp.Msg.Sources[0].GetSkipReason())
}

// --- Tests: background work that names its assets is still background (personal-ozdu) ---

func backgroundCtx() context.Context {
	return spend.WithClass(syncContext(), spend.ClassBackground)
}

// The balance sweep names what each sync touched, and naming used to make the
// call a reconciliation: every asset priced whatever its freshness, outside the
// share the planner sizes. On prod in October 2026 that path spent 77% of the
// CoinGecko plan while the planned sweep got 20% — the unplanned spender ate
// the allowance the planner sizes from. Background work that names assets is
// selected the way the sweep selects: due only, within the share, and what it
// leaves is counted rather than dropped.
func TestFetchExternalPrices_BackgroundNamedIsPlanned(t *testing.T) {
	newAsset := testAsset("new-1")
	named := []string{"new-1", "fresh-1", "tail-1"}

	s := &mockStore{}
	s.On("FreshTradedQuotes", mock.Anything, mock.Anything, mock.Anything).Return(nil, nil).Maybe()
	expectBaseAsset(s)
	expectExternalRefs(s)
	s.On("ListStalePricingTargets", mock.Anything, mock.MatchedBy(func(o StalePricingOpts) bool {
		return o.SourceID == "fake" && o.Limit == 1 && assert.ObjectsAreEqual(named, o.IDs)
	})).Return([]*entity.Asset{newAsset}, nil)
	s.On("CreatePrices", mock.Anything, mock.Anything).Return(1, nil)
	s.On("RecordPriceAttempts", mock.Anything, mock.Anything).Return(nil)

	p := &fakePriceProvider{budget: 1, hasBudget: true, prices: map[string]bool{"new-1": true}}
	h := newHandler(s).WithProvider("fake", p).WithRefreshWindow(time.Hour)

	resp, err := h.FetchExternalPrices(backgroundCtx(), connect.NewRequest(
		&apiv1.FetchExternalPricesRequest{AssetIds: named}))
	require.NoError(t, err)
	s.AssertExpectations(t)
	require.Len(t, p.asked, 1)
	assert.Equal(t, []*entity.Asset{newAsset}, p.asked[0], "only what is due and affordable is asked for")
	assert.Equal(t, int32(2), resp.Msg.GetDeferredAssets()["fake"], "the rest is left to the sweep, and counted")
}

// The health check was waived for named assets because a person was waiting.
// A sweep is not a person: a provider whose share is spent is not asked, and
// everything named for it is reported as left over.
func TestFetchExternalPrices_BackgroundNamedRespectsHealth(t *testing.T) {
	s := &mockStore{}
	p := &stuckProvider{reason: "share spent"}
	h := newHandler(s).WithProvider("coingecko", p)

	resp, err := h.FetchExternalPrices(backgroundCtx(), connect.NewRequest(
		&apiv1.FetchExternalPricesRequest{AssetIds: []string{"a1", "a2"}}))
	require.NoError(t, err)
	assert.Empty(t, p.asked, "an unusable provider is not asked by background work")
	assert.Equal(t, int32(2), resp.Msg.GetDeferredAssets()["coingecko"])
	s.AssertNotCalled(t, "ListStalePricingTargets", mock.Anything, mock.Anything)
}

// Nothing due among the named assets is the normal case — the hourly sweep
// priced them half an hour ago. That is a statement about these assets, not
// about the source, so it must not land in idle_sources, and the source-wide
// schedule is not consulted to explain it.
func TestFetchExternalPrices_BackgroundNamedNothingDueIsNotAnIdleSource(t *testing.T) {
	s := &mockStore{}
	s.On("ListStalePricingTargets", mock.Anything, mock.Anything).Return([]*entity.Asset{}, nil)

	p := &fakePriceProvider{}
	h := newHandler(s).WithProvider("fake", p)

	resp, err := h.FetchExternalPrices(backgroundCtx(), connect.NewRequest(
		&apiv1.FetchExternalPricesRequest{AssetIds: []string{"a1"}}))
	require.NoError(t, err)
	assert.Empty(t, p.asked)
	assert.Empty(t, resp.Msg.GetIdleSources())
	assert.Equal(t, int32(1), resp.Msg.GetDeferredAssets()["fake"])
	s.AssertNotCalled(t, "SweepSchedule", mock.Anything, mock.Anything)
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

	resp, err := h.FetchExternalPrices(syncContext(),
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
		return assert.ObjectsAreEqual([]string{"fake"}, scheduleSources(o))
	})).Return([]*entity.SourceSchedule{{SourceID: "fake", Deferred: 12}}, nil)

	h := newHandler(s).WithProvider("fake", &fakePriceProvider{})

	resp, err := h.FetchExternalPrices(syncContext(),
		connect.NewRequest(&apiv1.FetchExternalPricesRequest{}))
	require.NoError(t, err)
	assert.Equal(t, "all_deferred", resp.Msg.GetIdleSources()["fake"])
}

// An asset the sweep selected and could not ask about sits in price_unasked_
// deferrals, which the schedule counts as unaskable and not as deferred. A source
// whose whole population is in that state is postponed all the same, and used to
// read as "nothing due".
func TestFetchExternalPrices_AllUnaskedIsAllDeferred(t *testing.T) {
	s := &mockStore{}
	expectExternalRefs(s)
	s.On("ListStalePricingTargets", mock.Anything, mock.Anything).
		Return([]*entity.Asset{}, nil)
	s.On("SweepSchedule", mock.Anything, mock.Anything).
		Return([]*entity.SourceSchedule{{SourceID: "fake", Unaskable: 4}}, nil)

	h := newHandler(s).WithProvider("fake", &fakePriceProvider{})

	resp, err := h.FetchExternalPrices(syncContext(),
		connect.NewRequest(&apiv1.FetchExternalPricesRequest{}))
	require.NoError(t, err)
	assert.Equal(t, "all_deferred", resp.Msg.GetIdleSources()["fake"])
}

// The deadline a frozen queue logs is the nearest of the two clocks that hold
// it: a back-off and an unasked deferral each end on their own date.
func TestSoonestDue_TakesTheNearerOfBackoffAndUnaskedDeferral(t *testing.T) {
	backoff := time.Now().Add(3 * time.Hour)
	reconsider := time.Now().Add(20 * time.Hour)

	tests := []struct {
		name  string
		sched entity.SourceSchedule
		want  time.Time
	}{
		{"only a back-off", entity.SourceSchedule{SoonestDue: backoff}, backoff},
		{"only an unasked deferral", entity.SourceSchedule{SoonestReconsider: reconsider}, reconsider},
		{"the back-off ends first", entity.SourceSchedule{SoonestDue: backoff, SoonestReconsider: reconsider}, backoff},
		{"the deferral ends first", entity.SourceSchedule{SoonestDue: reconsider, SoonestReconsider: backoff}, backoff},
		{"neither", entity.SourceSchedule{}, time.Time{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := &mockStore{}
			s.On("SweepSchedule", mock.Anything, mock.Anything).
				Return([]*entity.SourceSchedule{&tt.sched}, nil)
			got := newHandler(s).soonestDue(context.Background(), "fake", &fakePriceProvider{})
			assert.True(t, tt.want.Equal(got), "want %v, got %v", tt.want, got)
		})
	}
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

	resp, err := h.FetchExternalPrices(syncContext(),
		connect.NewRequest(&apiv1.FetchExternalPricesRequest{}))
	require.NoError(t, err)
	assert.Equal(t, "nothing_due", resp.Msg.GetIdleSources()["fake"])
}

// A named source the caller has no provider for is answered by name: otherwise
// the response is the same zero as a source that priced nothing, with no
// reason attached.
func TestFetchExternalPrices_UnconfiguredNamedSourceIsNamed(t *testing.T) {
	s := &mockStore{}
	p := &fakePriceProvider{}
	h := newHandler(s).WithProvider("fake", p)

	resp, err := h.FetchExternalPrices(syncContext(), connect.NewRequest(
		&apiv1.FetchExternalPricesRequest{SourceIds: []string{"binance"}}))
	require.NoError(t, err)
	assert.Equal(t, map[string]string{"binance": "not_configured"}, resp.Msg.GetIdleSources())
	assert.Zero(t, resp.Msg.GetPricesFetched())
	assert.Empty(t, p.asked)
}

// Named assets that resolve to nothing end the request early; the unconfigured
// source must still be named rather than vanish with the rest of the work.
func TestFetchExternalPrices_UnconfiguredSourceSurvivesUnknownAssets(t *testing.T) {
	s := &mockStore{}
	s.On("ListAssets", mock.Anything, mock.Anything).Return([]*entity.Asset{}, "", nil)
	h := newHandler(s).WithProvider("fake", &fakePriceProvider{})

	resp, err := h.FetchExternalPrices(syncContext(), connect.NewRequest(
		&apiv1.FetchExternalPricesRequest{AssetIds: []string{"missing"}, SourceIds: []string{"binance"}}))
	require.NoError(t, err)
	assert.Equal(t, map[string]string{"binance": "not_configured"}, resp.Msg.GetIdleSources())
}

// Named assets that resolve to nothing used to end the request with an empty
// response, the same zero as a sweep with nothing to do. The ids are named so
// the caller can tell a typo from "everything is current", and each configured
// source says it had nothing to ask.
func TestFetchExternalPrices_OnlyUnknownAssetIdsAreNamed(t *testing.T) {
	s := &mockStore{}
	s.On("ListAssets", mock.Anything, mock.Anything).Return([]*entity.Asset{}, "", nil)
	p := &fakePriceProvider{}
	h := newHandler(s).WithProvider("fake", p)

	resp, err := h.FetchExternalPrices(syncContext(), connect.NewRequest(
		&apiv1.FetchExternalPricesRequest{AssetIds: []string{"ghost-1", "ghost-2"}}))
	require.NoError(t, err)
	require.Len(t, resp.Msg.GetErrors(), 1)
	assert.Contains(t, resp.Msg.GetErrors()[0], "ghost-1")
	assert.Contains(t, resp.Msg.GetErrors()[0], "ghost-2")
	assert.Equal(t, map[string]string{"fake": "no_assets"}, resp.Msg.GetIdleSources())
	assert.Zero(t, resp.Msg.GetPricesFetched())
	assert.Empty(t, p.asked)
}

// A partly unknown list is priced for what exists and names the rest: dropping
// the unknown ids silently would read as "all of them were priced".
func TestFetchExternalPrices_KnownAndUnknownAssetIds(t *testing.T) {
	s := &mockStore{}
	expectBaseAsset(s)
	expectExternalRefs(s)
	s.On("ListAssets", mock.Anything, mock.Anything).Return([]*entity.Asset{testAsset("a1")}, "", nil)
	s.On("RecordPriceAttempts", mock.Anything, mock.Anything).Return(nil)
	s.On("CreatePrices", mock.Anything, mock.Anything).Return(1, nil)
	p := &fakePriceProvider{prices: map[string]bool{"a1": true}}
	h := newHandler(s).WithProvider("fake", p)

	resp, err := h.FetchExternalPrices(syncContext(), connect.NewRequest(
		&apiv1.FetchExternalPricesRequest{AssetIds: []string{"a1", "ghost", "a1"}}))
	require.NoError(t, err)
	assert.Equal(t, int32(1), resp.Msg.GetPricesFetched())
	require.Len(t, resp.Msg.GetErrors(), 1)
	assert.Contains(t, resp.Msg.GetErrors()[0], "ghost")
	assert.NotContains(t, resp.Msg.GetErrors()[0], "a1", "a duplicate of a known id is not unknown")
	assert.Empty(t, resp.Msg.GetIdleSources())
}

// The store matches ids as uuids, so a request may spell a known id in upper
// case or without hyphens; that asset is priced and must not be called unknown.
func TestFetchExternalPrices_NonCanonicalKnownIdIsNotUnknown(t *testing.T) {
	const id = "550e8400-e29b-41d4-a716-446655440000"
	s := &mockStore{}
	expectBaseAsset(s)
	expectExternalRefs(s)
	s.On("ListAssets", mock.Anything, mock.Anything).Return([]*entity.Asset{testAsset(id)}, "", nil)
	s.On("RecordPriceAttempts", mock.Anything, mock.Anything).Return(nil)
	s.On("CreatePrices", mock.Anything, mock.Anything).Return(1, nil)
	h := newHandler(s).WithProvider("fake", &fakePriceProvider{prices: map[string]bool{id: true}})

	resp, err := h.FetchExternalPrices(syncContext(), connect.NewRequest(
		&apiv1.FetchExternalPricesRequest{AssetIds: []string{"550E8400-E29B-41D4-A716-446655440000", "550e8400e29b41d4a716446655440000"}}))
	require.NoError(t, err)
	assert.Empty(t, resp.Msg.GetErrors())
}

// no_assets is said only by the sources the request is about.
func TestFetchExternalPrices_UnknownAssetsOnlyIdleTheNamedSources(t *testing.T) {
	s := &mockStore{}
	s.On("ListAssets", mock.Anything, mock.Anything).Return([]*entity.Asset{}, "", nil)
	h := newHandler(s).WithProvider("fake", &fakePriceProvider{}).WithProvider("other", &fakePriceProvider{})

	resp, err := h.FetchExternalPrices(syncContext(), connect.NewRequest(
		&apiv1.FetchExternalPricesRequest{AssetIds: []string{"ghost"}, SourceIds: []string{"fake"}}))
	require.NoError(t, err)
	assert.Equal(t, map[string]string{"fake": "no_assets"}, resp.Msg.GetIdleSources())
}

// Background work names its assets too; an unusable source is both idle (the
// source cannot work) and has everything named left to the sweep.
func TestFetchExternalPrices_BackgroundUnusableSourceIsIdleAndDeferred(t *testing.T) {
	s := &mockStore{}
	h := newHandler(s).WithProvider("coingecko", &stuckProvider{reason: "share spent"})

	resp, err := h.FetchExternalPrices(backgroundCtx(), connect.NewRequest(
		&apiv1.FetchExternalPricesRequest{AssetIds: []string{"a1", "a2"}}))
	require.NoError(t, err)
	assert.Equal(t, "unusable: share spent", resp.Msg.GetIdleSources()["coingecko"])
	assert.Equal(t, int32(2), resp.Msg.GetDeferredAssets()["coingecko"])
}

// A source the sweep skipped because its credential cannot carry work was
// invisible: the request named it, it was configured, and the response said
// nothing. The reason the source itself gave is the answer.
func TestFetchExternalPrices_UnusableNamedSourceIsNamed(t *testing.T) {
	s := &mockStore{}
	p := &stuckProvider{reason: "share spent"}
	h := newHandler(s).WithProvider("coingecko", p)

	resp, err := h.FetchExternalPrices(syncContext(), connect.NewRequest(
		&apiv1.FetchExternalPricesRequest{SourceIds: []string{"coingecko"}}))
	require.NoError(t, err)
	assert.Equal(t, map[string]string{"coingecko": "unusable: share spent"}, resp.Msg.GetIdleSources())
	assert.Empty(t, p.asked)
}

// A later key that can carry the work is the source's answer; an earlier key
// that cannot must not make the source read as idle.
func TestFetchExternalPrices_UnusableKeyBeforeUsableKeyIsNotIdle(t *testing.T) {
	s := &mockStore{}
	expectBaseAsset(s)
	expectExternalRefs(s)
	s.On("ListStalePricingTargets", mock.Anything, mock.Anything).Return([]*entity.Asset{testAsset("a1")}, nil)
	s.On("RecordPriceAttempts", mock.Anything, mock.Anything).Return(nil)
	s.On("CreatePrices", mock.Anything, mock.Anything).Return(1, nil)
	h := newHandler(s).WithProviderSource(keySource{"coingecko": {
		&stuckProvider{reason: "share spent"},
		&fakePriceProvider{prices: map[string]bool{"a1": true}},
	}})

	resp, err := h.FetchExternalPrices(syncContext(), connect.NewRequest(
		&apiv1.FetchExternalPricesRequest{SourceIds: []string{"coingecko"}}))
	require.NoError(t, err)
	assert.Empty(t, resp.Msg.GetIdleSources())
	assert.Equal(t, int32(1), resp.Msg.GetPricesFetched())
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

	resp, err := h.FetchExternalPrices(syncContext(),
		connect.NewRequest(&apiv1.FetchExternalPricesRequest{}))
	require.NoError(t, err)
	assert.Empty(t, resp.Msg.GetIdleSources())
	s.AssertNotCalled(t, "SweepSchedule", mock.Anything, mock.Anything)
}

// --- Tests: GetSweepSchedule ---

// scheduleSources names the sources a schedule read covers, in order.
func scheduleSources(o SweepScheduleOpts) []string {
	out := make([]string, 0, len(o.Sources))
	for _, src := range o.Sources {
		out = append(out, src.SourceID)
	}
	return out
}

// The RPC reports every source the instance can price with, so a single frozen
// source is visible without having to name it first.
func TestGetSweepSchedule_ReportsEverySource(t *testing.T) {
	s := &mockStore{}
	s.On("SweepSchedule", mock.Anything, mock.MatchedBy(func(o SweepScheduleOpts) bool {
		return assert.ObjectsAreEqual([]string{"a", "b"}, scheduleSources(o)) &&
			assert.ObjectsAreEqual(quarantineVerdicts, o.Sources[0].ExcludeVerdicts) &&
			o.Sources[0].ExcludeContested
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

// unaskable is reported per source, apart from the back-off counts.
func TestGetSweepSchedule_ReportsUnaskable(t *testing.T) {
	s := &mockStore{}
	s.On("SweepSchedule", mock.Anything, mock.Anything).
		Return([]*entity.SourceSchedule{{SourceID: "a", DueNow: 2, Deferred: 3, NeverAttempted: 4, Unaskable: 5}}, nil)

	h := newHandler(s).WithProvider("a", &fakePriceProvider{})

	resp, err := h.GetSweepSchedule(context.Background(),
		connect.NewRequest(&apiv1.GetSweepScheduleRequest{}))
	require.NoError(t, err)
	require.Len(t, resp.Msg.GetSources(), 1)
	got := resp.Msg.GetSources()[0]
	assert.Equal(t, uint32(5), got.GetUnaskable())
	assert.Equal(t, uint32(2), got.GetDueNow())
	assert.Equal(t, uint32(3), got.GetDeferred())
	assert.Equal(t, uint32(4), got.GetNeverAttempted())
}

// The schedule counts the population the sweep selects from: the provider's
// binding, and for a source with no budget only the symbols its one request
// covers. A schedule over the whole catalogue reported CBR's never-asked
// crypto as due work forever.
func TestGetSweepSchedule_CountsWhatTheSweepSelectsFrom(t *testing.T) {
	s := &mockStore{}
	var got SweepScheduleOpts
	s.On("SweepSchedule", mock.Anything, mock.Anything).
		Run(func(args mock.Arguments) { got = args.Get(1).(SweepScheduleOpts) }).
		Return([]*entity.SourceSchedule{{SourceID: "feed"}, {SourceID: "metered"}}, nil)

	h := newHandler(s).
		WithProvider("feed", &fakePriceProvider{exempt: []string{"EUR"}, budget: 0, hasBudget: true}).
		WithProvider("metered", &fakePriceProvider{exempt: []string{"BTC"}, budget: 10, hasBudget: true})
	h.refreshWindow = time.Hour

	_, err := h.GetSweepSchedule(context.Background(),
		connect.NewRequest(&apiv1.GetSweepScheduleRequest{}))
	require.NoError(t, err)
	require.Len(t, got.Sources, 2)

	feed, metered := got.Sources[0], got.Sources[1]
	assert.Equal(t, "feed", feed.SourceID)
	require.NotNil(t, feed.Binding)
	assert.Equal(t, testBinding, *feed.Binding)
	assert.Equal(t, []string{"EUR"}, feed.Symbols, "no budget: only the exempt symbols are ever selected")

	assert.Equal(t, "metered", metered.SourceID)
	assert.Nil(t, metered.Symbols, "a budget leaves the whole admitted market in the queue")
	assert.Zero(t, metered.Limit, "the schedule is the queue before the budget")
	assert.True(t, metered.ExcludeContested)
}

func TestResetSweepSchedule_ForgivesNamedSourcesOnly(t *testing.T) {
	s := &mockStore{}
	s.On("ResetPriceAttempts", mock.Anything, "a", mock.Anything).Return(int64(204), nil)

	h := newHandler(s).
		WithProvider("a", &fakePriceProvider{}).
		WithProvider("b", &fakePriceProvider{})

	resp, err := h.ResetSweepSchedule(adminContext(),
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

	_, err := h.ResetSweepSchedule(adminContext(),
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

	_, err := h.ResetSweepSchedule(adminContext(),
		connect.NewRequest(&apiv1.ResetSweepScheduleRequest{SourceIds: []string{"moex"}}))
	require.Error(t, err)
	assert.Equal(t, connect.CodeNotFound, connect.CodeOf(err))
	s.AssertNotCalled(t, "ResetPriceAttempts", mock.Anything, mock.Anything, mock.Anything)
}

// Forgiving back-off re-spends a plan every holder shares, so it is an
// operator's call: a signed-in user is refused, and so is the in-process mark,
// which nothing that resets the schedule carries (personal-nywa).
func TestResetSweepSchedule_IsAdminOnly(t *testing.T) {
	s := &mockStore{}
	h := newHandler(s).WithProvider("a", &fakePriceProvider{})
	req := connect.NewRequest(&apiv1.ResetSweepScheduleRequest{SourceIds: []string{"a"}})

	plain := middleware.ContextWithUser(context.Background(), &entity.User{ID: "user-1"})
	_, err := h.ResetSweepSchedule(plain, req)
	assert.Equal(t, connect.CodePermissionDenied, connect.CodeOf(err))

	_, err = h.ResetSweepSchedule(syncContext(), req)
	assert.Equal(t, connect.CodeUnauthenticated, connect.CodeOf(err))

	s.AssertNotCalled(t, "ResetPriceAttempts", mock.Anything, mock.Anything, mock.Anything)
}

// A fetch spends the shared provider plan whether it sweeps the catalogue or
// names assets, so a signed-in user is refused both before any provider or
// store is touched. A user's own prices come from syncing (personal-nywa).
func TestFetchExternalPrices_IsSyncOrAdminOnly(t *testing.T) {
	plain := middleware.ContextWithUser(context.Background(), &entity.User{ID: "user-1"})
	for _, tc := range []struct {
		name string
		msg  *apiv1.FetchExternalPricesRequest
	}{
		{"sweep", &apiv1.FetchExternalPricesRequest{}},
		{"named", &apiv1.FetchExternalPricesRequest{AssetIds: []string{"btc"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := &mockStore{}
			p := &fakePriceProvider{}
			h := newHandler(s).WithProvider("a", p)

			_, err := h.FetchExternalPrices(plain, connect.NewRequest(tc.msg))
			assert.Equal(t, connect.CodePermissionDenied, connect.CodeOf(err))

			_, err = h.FetchExternalPrices(context.Background(), connect.NewRequest(tc.msg))
			assert.Equal(t, connect.CodeUnauthenticated, connect.CodeOf(err))

			// mockStore has no expectations, so any store read would panic.
			assert.Empty(t, p.asked)
		})
	}
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
	// On an admitted market, so only Asked can drop it: a market testBinding
	// does not admit never reaches Asked.
	foreign := &entity.Asset{ID: "asset-foreign", Symbol: "SBER", Market: entity.MarketCrypto}
	s.On("ListStalePricingTargets", mock.Anything, mock.Anything).
		Return([]*entity.Asset{covered, foreign}, nil)
	s.On("CreatePrices", mock.Anything, mock.Anything).Return(1, nil).Maybe()

	var recorded RecordAttemptsOpts
	s.On("RecordPriceAttempts", mock.Anything, mock.Anything).
		Run(func(args mock.Arguments) { recorded = args.Get(1).(RecordAttemptsOpts) }).
		Return(nil)
	var deferred []string
	s.On("DeferUnaskedPricing", mock.Anything, mock.Anything).
		Run(func(args mock.Arguments) {
			deferred = append(deferred, args.Get(1).(DeferUnaskedOpts).AssetIDs...)
		}).
		Return(nil)

	provider := &selectiveProvider{
		fakePriceProvider: fakePriceProvider{prices: map[string]bool{"asset-covered": true}},
		speaksFor:         map[string]bool{"asset-covered": true},
	}
	h := newHandler(s).WithProvider("fake", provider)

	_, err := h.FetchExternalPrices(syncContext(),
		connect.NewRequest(&apiv1.FetchExternalPricesRequest{}))
	require.NoError(t, err)

	assert.Equal(t, []string{"asset-covered"}, recorded.Priced)
	assert.Empty(t, recorded.Missed,
		"an asset this provider never requested must not carry its silence")
	assert.Equal(t, []string{"asset-foreign"}, deferred,
		"Asked is what dropped it: deferred for the day instead")
}

// An attempt stamped after the fetch came due seconds after the next hourly
// sweep had started, so an hourly source priced every other hour (dev: 168 rows
// at 12:00, none at 13:00). The stamp is the sweep's start, and the next sweep
// selects with dueSlack, so one starting a little early still finds it due.
func TestFetchExternalPrices_AttemptStampedAtSweepStart(t *testing.T) {
	s := &mockStore{}
	expectExternalRefs(s)
	expectBaseAsset(s)

	var selected StalePricingOpts
	s.On("ListStalePricingTargets", mock.Anything, mock.Anything).
		Run(func(args mock.Arguments) { selected = args.Get(1).(StalePricingOpts) }).
		Return([]*entity.Asset{testAsset("asset-1")}, nil)
	s.On("CreatePrices", mock.Anything, mock.Anything).Return(1, nil)
	var recorded RecordAttemptsOpts
	s.On("RecordPriceAttempts", mock.Anything, mock.Anything).
		Run(func(args mock.Arguments) { recorded = args.Get(1).(RecordAttemptsOpts) }).
		Return(nil)

	p := &stampingProvider{fakePriceProvider: fakePriceProvider{prices: map[string]bool{"asset-1": true}}}
	h := newHandler(s).WithProvider("fake", p)

	_, err := h.FetchExternalPrices(syncContext(), connect.NewRequest(&apiv1.FetchExternalPricesRequest{}))
	require.NoError(t, err)

	require.False(t, recorded.At.IsZero())
	assert.True(t, recorded.At.Before(p.fetchedAt), "stamped before the fetch, not after it")
	assert.Equal(t, dueSlack, selected.Now.Sub(recorded.At), "selection and stamp share one clock")

	nextDeadline := recorded.At.Add(recorded.TTL)
	earlyNextSweep := recorded.At.Add(priceTTL - time.Second)
	assert.False(t, nextDeadline.After(earlyNextSweep.Add(dueSlack)),
		"a sweep starting a second early an hour later still finds the asset due")
}

// stampingProvider records when it was asked, after a pause long enough to
// tell a post-fetch stamp from a pre-fetch one.
type stampingProvider struct {
	fakePriceProvider
	fetchedAt time.Time
}

func (p *stampingProvider) FetchPrices(ctx context.Context, assets []*entity.Asset) ([]entity.StoredPrice, error) {
	time.Sleep(time.Millisecond)
	p.fetchedAt = time.Now()
	return p.fakePriceProvider.FetchPrices(ctx, assets)
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

	_, err := h.FetchExternalPrices(syncContext(),
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

	resp, err := h.FetchExternalPrices(syncContext(),
		connect.NewRequest(&apiv1.FetchExternalPricesRequest{}))
	require.NoError(t, err)
	assert.NotEmpty(t, resp.Msg.GetErrors(), "the failure is still reported")
	s.AssertNotCalled(t, "RecordPriceAttempts", mock.Anything, mock.Anything)
}

// --- Tests: selected but not asked ---

// unaskingProvider binds by ref and requests only the assets it speaks for,
// the way Binance does with its listing snapshot.
type unaskingProvider struct {
	bindingProvider
	speaksFor  map[string]bool
	fetchError error
}

func (u *unaskingProvider) Asked(assets []*entity.Asset) []*entity.Asset {
	out := make([]*entity.Asset, 0, len(assets))
	for _, a := range assets {
		if u.speaksFor[a.ID] {
			out = append(out, a)
		}
	}
	return out
}

func (u *unaskingProvider) FetchPrices(ctx context.Context, assets []*entity.Asset) ([]entity.StoredPrice, error) {
	if u.fetchError != nil {
		return nil, u.fetchError
	}
	return u.bindingProvider.FetchPrices(ctx, u.Asked(assets))
}

// unaskedSweep runs one unattended sweep over three selected crypto assets: one
// bound and asked, one with no ref (dropped by Bound), one bound that the
// adapter has no request for (dropped by Asked).
func unaskedSweep(t *testing.T, fetchError error) (*mockStore, StalePricingOpts, *RecordAttemptsOpts, []DeferUnaskedOpts) {
	t.Helper()
	s := &mockStore{}
	expectExternalRefs(s)
	expectBaseAsset(s)

	bound := func(id string) *entity.Asset {
		a := marketAsset(id, strings.ToUpper(id), entity.MarketCrypto)
		a.ExternalRefs = []entity.AssetExternalRef{{AssetID: id, Source: "venue", Ref: id + "USDT"}}
		return a
	}
	asked, unasked := bound("asked"), bound("unasked")
	unbound := marketAsset("unbound", "UNBOUND", entity.MarketCrypto)

	var selected StalePricingOpts
	s.On("ListStalePricingTargets", mock.Anything, mock.Anything).
		Run(func(args mock.Arguments) { selected = args.Get(1).(StalePricingOpts) }).
		Return([]*entity.Asset{asked, unbound, unasked}, nil)
	s.On("CreatePrices", mock.Anything, mock.Anything).Return(1, nil).Maybe()
	var recorded *RecordAttemptsOpts
	s.On("RecordPriceAttempts", mock.Anything, mock.Anything).
		Run(func(args mock.Arguments) { o := args.Get(1).(RecordAttemptsOpts); recorded = &o }).
		Return(nil).Maybe()
	var deferred []DeferUnaskedOpts
	s.On("DeferUnaskedPricing", mock.Anything, mock.Anything).
		Run(func(args mock.Arguments) { deferred = append(deferred, args.Get(1).(DeferUnaskedOpts)) }).
		Return(nil)

	p := &unaskingProvider{
		bindingProvider: bindingProvider{binding: entity.PriceBinding{Markets: []string{entity.MarketCrypto}, RefSource: "venue"}},
		speaksFor:       map[string]bool{"asked": true},
		fetchError:      fetchError,
	}
	_, err := newHandler(s).WithProvider("venue", p).FetchExternalPrices(syncContext(),
		connect.NewRequest(&apiv1.FetchExternalPricesRequest{}))
	require.NoError(t, err)
	return s, selected, recorded, deferred
}

// An asset the sweep selected and did not ask about used to leave no trace: no
// attempt row, so its deadline stayed NULL and it held the head of the queue on
// every sweep, counted as due and taking a budgeted source's limit. It is now
// deferred for a day, and still has no attempt row: nothing was asked, so
// PricingStatus must not learn "asked, no quote" from it.
func TestFetchExternalPrices_SelectedButUnaskedIsDeferredNotRecorded(t *testing.T) {
	_, selected, recorded, deferred := unaskedSweep(t, nil)

	require.NotNil(t, selected.Binding, "the selection admits by the provider's binding")
	assert.Equal(t, []string{entity.MarketCrypto}, selected.Binding.Markets)

	require.NotNil(t, recorded)
	assert.Equal(t, []string{"asked"}, recorded.Priced)
	assert.Empty(t, recorded.Missed, "an unasked asset gets no attempt row")

	var ids []string
	for _, d := range deferred {
		assert.Equal(t, "venue", d.SourceID)
		assert.Equal(t, recorded.At, d.At, "deferred from the sweep's own clock")
		assert.GreaterOrEqual(t, d.Retry, 24*time.Hour, "reconsidered no sooner than a day later")
		ids = append(ids, d.AssetIDs...)
	}
	assert.ElementsMatch(t, []string{"unbound", "unasked"}, ids)
}

// A failed request says nothing about which assets the adapter would have
// asked about, so only the Bound drop — known before any request — is deferred.
func TestFetchExternalPrices_TransportFailureDefersOnlyTheUnbound(t *testing.T) {
	s, _, recorded, deferred := unaskedSweep(t, errors.New("unexpected status 400"))

	assert.Nil(t, recorded)
	s.AssertNotCalled(t, "RecordPriceAttempts", mock.Anything, mock.Anything)
	require.Len(t, deferred, 1)
	assert.Equal(t, []string{"unbound"}, deferred[0].AssetIDs)
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

// --- Tests: the binding gate (personal-avm.1) ---

// bindingProvider discovers and prices whatever it is handed, and records both,
// so a test can see exactly what the handler let through to each step.
type bindingProvider struct {
	binding    entity.PriceBinding
	discovered [][]string
	priced     [][]string
}

func (b *bindingProvider) DiscoverRefs(_ context.Context, assets []*entity.Asset) ([]entity.AssetExternalRef, error) {
	b.discovered = append(b.discovered, assetIDs(assets))
	return nil, nil
}

func (b *bindingProvider) FetchPrices(_ context.Context, assets []*entity.Asset) ([]entity.StoredPrice, error) {
	b.priced = append(b.priced, assetIDs(assets))
	out := make([]entity.StoredPrice, 0, len(assets))
	for _, a := range assets {
		out = append(out, entity.StoredPrice{AssetID: a.ID, Last: decimal.NewFromInt(1)})
	}
	return out, nil
}
func (b *bindingProvider) BaseAssetSymbol() string         { return "USD" }
func (b *bindingProvider) BaseAssetType() entity.AssetType { return entity.AssetTypeForex }
func (b *bindingProvider) Binding() entity.PriceBinding    { return b.binding }

func marketAsset(id, symbol, market string) *entity.Asset {
	return &entity.Asset{ID: id, Symbol: symbol, Market: market, Type: entity.AssetTypeStock}
}

// fetchNamed runs a reconciliation fetch over the given assets against one
// provider, with the store answering the contest question as told.
func fetchNamed(t *testing.T, p PriceProvider, assets []*entity.Asset, contested map[string]bool) (*apiv1.FetchExternalPricesResponse, *mockStore) {
	t.Helper()
	s := &mockStore{}
	expectBaseAsset(s)
	s.On("ListAssetExternalRefs", mock.Anything, mock.Anything).
		Return([]*entity.AssetExternalRef{}, nil).Maybe()
	s.On("ContestedAssets", mock.Anything, mock.Anything, quarantineVerdicts).Return(contested, nil).Maybe()
	s.On("ListAssets", mock.Anything, mock.Anything).Return(assets, "", nil)
	s.On("RecordPriceAttempts", mock.Anything, mock.Anything).Return(nil).Maybe()
	s.On("DeferUnaskedPricing", mock.Anything, mock.Anything).Return(nil).Maybe()
	s.On("CreatePrices", mock.Anything, mock.Anything).Return(len(assets), nil).Maybe()

	resp, err := newHandler(s).WithProvider("venue", p).FetchExternalPrices(syncContext(),
		connect.NewRequest(&apiv1.FetchExternalPricesRequest{AssetIds: assetIDs(assets)}))
	require.NoError(t, err)
	return resp.Msg, s
}

// An adapter is never handed an asset its binding does not speak for. Before
// the gate, every provider received the whole selection and filtering was each
// adapter's own discipline — the discipline Binance once did not have.
func TestFetchExternalPrices_ProviderIsHandedOnlyWhatItsBindingAdmits(t *testing.T) {
	p := &bindingProvider{binding: entity.PriceBinding{Markets: []string{"moex"}}}
	fetchNamed(t, p, []*entity.Asset{
		marketAsset("sber", "SBER", "moex"),
		marketAsset("btc", "BTC", entity.MarketCrypto),
		marketAsset("aapl", "AAPL", "spbex"),
	}, nil)

	require.Len(t, p.priced, 1)
	assert.Equal(t, []string{"sber"}, p.priced[0])
}

// A ref binding admits a market to discovery but only bound assets to
// pricing: the ticker proposes a listing, the ref is the listing.
func TestFetchExternalPrices_RefBindingPricesOnlyBoundAssets(t *testing.T) {
	p := &bindingProvider{binding: entity.PriceBinding{Markets: []string{entity.MarketCrypto}, RefSource: "venue"}}
	bound := marketAsset("btc", "BTC", entity.MarketCrypto)
	bound.ExternalRefs = []entity.AssetExternalRef{{AssetID: "btc", Source: "venue", Ref: "BTCUSDT"}}
	unbound := marketAsset("eth", "ETH", entity.MarketCrypto)
	foreign := marketAsset("tok", "TOK", entity.MarketCrypto)
	foreign.ExternalRefs = []entity.AssetExternalRef{{AssetID: "tok", Source: "onchain:eth", Ref: "0xabc"}}

	fetchNamed(t, p, []*entity.Asset{bound, unbound, foreign}, nil)

	require.Len(t, p.discovered, 1)
	assert.ElementsMatch(t, []string{"btc", "eth", "tok"}, p.discovered[0], "discovery sees the whole admitted market")
	require.Len(t, p.priced, 1)
	assert.Equal(t, []string{"btc"}, p.priced[0], "a ref in another namespace is not a binding here")
}

// A contested ticker reaches no provider, not even through a binding made
// before the twin appeared, and the binding itself is left in place.
func TestFetchExternalPrices_ContestedTickerReachesNoProvider(t *testing.T) {
	p := &bindingProvider{binding: entity.PriceBinding{Markets: []string{entity.MarketCrypto}, RefSource: "venue"}}
	bound := marketAsset("usdt", "USDT", entity.MarketCrypto)
	bound.ExternalRefs = []entity.AssetExternalRef{{AssetID: "usdt", Source: "venue", Ref: "USDTUSDT"}}
	btc := marketAsset("btc", "BTC", entity.MarketCrypto)
	btc.ExternalRefs = []entity.AssetExternalRef{{AssetID: "btc", Source: "venue", Ref: "BTCUSDT"}}

	_, s := fetchNamed(t, p, []*entity.Asset{bound, btc}, map[string]bool{"usdt": true})

	require.Len(t, p.discovered, 1)
	assert.Equal(t, []string{"btc"}, p.discovered[0])
	require.Len(t, p.priced, 1)
	assert.Equal(t, []string{"btc"}, p.priced[0])
	s.AssertNotCalled(t, "DeleteAssetExternalRef", mock.Anything, mock.Anything)
}

// A batch emptied by a contest says so. A person asking for one contested
// asset's price otherwise gets a bare zero, indistinguishable from "nothing
// was due".
func TestFetchExternalPrices_ContestedBatchIsNamedIdle(t *testing.T) {
	p := &bindingProvider{binding: entity.PriceBinding{Markets: []string{entity.MarketCrypto}}}
	resp, _ := fetchNamed(t, p, []*entity.Asset{marketAsset("usdt", "USDT", entity.MarketCrypto)},
		map[string]bool{"usdt": true})

	assert.Empty(t, p.priced)
	assert.Equal(t, map[string]string{"venue": "contested"}, resp.GetIdleSources())
}

// The contest is a property of the catalogue, so one lookup serves every
// provider in the run: a second provider is not a second question.
func TestFetchExternalPrices_ContestAskedOncePerAsset(t *testing.T) {
	s := &mockStore{}
	expectBaseAsset(s)
	s.On("ListAssetExternalRefs", mock.Anything, mock.Anything).
		Return([]*entity.AssetExternalRef{}, nil).Maybe()
	s.On("ContestedAssets", mock.Anything, []string{"a"}, quarantineVerdicts).Return(map[string]bool{}, nil).Once()
	s.On("ListAssets", mock.Anything, mock.Anything).Return([]*entity.Asset{marketAsset("a", "A", "moex")}, "", nil)
	s.On("RecordPriceAttempts", mock.Anything, mock.Anything).Return(nil)
	s.On("CreatePrices", mock.Anything, mock.Anything).Return(1, nil)

	moex := entity.PriceBinding{Markets: []string{"moex"}}
	h := newHandler(s).
		WithProvider("one", &bindingProvider{binding: moex}).
		WithProvider("two", &bindingProvider{binding: moex})
	_, err := h.FetchExternalPrices(syncContext(),
		connect.NewRequest(&apiv1.FetchExternalPricesRequest{AssetIds: []string{"a"}}))
	require.NoError(t, err)
	s.AssertExpectations(t)
}

// "Could not check" is not "nobody contests it": a failed lookup withholds the
// batch and says so, rather than pricing assets it could not vouch for.
func TestFetchExternalPrices_ContestLookupFailureWithholdsTheBatch(t *testing.T) {
	p := &bindingProvider{binding: entity.PriceBinding{Markets: []string{"moex"}}}
	s := &mockStore{}
	s.On("ContestedAssets", mock.Anything, mock.Anything, mock.Anything).Return(nil, errors.New("db down"))
	s.On("ListAssets", mock.Anything, mock.Anything).Return([]*entity.Asset{marketAsset("sber", "SBER", "moex")}, "", nil)

	resp, err := newHandler(s).WithProvider("venue", p).FetchExternalPrices(syncContext(),
		connect.NewRequest(&apiv1.FetchExternalPricesRequest{AssetIds: []string{"sber"}}))
	require.NoError(t, err)
	assert.Empty(t, p.priced)
	require.Len(t, resp.Msg.Errors, 1)
	assert.Contains(t, resp.Msg.Errors[0], "contested")
}

// A contested asset is never asked about, so it has no attempt record — and is
// reported anyway, because the flag is a property of the catalogue.
func TestGetPricingStatus_ReportsAmbiguousTickerWithoutAttempts(t *testing.T) {
	asked := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	s := &mockStore{}
	s.On("PricingStatus", mock.Anything, []string{"silent", "twin", "fresh"}).Return([]*entity.AssetPricingStatus{
		{AssetID: "silent", FirstAskedAt: asked, LastAskedAt: asked, SourcesAsked: 2},
	}, nil)
	s.On("ContestedAssets", mock.Anything, []string{"silent", "twin", "fresh"}, quarantineVerdicts).
		Return(map[string]bool{"twin": true}, nil)

	resp, err := newHandler(s).GetPricingStatus(context.Background(), connect.NewRequest(
		&apiv1.GetPricingStatusRequest{AssetIds: []string{"silent", "twin", "fresh"}}))
	require.NoError(t, err)

	got := resp.Msg.GetStatuses()
	require.Len(t, got, 2, "an asset neither asked about nor contested stays absent")
	assert.Equal(t, "silent", got[0].GetAssetId())
	assert.False(t, got[0].GetAmbiguousTicker())
	assert.Equal(t, "twin", got[1].GetAssetId())
	assert.True(t, got[1].GetAmbiguousTicker())
	assert.Nil(t, got[1].GetFirstAskedAt(), "no attempt record is invented")
}

// A ref is identity for every holder, so only the sync binding what its
// provider reported, or an admin, may name one (personal-l4tc).
func TestFindOrCreateAsset_ProviderNamespaceRefNeedsAuthority(t *testing.T) {
	user := middleware.ContextWithUser(context.Background(), &entity.User{ID: "user-1"})
	request := func(source, ref string) *connect.Request[apiv1.FindOrCreateAssetRequest] {
		return connect.NewRequest(&apiv1.FindOrCreateAssetRequest{
			Symbol: "BTC", Type: apiv1.AssetType_ASSET_TYPE_CRYPTOCURRENCY,
			ExternalRefSource: &source, ExternalRef: &ref,
		})
	}
	resolving := func() *mockStore {
		s := &mockStore{}
		s.On("FindAssetIDByExternalRef", mock.Anything, mock.Anything, mock.Anything).Return("id-1", nil)
		s.On("GetAsset", mock.Anything, "id-1").Return(testAsset("id-1"), nil)
		expectVerdict(s)
		return s
	}

	t.Run("an RPC caller is refused before the store is touched", func(t *testing.T) {
		_, err := newHandler(&mockStore{}).FindOrCreateAsset(user, request("binance", "PEPEUSDT"))
		assert.Equal(t, connect.CodePermissionDenied, connect.CodeOf(err))
	})
	t.Run("the sync binds what its provider reported", func(t *testing.T) {
		_, err := newHandler(resolving()).FindOrCreateAsset(middleware.WithSyncAuthority(user), request("tinvest", "BBG000000001"))
		require.NoError(t, err)
	})
	t.Run("an admin may bind by hand", func(t *testing.T) {
		_, err := newHandler(resolving()).FindOrCreateAsset(adminContext(), request("binance", "BTCUSDT"))
		require.NoError(t, err)
	})
	t.Run("a chain ref is refused too: the resolver picks a market, not who may squat", func(t *testing.T) {
		_, err := newHandler(&mockStore{}).FindOrCreateAsset(user, request("onchain:eth", "0xCAFE"))
		assert.Equal(t, connect.CodePermissionDenied, connect.CodeOf(err))
	})
	t.Run("the sync binds a contract its wallet reported", func(t *testing.T) {
		_, err := newHandler(resolving()).FindOrCreateAsset(middleware.WithSyncAuthority(user), request("onchain:eth", "0xCAFE"))
		require.NoError(t, err)
	})
}
