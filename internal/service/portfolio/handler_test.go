package portfolio

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"slices"
	"testing"
	"time"

	"connectrpc.com/connect"
	apiv1 "github.com/foxcool/greedy-eye/api/v1"
	"github.com/foxcool/greedy-eye/internal/entity"
	"github.com/foxcool/greedy-eye/internal/middleware"
	"github.com/foxcool/greedy-eye/internal/store"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/fieldmaskpb"
)

// --- Mock Store ---

type mockStore struct {
	mock.Mock
}

func (m *mockStore) CreatePortfolio(ctx context.Context, p *entity.Portfolio) (*entity.Portfolio, error) {
	args := m.Called(ctx, p)
	if v := args.Get(0); v != nil {
		return v.(*entity.Portfolio), args.Error(1)
	}
	return nil, args.Error(1)
}

func (m *mockStore) GetPortfolio(ctx context.Context, id string) (*entity.Portfolio, error) {
	args := m.Called(ctx, id)
	if v := args.Get(0); v != nil {
		return v.(*entity.Portfolio), args.Error(1)
	}
	return nil, args.Error(1)
}

func (m *mockStore) UpdatePortfolio(ctx context.Context, p *entity.Portfolio, fields []string) (*entity.Portfolio, error) {
	args := m.Called(ctx, p, fields)
	if v := args.Get(0); v != nil {
		return v.(*entity.Portfolio), args.Error(1)
	}
	return nil, args.Error(1)
}

func (m *mockStore) DeletePortfolio(ctx context.Context, id string) error {
	return m.Called(ctx, id).Error(0)
}

func (m *mockStore) ListPortfolios(ctx context.Context, opts ListPortfoliosOpts) ([]*entity.Portfolio, string, error) {
	args := m.Called(ctx, opts)
	if v := args.Get(0); v != nil {
		return v.([]*entity.Portfolio), args.String(1), args.Error(2)
	}
	return nil, args.String(1), args.Error(2)
}

func (m *mockStore) CreateAccount(ctx context.Context, a *entity.Account) (*entity.Account, error) {
	args := m.Called(ctx, a)
	if v := args.Get(0); v != nil {
		return v.(*entity.Account), args.Error(1)
	}
	return nil, args.Error(1)
}

func (m *mockStore) GetAccount(ctx context.Context, id string) (*entity.Account, error) {
	args := m.Called(ctx, id)
	if v := args.Get(0); v != nil {
		return v.(*entity.Account), args.Error(1)
	}
	return nil, args.Error(1)
}

func (m *mockStore) UpdateAccount(ctx context.Context, a *entity.Account, fields []string) (*entity.Account, error) {
	args := m.Called(ctx, a, fields)
	if v := args.Get(0); v != nil {
		return v.(*entity.Account), args.Error(1)
	}
	return nil, args.Error(1)
}

func (m *mockStore) DeleteAccount(ctx context.Context, id string) error {
	return m.Called(ctx, id).Error(0)
}

func (m *mockStore) ListAccounts(ctx context.Context, opts ListAccountsOpts) ([]*entity.Account, string, error) {
	args := m.Called(ctx, opts)
	if v := args.Get(0); v != nil {
		return v.([]*entity.Account), args.String(1), args.Error(2)
	}
	return nil, args.String(1), args.Error(2)
}

func (m *mockStore) CreateHolding(ctx context.Context, h *entity.Holding) (*entity.Holding, error) {
	args := m.Called(ctx, h)
	if v := args.Get(0); v != nil {
		return v.(*entity.Holding), args.Error(1)
	}
	return nil, args.Error(1)
}

func (m *mockStore) GetHolding(ctx context.Context, id string) (*entity.Holding, error) {
	args := m.Called(ctx, id)
	if v := args.Get(0); v != nil {
		return v.(*entity.Holding), args.Error(1)
	}
	return nil, args.Error(1)
}

func (m *mockStore) UpdateHolding(ctx context.Context, h *entity.Holding, fields []string) (*entity.Holding, error) {
	args := m.Called(ctx, h, fields)
	if v := args.Get(0); v != nil {
		return v.(*entity.Holding), args.Error(1)
	}
	return nil, args.Error(1)
}

func (m *mockStore) DeleteHolding(ctx context.Context, id string) error {
	return m.Called(ctx, id).Error(0)
}

