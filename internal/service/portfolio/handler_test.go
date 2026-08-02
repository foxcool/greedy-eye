package portfolio

import (
	"context"
	"errors"
	"fmt"
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

func (m *mockStore) DeleteAccountWithHoldings(ctx context.Context, id string) error {
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

	_, err := h.GetPortfolio(ctxWithUser(testUserID), connect.NewRequest(&apiv1.GetPortfolioRequest{Id: testPortfolioID2}))
	require.Error(t, err)
	assert.Equal(t, connect.CodeNotFound, connect.CodeOf(err))
}

func TestGetPortfolio_OK(t *testing.T) {
	s := &mockStore{}
	s.On("GetPortfolio", mock.Anything, testPortfolioID).Return(testPortfolio(testPortfolioID), nil)
	h := newHandler(s)

	resp, err := h.GetPortfolio(ctxWithUser(testUserID), connect.NewRequest(&apiv1.GetPortfolioRequest{Id: testPortfolioID}))
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
	s.On("GetPortfolio", mock.Anything, testPortfolioID).Return(testPortfolio(testPortfolioID), nil)
	s.On("DeletePortfolio", mock.Anything, testPortfolioID).Return(nil)
	h := newHandler(s)

	_, err := h.DeletePortfolio(ctxWithUser(testUserID), connect.NewRequest(&apiv1.DeletePortfolioRequest{Id: testPortfolioID}))
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

	_, err := h.GetAccount(ctxWithUser(testUserID), connect.NewRequest(&apiv1.GetAccountRequest{Id: testAccountID}))
	require.Error(t, err)
	assert.Equal(t, connect.CodeNotFound, connect.CodeOf(err))
}

func TestDeleteAccount_OK(t *testing.T) {
	s := &mockStore{}
	s.On("GetAccount", mock.Anything, testAccountID).Return(testAccount(testAccountID), nil)
	s.On("DeleteAccount", mock.Anything, testAccountID).Return(nil)
	h := newHandler(s)

	_, err := h.DeleteAccount(ctxWithUser(testUserID), connect.NewRequest(&apiv1.DeleteAccountRequest{Id: testAccountID}))
	require.NoError(t, err)
}

// TestDeleteAccount_StillHoldsPositions: holdings reference the account, so the
// database refuses the delete. The message has to name what blocks it —
// "existing dependencies" leaves the user with a button that appears dead.
func TestDeleteAccount_StillHoldsPositions(t *testing.T) {
	s := &mockStore{}
	s.On("GetAccount", mock.Anything, testAccountID).Return(testAccount(testAccountID), nil)
	s.On("DeleteAccount", mock.Anything, testAccountID).
		Return(fmt.Errorf("%w: cannot delete account due to existing dependencies", store.ErrConstraint))
	s.On("ListHoldings", mock.Anything, mock.Anything).
		Return([]*entity.Holding{{ID: testHoldingID}, {ID: "h2"}}, "", nil)

	_, err := newHandler(s).DeleteAccount(ctxWithUser(testUserID),
		connect.NewRequest(&apiv1.DeleteAccountRequest{Id: testAccountID}))

	require.Error(t, err)
	assert.Equal(t, connect.CodeFailedPrecondition, connect.CodeOf(err))
	assert.Contains(t, err.Error(), "2 position(s)")
}

// TestDeleteAccount_BlockedByTransactions: with no holdings left, the blocker
// is transaction history, and the message must say so rather than claim
// positions that are not there.
func TestDeleteAccount_BlockedByTransactions(t *testing.T) {
	s := &mockStore{}
	s.On("GetAccount", mock.Anything, testAccountID).Return(testAccount(testAccountID), nil)
	s.On("DeleteAccount", mock.Anything, testAccountID).
		Return(fmt.Errorf("%w: cannot delete account due to existing dependencies", store.ErrConstraint))
	s.On("ListHoldings", mock.Anything, mock.Anything).Return([]*entity.Holding{}, "", nil)

	_, err := newHandler(s).DeleteAccount(ctxWithUser(testUserID),
		connect.NewRequest(&apiv1.DeleteAccountRequest{Id: testAccountID}))

	require.Error(t, err)
	assert.Equal(t, connect.CodeFailedPrecondition, connect.CodeOf(err))
	assert.Contains(t, err.Error(), "transactions")
}

// TestDeleteAccount_Cascade: with cascade the holdings go with the account, in
// one store call so the two cannot come apart.
func TestDeleteAccount_Cascade(t *testing.T) {
	s := &mockStore{}
	s.On("GetAccount", mock.Anything, testAccountID).Return(testAccount(testAccountID), nil)
	s.On("DeleteAccountWithHoldings", mock.Anything, testAccountID).Return(nil)

	_, err := newHandler(s).DeleteAccount(ctxWithUser(testUserID),
		connect.NewRequest(&apiv1.DeleteAccountRequest{Id: testAccountID, Cascade: true}))

	require.NoError(t, err)
	s.AssertExpectations(t)
	s.AssertNotCalled(t, "DeleteAccount", mock.Anything, mock.Anything)
}

// TestDeleteAccount_CascadeStillRefusesTransactions: cascade covers positions,
// never history. Once the holdings are gone a remaining constraint failure can
// only be transactions, and the message must say so instead of blaming
// positions that no longer exist.
func TestDeleteAccount_CascadeStillRefusesTransactions(t *testing.T) {
	s := &mockStore{}
	s.On("GetAccount", mock.Anything, testAccountID).Return(testAccount(testAccountID), nil)
	s.On("DeleteAccountWithHoldings", mock.Anything, testAccountID).
		Return(fmt.Errorf("%w: cannot delete account due to existing dependencies", store.ErrConstraint))

	_, err := newHandler(s).DeleteAccount(ctxWithUser(testUserID),
		connect.NewRequest(&apiv1.DeleteAccountRequest{Id: testAccountID, Cascade: true}))

	require.Error(t, err)
	assert.Equal(t, connect.CodeFailedPrecondition, connect.CodeOf(err))
	assert.Contains(t, err.Error(), "transaction history")
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

	resp, err := h.GetAccount(ctxWithUser(testUserID), connect.NewRequest(&apiv1.GetAccountRequest{Id: testAccountID}))
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
	s.On("GetAccount", mock.Anything, testAccountID).Return(testAccount(testAccountID), nil)
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

	denied := &mockStore{}
	denied.On("GetAccount", mock.Anything, testAccountID).Return(testAccount(testAccountID), nil)
	h := newHandler(denied)
	_, err := h.UpdateAccount(ctxWithUser(testUserID), req)
	require.Error(t, err)
	assert.Equal(t, connect.CodePermissionDenied, connect.CodeOf(err))

	_, err = h.UpdateAccount(context.Background(), req)
	require.Error(t, err)
	assert.Equal(t, connect.CodeUnauthenticated, connect.CodeOf(err))

	s := &mockStore{}
	s.On("GetAccount", mock.Anything, testAccountID).Return(testAccount(testAccountID), nil)
	s.On("UpdateAccount", mock.Anything, mock.Anything, []string{"system_scopes"}).Return(testAccount(testAccountID), nil)
	h = newHandler(s)
	_, err = h.UpdateAccount(ctxWithAdmin(testUserID), req)
	require.NoError(t, err)
	s.AssertExpectations(t)
}

// --- Tests: ownership (IDOR) ---

func TestOwnership_ForeignEntitiesReportNotFound(t *testing.T) {
	s := &mockStore{}
	s.On("GetPortfolio", mock.Anything, testPortfolioID).Return(testPortfolio(testPortfolioID), nil)
	s.On("GetAccount", mock.Anything, testAccountID).Return(testAccount(testAccountID), nil)
	s.On("GetHolding", mock.Anything, testHoldingID).Return(testHolding(testHoldingID), nil)
	s.On("GetTransaction", mock.Anything, testTxID).Return(&entity.Transaction{ID: testTxID, AccountID: testAccountID}, nil)
	h := newHandler(s)

	// All entities are owned by testUserID; testUserID2 must see NotFound,
	// and no mutation must reach the store.
	ctx := ctxWithUser(testUserID2)

	calls := map[string]func() error{
		"GetPortfolio": func() error {
			_, err := h.GetPortfolio(ctx, connect.NewRequest(&apiv1.GetPortfolioRequest{Id: testPortfolioID}))
			return err
		},
		"UpdatePortfolio": func() error {
			_, err := h.UpdatePortfolio(ctx, connect.NewRequest(&apiv1.UpdatePortfolioRequest{Portfolio: &apiv1.Portfolio{Id: testPortfolioID, Name: "pwn"}}))
			return err
		},
		"DeletePortfolio": func() error {
			_, err := h.DeletePortfolio(ctx, connect.NewRequest(&apiv1.DeletePortfolioRequest{Id: testPortfolioID}))
			return err
		},
		"CalculatePortfolioValue": func() error {
			_, err := h.clone().WithMarketDataClient(&mockMDClient{}).CalculatePortfolioValue(ctx, connect.NewRequest(&apiv1.CalculatePortfolioValueRequest{PortfolioId: testPortfolioID}))
			return err
		},
		"GetAccount": func() error {
			_, err := h.GetAccount(ctx, connect.NewRequest(&apiv1.GetAccountRequest{Id: testAccountID}))
			return err
		},
		"UpdateAccount": func() error {
			_, err := h.UpdateAccount(ctx, connect.NewRequest(&apiv1.UpdateAccountRequest{Account: &apiv1.Account{Id: testAccountID, Name: "pwn"}}))
			return err
		},
		"DeleteAccount": func() error {
			_, err := h.DeleteAccount(ctx, connect.NewRequest(&apiv1.DeleteAccountRequest{Id: testAccountID}))
			return err
		},
		"GetHolding": func() error {
			_, err := h.GetHolding(ctx, connect.NewRequest(&apiv1.GetHoldingRequest{Id: testHoldingID}))
			return err
		},
		"UpdateHolding": func() error {
			_, err := h.UpdateHolding(ctx, connect.NewRequest(&apiv1.UpdateHoldingRequest{Holding: &apiv1.Holding{Id: testHoldingID, Amount: "1", Decimals: 1}}))
			return err
		},
		"CreateHolding": func() error {
			_, err := h.CreateHolding(ctx, connect.NewRequest(&apiv1.CreateHoldingRequest{Holding: &apiv1.Holding{AccountId: testAccountID, AssetId: testAssetID, Amount: "1", Decimals: 1}}))
			return err
		},
		"GetTransaction": func() error {
			_, err := h.GetTransaction(ctx, connect.NewRequest(&apiv1.GetTransactionRequest{Id: testTxID}))
			return err
		},
		"CreateTransaction": func() error {
			_, err := h.CreateTransaction(ctx, connect.NewRequest(&apiv1.CreateTransactionRequest{Transaction: &apiv1.Transaction{AccountId: testAccountID}}))
			return err
		},
	}
	for name, call := range calls {
		err := call()
		require.Error(t, err, name)
		assert.Equal(t, connect.CodeNotFound, connect.CodeOf(err), name)
	}
	s.AssertNotCalled(t, "UpdatePortfolio")
	s.AssertNotCalled(t, "DeletePortfolio")
	s.AssertNotCalled(t, "UpdateAccount")
	s.AssertNotCalled(t, "DeleteAccount")
	s.AssertNotCalled(t, "UpdateHolding")
	s.AssertNotCalled(t, "CreateHolding")
	s.AssertNotCalled(t, "CreateTransaction")
}

func TestOwnership_AdminBypasses(t *testing.T) {
	s := &mockStore{}
	s.On("GetAccount", mock.Anything, testAccountID).Return(testAccount(testAccountID), nil)
	s.On("DeleteAccount", mock.Anything, testAccountID).Return(nil)
	h := newHandler(s)

	// Admin with a different user ID may manage foreign accounts.
	_, err := h.DeleteAccount(ctxWithAdmin(testUserID2), connect.NewRequest(&apiv1.DeleteAccountRequest{Id: testAccountID}))
	require.NoError(t, err)
	s.AssertExpectations(t)
}

func TestListAccounts_UserOverrideIsAdminOnly(t *testing.T) {
	s := &mockStore{}
	// Non-admin asking for another user's accounts still gets their own scope.
	s.On("ListAccounts", mock.Anything, ListAccountsOpts{UserID: testUserID2}).Return([]*entity.Account{}, "", nil)
	h := newHandler(s)

	other := testUserID
	_, err := h.ListAccounts(ctxWithUser(testUserID2), connect.NewRequest(&apiv1.ListAccountsRequest{UserId: &other}))
	require.NoError(t, err)
	s.AssertExpectations(t)

	// Admin override works.
	s2 := &mockStore{}
	s2.On("ListAccounts", mock.Anything, ListAccountsOpts{UserID: testUserID}).Return([]*entity.Account{}, "", nil)
	h = newHandler(s2)
	_, err = h.ListAccounts(ctxWithAdmin(testUserID2), connect.NewRequest(&apiv1.ListAccountsRequest{UserId: &other}))
	require.NoError(t, err)
	s2.AssertExpectations(t)
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
	s.On("GetAccount", mock.Anything, testAccountID).Return(testAccount(testAccountID), nil)
	s.On("CreateHolding", mock.Anything, mock.Anything).Return(testHolding(testHoldingID), nil)
	h := newHandler(s)

	resp, err := h.CreateHolding(ctxWithUser(testUserID), connect.NewRequest(&apiv1.CreateHoldingRequest{
		Holding: &apiv1.Holding{AssetId: testAssetID, AccountId: testAccountID, Amount: "100000", Decimals: 8},
	}))
	require.NoError(t, err)
	assert.Equal(t, testHoldingID, resp.Msg.Id)
}

// TestCreateHolding_StampsManualSource verifies provenance is server-stamped:
// client-sent source/import_id are ignored and the RPC always records "manual".
func TestCreateHolding_StampsManualSource(t *testing.T) {
	spoofedImportID := "01926d35-6a1e-7007-8007-000000000001"

	s := &mockStore{}
	s.On("GetAccount", mock.Anything, testAccountID).Return(testAccount(testAccountID), nil)
	s.On("CreateHolding", mock.Anything, mock.MatchedBy(func(h *entity.Holding) bool {
		return h.Source == entity.SourceManual && h.ImportID == ""
	})).Return(testHolding(testHoldingID), nil)
	h := newHandler(s)

	_, err := h.CreateHolding(ctxWithUser(testUserID), connect.NewRequest(&apiv1.CreateHoldingRequest{
		Holding: &apiv1.Holding{
			AssetId:   testAssetID,
			AccountId: testAccountID,
			Amount:    "100000",
			Decimals:  8,
			Source:    apiv1.ProvenanceSource_PROVENANCE_SOURCE_SYNC,
			ImportId:  &spoofedImportID,
		},
	}))
	require.NoError(t, err)
	s.AssertExpectations(t)
}

func TestCreateHolding_NegativeAmount(t *testing.T) {
	h := newHandler(&mockStore{})

	_, err := h.CreateHolding(ctxWithUser(testUserID), connect.NewRequest(&apiv1.CreateHoldingRequest{
		Holding: &apiv1.Holding{AssetId: testAssetID, AccountId: testAccountID, Amount: "-1", Decimals: 8},
	}))
	require.Error(t, err)
	assert.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))
}

