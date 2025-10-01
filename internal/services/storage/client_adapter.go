package storage

import (
	"context"

	"github.com/foxcool/greedy-eye/internal/api/models"
	"github.com/foxcool/greedy-eye/internal/api/services"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/types/known/emptypb"
)

// LocalClientAdapter adapts StorageService to StorageServiceClient interface
// This allows using the concrete storage service as a client in monolithic deployments
type LocalClientAdapter struct {
	service *StorageService
}

// NewLocalClient creates a new local client adapter from a storage service
func NewLocalClient(service *StorageService) services.StorageServiceClient {
	return &LocalClientAdapter{service: service}
}

// Asset operations
func (a *LocalClientAdapter) CreateAsset(ctx context.Context, in *services.CreateAssetRequest, opts ...grpc.CallOption) (*models.Asset, error) {
	return a.service.CreateAsset(ctx, in)
}

func (a *LocalClientAdapter) GetAsset(ctx context.Context, in *services.GetAssetRequest, opts ...grpc.CallOption) (*models.Asset, error) {
	return a.service.GetAsset(ctx, in)
}

func (a *LocalClientAdapter) UpdateAsset(ctx context.Context, in *services.UpdateAssetRequest, opts ...grpc.CallOption) (*models.Asset, error) {
	return a.service.UpdateAsset(ctx, in)
}

func (a *LocalClientAdapter) DeleteAsset(ctx context.Context, in *services.DeleteAssetRequest, opts ...grpc.CallOption) (*emptypb.Empty, error) {
	return a.service.DeleteAsset(ctx, in)
}

func (a *LocalClientAdapter) ListAssets(ctx context.Context, in *services.ListAssetsRequest, opts ...grpc.CallOption) (*services.ListAssetsResponse, error) {
	return a.service.ListAssets(ctx, in)
}

// Price operations
func (a *LocalClientAdapter) CreatePrice(ctx context.Context, in *services.CreatePriceRequest, opts ...grpc.CallOption) (*models.Price, error) {
	return a.service.CreatePrice(ctx, in)
}

func (a *LocalClientAdapter) CreatePrices(ctx context.Context, in *services.CreatePricesRequest, opts ...grpc.CallOption) (*services.CreatePricesResponse, error) {
	return a.service.CreatePrices(ctx, in)
}

func (a *LocalClientAdapter) GetLatestPrice(ctx context.Context, in *services.GetLatestPriceRequest, opts ...grpc.CallOption) (*models.Price, error) {
	return a.service.GetLatestPrice(ctx, in)
}

func (a *LocalClientAdapter) ListPriceHistory(ctx context.Context, in *services.ListPriceHistoryRequest, opts ...grpc.CallOption) (*services.ListPriceHistoryResponse, error) {
	return a.service.ListPriceHistory(ctx, in)
}

func (a *LocalClientAdapter) ListPricesByInterval(ctx context.Context, in *services.ListPricesByIntervalRequest, opts ...grpc.CallOption) (*services.ListPriceHistoryResponse, error) {
	return a.service.ListPricesByInterval(ctx, in)
}

func (a *LocalClientAdapter) DeletePrice(ctx context.Context, in *services.DeletePriceRequest, opts ...grpc.CallOption) (*emptypb.Empty, error) {
	return a.service.DeletePrice(ctx, in)
}

func (a *LocalClientAdapter) DeletePrices(ctx context.Context, in *services.DeletePricesRequest, opts ...grpc.CallOption) (*emptypb.Empty, error) {
	return a.service.DeletePrices(ctx, in)
}

// Portfolio operations
func (a *LocalClientAdapter) CreatePortfolio(ctx context.Context, in *services.CreatePortfolioRequest, opts ...grpc.CallOption) (*models.Portfolio, error) {
	return a.service.CreatePortfolio(ctx, in)
}

func (a *LocalClientAdapter) GetPortfolio(ctx context.Context, in *services.GetPortfolioRequest, opts ...grpc.CallOption) (*models.Portfolio, error) {
	return a.service.GetPortfolio(ctx, in)
}

func (a *LocalClientAdapter) UpdatePortfolio(ctx context.Context, in *services.UpdatePortfolioRequest, opts ...grpc.CallOption) (*models.Portfolio, error) {
	return a.service.UpdatePortfolio(ctx, in)
}

func (a *LocalClientAdapter) DeletePortfolio(ctx context.Context, in *services.DeletePortfolioRequest, opts ...grpc.CallOption) (*emptypb.Empty, error) {
	return a.service.DeletePortfolio(ctx, in)
}

