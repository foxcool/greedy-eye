package user

import (
	"context"
	"testing"

	"github.com/foxcool/greedy-eye/internal/api/models"
	"github.com/foxcool/greedy-eye/internal/api/services"
	"github.com/stretchr/testify/assert"
	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/emptypb"
)

type mockStorageClient struct{}

func (m *mockStorageClient) GetUser(ctx context.Context, req *services.GetUserRequest, opts ...grpc.CallOption) (*models.User, error) {
	if req.Id == "" {
		return nil, status.Errorf(codes.InvalidArgument, "user_id is required")
	}
	return nil, status.Errorf(codes.NotFound, "user not found")
}

func (m *mockStorageClient) UpdateUser(ctx context.Context, req *services.UpdateUserRequest, opts ...grpc.CallOption) (*models.User, error) {
	return nil, status.Errorf(codes.Unimplemented, "not implemented in mock")
}

func (m *mockStorageClient) ListExternalAPIKeys(ctx context.Context, req *services.ListExternalAPIKeysRequest, opts ...grpc.CallOption) (*services.ListExternalAPIKeysResponse, error) {
	return nil, status.Errorf(codes.Unimplemented, "not implemented in mock")
}

// Stub methods to satisfy the interface - only implement what's needed for tests
func (m *mockStorageClient) CreateAsset(context.Context, *services.CreateAssetRequest, ...grpc.CallOption) (*models.Asset, error) {
	return nil, status.Errorf(codes.Unimplemented, "not implemented")
}
func (m *mockStorageClient) GetAsset(context.Context, *services.GetAssetRequest, ...grpc.CallOption) (*models.Asset, error) {
	return nil, status.Errorf(codes.Unimplemented, "not implemented")
}
func (m *mockStorageClient) UpdateAsset(context.Context, *services.UpdateAssetRequest, ...grpc.CallOption) (*models.Asset, error) {
	return nil, status.Errorf(codes.Unimplemented, "not implemented")
}
func (m *mockStorageClient) DeleteAsset(context.Context, *services.DeleteAssetRequest, ...grpc.CallOption) (*emptypb.Empty, error) {
	return nil, status.Errorf(codes.Unimplemented, "not implemented")
}
func (m *mockStorageClient) ListAssets(context.Context, *services.ListAssetsRequest, ...grpc.CallOption) (*services.ListAssetsResponse, error) {
	return nil, status.Errorf(codes.Unimplemented, "not implemented")
}
func (m *mockStorageClient) CreatePrice(context.Context, *services.CreatePriceRequest, ...grpc.CallOption) (*models.Price, error) {
	return nil, status.Errorf(codes.Unimplemented, "not implemented")
}
func (m *mockStorageClient) CreatePrices(context.Context, *services.CreatePricesRequest, ...grpc.CallOption) (*services.CreatePricesResponse, error) {
	return nil, status.Errorf(codes.Unimplemented, "not implemented")
}
func (m *mockStorageClient) GetLatestPrice(context.Context, *services.GetLatestPriceRequest, ...grpc.CallOption) (*models.Price, error) {
	return nil, status.Errorf(codes.Unimplemented, "not implemented")
}
func (m *mockStorageClient) ListPriceHistory(context.Context, *services.ListPriceHistoryRequest, ...grpc.CallOption) (*services.ListPriceHistoryResponse, error) {
	return nil, status.Errorf(codes.Unimplemented, "not implemented")
}
func (m *mockStorageClient) ListPricesByInterval(context.Context, *services.ListPricesByIntervalRequest, ...grpc.CallOption) (*services.ListPriceHistoryResponse, error) {
	return nil, status.Errorf(codes.Unimplemented, "not implemented")
}
func (m *mockStorageClient) DeletePrice(context.Context, *services.DeletePriceRequest, ...grpc.CallOption) (*emptypb.Empty, error) {
	return nil, status.Errorf(codes.Unimplemented, "not implemented")
}
func (m *mockStorageClient) DeletePrices(context.Context, *services.DeletePricesRequest, ...grpc.CallOption) (*emptypb.Empty, error) {
	return nil, status.Errorf(codes.Unimplemented, "not implemented")
}
func (m *mockStorageClient) CreatePortfolio(context.Context, *services.CreatePortfolioRequest, ...grpc.CallOption) (*models.Portfolio, error) {
	return nil, status.Errorf(codes.Unimplemented, "not implemented")
}
func (m *mockStorageClient) GetPortfolio(context.Context, *services.GetPortfolioRequest, ...grpc.CallOption) (*models.Portfolio, error) {
	return nil, status.Errorf(codes.Unimplemented, "not implemented")
}
func (m *mockStorageClient) UpdatePortfolio(context.Context, *services.UpdatePortfolioRequest, ...grpc.CallOption) (*models.Portfolio, error) {
	return nil, status.Errorf(codes.Unimplemented, "not implemented")
}
func (m *mockStorageClient) DeletePortfolio(context.Context, *services.DeletePortfolioRequest, ...grpc.CallOption) (*emptypb.Empty, error) {
	return nil, status.Errorf(codes.Unimplemented, "not implemented")
}
func (m *mockStorageClient) ListPortfolios(context.Context, *services.ListPortfoliosRequest, ...grpc.CallOption) (*services.ListPortfoliosResponse, error) {
	return nil, status.Errorf(codes.Unimplemented, "not implemented")
}
func (m *mockStorageClient) CreateHolding(context.Context, *services.CreateHoldingRequest, ...grpc.CallOption) (*models.Holding, error) {
	return nil, status.Errorf(codes.Unimplemented, "not implemented")
}
func (m *mockStorageClient) GetHolding(context.Context, *services.GetHoldingRequest, ...grpc.CallOption) (*models.Holding, error) {
	return nil, status.Errorf(codes.Unimplemented, "not implemented")
}
func (m *mockStorageClient) UpdateHolding(context.Context, *services.UpdateHoldingRequest, ...grpc.CallOption) (*models.Holding, error) {
	return nil, status.Errorf(codes.Unimplemented, "not implemented")
}
func (m *mockStorageClient) ListHoldings(context.Context, *services.ListHoldingsRequest, ...grpc.CallOption) (*services.ListHoldingsResponse, error) {
	return nil, status.Errorf(codes.Unimplemented, "not implemented")
}
func (m *mockStorageClient) CreateUser(context.Context, *services.CreateUserRequest, ...grpc.CallOption) (*models.User, error) {
	return nil, status.Errorf(codes.Unimplemented, "not implemented")
}
func (m *mockStorageClient) DeleteUser(context.Context, *services.DeleteUserRequest, ...grpc.CallOption) (*emptypb.Empty, error) {
	return nil, status.Errorf(codes.Unimplemented, "not implemented")
}
func (m *mockStorageClient) CreateAccount(context.Context, *services.CreateAccountRequest, ...grpc.CallOption) (*models.Account, error) {
	return nil, status.Errorf(codes.Unimplemented, "not implemented")
}
func (m *mockStorageClient) GetAccount(context.Context, *services.GetAccountRequest, ...grpc.CallOption) (*models.Account, error) {
	return nil, status.Errorf(codes.Unimplemented, "not implemented")
}
func (m *mockStorageClient) UpdateAccount(context.Context, *services.UpdateAccountRequest, ...grpc.CallOption) (*models.Account, error) {
	return nil, status.Errorf(codes.Unimplemented, "not implemented")
}
func (m *mockStorageClient) DeleteAccount(context.Context, *services.DeleteAccountRequest, ...grpc.CallOption) (*emptypb.Empty, error) {
	return nil, status.Errorf(codes.Unimplemented, "not implemented")
}
func (m *mockStorageClient) ListAccounts(context.Context, *services.ListAccountsRequest, ...grpc.CallOption) (*services.ListAccountsResponse, error) {
	return nil, status.Errorf(codes.Unimplemented, "not implemented")
}
func (m *mockStorageClient) CreateTransaction(context.Context, *services.CreateTransactionRequest, ...grpc.CallOption) (*models.Transaction, error) {
	return nil, status.Errorf(codes.Unimplemented, "not implemented")
}
func (m *mockStorageClient) GetTransaction(context.Context, *services.GetTransactionRequest, ...grpc.CallOption) (*models.Transaction, error) {
	return nil, status.Errorf(codes.Unimplemented, "not implemented")
}
func (m *mockStorageClient) UpdateTransaction(context.Context, *services.UpdateTransactionRequest, ...grpc.CallOption) (*models.Transaction, error) {
	return nil, status.Errorf(codes.Unimplemented, "not implemented")
}
func (m *mockStorageClient) ListTransactions(context.Context, *services.ListTransactionsRequest, ...grpc.CallOption) (*services.ListTransactionsResponse, error) {
	return nil, status.Errorf(codes.Unimplemented, "not implemented")
}
func (m *mockStorageClient) CreateRule(context.Context, *services.CreateRuleRequest, ...grpc.CallOption) (*models.Rule, error) {
	return nil, status.Errorf(codes.Unimplemented, "not implemented")
}
func (m *mockStorageClient) GetRule(context.Context, *services.GetRuleRequest, ...grpc.CallOption) (*models.Rule, error) {
	return nil, status.Errorf(codes.Unimplemented, "not implemented")
}
func (m *mockStorageClient) UpdateRule(context.Context, *services.UpdateRuleRequest, ...grpc.CallOption) (*models.Rule, error) {
	return nil, status.Errorf(codes.Unimplemented, "not implemented")
}
func (m *mockStorageClient) DeleteRule(context.Context, *services.DeleteRuleRequest, ...grpc.CallOption) (*emptypb.Empty, error) {
	return nil, status.Errorf(codes.Unimplemented, "not implemented")
}
func (m *mockStorageClient) ListRules(context.Context, *services.ListRulesRequest, ...grpc.CallOption) (*services.ListRulesResponse, error) {
	return nil, status.Errorf(codes.Unimplemented, "not implemented")
}
func (m *mockStorageClient) CreateRuleExecution(context.Context, *services.CreateRuleExecutionRequest, ...grpc.CallOption) (*models.RuleExecution, error) {
	return nil, status.Errorf(codes.Unimplemented, "not implemented")
}
func (m *mockStorageClient) GetRuleExecution(context.Context, *services.GetRuleExecutionRequest, ...grpc.CallOption) (*models.RuleExecution, error) {
	return nil, status.Errorf(codes.Unimplemented, "not implemented")
}
func (m *mockStorageClient) UpdateRuleExecution(context.Context, *services.UpdateRuleExecutionRequest, ...grpc.CallOption) (*models.RuleExecution, error) {
	return nil, status.Errorf(codes.Unimplemented, "not implemented")
}
func (m *mockStorageClient) ListRuleExecutions(context.Context, *services.ListRuleExecutionsRequest, ...grpc.CallOption) (*services.ListRuleExecutionsResponse, error) {
	return nil, status.Errorf(codes.Unimplemented, "not implemented")
}
func (m *mockStorageClient) CreateAPIKey(context.Context, *services.CreateAPIKeyRequest, ...grpc.CallOption) (*models.APIKey, error) {
	return nil, status.Errorf(codes.Unimplemented, "not implemented")
}
func (m *mockStorageClient) GetAPIKey(context.Context, *services.GetAPIKeyRequest, ...grpc.CallOption) (*models.APIKey, error) {
	return nil, status.Errorf(codes.Unimplemented, "not implemented")
}
func (m *mockStorageClient) UpdateAPIKey(context.Context, *services.UpdateAPIKeyRequest, ...grpc.CallOption) (*models.APIKey, error) {
	return nil, status.Errorf(codes.Unimplemented, "not implemented")
}
func (m *mockStorageClient) DeleteAPIKey(context.Context, *services.DeleteAPIKeyRequest, ...grpc.CallOption) (*emptypb.Empty, error) {
	return nil, status.Errorf(codes.Unimplemented, "not implemented")
}
func (m *mockStorageClient) ListAPIKeys(context.Context, *services.ListAPIKeysRequest, ...grpc.CallOption) (*services.ListAPIKeysResponse, error) {
	return nil, status.Errorf(codes.Unimplemented, "not implemented")
}
func (m *mockStorageClient) CreateExternalAPIKey(context.Context, *services.CreateExternalAPIKeyRequest, ...grpc.CallOption) (*models.ExternalAPIKey, error) {
	return nil, status.Errorf(codes.Unimplemented, "not implemented")
}
func (m *mockStorageClient) GetExternalAPIKey(context.Context, *services.GetExternalAPIKeyRequest, ...grpc.CallOption) (*models.ExternalAPIKey, error) {
	return nil, status.Errorf(codes.Unimplemented, "not implemented")
}
func (m *mockStorageClient) UpdateExternalAPIKey(context.Context, *services.UpdateExternalAPIKeyRequest, ...grpc.CallOption) (*models.ExternalAPIKey, error) {
	return nil, status.Errorf(codes.Unimplemented, "not implemented")
}
func (m *mockStorageClient) DeleteExternalAPIKey(context.Context, *services.DeleteExternalAPIKeyRequest, ...grpc.CallOption) (*emptypb.Empty, error) {
	return nil, status.Errorf(codes.Unimplemented, "not implemented")
}

func TestUserService_UpdateUserPreferences(t *testing.T) {
	logger := zap.NewNop()
	mockClient := &mockStorageClient{}
	service := NewService(logger, mockClient)

	t.Run("should return NotFound for non-existent user", func(t *testing.T) {
		req := &services.UpdateUserPreferencesRequest{
			UserId: "non-existent-user",
			PreferencesToUpdate: map[string]string{
				"theme": "dark",
			},
		}

		resp, err := service.UpdateUserPreferences(context.Background(), req)

		assert.Nil(t, resp)
		assert.Error(t, err)
		// Service calls GetUser first, which returns NotFound
		assert.Equal(t, codes.NotFound, status.Code(err))
	})
}