// TestUpdateHolding_SourceNotUpdatable verifies an explicit update_mask entry for
// source is passed through to the store, which has no case for it — the field list
// reaching the store must still carry only what the mask says, and the store switch
// ignores unknown fields, so source stays untouched.
func TestUpdateHolding_SourceMaskIgnored(t *testing.T) {
	s := &mockStore{}
	s.On("GetHolding", mock.Anything, testHoldingID).Return(testHolding(testHoldingID), nil)
	s.On("GetAccount", mock.Anything, testAccountID).Return(testAccount(testAccountID), nil)
	stored := testHolding(testHoldingID)
	stored.Source = entity.SourceSync
	s.On("UpdateHolding", mock.Anything, mock.Anything, []string{"source"}).Return(stored, nil)
	h := newHandler(s)

	resp, err := h.UpdateHolding(ctxWithUser(testUserID), connect.NewRequest(&apiv1.UpdateHoldingRequest{
		Holding:    &apiv1.Holding{Id: testHoldingID, Amount: "1"},
		UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"source"}},
	}))
	require.NoError(t, err)
	// The stored source survives; the response reflects it, not anything client-sent.
	assert.Equal(t, apiv1.ProvenanceSource_PROVENANCE_SOURCE_SYNC, resp.Msg.Source)
}

func TestDeleteHolding_MissingID(t *testing.T) {
	h := newHandler(&mockStore{})
	_, err := h.DeleteHolding(context.Background(), connect.NewRequest(&apiv1.DeleteHoldingRequest{}))
	require.Error(t, err)
	assert.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))
}

func TestDeleteHolding_OK(t *testing.T) {
	s := &mockStore{}
	s.On("GetHolding", mock.Anything, testHoldingID).Return(testHolding(testHoldingID), nil)
	s.On("GetAccount", mock.Anything, testAccountID).Return(testAccount(testAccountID), nil)
	s.On("DeleteHolding", mock.Anything, testHoldingID).Return(nil)
	h := newHandler(s)

	_, err := h.DeleteHolding(ctxWithUser(testUserID), connect.NewRequest(&apiv1.DeleteHoldingRequest{Id: testHoldingID}))
	require.NoError(t, err)
	s.AssertExpectations(t)
}

// TestDeleteHolding_ForeignOwner verifies a holding owned by another user reports
// NotFound (not PermissionDenied) and the delete never reaches the store.
func TestDeleteHolding_ForeignOwner(t *testing.T) {
	s := &mockStore{}
	s.On("GetHolding", mock.Anything, testHoldingID).Return(testHolding(testHoldingID), nil)
	s.On("GetAccount", mock.Anything, testAccountID).Return(testAccount(testAccountID), nil)
	h := newHandler(s)

	_, err := h.DeleteHolding(ctxWithUser(testUserID2), connect.NewRequest(&apiv1.DeleteHoldingRequest{Id: testHoldingID}))
	require.Error(t, err)
	assert.Equal(t, connect.CodeNotFound, connect.CodeOf(err))
	s.AssertNotCalled(t, "DeleteHolding")
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
	s.On("GetAccount", mock.Anything, testAccountID).Return(testAccount(testAccountID), nil)
	s.On("CreateTransaction", mock.Anything, mock.Anything).Return(&entity.Transaction{
		ID:        testTxID,
		Type:      entity.TransactionTypeTrade,
		Status:    entity.TransactionStatusCompleted,
		AccountID: testAccountID,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}, nil)
	h := newHandler(s)

	resp, err := h.CreateTransaction(ctxWithUser(testUserID), connect.NewRequest(&apiv1.CreateTransactionRequest{
		Transaction: &apiv1.Transaction{AccountId: testAccountID},
	}))
	require.NoError(t, err)
	assert.Equal(t, testTxID, resp.Msg.Id)
}