func (m *mockStore) ListHoldings(ctx context.Context, opts ListHoldingsOpts) ([]*entity.Holding, string, error) {
	args := m.Called(ctx, opts)
	if v := args.Get(0); v != nil {
		return v.([]*entity.Holding), args.String(1), args.Error(2)
	}
	return nil, args.String(1), args.Error(2)
}

func (m *mockStore) CreateTransaction(ctx context.Context, t *entity.Transaction) (*entity.Transaction, error) {
	args := m.Called(ctx, t)
	if v := args.Get(0); v != nil {
		return v.(*entity.Transaction), args.Error(1)
	}
	return nil, args.Error(1)
}

func (m *mockStore) GetTransaction(ctx context.Context, id string) (*entity.Transaction, error) {
	args := m.Called(ctx, id)
	if v := args.Get(0); v != nil {
		return v.(*entity.Transaction), args.Error(1)
	}
	return nil, args.Error(1)
}

func (m *mockStore) UpdateTransaction(ctx context.Context, t *entity.Transaction, fields []string) (*entity.Transaction, error) {
	args := m.Called(ctx, t, fields)
	if v := args.Get(0); v != nil {
		return v.(*entity.Transaction), args.Error(1)
	}
	return nil, args.Error(1)
}

func (m *mockStore) ListTransactions(ctx context.Context, opts ListTransactionsOpts) ([]*entity.Transaction, string, error) {
	args := m.Called(ctx, opts)
	if v := args.Get(0); v != nil {
		return v.([]*entity.Transaction), args.String(1), args.Error(2)
	}
	return nil, args.String(1), args.Error(2)
}

// Fixed UUID v7 constants for use across unit tests.
// Using real UUID format so tests remain valid if UUID validation moves to handler layer.
const (
	testUserID       = "01926d35-6a1e-7001-8001-000000000001"
	testUserID2      = "01926d35-6a1e-7001-8001-000000000002"
	testPortfolioID  = "01926d35-6a1e-7002-8002-000000000001"
	testPortfolioID2 = "01926d35-6a1e-7002-8002-000000000002"
	testAccountID    = "01926d35-6a1e-7003-8003-000000000001"
	testHoldingID    = "01926d35-6a1e-7004-8004-000000000001"
	testHoldingID2   = "01926d35-6a1e-7004-8004-000000000002"
	testAssetID      = "01926d35-6a1e-7005-8005-000000000001"
	testTxID         = "01926d35-6a1e-7006-8006-000000000001"
)

// --- Helpers ---

func newHandler(s Store) *Handler {
	return NewHandler(s, slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError})))
}

func ctxWithUser(userID string) context.Context {
	return middleware.ContextWithUser(context.Background(), &entity.User{ID: userID})
}

func ctxWithAdmin(userID string) context.Context {
	return middleware.ContextWithUser(context.Background(), &entity.User{ID: userID, Roles: []string{"admin"}})
}

func testPortfolio(id string) *entity.Portfolio {
	return &entity.Portfolio{
		ID:        id,
		UserID:    testUserID,
		Name:      "My Portfolio",
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
}

func testAccount(id string) *entity.Account {
	return &entity.Account{
		ID:        id,
		UserID:    testUserID,
		Name:      "Binance",
		Type:      entity.AccountTypeExchange,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
}

func testHolding(id string) *entity.Holding {
	return &entity.Holding{
		ID:        id,
		Amount:    decimal.NewFromInt(100000),
		Decimals:  8,
		AssetID:   testAssetID,
		AccountID: testAccountID,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
}

// --- Tests: Portfolio ---

func TestCreatePortfolio_MissingPortfolio(t *testing.T) {
	h := newHandler(&mockStore{})
	_, err := h.CreatePortfolio(context.Background(), connect.NewRequest(&apiv1.CreatePortfolioRequest{}))
	require.Error(t, err)
	assert.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))
}

func TestCreatePortfolio_StoreError(t *testing.T) {
	s := &mockStore{}
	s.On("CreatePortfolio", mock.Anything, mock.Anything).Return(nil, errors.New("db error"))
	h := newHandler(s)

	_, err := h.CreatePortfolio(ctxWithUser(testUserID), connect.NewRequest(&apiv1.CreatePortfolioRequest{
		Portfolio: &apiv1.Portfolio{Name: "Test", UserId: testUserID},
	}))
	require.Error(t, err)
	assert.Equal(t, connect.CodeInternal, connect.CodeOf(err))
}

func TestCreatePortfolio_OK(t *testing.T) {
	s := &mockStore{}
	s.On("CreatePortfolio", mock.Anything, mock.Anything).Return(testPortfolio(testPortfolioID), nil)
	h := newHandler(s)

	resp, err := h.CreatePortfolio(ctxWithUser(testUserID), connect.NewRequest(&apiv1.CreatePortfolioRequest{
		Portfolio: &apiv1.Portfolio{Name: "My Portfolio", UserId: testUserID},
	}))
	require.NoError(t, err)
	assert.Equal(t, testPortfolioID, resp.Msg.Id)
	assert.Equal(t, "My Portfolio", resp.Msg.Name)
}

func TestGetPortfolio_MissingID(t *testing.T) {
	h := newHandler(&mockStore{})
	_, err := h.GetPortfolio(context.Background(), connect.NewRequest(&apiv1.GetPortfolioRequest{}))
	require.Error(t, err)
	assert.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))
}