func (a *LocalClientAdapter) ListPortfolios(ctx context.Context, in *services.ListPortfoliosRequest, opts ...grpc.CallOption) (*services.ListPortfoliosResponse, error) {
	return a.service.ListPortfolios(ctx, in)
}

// Holding operations
func (a *LocalClientAdapter) CreateHolding(ctx context.Context, in *services.CreateHoldingRequest, opts ...grpc.CallOption) (*models.Holding, error) {
	return a.service.CreateHolding(ctx, in)
}

func (a *LocalClientAdapter) GetHolding(ctx context.Context, in *services.GetHoldingRequest, opts ...grpc.CallOption) (*models.Holding, error) {
	return a.service.GetHolding(ctx, in)
}

func (a *LocalClientAdapter) UpdateHolding(ctx context.Context, in *services.UpdateHoldingRequest, opts ...grpc.CallOption) (*models.Holding, error) {
	return a.service.UpdateHolding(ctx, in)
}

func (a *LocalClientAdapter) ListHoldings(ctx context.Context, in *services.ListHoldingsRequest, opts ...grpc.CallOption) (*services.ListHoldingsResponse, error) {
	return a.service.ListHoldings(ctx, in)
}

// User operations
func (a *LocalClientAdapter) CreateUser(ctx context.Context, in *services.CreateUserRequest, opts ...grpc.CallOption) (*models.User, error) {
	return a.service.CreateUser(ctx, in)
}

func (a *LocalClientAdapter) GetUser(ctx context.Context, in *services.GetUserRequest, opts ...grpc.CallOption) (*models.User, error) {
	return a.service.GetUser(ctx, in)
}

func (a *LocalClientAdapter) UpdateUser(ctx context.Context, in *services.UpdateUserRequest, opts ...grpc.CallOption) (*models.User, error) {
	return a.service.UpdateUser(ctx, in)
}

func (a *LocalClientAdapter) DeleteUser(ctx context.Context, in *services.DeleteUserRequest, opts ...grpc.CallOption) (*emptypb.Empty, error) {
	return a.service.DeleteUser(ctx, in)
}

// APIKey operations
func (a *LocalClientAdapter) CreateAPIKey(ctx context.Context, in *services.CreateAPIKeyRequest, opts ...grpc.CallOption) (*models.APIKey, error) {
	return a.service.CreateAPIKey(ctx, in)
}

func (a *LocalClientAdapter) GetAPIKey(ctx context.Context, in *services.GetAPIKeyRequest, opts ...grpc.CallOption) (*models.APIKey, error) {
	return a.service.GetAPIKey(ctx, in)
}

func (a *LocalClientAdapter) UpdateAPIKey(ctx context.Context, in *services.UpdateAPIKeyRequest, opts ...grpc.CallOption) (*models.APIKey, error) {
	return a.service.UpdateAPIKey(ctx, in)
}

func (a *LocalClientAdapter) DeleteAPIKey(ctx context.Context, in *services.DeleteAPIKeyRequest, opts ...grpc.CallOption) (*emptypb.Empty, error) {
	return a.service.DeleteAPIKey(ctx, in)
}

func (a *LocalClientAdapter) ListAPIKeys(ctx context.Context, in *services.ListAPIKeysRequest, opts ...grpc.CallOption) (*services.ListAPIKeysResponse, error) {
	return a.service.ListAPIKeys(ctx, in)
}

// Account operations
func (a *LocalClientAdapter) CreateAccount(ctx context.Context, in *services.CreateAccountRequest, opts ...grpc.CallOption) (*models.Account, error) {
	return a.service.CreateAccount(ctx, in)
}

func (a *LocalClientAdapter) GetAccount(ctx context.Context, in *services.GetAccountRequest, opts ...grpc.CallOption) (*models.Account, error) {
	return a.service.GetAccount(ctx, in)
}

func (a *LocalClientAdapter) UpdateAccount(ctx context.Context, in *services.UpdateAccountRequest, opts ...grpc.CallOption) (*models.Account, error) {
	return a.service.UpdateAccount(ctx, in)
}

func (a *LocalClientAdapter) DeleteAccount(ctx context.Context, in *services.DeleteAccountRequest, opts ...grpc.CallOption) (*emptypb.Empty, error) {
	return a.service.DeleteAccount(ctx, in)
}

func (a *LocalClientAdapter) ListAccounts(ctx context.Context, in *services.ListAccountsRequest, opts ...grpc.CallOption) (*services.ListAccountsResponse, error) {
	return a.service.ListAccounts(ctx, in)
}