// TestCreateTransaction_StampsManualSource verifies provenance is server-stamped
// on transactions the same way as on holdings.
func TestCreateTransaction_StampsManualSource(t *testing.T) {
	s := &mockStore{}
	s.On("GetAccount", mock.Anything, testAccountID).Return(testAccount(testAccountID), nil)
	s.On("CreateTransaction", mock.Anything, mock.MatchedBy(func(tx *entity.Transaction) bool {
		return tx.Source == entity.SourceManual && tx.ImportID == ""
	})).Return(&entity.Transaction{ID: testTxID, AccountID: testAccountID}, nil)
	h := newHandler(s)

	_, err := h.CreateTransaction(ctxWithUser(testUserID), connect.NewRequest(&apiv1.CreateTransactionRequest{
		Transaction: &apiv1.Transaction{
			AccountId: testAccountID,
			Source:    apiv1.ProvenanceSource_PROVENANCE_SOURCE_SYNC,
		},
	}))
	require.NoError(t, err)
	s.AssertExpectations(t)
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
	s.On("GetPortfolio", mock.Anything, testPortfolioID).Return(testPortfolio(testPortfolioID), nil)
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
	resp, err := h.CalculatePortfolioValue(ctxWithUser(testUserID), connect.NewRequest(&apiv1.CalculatePortfolioValueRequest{
		PortfolioId: testPortfolioID,
	}))
	require.NoError(t, err)
	// 1.0 token × 2.0 USDT × 0.99 USD/USDT = 1.98 USD → 198 (2 decimals).
	assert.Equal(t, "198", resp.Msg.TotalValueAmount)
	assert.Equal(t, uint32(2), resp.Msg.Decimals)
}

// TestCalculatePortfolioValue_DisclosesExcluded: a quarantined holding stays out
// of the total but is disclosed as excluded count and value, so the number never
// silently diverges from the wallet.
func TestCalculatePortfolioValue_DisclosesExcluded(t *testing.T) {
	const (
		legitAsset = "00000000-0000-0000-0000-0000000000a1"
		scamAsset  = "00000000-0000-0000-0000-0000000000a2"
	)
	s := &mockStore{}
	s.On("GetPortfolio", mock.Anything, testPortfolioID).Return(testPortfolio(testPortfolioID), nil)
	// Both holdings come back; the store no longer hides excluded here.
	s.On("ListHoldings", mock.Anything, mock.MatchedBy(func(o ListHoldingsOpts) bool {
		return !o.HideExcluded
	})).Return([]*entity.Holding{
		{ID: "h1", AssetID: legitAsset, Amount: decimal.NewFromInt(100000000), Decimals: 8},                // 1.0
		{ID: "h2", AssetID: scamAsset, Amount: decimal.NewFromInt(500000000), Decimals: 8, Excluded: true}, // 5.0, quarantined
	}, "", nil)

	md := &mockMDClient{}
	md.On("GetLatestPrice", mock.Anything, mock.MatchedBy(func(r *connect.Request[apiv1.GetLatestPriceRequest]) bool {
		return r.Msg.AssetId == legitAsset && r.Msg.BaseAssetId == "USD"
	})).Return(connect.NewResponse(&apiv1.Price{Last: "300000000", Decimals: 8, BaseAssetId: "USD"}), nil) // 3.0
	md.On("GetLatestPrice", mock.Anything, mock.MatchedBy(func(r *connect.Request[apiv1.GetLatestPriceRequest]) bool {
		return r.Msg.AssetId == scamAsset && r.Msg.BaseAssetId == "USD"
	})).Return(connect.NewResponse(&apiv1.Price{Last: "100000000", Decimals: 8, BaseAssetId: "USD"}), nil) // 1.0

	h := newHandler(s).WithMarketDataClient(md)
	resp, err := h.CalculatePortfolioValue(ctxWithUser(testUserID), connect.NewRequest(&apiv1.CalculatePortfolioValueRequest{
		PortfolioId: testPortfolioID,
	}))
	require.NoError(t, err)
	assert.Equal(t, "300", resp.Msg.TotalValueAmount, "only the legit 1.0 × 3.0 counts")
	assert.Equal(t, uint32(1), resp.Msg.ExcludedCount)
	assert.Equal(t, "500", resp.Msg.ExcludedValueAmount, "quarantined 5.0 × 1.0 disclosed")
}

// TestCalculatePortfolioValue_ReportsUnpricedCoverage: a holding with no price
// path stays out of the total, as before, but is now disclosed by count and
// identity. Absence of a quote is not a valuation of zero, and a total that
// silently drops it looks complete when it is not.
func TestCalculatePortfolioValue_ReportsUnpricedCoverage(t *testing.T) {
	const (
		pricedAsset   = "00000000-0000-0000-0000-0000000000a1"
		unpricedAsset = "00000000-0000-0000-0000-0000000000a2"
		scamAsset     = "00000000-0000-0000-0000-0000000000a3"
	)
	s := &mockStore{}
	s.On("GetPortfolio", mock.Anything, testPortfolioID).Return(testPortfolio(testPortfolioID), nil)
	s.On("ListHoldings", mock.Anything, mock.Anything).Return([]*entity.Holding{
		{ID: "h1", AssetID: pricedAsset, Amount: decimal.NewFromInt(100000000), Decimals: 8},               // 1.0, priced
		{ID: "h2", AssetID: unpricedAsset, Amount: decimal.NewFromInt(700000000), Decimals: 8},             // 7.0, no quote
		{ID: "h3", AssetID: scamAsset, Amount: decimal.NewFromInt(500000000), Decimals: 8, Excluded: true}, // quarantined
	}, "", nil)

	md := &mockMDClient{}
	md.On("GetLatestPrice", mock.Anything, mock.MatchedBy(func(r *connect.Request[apiv1.GetLatestPriceRequest]) bool {
		return r.Msg.AssetId == pricedAsset && r.Msg.BaseAssetId == "USD"
	})).Return(connect.NewResponse(&apiv1.Price{Last: "300000000", Decimals: 8, BaseAssetId: "USD"}), nil) // 3.0
	// The FinEx-style case: no direct quote and no traded pair to cross through.
	md.On("GetLatestPrice", mock.Anything, mock.MatchedBy(func(r *connect.Request[apiv1.GetLatestPriceRequest]) bool {
		return r.Msg.AssetId == unpricedAsset
	})).Return(nil, connect.NewError(connect.CodeNotFound, errors.New("not found")))
	md.On("GetLatestPrice", mock.Anything, mock.MatchedBy(func(r *connect.Request[apiv1.GetLatestPriceRequest]) bool {
		return r.Msg.AssetId == scamAsset && r.Msg.BaseAssetId == "USD"
	})).Return(connect.NewResponse(&apiv1.Price{Last: "100000000", Decimals: 8, BaseAssetId: "USD"}), nil) // 1.0
	symbol := "FXUS"
	md.On("GetAsset", mock.Anything, mock.MatchedBy(func(r *connect.Request[apiv1.GetAssetRequest]) bool {
		return r.Msg.Id == unpricedAsset
	})).Return(connect.NewResponse(&apiv1.Asset{Id: unpricedAsset, Symbol: &symbol}), nil)

	h := newHandler(s).WithMarketDataClient(md)
	resp, err := h.CalculatePortfolioValue(ctxWithUser(testUserID), connect.NewRequest(&apiv1.CalculatePortfolioValueRequest{
		PortfolioId: testPortfolioID,
	}))
	require.NoError(t, err)

	assert.Equal(t, "300", resp.Msg.TotalValueAmount, "only the priced 1.0 × 3.0 counts")
	assert.Equal(t, uint32(1), resp.Msg.ExcludedCount, "quarantine disclosure is unchanged")
	assert.Equal(t, "500", resp.Msg.ExcludedValueAmount)

	cov := resp.Msg.Coverage
	require.NotNil(t, cov)
	assert.Equal(t, uint32(1), cov.PricedCount)
	assert.Equal(t, uint32(1), cov.UnpricedCount, "the excluded holding is not counted here: quarantine is a separate axis")
	assert.False(t, cov.UnpricedTruncated)
	require.Len(t, cov.Unpriced, 1)
	assert.Equal(t, "h2", cov.Unpriced[0].HoldingId)
	assert.Equal(t, unpricedAsset, cov.Unpriced[0].AssetId)
	assert.Equal(t, "FXUS", cov.Unpriced[0].Symbol)
}

// TestCalculatePortfolioValue_UnpricedListIsCapped: the count stays exact while
// the per-holding list is a bounded sample, and a failed asset lookup costs a
// label rather than the whole valuation.
func TestCalculatePortfolioValue_UnpricedListIsCapped(t *testing.T) {
	holdings := make([]*entity.Holding, 0, maxUnpricedDisclosed+3)
	for i := range maxUnpricedDisclosed + 3 {
		holdings = append(holdings, &entity.Holding{
			ID:       fmt.Sprintf("h%d", i),
			AssetID:  fmt.Sprintf("00000000-0000-0000-0000-%012d", i),
			Amount:   decimal.NewFromInt(100000000),
			Decimals: 8,
		})
	}
	s := &mockStore{}
	s.On("GetPortfolio", mock.Anything, testPortfolioID).Return(testPortfolio(testPortfolioID), nil)
	s.On("ListHoldings", mock.Anything, mock.Anything).Return(holdings, "", nil)

	md := &mockMDClient{}
	md.On("GetLatestPrice", mock.Anything, mock.Anything).
		Return(nil, connect.NewError(connect.CodeNotFound, errors.New("not found")))
	md.On("GetAsset", mock.Anything, mock.Anything).
		Return(nil, connect.NewError(connect.CodeNotFound, errors.New("asset gone")))

	h := newHandler(s).WithMarketDataClient(md)
	resp, err := h.CalculatePortfolioValue(ctxWithUser(testUserID), connect.NewRequest(&apiv1.CalculatePortfolioValueRequest{
		PortfolioId: testPortfolioID,
	}))
	require.NoError(t, err)

	cov := resp.Msg.Coverage
	require.NotNil(t, cov)
	assert.Equal(t, uint32(0), cov.PricedCount)
	assert.Equal(t, uint32(maxUnpricedDisclosed+3), cov.UnpricedCount, "the count is exact")
	assert.Len(t, cov.Unpriced, maxUnpricedDisclosed, "the list is capped")
	assert.True(t, cov.UnpricedTruncated)
	assert.Empty(t, cov.Unpriced[0].Symbol, "a failed lookup degrades to no symbol")
	assert.NotEmpty(t, cov.Unpriced[0].AssetId, "the position is still identified")
	assert.Equal(t, "0", resp.Msg.TotalValueAmount, "nothing could be valued")
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
	// autoAsset makes FindOrCreateAsset resolve deterministically without an
	// explicit expectation: one asset per normalized symbol, Created only the
	// first time a symbol is seen — the find-vs-create semantics the sync path
	// relies on. Import tests leave it false and mock FindOrCreateAsset directly.
	autoAsset bool
	seenAsset map[string]bool
}