func TestGetPortfolio_NotFound(t *testing.T) {
	s := &mockStore{}
	s.On("GetPortfolio", mock.Anything, testPortfolioID2).Return(nil, store.ErrNotFound)
	h := newHandler(s)

	_, err := h.GetPortfolio(context.Background(), connect.NewRequest(&apiv1.GetPortfolioRequest{Id: testPortfolioID2}))
	require.Error(t, err)
	assert.Equal(t, connect.CodeNotFound, connect.CodeOf(err))
}

func TestGetPortfolio_OK(t *testing.T) {
	s := &mockStore{}
	s.On("GetPortfolio", mock.Anything, testPortfolioID).Return(testPortfolio(testPortfolioID), nil)
	h := newHandler(s)

	resp, err := h.GetPortfolio(context.Background(), connect.NewRequest(&apiv1.GetPortfolioRequest{Id: testPortfolioID}))
	require.NoError(t, err)
	assert.Equal(t, testPortfolioID, resp.Msg.Id)
}

func TestDeletePortfolio_MissingID(t *testing.T) {
	h := newHandler(&mockStore{})
	_, err := h.DeletePortfolio(context.Background(), connect.NewRequest(&apiv1.DeletePortfolioRequest{}))
	require.Error(t, err)
	assert.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))
}

func TestDeletePortfolio_OK(t *testing.T) {
	s := &mockStore{}
	s.On("DeletePortfolio", mock.Anything, testPortfolioID).Return(nil)
	h := newHandler(s)

	_, err := h.DeletePortfolio(context.Background(), connect.NewRequest(&apiv1.DeletePortfolioRequest{Id: testPortfolioID}))
	require.NoError(t, err)
}

func TestListPortfolios_WithUserFilter(t *testing.T) {
	s := &mockStore{}
	userID := testUserID
	s.On("ListPortfolios", mock.Anything, ListPortfoliosOpts{UserID: testUserID}).
		Return([]*entity.Portfolio{testPortfolio(testPortfolioID)}, "", nil)
	h := newHandler(s)

	resp, err := h.ListPortfolios(ctxWithUser(testUserID), connect.NewRequest(&apiv1.ListPortfoliosRequest{
		UserId: &userID,
	}))
	require.NoError(t, err)
	assert.Len(t, resp.Msg.Portfolios, 1)
}

// --- Tests: Account ---

func TestCreateAccount_MissingAccount(t *testing.T) {
	h := newHandler(&mockStore{})
	_, err := h.CreateAccount(context.Background(), connect.NewRequest(&apiv1.CreateAccountRequest{}))
	require.Error(t, err)
	assert.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))
}

func TestCreateAccount_OK(t *testing.T) {
	s := &mockStore{}
	s.On("CreateAccount", mock.Anything, mock.Anything).Return(testAccount(testAccountID), nil)
	h := newHandler(s)

	resp, err := h.CreateAccount(ctxWithUser(testUserID), connect.NewRequest(&apiv1.CreateAccountRequest{
		Account: &apiv1.Account{Name: "Binance", UserId: testUserID},
	}))
	require.NoError(t, err)
	assert.Equal(t, testAccountID, resp.Msg.Id)
}

func TestGetAccount_NotFound(t *testing.T) {
	s := &mockStore{}
	s.On("GetAccount", mock.Anything, testAccountID).Return(nil, store.ErrNotFound)
	h := newHandler(s)

	_, err := h.GetAccount(context.Background(), connect.NewRequest(&apiv1.GetAccountRequest{Id: testAccountID}))
	require.Error(t, err)
	assert.Equal(t, connect.CodeNotFound, connect.CodeOf(err))
}

