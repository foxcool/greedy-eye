package portfolio

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"testing"
	"time"

	"connectrpc.com/connect"
	apiv1 "github.com/foxcool/greedy-eye/internal/api/v1"
	"github.com/foxcool/greedy-eye/internal/entity"
	"github.com/foxcool/greedy-eye/internal/middleware"
	"github.com/foxcool/greedy-eye/internal/store"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
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
	testUserID      = "01926d35-6a1e-7001-8001-000000000001"
	testUserID2     = "01926d35-6a1e-7001-8001-000000000002"
	testPortfolioID = "01926d35-6a1e-7002-8002-000000000001"
	testPortfolioID2 = "01926d35-6a1e-7002-8002-000000000002"
	testAccountID   = "01926d35-6a1e-7003-8003-000000000001"
	testHoldingID   = "01926d35-6a1e-7004-8004-000000000001"
	testHoldingID2  = "01926d35-6a1e-7004-8004-000000000002"
	testAssetID     = "01926d35-6a1e-7005-8005-000000000001"
	testTxID        = "01926d35-6a1e-7006-8006-000000000001"
)

// --- Helpers ---

func newHandler(s Store) *Handler {
	return NewHandler(s, slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError})))
}

func ctxWithUser(userID string) context.Context {
	return middleware.ContextWithUser(context.Background(), &entity.User{ID: userID})
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
		Amount:    100000,
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
		Holding: &apiv1.Holding{AssetId: testAssetID, AccountId: testAccountID, Amount: 100000, Decimals: 8},
	}))
	require.NoError(t, err)
	assert.Equal(t, testHoldingID, resp.Msg.Id)
}

func TestListHoldings_WithFilters(t *testing.T) {
	s := &mockStore{}
	portfolioID := testPortfolioID
	s.On("ListHoldings", mock.Anything, ListHoldingsOpts{PortfolioID: testPortfolioID}).
		Return([]*entity.Holding{testHolding(testHoldingID), testHolding(testHoldingID2)}, "next", nil)
	h := newHandler(s)

	resp, err := h.ListHoldings(context.Background(), connect.NewRequest(&apiv1.ListHoldingsRequest{
		PortfolioId: &portfolioID,
	}))
	require.NoError(t, err)
	assert.Len(t, resp.Msg.Holdings, 2)
	assert.Equal(t, "next", resp.Msg.NextPageToken)
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

// --- Tests: Stubs return Unimplemented ---

func TestStubs_ReturnUnimplemented(t *testing.T) {
	h := newHandler(&mockStore{})
	ctx := context.Background()

	_, err := h.CalculatePortfolioValue(ctx, connect.NewRequest(&apiv1.CalculatePortfolioValueRequest{}))
	assert.Equal(t, connect.CodeUnimplemented, connect.CodeOf(err))

	_, err = h.GetPortfolioPerformance(ctx, connect.NewRequest(&apiv1.GetPortfolioPerformanceRequest{}))
	assert.Equal(t, connect.CodeUnimplemented, connect.CodeOf(err))
}