func (m *mockMDClient) CreateAsset(ctx context.Context, req *connect.Request[apiv1.CreateAssetRequest]) (*connect.Response[apiv1.Asset], error) {
	args := m.Called(ctx, req)
	if v := args.Get(0); v != nil {
		return v.(*connect.Response[apiv1.Asset]), args.Error(1)
	}
	return nil, args.Error(1)
}

func (m *mockMDClient) GetAsset(ctx context.Context, req *connect.Request[apiv1.GetAssetRequest]) (*connect.Response[apiv1.Asset], error) {
	args := m.Called(ctx, req)
	if v := args.Get(0); v != nil {
		return v.(*connect.Response[apiv1.Asset]), args.Error(1)
	}
	return nil, args.Error(1)
}

func (m *mockMDClient) FindOrCreateAsset(ctx context.Context, req *connect.Request[apiv1.FindOrCreateAssetRequest]) (*connect.Response[apiv1.FindOrCreateAssetResponse], error) {
	if m.autoAsset {
		sym := entity.NormalizeSymbol(req.Msg.Symbol)
		if m.seenAsset == nil {
			m.seenAsset = map[string]bool{}
		}
		created := !m.seenAsset[sym]
		m.seenAsset[sym] = true
		return connect.NewResponse(&apiv1.FindOrCreateAssetResponse{
			Asset:   &apiv1.Asset{Id: "asset-" + sym, Symbol: &sym},
			Created: created,
		}), nil
	}
	args := m.Called(ctx, req)
	if v := args.Get(0); v != nil {
		return v.(*connect.Response[apiv1.FindOrCreateAssetResponse]), args.Error(1)
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

// TestSyncAccount_ScamVerdictExcludesHolding: a synced token whose asset comes
// back with a scam identity verdict is created as an excluded holding — it keeps
// syncing (no frozen position) but stays out of the sums — while a legit token
// alongside it is included.
func TestSyncAccount_ScamVerdictExcludesHolding(t *testing.T) {
	acct := testAccount(testAccountID)
	acct.Type = entity.AccountTypeWallet
	acct.Data = map[string]string{"address": "0xabc", "chain": "eth"}

	s := &mockStore{}
	s.On("GetAccount", mock.Anything, testAccountID).Return(acct, nil)
	s.On("ListHoldings", mock.Anything, mock.Anything).Return([]*entity.Holding{}, "", nil)
	s.On("CreateHolding", mock.Anything, mock.MatchedBy(func(h *entity.Holding) bool {
		return h.AssetID == "asset-scam" && h.Excluded
	})).Return(&entity.Holding{ID: "h-scam"}, nil)
	s.On("CreateHolding", mock.Anything, mock.MatchedBy(func(h *entity.Holding) bool {
		return h.AssetID == "asset-legit" && !h.Excluded
	})).Return(&entity.Holding{ID: "h-legit"}, nil)

	ws := &mockWalletSyncer{}
	ws.On("SyncWallet", mock.Anything, "0xabc", []string{"eth"}).Return([]entity.WalletBalance{
		{Symbol: "USDT", Name: "Fake USDT", Amount: "100", Decimals: 6, ContractAddress: "0xscam", Chain: "eth"},
		{Symbol: "DAI", Name: "Dai", Amount: "100", Decimals: 18, ContractAddress: "0xdai", Chain: "eth"},
	}, nil)

	md := &mockMDClient{}
	scam, legit := "scam", "legit"
	md.On("FindOrCreateAsset", mock.Anything, mock.MatchedBy(func(r *connect.Request[apiv1.FindOrCreateAssetRequest]) bool {
		return r.Msg.Symbol == "USDT"
	})).Return(connect.NewResponse(&apiv1.FindOrCreateAssetResponse{
		Asset: &apiv1.Asset{Id: "asset-scam", IdentityVerdict: &scam}, Created: true,
	}), nil)
	md.On("FindOrCreateAsset", mock.Anything, mock.MatchedBy(func(r *connect.Request[apiv1.FindOrCreateAssetRequest]) bool {
		return r.Msg.Symbol == "DAI"
	})).Return(connect.NewResponse(&apiv1.FindOrCreateAssetResponse{
		Asset: &apiv1.Asset{Id: "asset-legit", IdentityVerdict: &legit}, Created: true,
	}), nil)
	md.On("FetchExternalPrices", mock.Anything, mock.Anything).
		Return(connect.NewResponse(&apiv1.FetchExternalPricesResponse{}), nil)

	h := newHandler(s).WithMarketDataClient(md).WithWalletSyncer(ws)

	resp, err := h.SyncAccount(ctxWithUser(testUserID), connect.NewRequest(&apiv1.SyncAccountRequest{
		AccountId: testAccountID,
	}))
	require.NoError(t, err)
	assert.Empty(t, resp.Msg.Errors)
	assert.Equal(t, int32(2), resp.Msg.HoldingsUpserted)
	s.AssertExpectations(t)
}

// TestSyncAccount_PricesOnlySyncedAssets: the post-sync price fetch names the
// assets this sync wrote. An unfiltered request would re-price the entire
// catalogue on every sync, which is what exhausted the provider's monthly quota.
func TestSyncAccount_PricesOnlySyncedAssets(t *testing.T) {
	acct := testAccount(testAccountID)
	acct.Type = entity.AccountTypeWallet
	acct.Data = map[string]string{"address": "0xabc", "chain": "eth"}

	s := &mockStore{}
	s.On("GetAccount", mock.Anything, testAccountID).Return(acct, nil)
	s.On("ListHoldings", mock.Anything, mock.Anything).Return([]*entity.Holding{}, "", nil)
	s.On("CreateHolding", mock.Anything, mock.Anything).Return(&entity.Holding{ID: "h-1"}, nil)

	ws := &mockWalletSyncer{}
	ws.On("SyncWallet", mock.Anything, "0xabc", []string{"eth"}).Return([]entity.WalletBalance{
		{Symbol: "DAI", Amount: "100", Decimals: 18, ContractAddress: "0xdai", Chain: "eth"},
		{Symbol: "WETH", Amount: "200", Decimals: 18, ContractAddress: "0xweth", Chain: "eth"},
	}, nil)

	md := &mockMDClient{autoAsset: true}
	var priced []string
	md.On("FetchExternalPrices", mock.Anything, mock.Anything).
		Run(func(args mock.Arguments) {
			req := args.Get(1).(*connect.Request[apiv1.FetchExternalPricesRequest])
			priced = req.Msg.AssetIds
		}).
		Return(connect.NewResponse(&apiv1.FetchExternalPricesResponse{}), nil)

	h := newHandler(s).WithMarketDataClient(md).WithWalletSyncer(ws)

	_, err := h.SyncAccount(ctxWithUser(testUserID), connect.NewRequest(&apiv1.SyncAccountRequest{
		AccountId: testAccountID,
	}))
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{"asset-DAI", "asset-WETH"}, priced)
	md.AssertNumberOfCalls(t, "FetchExternalPrices", 1)
}

// TestSyncAccount_NoBalancesSkipsPriceFetch: a sync that wrote nothing spends no
// provider quota.
func TestSyncAccount_NoBalancesSkipsPriceFetch(t *testing.T) {
	acct := testAccount(testAccountID)
	acct.Type = entity.AccountTypeWallet
	acct.Data = map[string]string{"address": "0xabc", "chain": "eth"}

	s := &mockStore{}
	s.On("GetAccount", mock.Anything, testAccountID).Return(acct, nil)
	s.On("ListHoldings", mock.Anything, mock.Anything).Return([]*entity.Holding{}, "", nil)

	ws := &mockWalletSyncer{}
	ws.On("SyncWallet", mock.Anything, "0xabc", []string{"eth"}).Return([]entity.WalletBalance{}, nil)

	md := &mockMDClient{autoAsset: true}
	h := newHandler(s).WithMarketDataClient(md).WithWalletSyncer(ws)

	resp, err := h.SyncAccount(ctxWithUser(testUserID), connect.NewRequest(&apiv1.SyncAccountRequest{
		AccountId: testAccountID,
	}))
	require.NoError(t, err)
	assert.Zero(t, resp.Msg.HoldingsUpserted)
	md.AssertNotCalled(t, "FetchExternalPrices", mock.Anything, mock.Anything)
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

	md := &mockMDClient{autoAsset: true}
	md.On("FetchExternalPrices", mock.Anything, mock.Anything).
		Return(connect.NewResponse(&apiv1.FetchExternalPricesResponse{}), nil)

	h := newHandler(s).WithMarketDataClient(md).WithWalletSyncer(ws)

	resp, err := h.SyncAccount(ctxWithUser(testUserID), connect.NewRequest(&apiv1.SyncAccountRequest{
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

	md := &mockMDClient{autoAsset: true}
	md.On("FetchExternalPrices", mock.Anything, mock.Anything).
		Return(connect.NewResponse(&apiv1.FetchExternalPricesResponse{}), nil)

	h := newHandler(s).WithMarketDataClient(md).WithWalletSyncer(ws)

	resp, err := h.SyncAccount(ctxWithUser(testUserID), connect.NewRequest(&apiv1.SyncAccountRequest{
		AccountId: testAccountID,
	}))
	require.NoError(t, err)
	assert.Empty(t, resp.Msg.Errors)
	// One asset, one holding — the two chain balances collapsed into a single USDC holding.
	assert.Equal(t, int32(1), resp.Msg.AssetsUpserted)
	assert.Equal(t, int32(1), resp.Msg.HoldingsUpserted)
	s.AssertExpectations(t)
}

// TestSyncAccount_MultipleAddresses covers the UTXO shape: one wallet spread
// over several addresses must report one holding per asset, not one per address.
func TestSyncAccount_MultipleAddresses(t *testing.T) {
	acct := testAccount(testAccountID)
	acct.Type = entity.AccountTypeWallet
	acct.Data = map[string]string{"addresses": "bc1aaa, bc1bbb", "chain": "bitcoin"}

	s := &mockStore{}
	s.On("GetAccount", mock.Anything, testAccountID).Return(acct, nil)
	s.On("ListHoldings", mock.Anything, mock.Anything).Return([]*entity.Holding{}, "", nil)
	// 0.5 BTC + 1.5 BTC across the two addresses = 2.0 BTC in one holding.
	s.On("CreateHolding", mock.Anything, mock.MatchedBy(func(h *entity.Holding) bool {
		return h.Amount.String() == "200000000" && h.Decimals == 8
	})).Return(&entity.Holding{ID: testHoldingID}, nil)

	ws := &mockWalletSyncer{}
	ws.On("SyncWallet", mock.Anything, "bc1aaa", []string{"bitcoin"}).Return([]entity.WalletBalance{
		{Symbol: "BTC", Name: "Bitcoin", Amount: "50000000", Decimals: 8},
	}, nil)
	ws.On("SyncWallet", mock.Anything, "bc1bbb", []string{"bitcoin"}).Return([]entity.WalletBalance{
		{Symbol: "BTC", Name: "Bitcoin", Amount: "150000000", Decimals: 8},
	}, nil)

	md := &mockMDClient{autoAsset: true}
	md.On("FetchExternalPrices", mock.Anything, mock.Anything).
		Return(connect.NewResponse(&apiv1.FetchExternalPricesResponse{}), nil)

	h := newHandler(s).WithMarketDataClient(md).WithWalletSyncer(ws)

	resp, err := h.SyncAccount(ctxWithUser(testUserID), connect.NewRequest(&apiv1.SyncAccountRequest{
		AccountId: testAccountID,
	}))
	require.NoError(t, err)
	assert.Empty(t, resp.Msg.Errors)
	assert.Equal(t, int32(1), resp.Msg.HoldingsUpserted)
	ws.AssertExpectations(t)
	s.AssertExpectations(t)
}

// TestSyncAccount_MultipleAddressesPartialFailure: one unreachable address must
// not discard the rest of the wallet, and the error has to say which address
// failed — otherwise a silently shrunk balance looks like a real outflow.
func TestSyncAccount_MultipleAddressesPartialFailure(t *testing.T) {
	acct := testAccount(testAccountID)
	acct.Type = entity.AccountTypeWallet
	acct.Data = map[string]string{"addresses": "bc1good bc1bad", "chain": "bitcoin"}

	s := &mockStore{}
	s.On("GetAccount", mock.Anything, testAccountID).Return(acct, nil)
	s.On("ListHoldings", mock.Anything, mock.Anything).Return([]*entity.Holding{}, "", nil)
	s.On("CreateHolding", mock.Anything, mock.Anything).
		Return(&entity.Holding{ID: testHoldingID}, nil)

	ws := &mockWalletSyncer{}
	ws.On("SyncWallet", mock.Anything, "bc1good", []string{"bitcoin"}).Return([]entity.WalletBalance{
		{Symbol: "BTC", Name: "Bitcoin", Amount: "50000000", Decimals: 8},
	}, nil)
	ws.On("SyncWallet", mock.Anything, "bc1bad", []string{"bitcoin"}).
		Return([]entity.WalletBalance{}, errors.New("esplora status 503"))

	md := &mockMDClient{autoAsset: true}
	md.On("FetchExternalPrices", mock.Anything, mock.Anything).
		Return(connect.NewResponse(&apiv1.FetchExternalPricesResponse{}), nil)

	h := newHandler(s).WithMarketDataClient(md).WithWalletSyncer(ws)

	resp, err := h.SyncAccount(ctxWithUser(testUserID), connect.NewRequest(&apiv1.SyncAccountRequest{
		AccountId: testAccountID,
	}))
	require.NoError(t, err)
	require.Len(t, resp.Msg.Errors, 1)
	assert.Contains(t, resp.Msg.Errors[0], "bc1bad")
	assert.Equal(t, int32(1), resp.Msg.HoldingsUpserted, "the reachable address still syncs")
}

// TestSyncAccount_AddressRequired: both forms absent is a config error, not an
// empty wallet. An account whose addresses field parses to nothing must not be
// treated as a valid single-address account either.
func TestSyncAccount_AddressRequired(t *testing.T) {
	for _, data := range []map[string]string{
		{"chain": "bitcoin"},
		{"addresses": " , ", "chain": "bitcoin"},
	} {
		acct := testAccount(testAccountID)
		acct.Type = entity.AccountTypeWallet
		acct.Data = data

		s := &mockStore{}
		s.On("GetAccount", mock.Anything, testAccountID).Return(acct, nil)

		h := newHandler(s).WithMarketDataClient(&mockMDClient{}).WithWalletSyncer(&mockWalletSyncer{})

		_, err := h.SyncAccount(ctxWithUser(testUserID), connect.NewRequest(&apiv1.SyncAccountRequest{
			AccountId: testAccountID,
		}))
		require.Error(t, err)
		assert.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))
	}
}

// --- Exchange sync ---

type mockExchangeSyncer struct {
	mock.Mock
}

func (m *mockExchangeSyncer) SyncExchange(ctx context.Context) ([]entity.ExchangeBalance, error) {
	args := m.Called(ctx)
	if v := args.Get(0); v != nil {
		return v.([]entity.ExchangeBalance), args.Error(1)
	}
	return nil, args.Error(1)
}

type mockExchangeSource struct {
	syncer entity.ExchangeSyncer
	err    error
}

func (m *mockExchangeSource) ExchangeSyncerForAccount(_ *entity.Account) (entity.ExchangeSyncer, error) {
	return m.syncer, m.err
}

func TestSyncAccount_Exchange(t *testing.T) {
	acct := testAccount(testAccountID)
	acct.Type = entity.AccountTypeExchange
	acct.Data = map[string]string{"provider": "binance", "api_key": "k", "api_secret": "s"}

	s := &mockStore{}
	s.On("GetAccount", mock.Anything, testAccountID).Return(acct, nil)
	s.On("ListHoldings", mock.Anything, mock.Anything).Return([]*entity.Holding{}, "", nil)
	s.On("CreateHolding", mock.Anything, mock.MatchedBy(func(h *entity.Holding) bool {
		return h.Amount.String() == "60000000" && h.Decimals == 8 && h.Source == entity.SourceSync
	})).Return(&entity.Holding{ID: testHoldingID}, nil)

	syncer := &mockExchangeSyncer{}
	syncer.On("SyncExchange", mock.Anything).Return([]entity.ExchangeBalance{
		{Symbol: "BTC", Amount: "60000000", Decimals: 8},
	}, nil)

	md := &mockMDClient{autoAsset: true}
	md.On("FetchExternalPrices", mock.Anything, mock.Anything).Return(connect.NewResponse(&apiv1.FetchExternalPricesResponse{}), nil)

	h := newHandler(s).WithMarketDataClient(md).WithExchangeSyncerSource(&mockExchangeSource{syncer: syncer})

	resp, err := h.SyncAccount(ctxWithUser(testUserID), connect.NewRequest(&apiv1.SyncAccountRequest{AccountId: testAccountID}))
	require.NoError(t, err)
	assert.Empty(t, resp.Msg.Errors)
	assert.Equal(t, int32(1), resp.Msg.AssetsUpserted)
	assert.Equal(t, int32(1), resp.Msg.HoldingsUpserted)
	s.AssertExpectations(t)
	syncer.AssertExpectations(t)
}

func TestSyncAccount_ExchangeNoAdapter(t *testing.T) {
	acct := testAccount(testAccountID)
	acct.Type = entity.AccountTypeExchange
	acct.Data = map[string]string{"provider": "unknown"}

	s := &mockStore{}
	s.On("GetAccount", mock.Anything, testAccountID).Return(acct, nil)

	h := newHandler(s).WithMarketDataClient(&mockMDClient{}).WithExchangeSyncerSource(&mockExchangeSource{syncer: nil})

	_, err := h.SyncAccount(ctxWithUser(testUserID), connect.NewRequest(&apiv1.SyncAccountRequest{AccountId: testAccountID}))
	require.Error(t, err)
	assert.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))
}