func TestDeleteAccount_OK(t *testing.T) {
	s := &mockStore{}
	s.On("DeleteAccount", mock.Anything, testAccountID).Return(nil)
	h := newHandler(s)

	_, err := h.DeleteAccount(context.Background(), connect.NewRequest(&apiv1.DeleteAccountRequest{Id: testAccountID}))
	require.NoError(t, err)
}

func TestIsSecretKey(t *testing.T) {
	secret := []string{"api_key", "api_secret", "gate_token", "password", "PRIVATE_KEY"}
	for _, k := range secret {
		assert.True(t, isSecretKey(k), k)
	}
	plain := []string{"provider", "address", "chain", "pro", "label"}
	for _, k := range plain {
		assert.False(t, isSecretKey(k), k)
	}
}

func TestMaskSecrets(t *testing.T) {
	assert.Nil(t, maskSecrets(nil))

	data := map[string]string{
		"provider":   "binance",
		"api_key":    "abcdef1a2b",
		"api_secret": "short",
	}
	masked := maskSecrets(data)
	assert.Equal(t, "binance", masked["provider"])
	assert.Equal(t, maskPrefix+"1a2b", masked["api_key"])
	assert.Equal(t, maskPrefix, masked["api_secret"])
	// input map is shared with the credentials resolver and must stay intact
	assert.Equal(t, "abcdef1a2b", data["api_key"])
}

func TestCreateAccount_MasksSecretsInResponse(t *testing.T) {
	created := testAccount(testAccountID)
	created.Data = map[string]string{"provider": "binance", "api_key": "abcdef1a2b"}
	created.Capabilities = []entity.AccountCapability{entity.CapabilityMarketData}

	s := &mockStore{}
	s.On("CreateAccount", mock.Anything, mock.Anything).Return(created, nil)
	h := newHandler(s)

	resp, err := h.CreateAccount(ctxWithUser(testUserID), connect.NewRequest(&apiv1.CreateAccountRequest{
		Account: &apiv1.Account{Name: "Binance", Data: map[string]string{"provider": "binance", "api_key": "abcdef1a2b"}},
	}))
	require.NoError(t, err)
	assert.Equal(t, maskPrefix+"1a2b", resp.Msg.Data["api_key"])
	assert.Equal(t, "binance", resp.Msg.Data["provider"])
	assert.Equal(t, []string{"market_data"}, resp.Msg.Capabilities)
}

func TestCreateAccount_RejectsMaskedValue(t *testing.T) {
	h := newHandler(&mockStore{})
	_, err := h.CreateAccount(ctxWithUser(testUserID), connect.NewRequest(&apiv1.CreateAccountRequest{
		Account: &apiv1.Account{Name: "Binance", Data: map[string]string{"api_key": maskPrefix + "1a2b"}},
	}))
	require.Error(t, err)
	assert.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))
}

func TestCreateAccount_SystemScopesRequireAdmin(t *testing.T) {
	account := &apiv1.Account{
		Name:         "Moralis",
		Type:         apiv1.AccountType(entity.AccountTypeService),
		Capabilities: []string{"onchain_lookup"},
		SystemScopes: []string{"onchain_lookup"},
	}

	h := newHandler(&mockStore{})
	_, err := h.CreateAccount(ctxWithUser(testUserID), connect.NewRequest(&apiv1.CreateAccountRequest{Account: account}))
	require.Error(t, err)
	assert.Equal(t, connect.CodePermissionDenied, connect.CodeOf(err))

	s := &mockStore{}
	s.On("CreateAccount", mock.Anything, mock.Anything).Return(testAccount(testAccountID), nil)
	h = newHandler(s)
	_, err = h.CreateAccount(ctxWithAdmin(testUserID), connect.NewRequest(&apiv1.CreateAccountRequest{Account: account}))
	require.NoError(t, err)
}

func TestGetAccount_MasksSecrets(t *testing.T) {
	account := testAccount(testAccountID)
	account.Data = map[string]string{"api_secret": "abcdefx9z0"}

	s := &mockStore{}
	s.On("GetAccount", mock.Anything, testAccountID).Return(account, nil)
	h := newHandler(s)

	resp, err := h.GetAccount(context.Background(), connect.NewRequest(&apiv1.GetAccountRequest{Id: testAccountID}))
	require.NoError(t, err)
	assert.Equal(t, maskPrefix+"x9z0", resp.Msg.Data["api_secret"])
}