// Transaction operations
func (a *LocalClientAdapter) CreateTransaction(ctx context.Context, in *services.CreateTransactionRequest, opts ...grpc.CallOption) (*models.Transaction, error) {
	return a.service.CreateTransaction(ctx, in)
}

func (a *LocalClientAdapter) GetTransaction(ctx context.Context, in *services.GetTransactionRequest, opts ...grpc.CallOption) (*models.Transaction, error) {
	return a.service.GetTransaction(ctx, in)
}

func (a *LocalClientAdapter) UpdateTransaction(ctx context.Context, in *services.UpdateTransactionRequest, opts ...grpc.CallOption) (*models.Transaction, error) {
	return a.service.UpdateTransaction(ctx, in)
}

func (a *LocalClientAdapter) ListTransactions(ctx context.Context, in *services.ListTransactionsRequest, opts ...grpc.CallOption) (*services.ListTransactionsResponse, error) {
	return a.service.ListTransactions(ctx, in)
}

// External API Key operations
func (a *LocalClientAdapter) CreateExternalAPIKey(ctx context.Context, in *services.CreateExternalAPIKeyRequest, opts ...grpc.CallOption) (*models.ExternalAPIKey, error) {
	return a.service.CreateExternalAPIKey(ctx, in)
}

func (a *LocalClientAdapter) GetExternalAPIKey(ctx context.Context, in *services.GetExternalAPIKeyRequest, opts ...grpc.CallOption) (*models.ExternalAPIKey, error) {
	return a.service.GetExternalAPIKey(ctx, in)
}

func (a *LocalClientAdapter) UpdateExternalAPIKey(ctx context.Context, in *services.UpdateExternalAPIKeyRequest, opts ...grpc.CallOption) (*models.ExternalAPIKey, error) {
	return a.service.UpdateExternalAPIKey(ctx, in)
}

func (a *LocalClientAdapter) DeleteExternalAPIKey(ctx context.Context, in *services.DeleteExternalAPIKeyRequest, opts ...grpc.CallOption) (*emptypb.Empty, error) {
	return a.service.DeleteExternalAPIKey(ctx, in)
}

func (a *LocalClientAdapter) ListExternalAPIKeys(ctx context.Context, in *services.ListExternalAPIKeysRequest, opts ...grpc.CallOption) (*services.ListExternalAPIKeysResponse, error) {
	return a.service.ListExternalAPIKeys(ctx, in)
}

// Rule operations
func (a *LocalClientAdapter) CreateRule(ctx context.Context, in *services.CreateRuleRequest, opts ...grpc.CallOption) (*models.Rule, error) {
	return a.service.CreateRule(ctx, in)
}

func (a *LocalClientAdapter) GetRule(ctx context.Context, in *services.GetRuleRequest, opts ...grpc.CallOption) (*models.Rule, error) {
	return a.service.GetRule(ctx, in)
}

func (a *LocalClientAdapter) UpdateRule(ctx context.Context, in *services.UpdateRuleRequest, opts ...grpc.CallOption) (*models.Rule, error) {
	return a.service.UpdateRule(ctx, in)
}

func (a *LocalClientAdapter) DeleteRule(ctx context.Context, in *services.DeleteRuleRequest, opts ...grpc.CallOption) (*emptypb.Empty, error) {
	return a.service.DeleteRule(ctx, in)
}

func (a *LocalClientAdapter) ListRules(ctx context.Context, in *services.ListRulesRequest, opts ...grpc.CallOption) (*services.ListRulesResponse, error) {
	return a.service.ListRules(ctx, in)
}

// Rule Execution operations
func (a *LocalClientAdapter) CreateRuleExecution(ctx context.Context, in *services.CreateRuleExecutionRequest, opts ...grpc.CallOption) (*models.RuleExecution, error) {
	return a.service.CreateRuleExecution(ctx, in)
}

func (a *LocalClientAdapter) GetRuleExecution(ctx context.Context, in *services.GetRuleExecutionRequest, opts ...grpc.CallOption) (*models.RuleExecution, error) {
	return a.service.GetRuleExecution(ctx, in)
}

func (a *LocalClientAdapter) UpdateRuleExecution(ctx context.Context, in *services.UpdateRuleExecutionRequest, opts ...grpc.CallOption) (*models.RuleExecution, error) {
	return a.service.UpdateRuleExecution(ctx, in)
}

func (a *LocalClientAdapter) ListRuleExecutions(ctx context.Context, in *services.ListRuleExecutionsRequest, opts ...grpc.CallOption) (*services.ListRuleExecutionsResponse, error) {
	return a.service.ListRuleExecutions(ctx, in)
}