// mockWalletSource resolves a wallet syncer per requested chains, standing in
// for the credentials resolver's chain routing.
type mockWalletSource struct {
	syncer entity.WalletSyncer
	err    error

	gotChains []string // chains the handler asked for
}

func (m *mockWalletSource) WalletSyncerFor(_ context.Context, _, _ string, chains []string) (entity.WalletSyncer, error) {
	m.gotChains = chains
	return m.syncer, m.err
}

// TestSyncAccount_NonEVMChainWithoutAdapter is the guard that makes non-EVM
// accounts safe to create before their adapter exists: an unroutable chain must
// fail loudly instead of falling through to the EVM syncer, which would report
// an empty wallet and silently zero out the position.
func TestSyncAccount_NonEVMChainWithoutAdapter(t *testing.T) {
	acct := testAccount(testAccountID)
	acct.Type = entity.AccountTypeWallet
	acct.Data = map[string]string{"address": "1FRMM8...", "chain": "polkadot"}

	s := &mockStore{}
	s.On("GetAccount", mock.Anything, testAccountID).Return(acct, nil)

	evmSyncer := &mockWalletSyncer{} // wired statically, must never be called
	source := &mockWalletSource{syncer: nil}
	h := newHandler(s).WithMarketDataClient(&mockMDClient{}).
		WithWalletSyncer(evmSyncer).
		WithWalletSyncerSource(source)

	_, err := h.SyncAccount(ctxWithUser(testUserID), connect.NewRequest(&apiv1.SyncAccountRequest{AccountId: testAccountID}))
	require.Error(t, err)
	assert.Equal(t, connect.CodeUnimplemented, connect.CodeOf(err))
	assert.Contains(t, err.Error(), "polkadot", "error must name the chain that could not be routed")
	assert.Equal(t, []string{"polkadot"}, source.gotChains)
	evmSyncer.AssertNotCalled(t, "SyncWallet", mock.Anything, mock.Anything, mock.Anything)
}