func TestUpdateAccount_MaskedSecretPreserved(t *testing.T) {
	existing := testAccount(testAccountID)
	existing.Data = map[string]string{"provider": "binance", "api_key": "abcdef1a2b"}

	s := &mockStore{}
	s.On("GetAccount", mock.Anything, testAccountID).Return(existing, nil)
	s.On("UpdateAccount", mock.Anything, mock.MatchedBy(func(a *entity.Account) bool {
		return a.Data["api_key"] == "abcdef1a2b" && a.Data["provider"] == "kraken"
	}), mock.Anything).Return(existing, nil)
	h := newHandler(s)

	_, err := h.UpdateAccount(ctxWithUser(testUserID), connect.NewRequest(&apiv1.UpdateAccountRequest{
		Account: &apiv1.Account{
			Id:   testAccountID,
			Data: map[string]string{"provider": "kraken", "api_key": maskPrefix + "1a2b"},
		},
	}))
	require.NoError(t, err)
	s.AssertExpectations(t)
}

func TestUpdateAccount_MaskedValueWithoutStoredSecret(t *testing.T) {
	existing := testAccount(testAccountID)
	existing.Data = map[string]string{"provider": "binance"}

	s := &mockStore{}
	s.On("GetAccount", mock.Anything, testAccountID).Return(existing, nil)
	h := newHandler(s)

	_, err := h.UpdateAccount(ctxWithUser(testUserID), connect.NewRequest(&apiv1.UpdateAccountRequest{
		Account: &apiv1.Account{Id: testAccountID, Data: map[string]string{"api_key": maskPrefix + "1a2b"}},
	}))
	require.Error(t, err)
	assert.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))
}

func TestUpdateAccount_SystemScopesNotInDefaultMask(t *testing.T) {
	s := &mockStore{}
	s.On("UpdateAccount", mock.Anything, mock.Anything, mock.MatchedBy(func(fields []string) bool {
		return !slices.Contains(fields, "system_scopes") && slices.Contains(fields, "capabilities")
	})).Return(testAccount(testAccountID), nil)
	h := newHandler(s)

	// no admin role needed: system_scopes stays untouched without an explicit mask
	_, err := h.UpdateAccount(ctxWithUser(testUserID), connect.NewRequest(&apiv1.UpdateAccountRequest{
		Account: &apiv1.Account{Id: testAccountID, Name: "Renamed", SystemScopes: []string{"market_data"}},
	}))
	require.NoError(t, err)
	s.AssertExpectations(t)
}

func TestUpdateAccount_SystemScopesRequireAdmin(t *testing.T) {
	req := connect.NewRequest(&apiv1.UpdateAccountRequest{
		Account:    &apiv1.Account{Id: testAccountID, SystemScopes: []string{"market_data"}},
		UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"system_scopes"}},
	})

	h := newHandler(&mockStore{})
	_, err := h.UpdateAccount(ctxWithUser(testUserID), req)
	require.Error(t, err)
	assert.Equal(t, connect.CodePermissionDenied, connect.CodeOf(err))

	_, err = h.UpdateAccount(context.Background(), req)
	require.Error(t, err)
	assert.Equal(t, connect.CodeUnauthenticated, connect.CodeOf(err))

	s := &mockStore{}
	s.On("UpdateAccount", mock.Anything, mock.Anything, []string{"system_scopes"}).Return(testAccount(testAccountID), nil)
	h = newHandler(s)
	_, err = h.UpdateAccount(ctxWithAdmin(testUserID), req)
	require.NoError(t, err)
	s.AssertExpectations(t)
}

// --- Tests: Holding ---

func TestCreateHolding_MissingHolding(t *testing.T) {
	h := newHandler(&mockStore{})
	_, err := h.CreateHolding(context.Background(), connect.NewRequest(&apiv1.CreateHoldingRequest{}))
	require.Error(t, err)
	assert.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))
}

func TestCreateHolding_OK(t *testing.T) {
	s := &mockStore{}
	s.On("CreateHolding", mock.Anything, mock.Anything).Return(testHolding(testHoldingID), nil)
	h := newHandler(s)

	resp, err := h.CreateHolding(context.Background(), connect.NewRequest(&apiv1.CreateHoldingRequest{
		Holding: &apiv1.Holding{AssetId: testAssetID, AccountId: testAccountID, Amount: "100000", Decimals: 8},
	}))
	require.NoError(t, err)
	assert.Equal(t, testHoldingID, resp.Msg.Id)
}