// TestSyncAccount_ChainsPassedToSource pins the routing input: the account's
// chain config reaches the resolver verbatim, and "auto" means auto-discovery
// (nil chains), not a chain literally named "auto".
func TestSyncAccount_ChainsPassedToSource(t *testing.T) {
	tests := []struct {
		name       string
		chainData  string
		wantChains []string
	}{
		{"explicit chain list", "eth,base", []string{"eth", "base"}},
		{"auto means discovery", "auto", nil},
		{"empty means discovery", "", nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			acct := testAccount(testAccountID)
			acct.Type = entity.AccountTypeWallet
			acct.Data = map[string]string{"address": "0xabc", "chain": tt.chainData}

			s := &mockStore{}
			s.On("GetAccount", mock.Anything, testAccountID).Return(acct, nil)
			s.On("ListHoldings", mock.Anything, mock.Anything).Return([]*entity.Holding{}, "", nil)

			syncer := &mockWalletSyncer{}
			syncer.On("SyncWallet", mock.Anything, "0xabc", tt.wantChains).
				Return([]entity.WalletBalance{}, nil)

			md := &mockMDClient{}
			md.On("ListAssets", mock.Anything, mock.Anything).
				Return(connect.NewResponse(&apiv1.ListAssetsResponse{}), nil)

			source := &mockWalletSource{syncer: syncer}
			h := newHandler(s).WithMarketDataClient(md).WithWalletSyncerSource(source)

			_, err := h.SyncAccount(ctxWithUser(testUserID), connect.NewRequest(&apiv1.SyncAccountRequest{AccountId: testAccountID}))
			require.NoError(t, err)
			assert.Equal(t, tt.wantChains, source.gotChains)
			syncer.AssertExpectations(t)
		})
	}
}

func TestSyncAccount_UnsupportedType(t *testing.T) {
	acct := testAccount(testAccountID)
	acct.Type = entity.AccountTypeBank

	s := &mockStore{}
	s.On("GetAccount", mock.Anything, testAccountID).Return(acct, nil)

	h := newHandler(s).WithMarketDataClient(&mockMDClient{})

	_, err := h.SyncAccount(ctxWithUser(testUserID), connect.NewRequest(&apiv1.SyncAccountRequest{AccountId: testAccountID}))
	require.Error(t, err)
	assert.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))
}

// TestSyncAccount_ManualAccount verifies a manual account reports FailedPrecondition
// ("nothing to sync") — a distinct code from the generic unsupported-type error.
func TestSyncAccount_ManualAccount(t *testing.T) {
	acct := testAccount(testAccountID)
	acct.Type = entity.AccountTypeManual

	s := &mockStore{}
	s.On("GetAccount", mock.Anything, testAccountID).Return(acct, nil)

	h := newHandler(s).WithMarketDataClient(&mockMDClient{})

	_, err := h.SyncAccount(ctxWithUser(testUserID), connect.NewRequest(&apiv1.SyncAccountRequest{AccountId: testAccountID}))
	require.Error(t, err)
	assert.Equal(t, connect.CodeFailedPrecondition, connect.CodeOf(err))
}

// --- Tests: batch import ---

func manualAccount(id string) *entity.Account {
	a := testAccount(id)
	a.Type = entity.AccountTypeManual
	a.Capabilities = []entity.AccountCapability{entity.CapabilityManualPositions}
	return a
}

func TestImportPositions_RequiresManualAccount(t *testing.T) {
	s := &mockStore{}
	s.On("GetAccount", mock.Anything, testAccountID).Return(testAccount(testAccountID), nil) // exchange
	h := newHandler(s).WithMarketDataClient(&mockMDClient{})

	_, err := h.ImportPositions(ctxWithUser(testUserID), connect.NewRequest(&apiv1.ImportPositionsRequest{
		AccountId: testAccountID,
		Positions: []*apiv1.ImportPositionItem{{Symbol: "BTC", Amount: "1"}},
	}))
	require.Error(t, err)
	assert.Equal(t, connect.CodeFailedPrecondition, connect.CodeOf(err))
}

// TestImportPositions_DryRunPlan verifies the plan covers create/update/skip
// without a single write, and that a missing asset is reported as would-create.
func TestImportPositions_DryRunPlan(t *testing.T) {
	s := &mockStore{}
	s.On("GetAccount", mock.Anything, testAccountID).Return(manualAccount(testAccountID), nil)
	existing := testHolding(testHoldingID) // asset testAssetID, amount 100000, decimals 8
	s.On("ListHoldings", mock.Anything, mock.Anything).Return([]*entity.Holding{existing}, "", nil)

	md := &mockMDClient{}
	// ETH: exists -> holding exists with same raw amount -> SKIP is exercised via testAssetID below.
	md.On("FindOrCreateAsset", mock.Anything, mock.MatchedBy(func(r *connect.Request[apiv1.FindOrCreateAssetRequest]) bool {
		return r.Msg.Symbol == "BTC" && r.Msg.DryRun
	})).Return(connect.NewResponse(&apiv1.FindOrCreateAssetResponse{Asset: &apiv1.Asset{Id: testAssetID}}), nil)
	// NEW: does not exist -> would create.
	md.On("FindOrCreateAsset", mock.Anything, mock.MatchedBy(func(r *connect.Request[apiv1.FindOrCreateAssetRequest]) bool {
		return r.Msg.Symbol == "NEW" && r.Msg.DryRun
	})).Return(connect.NewResponse(&apiv1.FindOrCreateAssetResponse{Created: true}), nil)

	h := newHandler(s).WithMarketDataClient(md)

	resp, err := h.ImportPositions(ctxWithUser(testUserID), connect.NewRequest(&apiv1.ImportPositionsRequest{
		AccountId: testAccountID,
		DryRun:    true,
		Positions: []*apiv1.ImportPositionItem{
			{Symbol: "BTC", Amount: "0.002"}, // raw 200000 != 100000 -> UPDATE
			{Symbol: "NEW", Amount: "5"},     // asset missing -> CREATE + asset_created
		},
	}))
	require.NoError(t, err)
	assert.True(t, resp.Msg.DryRun)
	assert.NotEmpty(t, resp.Msg.ImportId)
	require.Len(t, resp.Msg.Items, 2)

	update := resp.Msg.Items[0]
	assert.Equal(t, apiv1.ImportAction_IMPORT_ACTION_UPDATE, update.Action)
	require.NotNil(t, update.PreviousAmount)
	assert.Equal(t, "100000", *update.PreviousAmount)
	assert.Equal(t, "200000", update.Amount)

	created := resp.Msg.Items[1]
	assert.Equal(t, apiv1.ImportAction_IMPORT_ACTION_CREATE, created.Action)
	assert.True(t, created.AssetCreated)
	assert.Empty(t, created.AssetId)

	assert.Equal(t, int32(1), resp.Msg.Updated)
	assert.Equal(t, int32(1), resp.Msg.Created)
	assert.Equal(t, int32(1), resp.Msg.AssetsCreated)

	s.AssertNotCalled(t, "CreateHolding")
	s.AssertNotCalled(t, "UpdateHolding")
}