func TestListHoldings_WithFilters(t *testing.T) {
	s := &mockStore{}
	portfolioID := testPortfolioID
	s.On("ListHoldings", mock.Anything, ListHoldingsOpts{UserID: testUserID, PortfolioID: testPortfolioID}).
		Return([]*entity.Holding{testHolding(testHoldingID), testHolding(testHoldingID2)}, "next", nil)
	h := newHandler(s)

	resp, err := h.ListHoldings(ctxWithUser(testUserID), connect.NewRequest(&apiv1.ListHoldingsRequest{
		PortfolioId: &portfolioID,
	}))
	require.NoError(t, err)
	assert.Len(t, resp.Msg.Holdings, 2)
	assert.Equal(t, "next", resp.Msg.NextPageToken)
}

func TestListHoldings_Unauthenticated(t *testing.T) {
	h := newHandler(&mockStore{})

	_, err := h.ListHoldings(context.Background(), connect.NewRequest(&apiv1.ListHoldingsRequest{}))
	require.Error(t, err)
	assert.Equal(t, connect.CodeUnauthenticated, connect.CodeOf(err))
}

// --- Tests: Transaction ---

func TestCreateTransaction_MissingTransaction(t *testing.T) {
	h := newHandler(&mockStore{})
	_, err := h.CreateTransaction(context.Background(), connect.NewRequest(&apiv1.CreateTransactionRequest{}))
	require.Error(t, err)
	assert.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))
}

func TestCreateTransaction_OK(t *testing.T) {
	s := &mockStore{}
	s.On("CreateTransaction", mock.Anything, mock.Anything).Return(&entity.Transaction{
		ID:        testTxID,
		Type:      entity.TransactionTypeTrade,
		Status:    entity.TransactionStatusCompleted,
		AccountID: testAccountID,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}, nil)
	h := newHandler(s)

	resp, err := h.CreateTransaction(context.Background(), connect.NewRequest(&apiv1.CreateTransactionRequest{
		Transaction: &apiv1.Transaction{AccountId: testAccountID},
	}))
	require.NoError(t, err)
	assert.Equal(t, testTxID, resp.Msg.Id)
}

// TestCalculatePortfolioValue_CrossRate verifies a holding priced only in its own
// traded pair (USDT) is valued in the requested quote (USD) via a cross rate, and that
// a depeg in the USDT/USD leg is reflected rather than assuming USDT == 1 USD.
func TestCalculatePortfolioValue_CrossRate(t *testing.T) {
	const (
		assetX   = "00000000-0000-0000-0000-0000000000a1"
		usdtUUID = "00000000-0000-0000-0000-0000000000d7"
	)
	s := &mockStore{}
	s.On("ListHoldings", mock.Anything, mock.Anything).Return([]*entity.Holding{{
		ID: testHoldingID, AssetID: assetX, Amount: decimal.NewFromInt(100000000), Decimals: 8, // 1.0 token
	}}, "", nil)

	md := &mockMDClient{}
	// 1. No direct X/USD price.
	md.On("GetLatestPrice", mock.Anything, mock.MatchedBy(func(r *connect.Request[apiv1.GetLatestPriceRequest]) bool {
		return r.Msg.AssetId == assetX && r.Msg.BaseAssetId == "USD"
	})).Return(nil, connect.NewError(connect.CodeNotFound, errors.New("not found")))
	// 2. X actually trades in USDT at 2.0.
	md.On("GetLatestPrice", mock.Anything, mock.MatchedBy(func(r *connect.Request[apiv1.GetLatestPriceRequest]) bool {
		return r.Msg.AssetId == assetX && r.Msg.BaseAssetId == ""
	})).Return(connect.NewResponse(&apiv1.Price{Last: "200000000", Decimals: 8, BaseAssetId: usdtUUID}), nil)
	// 3. USDT depegged to 0.99 USD.
	md.On("GetLatestPrice", mock.Anything, mock.MatchedBy(func(r *connect.Request[apiv1.GetLatestPriceRequest]) bool {
		return r.Msg.AssetId == usdtUUID && r.Msg.BaseAssetId == "USD"
	})).Return(connect.NewResponse(&apiv1.Price{Last: "99000000", Decimals: 8, BaseAssetId: "USD"}), nil)

	h := newHandler(s).WithMarketDataClient(md)
	resp, err := h.CalculatePortfolioValue(context.Background(), connect.NewRequest(&apiv1.CalculatePortfolioValueRequest{
		PortfolioId: testPortfolioID,
	}))
	require.NoError(t, err)
	// 1.0 token × 2.0 USDT × 0.99 USD/USDT = 1.98 USD → 198 (2 decimals).
	assert.Equal(t, "198", resp.Msg.TotalValueAmount)
	assert.Equal(t, uint32(2), resp.Msg.Decimals)
}