// TestImportPositions_CommitStampsProvenance verifies the commit path writes
// holdings with source=llm_import and the batch import_id.
func TestImportPositions_CommitStampsProvenance(t *testing.T) {
	const importID = "01926d35-6a1e-7008-8008-000000000001"
	const newAssetID = "01926d35-6a1e-7005-8005-000000000002"

	s := &mockStore{}
	acct := manualAccount(testAccountID)
	acct.PortfolioID = testPortfolioID
	s.On("GetAccount", mock.Anything, testAccountID).Return(acct, nil)
	s.On("ListHoldings", mock.Anything, mock.Anything).Return([]*entity.Holding{}, "", nil)
	s.On("CreateHolding", mock.Anything, mock.MatchedBy(func(h *entity.Holding) bool {
		return h.Source == entity.SourceLLMImport &&
			h.ImportID == importID &&
			h.AssetID == newAssetID &&
			h.PortfolioID == testPortfolioID &&
			h.Amount.String() == "500000000" && h.Decimals == 8
	})).Return(testHolding(testHoldingID), nil)

	md := &mockMDClient{}
	md.On("FindOrCreateAsset", mock.Anything, mock.MatchedBy(func(r *connect.Request[apiv1.FindOrCreateAssetRequest]) bool {
		return r.Msg.Symbol == "DOT" && !r.Msg.DryRun
	})).Return(connect.NewResponse(&apiv1.FindOrCreateAssetResponse{Asset: &apiv1.Asset{Id: newAssetID}, Created: true}), nil)

	h := newHandler(s).WithMarketDataClient(md)

	iid := importID
	resp, err := h.ImportPositions(ctxWithUser(testUserID), connect.NewRequest(&apiv1.ImportPositionsRequest{
		AccountId: testAccountID,
		ImportId:  &iid,
		Positions: []*apiv1.ImportPositionItem{{Symbol: "DOT", Amount: "5"}},
	}))
	require.NoError(t, err)
	assert.Equal(t, importID, resp.Msg.ImportId)
	assert.Equal(t, int32(1), resp.Msg.Created)
	assert.Equal(t, int32(1), resp.Msg.AssetsCreated)
	s.AssertExpectations(t)
}

// TestImportPositions_PerItemErrors verifies bad items fail individually
// without sinking the batch, including duplicate assets within one batch.
func TestImportPositions_PerItemErrors(t *testing.T) {
	s := &mockStore{}
	s.On("GetAccount", mock.Anything, testAccountID).Return(manualAccount(testAccountID), nil)
	s.On("ListHoldings", mock.Anything, mock.Anything).Return([]*entity.Holding{}, "", nil)

	md := &mockMDClient{}
	md.On("FindOrCreateAsset", mock.Anything, mock.Anything).
		Return(connect.NewResponse(&apiv1.FindOrCreateAssetResponse{Asset: &apiv1.Asset{Id: testAssetID}}), nil)

	h := newHandler(s).WithMarketDataClient(md)

	resp, err := h.ImportPositions(ctxWithUser(testUserID), connect.NewRequest(&apiv1.ImportPositionsRequest{
		AccountId: testAccountID,
		DryRun:    true,
		Positions: []*apiv1.ImportPositionItem{
			{Symbol: "BTC", Amount: "abc"},          // bad amount
			{Symbol: "BTC", Amount: "-1"},           // negative
			{Amount: "1"},                           // no symbol, no asset_id
			{Symbol: "BTC", Amount: "0.0000000001"}, // more digits than decimals=8
			{Symbol: "BTC", Amount: "1"},            // OK
			{Symbol: "BTC", Amount: "2"},            // duplicate asset in batch
		},
	}))
	require.NoError(t, err)
	assert.Equal(t, int32(5), resp.Msg.Failed)
	assert.Equal(t, int32(1), resp.Msg.Created)
	for _, idx := range []int{0, 1, 2, 3, 5} {
		assert.NotNil(t, resp.Msg.Items[idx].Error, "item %d should carry an error", idx)
	}
	assert.Nil(t, resp.Msg.Items[4].Error)
}

func TestImportTransactions_DedupAndProvenance(t *testing.T) {
	const importID = "01926d35-6a1e-7008-8008-000000000002"

	s := &mockStore{}
	s.On("GetAccount", mock.Anything, testAccountID).Return(manualAccount(testAccountID), nil)
	// One existing transaction with an external_id and heuristic fields.
	s.On("ListTransactions", mock.Anything, mock.Anything).Return([]*entity.Transaction{{
		ID:        testTxID,
		Type:      entity.TransactionTypeDeposit,
		AccountID: testAccountID,
		AssetID:   testAssetID,
		Data:      map[string]string{"external_id": "ext-1", "date": "2026-07-01", "amount": "5"},
	}}, "", nil)
	s.On("CreateTransaction", mock.Anything, mock.MatchedBy(func(tx *entity.Transaction) bool {
		return tx.Source == entity.SourceLLMImport &&
			tx.ImportID == importID &&
			tx.Status == entity.TransactionStatusCompleted &&
			tx.Data["external_id"] == "ext-2"
	})).Return(&entity.Transaction{ID: "01926d35-6a1e-7006-8006-000000000009"}, nil)

	md := &mockMDClient{}
	md.On("FindOrCreateAsset", mock.Anything, mock.MatchedBy(func(r *connect.Request[apiv1.FindOrCreateAssetRequest]) bool {
		return r.Msg.DryRun // transaction import never creates assets
	})).Return(connect.NewResponse(&apiv1.FindOrCreateAssetResponse{Asset: &apiv1.Asset{Id: testAssetID}}), nil)

	h := newHandler(s).WithMarketDataClient(md)

	iid := importID
	extDup := "ext-1"
	extNew := "ext-2"
	resp, err := h.ImportTransactions(ctxWithUser(testUserID), connect.NewRequest(&apiv1.ImportTransactionsRequest{
		AccountId: testAccountID,
		ImportId:  &iid,
		Transactions: []*apiv1.ImportTransactionItem{
			// Duplicate by external_id -> SKIP.
			{Type: apiv1.TransactionType_TRANSACTION_TYPE_DEPOSIT, ExternalId: &extDup},
			// Duplicate by (type, asset, date, amount) heuristic -> SKIP.
			{Type: apiv1.TransactionType_TRANSACTION_TYPE_DEPOSIT, Symbol: strPtr("BTC"), Data: map[string]string{"date": "2026-07-01", "amount": "5"}},
			// New -> CREATE with provenance.
			{Type: apiv1.TransactionType_TRANSACTION_TYPE_DEPOSIT, Symbol: strPtr("BTC"), ExternalId: &extNew, Data: map[string]string{"date": "2026-07-02", "amount": "7"}},
		},
	}))
	require.NoError(t, err)
	assert.Equal(t, int32(2), resp.Msg.Skipped)
	assert.Equal(t, int32(1), resp.Msg.Created)
	assert.Equal(t, int32(0), resp.Msg.Failed)
	s.AssertExpectations(t)
}

func TestImportTransactions_UnknownAssetFailsItem(t *testing.T) {
	s := &mockStore{}
	s.On("GetAccount", mock.Anything, testAccountID).Return(manualAccount(testAccountID), nil)
	s.On("ListTransactions", mock.Anything, mock.Anything).Return([]*entity.Transaction{}, "", nil)

	md := &mockMDClient{}
	md.On("FindOrCreateAsset", mock.Anything, mock.Anything).
		Return(connect.NewResponse(&apiv1.FindOrCreateAssetResponse{Created: true}), nil) // not found

	h := newHandler(s).WithMarketDataClient(md)

	resp, err := h.ImportTransactions(ctxWithUser(testUserID), connect.NewRequest(&apiv1.ImportTransactionsRequest{
		AccountId: testAccountID,
		DryRun:    true,
		Transactions: []*apiv1.ImportTransactionItem{
			{Type: apiv1.TransactionType_TRANSACTION_TYPE_TRADE, Symbol: strPtr("GHOST")},
		},
	}))
	require.NoError(t, err)
	assert.Equal(t, int32(1), resp.Msg.Failed)
	require.NotNil(t, resp.Msg.Items[0].Error)
	assert.Contains(t, *resp.Msg.Items[0].Error, "not found")
	s.AssertNotCalled(t, "CreateTransaction")
}

func strPtr(s string) *string { return &s }

// --- Tests: full-snapshot reconcile ---

// TestImportPositions_FullSnapshotPlan verifies reconcile mode: holdings absent
// from the batch are planned as DELETE, excluded holdings are never touched,
// and a dry run performs no writes.
func TestImportPositions_FullSnapshotPlan(t *testing.T) {
	const absentAssetID = "01926d35-6a1e-7005-8005-00000000000a"
	const excludedAssetID = "01926d35-6a1e-7005-8005-00000000000b"

	s := &mockStore{}
	s.On("GetAccount", mock.Anything, testAccountID).Return(manualAccount(testAccountID), nil)

	kept := testHolding(testHoldingID) // asset testAssetID, present in the batch
	absent := testHolding(testHoldingID2)
	absent.AssetID = absentAssetID
	excluded := testHolding("01926d35-6a1e-7004-8004-000000000003")
	excluded.AssetID = excludedAssetID
	excluded.Excluded = true
	s.On("ListHoldings", mock.Anything, mock.Anything).
		Return([]*entity.Holding{kept, absent, excluded}, "", nil)

	md := &mockMDClient{}
	md.On("FindOrCreateAsset", mock.Anything, mock.Anything).
		Return(connect.NewResponse(&apiv1.FindOrCreateAssetResponse{Asset: &apiv1.Asset{Id: testAssetID}}), nil)
	sym := "GONE"
	md.On("GetAsset", mock.Anything, mock.MatchedBy(func(r *connect.Request[apiv1.GetAssetRequest]) bool {
		return r.Msg.Id == absentAssetID
	})).Return(connect.NewResponse(&apiv1.Asset{Id: absentAssetID, Symbol: &sym}), nil)

	h := newHandler(s).WithMarketDataClient(md)

	resp, err := h.ImportPositions(ctxWithUser(testUserID), connect.NewRequest(&apiv1.ImportPositionsRequest{
		AccountId:    testAccountID,
		DryRun:       true,
		FullSnapshot: true,
		Positions:    []*apiv1.ImportPositionItem{{Symbol: "BTC", Amount: "0.001"}}, // matches kept exactly
	}))
	require.NoError(t, err)
	assert.False(t, resp.Msg.DeletionsSuppressed)
	assert.Equal(t, int32(1), resp.Msg.Skipped)
	assert.Equal(t, int32(1), resp.Msg.Deleted)
	require.Len(t, resp.Msg.Items, 2)

	del := resp.Msg.Items[1]
	assert.Equal(t, apiv1.ImportAction_IMPORT_ACTION_DELETE, del.Action)
	assert.Equal(t, absentAssetID, del.AssetId)
	assert.Equal(t, "GONE", del.Symbol)
	require.NotNil(t, del.PreviousAmount)
	assert.Equal(t, "100000", *del.PreviousAmount)

	s.AssertNotCalled(t, "DeleteHolding")
}

func TestImportPositions_FullSnapshotCommitDeletes(t *testing.T) {
	const absentAssetID = "01926d35-6a1e-7005-8005-00000000000a"

	s := &mockStore{}
	s.On("GetAccount", mock.Anything, testAccountID).Return(manualAccount(testAccountID), nil)
	absent := testHolding(testHoldingID2)
	absent.AssetID = absentAssetID
	s.On("ListHoldings", mock.Anything, mock.Anything).
		Return([]*entity.Holding{testHolding(testHoldingID), absent}, "", nil)
	s.On("DeleteHolding", mock.Anything, testHoldingID2).Return(nil)

	md := &mockMDClient{}
	md.On("FindOrCreateAsset", mock.Anything, mock.Anything).
		Return(connect.NewResponse(&apiv1.FindOrCreateAssetResponse{Asset: &apiv1.Asset{Id: testAssetID}}), nil)
	md.On("GetAsset", mock.Anything, mock.Anything).
		Return(connect.NewResponse(&apiv1.Asset{Id: absentAssetID}), nil)

	h := newHandler(s).WithMarketDataClient(md)

	resp, err := h.ImportPositions(ctxWithUser(testUserID), connect.NewRequest(&apiv1.ImportPositionsRequest{
		AccountId:    testAccountID,
		FullSnapshot: true,
		Positions:    []*apiv1.ImportPositionItem{{Symbol: "BTC", Amount: "0.001"}},
	}))
	require.NoError(t, err)
	assert.Equal(t, int32(1), resp.Msg.Deleted)
	s.AssertExpectations(t)
}

// TestImportPositions_FullSnapshotSuppressedOnFailure verifies the safety
// guard: any failed batch item suppresses deletions entirely — a partially
// parsed export must not close positions it merely failed to mention.
func TestImportPositions_FullSnapshotSuppressedOnFailure(t *testing.T) {
	const absentAssetID = "01926d35-6a1e-7005-8005-00000000000a"

	s := &mockStore{}
	s.On("GetAccount", mock.Anything, testAccountID).Return(manualAccount(testAccountID), nil)
	absent := testHolding(testHoldingID2)
	absent.AssetID = absentAssetID
	s.On("ListHoldings", mock.Anything, mock.Anything).
		Return([]*entity.Holding{absent}, "", nil)

	h := newHandler(s).WithMarketDataClient(&mockMDClient{})

	resp, err := h.ImportPositions(ctxWithUser(testUserID), connect.NewRequest(&apiv1.ImportPositionsRequest{
		AccountId:    testAccountID,
		FullSnapshot: true, // commit mode — deletions would be real
		Positions:    []*apiv1.ImportPositionItem{{Symbol: "BTC", Amount: "not-a-number"}},
	}))
	require.NoError(t, err)
	assert.Equal(t, int32(1), resp.Msg.Failed)
	assert.True(t, resp.Msg.DeletionsSuppressed)
	assert.Equal(t, int32(0), resp.Msg.Deleted)
	s.AssertNotCalled(t, "DeleteHolding")
}

// TestSyncAccount_SplitsPositionsByChain: the same token on two chains is two
// positions. Collapsing them by asset_id destroyed the only record of where an
// amount sits, and summed quantities across mismatched decimals on the way —
// USDC is 6 decimals on Ethereum and 18 on BSC, and the merged row was stored
// at whichever scale happened to be larger. Two addresses on the SAME chain
// still merge: that is one place.
func TestSyncAccount_SplitsPositionsByChain(t *testing.T) {
	acct := testAccount(testAccountID)
	acct.Type = entity.AccountTypeWallet
	acct.Data = map[string]string{"address": "0xabc", "chain": "eth,base"}

	s := &mockStore{}
	s.On("GetAccount", mock.Anything, testAccountID).Return(acct, nil)
	s.On("ListHoldings", mock.Anything, mock.Anything).Return([]*entity.Holding{}, "", nil)

	created := map[string]*entity.Holding{}
	s.On("CreateHolding", mock.Anything, mock.Anything).
		Run(func(args mock.Arguments) {
			h := args.Get(1).(*entity.Holding)
			created[h.Chain] = h
		}).
		Return(&entity.Holding{ID: "h-1"}, nil)

	ws := &mockWalletSyncer{}
	ws.On("SyncWallet", mock.Anything, "0xabc", []string{"eth", "base"}).Return([]entity.WalletBalance{
		{Symbol: "USDC", Amount: "1000000", Decimals: 6, ContractAddress: "0xusdc", Chain: "eth"},
		{Symbol: "USDC", Amount: "3000000", Decimals: 6, ContractAddress: "0xusdc", Chain: "eth"},
		{Symbol: "USDC", Amount: "7000000000000000000", Decimals: 18, ContractAddress: "0xusdc", Chain: "base"},
	}, nil)

	md := &mockMDClient{autoAsset: true}
	md.On("FetchExternalPrices", mock.Anything, mock.Anything).
		Return(connect.NewResponse(&apiv1.FetchExternalPricesResponse{}), nil)

	h := newHandler(s).WithMarketDataClient(md).WithWalletSyncer(ws)

	resp, err := h.SyncAccount(ctxWithUser(testUserID), connect.NewRequest(&apiv1.SyncAccountRequest{
		AccountId: testAccountID,
	}))
	require.NoError(t, err)
	assert.Empty(t, resp.Msg.Errors)
	assert.Equal(t, int32(2), resp.Msg.HoldingsUpserted, "one row per chain, not one summed row")

	require.Contains(t, created, "eth")
	require.Contains(t, created, "base")
	assert.Equal(t, "4000000", created["eth"].Amount.String(), "same chain, two addresses: merged")
	assert.Equal(t, uint32(6), created["eth"].Decimals)
	assert.Equal(t, "7000000000000000000", created["base"].Amount.String(), "the 18-decimal chain keeps its own scale")
	assert.Equal(t, uint32(18), created["base"].Decimals)
}

// TestSyncAccount_AdoptsPreChainHolding: rows written before positions carried a
// chain are one summed row per asset with an empty chain. The first chain seen
// adopts that row — keeping its id, portfolio and manual excluded override —
// instead of leaving it beside the new per-chain rows, where it would double the
// position until someone deleted it by hand.
func TestSyncAccount_AdoptsPreChainHolding(t *testing.T) {
	acct := testAccount(testAccountID)
	acct.Type = entity.AccountTypeWallet
	acct.Data = map[string]string{"address": "0xabc", "chain": "eth,base"}

	legacy := &entity.Holding{
		ID:        "h-legacy",
		AssetID:   "asset-USDC", // what the auto-resolving market-data mock returns
		AccountID: testAccountID,
		Amount:    decimal.RequireFromString("4000000"),
		Decimals:  6,
		Source:    entity.SourceSync,
	}

	s := &mockStore{}
	s.On("GetAccount", mock.Anything, testAccountID).Return(acct, nil)
	s.On("ListHoldings", mock.Anything, mock.Anything).Return([]*entity.Holding{legacy}, "", nil)

	var updated *entity.Holding
	var updatedFields []string
	s.On("UpdateHolding", mock.Anything, mock.Anything, mock.Anything).
		Run(func(args mock.Arguments) {
			updated = args.Get(1).(*entity.Holding)
			updatedFields = args.Get(2).([]string)
		}).
		Return(&entity.Holding{ID: "h-legacy"}, nil)

	var createdChains []string
	s.On("CreateHolding", mock.Anything, mock.Anything).
		Run(func(args mock.Arguments) {
			createdChains = append(createdChains, args.Get(1).(*entity.Holding).Chain)
		}).
		Return(&entity.Holding{ID: "h-new"}, nil)

	ws := &mockWalletSyncer{}
	ws.On("SyncWallet", mock.Anything, "0xabc", []string{"eth", "base"}).Return([]entity.WalletBalance{
		{Symbol: "USDC", Amount: "1000000", Decimals: 6, ContractAddress: "0xusdc", Chain: "eth"},
		{Symbol: "USDC", Amount: "3000000", Decimals: 6, ContractAddress: "0xusdc", Chain: "base"},
	}, nil)

	md := &mockMDClient{autoAsset: true}
	md.On("FetchExternalPrices", mock.Anything, mock.Anything).
		Return(connect.NewResponse(&apiv1.FetchExternalPricesResponse{}), nil)

	h := newHandler(s).WithMarketDataClient(md).WithWalletSyncer(ws)

	resp, err := h.SyncAccount(ctxWithUser(testUserID), connect.NewRequest(&apiv1.SyncAccountRequest{
		AccountId: testAccountID,
	}))
	require.NoError(t, err)
	assert.Empty(t, resp.Msg.Errors)
	assert.Equal(t, int32(2), resp.Msg.HoldingsUpserted)

	require.NotNil(t, updated, "the pre-chain row must be reused, not orphaned")
	assert.Equal(t, "h-legacy", updated.ID)
	assert.Equal(t, "eth", updated.Chain, "the first chain seen adopts the row")
	assert.Contains(t, updatedFields, "chain")
	assert.Equal(t, "1000000", updated.Amount.String())
	assert.Equal(t, []string{"base"}, createdChains, "only the remaining chain becomes a new row")
}