// --- Tests: Stubs return Unimplemented ---

func TestStubs_ReturnUnimplemented(t *testing.T) {
	h := newHandler(&mockStore{})
	ctx := context.Background()

	_, err := h.CalculatePortfolioValue(ctx, connect.NewRequest(&apiv1.CalculatePortfolioValueRequest{}))
	assert.Equal(t, connect.CodeUnimplemented, connect.CodeOf(err))

	_, err = h.GetPortfolioPerformance(ctx, connect.NewRequest(&apiv1.GetPortfolioPerformanceRequest{}))
	assert.Equal(t, connect.CodeUnimplemented, connect.CodeOf(err))
}

// --- Mock WalletSyncer ---

type mockWalletSyncer struct {
	mock.Mock
}

func (m *mockWalletSyncer) SyncWallet(ctx context.Context, address string, chains []string) ([]entity.WalletBalance, error) {
	args := m.Called(ctx, address, chains)
	if v := args.Get(0); v != nil {
		return v.([]entity.WalletBalance), args.Error(1)
	}
	return nil, args.Error(1)
}

// --- Mock MarketDataClient ---

type mockMDClient struct {
	mock.Mock
}

func (m *mockMDClient) CreateAsset(ctx context.Context, req *connect.Request[apiv1.CreateAssetRequest]) (*connect.Response[apiv1.Asset], error) {
	args := m.Called(ctx, req)
	if v := args.Get(0); v != nil {
		return v.(*connect.Response[apiv1.Asset]), args.Error(1)
	}
	return nil, args.Error(1)
}

func (m *mockMDClient) ListAssets(ctx context.Context, req *connect.Request[apiv1.ListAssetsRequest]) (*connect.Response[apiv1.ListAssetsResponse], error) {
	args := m.Called(ctx, req)
	if v := args.Get(0); v != nil {
		return v.(*connect.Response[apiv1.ListAssetsResponse]), args.Error(1)
	}
	return nil, args.Error(1)
}

func (m *mockMDClient) GetLatestPrice(ctx context.Context, req *connect.Request[apiv1.GetLatestPriceRequest]) (*connect.Response[apiv1.Price], error) {
	args := m.Called(ctx, req)
	if v := args.Get(0); v != nil {
		return v.(*connect.Response[apiv1.Price]), args.Error(1)
	}
	return nil, args.Error(1)
}

func (m *mockMDClient) ListPriceHistory(ctx context.Context, req *connect.Request[apiv1.ListPriceHistoryRequest]) (*connect.Response[apiv1.ListPriceHistoryResponse], error) {
	args := m.Called(ctx, req)
	if v := args.Get(0); v != nil {
		return v.(*connect.Response[apiv1.ListPriceHistoryResponse]), args.Error(1)
	}
	return nil, args.Error(1)
}

func (m *mockMDClient) FetchExternalPrices(ctx context.Context, req *connect.Request[apiv1.FetchExternalPricesRequest]) (*connect.Response[apiv1.FetchExternalPricesResponse], error) {
	args := m.Called(ctx, req)
	if v := args.Get(0); v != nil {
		return v.(*connect.Response[apiv1.FetchExternalPricesResponse]), args.Error(1)
	}
	return nil, args.Error(1)
}

// TestSyncAccount_LargeBalance verifies a uint256 balance that overflows int64 is stored
// losslessly as a decimal string (regression for the bigint storage limit).
func TestSyncAccount_LargeBalance(t *testing.T) {
	// 1000 ETH at 18 decimals = 1e21, well above int64 max (~9.2e18).
	const bigAmount = "1000000000000000000000"

	acct := testAccount(testAccountID)
	acct.Type = entity.AccountTypeWallet
	acct.Data = map[string]string{"address": "0xabc", "chain": "eth"}

	s := &mockStore{}
	s.On("GetAccount", mock.Anything, testAccountID).Return(acct, nil)
	s.On("ListHoldings", mock.Anything, mock.Anything).Return([]*entity.Holding{}, "", nil)
	// The created holding must carry the full uint256 string, not a truncated int64.
	s.On("CreateHolding", mock.Anything, mock.MatchedBy(func(h *entity.Holding) bool {
		return h.Amount.String() == bigAmount && h.Decimals == 18
	})).Return(&entity.Holding{ID: testHoldingID}, nil)

	ws := &mockWalletSyncer{}
	ws.On("SyncWallet", mock.Anything, "0xabc", []string{"eth"}).Return([]entity.WalletBalance{
		{Symbol: "ETH", Name: "Ethereum", Amount: bigAmount, Decimals: 18},
	}, nil)

	md := &mockMDClient{}
	md.On("ListAssets", mock.Anything, mock.Anything).
		Return(connect.NewResponse(&apiv1.ListAssetsResponse{}), nil)
	md.On("CreateAsset", mock.Anything, mock.Anything).
		Return(connect.NewResponse(&apiv1.Asset{Id: testAssetID}), nil)
	md.On("FetchExternalPrices", mock.Anything, mock.Anything).
		Return(connect.NewResponse(&apiv1.FetchExternalPricesResponse{}), nil)

	h := newHandler(s).WithMarketDataClient(md).WithWalletSyncer(ws)

	resp, err := h.SyncAccount(context.Background(), connect.NewRequest(&apiv1.SyncAccountRequest{
		AccountId: testAccountID,
	}))
	require.NoError(t, err)
	assert.Empty(t, resp.Msg.Errors)
	assert.Equal(t, int32(1), resp.Msg.AssetsUpserted)
	assert.Equal(t, int32(1), resp.Msg.HoldingsUpserted)
	s.AssertExpectations(t)
}

// TestSyncAccount_MergeMixedDecimals verifies the same symbol across chains with
// different decimals (USDC is 6 on Ethereum, 18 on BSC) and mixed case is merged by
// real quantity, not by summing raw integers at mismatched scales.
func TestSyncAccount_MergeMixedDecimals(t *testing.T) {
	acct := testAccount(testAccountID)
	acct.Type = entity.AccountTypeWallet
	acct.Data = map[string]string{"address": "0xabc", "chain": "eth,bsc"}

	s := &mockStore{}
	s.On("GetAccount", mock.Anything, testAccountID).Return(acct, nil)
	s.On("ListHoldings", mock.Anything, mock.Anything).Return([]*entity.Holding{}, "", nil)
	// 1.0 USDC (6 dec) + 2.0 USDC (18 dec) = 3.0 USDC, stored at max decimals (18).
	s.On("CreateHolding", mock.Anything, mock.MatchedBy(func(h *entity.Holding) bool {
		return h.Amount.String() == "3000000000000000000" && h.Decimals == 18
	})).Return(&entity.Holding{ID: testHoldingID}, nil)

	ws := &mockWalletSyncer{}
	ws.On("SyncWallet", mock.Anything, "0xabc", []string{"eth", "bsc"}).Return([]entity.WalletBalance{
		{Symbol: "usdc", Name: "USD Coin", Amount: "1000000", Decimals: 6},              // 1.0 on Ethereum
		{Symbol: "USDC", Name: "USD Coin", Amount: "2000000000000000000", Decimals: 18}, // 2.0 on BSC
	}, nil)

	md := &mockMDClient{}
	md.On("ListAssets", mock.Anything, mock.Anything).
		Return(connect.NewResponse(&apiv1.ListAssetsResponse{}), nil)
	md.On("CreateAsset", mock.Anything, mock.Anything).
		Return(connect.NewResponse(&apiv1.Asset{Id: testAssetID}), nil)
	md.On("FetchExternalPrices", mock.Anything, mock.Anything).
		Return(connect.NewResponse(&apiv1.FetchExternalPricesResponse{}), nil)

	h := newHandler(s).WithMarketDataClient(md).WithWalletSyncer(ws)

	resp, err := h.SyncAccount(context.Background(), connect.NewRequest(&apiv1.SyncAccountRequest{
		AccountId: testAccountID,
	}))
	require.NoError(t, err)
	assert.Empty(t, resp.Msg.Errors)
	// One asset, one holding — the two chain balances collapsed into a single USDC holding.
	assert.Equal(t, int32(1), resp.Msg.AssetsUpserted)
	assert.Equal(t, int32(1), resp.Msg.HoldingsUpserted)
	s.AssertExpectations(t)
}
