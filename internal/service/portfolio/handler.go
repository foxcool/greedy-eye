package portfolio

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"

	"connectrpc.com/connect"
	apiv1 "github.com/foxcool/greedy-eye/internal/api/v1"
	"github.com/foxcool/greedy-eye/internal/api/v1/apiv1connect"
	"github.com/foxcool/greedy-eye/internal/entity"
	"github.com/foxcool/greedy-eye/internal/store"
	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/emptypb"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// Handler implements apiv1connect.PortfolioServiceHandler.
type Handler struct {
	apiv1connect.UnimplementedPortfolioServiceHandler
	store Store
	log   *slog.Logger
}

func NewHandler(store Store, log *slog.Logger) *Handler {
	return &Handler{store: store, log: log}
}

// --- Portfolio CRUD ---

func (h *Handler) CreatePortfolio(ctx context.Context, req *connect.Request[apiv1.CreatePortfolioRequest]) (*connect.Response[apiv1.Portfolio], error) {
	if req.Msg.Portfolio == nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("portfolio is required"))
	}

	p := portfolioFromProto(req.Msg.Portfolio)
	created, err := h.store.CreatePortfolio(ctx, p)
	if err != nil {
		return nil, toConnectError(err)
	}

	return connect.NewResponse(portfolioToProto(created)), nil
}

func (h *Handler) GetPortfolio(ctx context.Context, req *connect.Request[apiv1.GetPortfolioRequest]) (*connect.Response[apiv1.Portfolio], error) {
	if req.Msg.Id == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("portfolio ID is required"))
	}

	p, err := h.store.GetPortfolio(ctx, req.Msg.Id)
	if err != nil {
		return nil, toConnectError(err)
	}

	return connect.NewResponse(portfolioToProto(p)), nil
}

func (h *Handler) UpdatePortfolio(ctx context.Context, req *connect.Request[apiv1.UpdatePortfolioRequest]) (*connect.Response[apiv1.Portfolio], error) {
	if req.Msg.Portfolio == nil || req.Msg.Portfolio.Id == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("portfolio with ID is required"))
	}

	var fields []string
	if req.Msg.UpdateMask != nil {
		fields = req.Msg.UpdateMask.Paths
	}

	p := portfolioFromProto(req.Msg.Portfolio)
	updated, err := h.store.UpdatePortfolio(ctx, p, fields)
	if err != nil {
		return nil, toConnectError(err)
	}

	return connect.NewResponse(portfolioToProto(updated)), nil
}

func (h *Handler) DeletePortfolio(ctx context.Context, req *connect.Request[apiv1.DeletePortfolioRequest]) (*connect.Response[emptypb.Empty], error) {
	if req.Msg.Id == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("portfolio ID is required"))
	}

	if err := h.store.DeletePortfolio(ctx, req.Msg.Id); err != nil {
		return nil, toConnectError(err)
	}

	return connect.NewResponse(&emptypb.Empty{}), nil
}

func (h *Handler) ListPortfolios(ctx context.Context, req *connect.Request[apiv1.ListPortfoliosRequest]) (*connect.Response[apiv1.ListPortfoliosResponse], error) {
	opts := ListPortfoliosOpts{}
	if req.Msg.UserId != nil {
		opts.UserID = *req.Msg.UserId
	}
	if req.Msg.PageSize != nil {
		opts.PageSize = int(*req.Msg.PageSize)
	}
	if req.Msg.PageToken != nil {
		opts.PageToken = *req.Msg.PageToken
	}

	portfolios, nextPageToken, err := h.store.ListPortfolios(ctx, opts)
	if err != nil {
		return nil, toConnectError(err)
	}

	protoPortfolios := make([]*apiv1.Portfolio, 0, len(portfolios))
	for _, p := range portfolios {
		protoPortfolios = append(protoPortfolios, portfolioToProto(p))
	}

	return connect.NewResponse(&apiv1.ListPortfoliosResponse{
		Portfolios:    protoPortfolios,
		NextPageToken: nextPageToken,
	}), nil
}

// --- Portfolio business logic (stubs) ---

func (h *Handler) CalculatePortfolioValue(ctx context.Context, req *connect.Request[apiv1.CalculatePortfolioValueRequest]) (*connect.Response[apiv1.PortfolioValueResponse], error) {
	// TODO: Implement with MarketDataStore for prices
	return nil, connect.NewError(connect.CodeUnimplemented, errors.New("CalculatePortfolioValue not implemented"))
}

func (h *Handler) GetPortfolioPerformance(ctx context.Context, req *connect.Request[apiv1.GetPortfolioPerformanceRequest]) (*connect.Response[apiv1.PortfolioPerformanceResponse], error) {
	// TODO: Implement
	return nil, connect.NewError(connect.CodeUnimplemented, errors.New("GetPortfolioPerformance not implemented"))
}

// --- Holding CRUD ---

func (h *Handler) CreateHolding(ctx context.Context, req *connect.Request[apiv1.CreateHoldingRequest]) (*connect.Response[apiv1.Holding], error) {
	if req.Msg.Holding == nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("holding is required"))
	}

	holding := holdingFromProto(req.Msg.Holding)
	created, err := h.store.CreateHolding(ctx, holding)
	if err != nil {
		return nil, toConnectError(err)
	}

	return connect.NewResponse(holdingToProto(created)), nil
}

func (h *Handler) GetHolding(ctx context.Context, req *connect.Request[apiv1.GetHoldingRequest]) (*connect.Response[apiv1.Holding], error) {
	if req.Msg.Id == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("holding ID is required"))
	}

	holding, err := h.store.GetHolding(ctx, req.Msg.Id)
	if err != nil {
		return nil, toConnectError(err)
	}

	return connect.NewResponse(holdingToProto(holding)), nil
}

func (h *Handler) UpdateHolding(ctx context.Context, req *connect.Request[apiv1.UpdateHoldingRequest]) (*connect.Response[apiv1.Holding], error) {
	if req.Msg.Holding == nil || req.Msg.Holding.Id == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("holding with ID is required"))
	}

	var fields []string
	if req.Msg.UpdateMask != nil {
		fields = req.Msg.UpdateMask.Paths
	}

	holding := holdingFromProto(req.Msg.Holding)
	updated, err := h.store.UpdateHolding(ctx, holding, fields)
	if err != nil {
		return nil, toConnectError(err)
	}

	return connect.NewResponse(holdingToProto(updated)), nil
}

func (h *Handler) ListHoldings(ctx context.Context, req *connect.Request[apiv1.ListHoldingsRequest]) (*connect.Response[apiv1.ListHoldingsResponse], error) {
	opts := ListHoldingsOpts{}
	if req.Msg.PortfolioId != nil {
		opts.PortfolioID = *req.Msg.PortfolioId
	}
	if req.Msg.AccountId != nil {
		opts.AccountID = *req.Msg.AccountId
	}
	if req.Msg.AssetId != nil {
		opts.AssetID = *req.Msg.AssetId
	}
	if req.Msg.PageSize != nil {
		opts.PageSize = int(*req.Msg.PageSize)
	}
	if req.Msg.PageToken != nil {
		opts.PageToken = *req.Msg.PageToken
	}

	holdings, nextPageToken, err := h.store.ListHoldings(ctx, opts)
	if err != nil {
		return nil, toConnectError(err)
	}

	protoHoldings := make([]*apiv1.Holding, 0, len(holdings))
	for _, h := range holdings {
		protoHoldings = append(protoHoldings, holdingToProto(h))
	}

	return connect.NewResponse(&apiv1.ListHoldingsResponse{
		Holdings:      protoHoldings,
		NextPageToken: nextPageToken,
	}), nil
}

// --- Account CRUD ---

func (h *Handler) CreateAccount(ctx context.Context, req *connect.Request[apiv1.CreateAccountRequest]) (*connect.Response[apiv1.Account], error) {
	if req.Msg.Account == nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("account is required"))
	}

	account := accountFromProto(req.Msg.Account)
	created, err := h.store.CreateAccount(ctx, account)
	if err != nil {
		return nil, toConnectError(err)
	}

	return connect.NewResponse(accountToProto(created)), nil
}

func (h *Handler) GetAccount(ctx context.Context, req *connect.Request[apiv1.GetAccountRequest]) (*connect.Response[apiv1.Account], error) {
	if req.Msg.Id == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("account ID is required"))
	}

	account, err := h.store.GetAccount(ctx, req.Msg.Id)
	if err != nil {
		return nil, toConnectError(err)
	}

	return connect.NewResponse(accountToProto(account)), nil
}

func (h *Handler) UpdateAccount(ctx context.Context, req *connect.Request[apiv1.UpdateAccountRequest]) (*connect.Response[apiv1.Account], error) {
	if req.Msg.Account == nil || req.Msg.Account.Id == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("account with ID is required"))
	}

	var fields []string
	if req.Msg.UpdateMask != nil {
		fields = req.Msg.UpdateMask.Paths
	}

	account := accountFromProto(req.Msg.Account)
	updated, err := h.store.UpdateAccount(ctx, account, fields)
	if err != nil {
		return nil, toConnectError(err)
	}

	return connect.NewResponse(accountToProto(updated)), nil
}

func (h *Handler) DeleteAccount(ctx context.Context, req *connect.Request[apiv1.DeleteAccountRequest]) (*connect.Response[emptypb.Empty], error) {
	if req.Msg.Id == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("account ID is required"))
	}

	if err := h.store.DeleteAccount(ctx, req.Msg.Id); err != nil {
		return nil, toConnectError(err)
	}

	return connect.NewResponse(&emptypb.Empty{}), nil
}

func (h *Handler) ListAccounts(ctx context.Context, req *connect.Request[apiv1.ListAccountsRequest]) (*connect.Response[apiv1.ListAccountsResponse], error) {
	opts := ListAccountsOpts{}
	if req.Msg.UserId != nil {
		opts.UserID = *req.Msg.UserId
	}
	if req.Msg.Type != nil {
		opts.Type = entity.AccountType(*req.Msg.Type)
	}
	if req.Msg.PageSize != nil {
		opts.PageSize = int(*req.Msg.PageSize)
	}
	if req.Msg.PageToken != nil {
		opts.PageToken = *req.Msg.PageToken
	}

	accounts, nextPageToken, err := h.store.ListAccounts(ctx, opts)
	if err != nil {
		return nil, toConnectError(err)
	}

	protoAccounts := make([]*apiv1.Account, 0, len(accounts))
	for _, a := range accounts {
		protoAccounts = append(protoAccounts, accountToProto(a))
	}

	return connect.NewResponse(&apiv1.ListAccountsResponse{
		Accounts:      protoAccounts,
		NextPageToken: nextPageToken,
	}), nil
}

// --- Transaction CRUD ---

func (h *Handler) CreateTransaction(ctx context.Context, req *connect.Request[apiv1.CreateTransactionRequest]) (*connect.Response[apiv1.Transaction], error) {
	if req.Msg.Transaction == nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("transaction is required"))
	}

	tx := transactionFromProto(req.Msg.Transaction)
	created, err := h.store.CreateTransaction(ctx, tx)
	if err != nil {
		return nil, toConnectError(err)
	}

	return connect.NewResponse(transactionToProto(created)), nil
}

func (h *Handler) GetTransaction(ctx context.Context, req *connect.Request[apiv1.GetTransactionRequest]) (*connect.Response[apiv1.Transaction], error) {
	if req.Msg.Id == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("transaction ID is required"))
	}

	tx, err := h.store.GetTransaction(ctx, req.Msg.Id)
	if err != nil {
		return nil, toConnectError(err)
	}

	return connect.NewResponse(transactionToProto(tx)), nil
}

func (h *Handler) UpdateTransaction(ctx context.Context, req *connect.Request[apiv1.UpdateTransactionRequest]) (*connect.Response[apiv1.Transaction], error) {
	if req.Msg.Transaction == nil || req.Msg.Transaction.Id == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("transaction with ID is required"))
	}

	var fields []string
	if req.Msg.UpdateMask != nil {
		fields = req.Msg.UpdateMask.Paths
	}

	tx := transactionFromProto(req.Msg.Transaction)
	updated, err := h.store.UpdateTransaction(ctx, tx, fields)
	if err != nil {
		return nil, toConnectError(err)
	}

	return connect.NewResponse(transactionToProto(updated)), nil
}

func (h *Handler) ListTransactions(ctx context.Context, req *connect.Request[apiv1.ListTransactionsRequest]) (*connect.Response[apiv1.ListTransactionsResponse], error) {
	opts := ListTransactionsOpts{}
	if req.Msg.AccountId != nil {
		opts.AccountID = *req.Msg.AccountId
	}
	if req.Msg.Type != nil {
		opts.Type = entity.TransactionType(*req.Msg.Type)
	}
	if req.Msg.Status != nil {
		opts.Status = entity.TransactionStatus(*req.Msg.Status)
	}
	if req.Msg.PageSize != nil {
		opts.PageSize = int(*req.Msg.PageSize)
	}
	if req.Msg.PageToken != nil {
		opts.PageToken = *req.Msg.PageToken
	}

	transactions, nextPageToken, err := h.store.ListTransactions(ctx, opts)
	if err != nil {
		return nil, toConnectError(err)
	}

	protoTransactions := make([]*apiv1.Transaction, 0, len(transactions))
	for _, t := range transactions {
		protoTransactions = append(protoTransactions, transactionToProto(t))
	}

	return connect.NewResponse(&apiv1.ListTransactionsResponse{
		Transactions:  protoTransactions,
		NextPageToken: nextPageToken,
	}), nil
}

// --- Converters ---

func toConnectError(err error) error {
	if errors.Is(err, store.ErrNotFound) {
		return connect.NewError(connect.CodeNotFound, err)
	}
	if errors.Is(err, store.ErrInvalidArgument) {
		return connect.NewError(connect.CodeInvalidArgument, err)
	}
	if errors.Is(err, store.ErrConstraint) {
		return connect.NewError(connect.CodeFailedPrecondition, err)
	}
	return connect.NewError(connect.CodeInternal, err)
}

func portfolioFromProto(p *apiv1.Portfolio) *entity.Portfolio {
	result := &entity.Portfolio{
		ID:     p.Id,
		UserID: p.UserId,
		Name:   p.Name,
	}
	if p.Description != nil {
		result.Description = *p.Description
	}
	if p.Data != nil {
		result.Data = make(map[string]json.RawMessage)
		for k, v := range p.Data {
			if v != nil {
				result.Data[k] = v.Value
			}
		}
	}
	return result
}

func portfolioToProto(p *entity.Portfolio) *apiv1.Portfolio {
	result := &apiv1.Portfolio{
		Id:        p.ID,
		UserId:    p.UserID,
		Name:      p.Name,
		CreatedAt: timestamppb.New(p.CreatedAt),
		UpdatedAt: timestamppb.New(p.UpdatedAt),
	}
	if p.Description != "" {
		result.Description = &p.Description
	}
	if p.Data != nil {
		result.Data = make(map[string]*anypb.Any)
		for k, v := range p.Data {
			result.Data[k] = &anypb.Any{Value: v}
		}
	}
	return result
}

func holdingFromProto(h *apiv1.Holding) *entity.Holding {
	result := &entity.Holding{
		ID:        h.Id,
		Amount:    h.Amount,
		Decimals:  h.Decimals,
		AssetID:   h.AssetId,
		AccountID: h.AccountId,
	}
	if h.PortfolioId != nil {
		result.PortfolioID = *h.PortfolioId
	}
	return result
}

func holdingToProto(h *entity.Holding) *apiv1.Holding {
	result := &apiv1.Holding{
		Id:        h.ID,
		Amount:    h.Amount,
		Decimals:  h.Decimals,
		AssetId:   h.AssetID,
		AccountId: h.AccountID,
		CreatedAt: timestamppb.New(h.CreatedAt),
		UpdatedAt: timestamppb.New(h.UpdatedAt),
	}
	if h.PortfolioID != "" {
		result.PortfolioId = &h.PortfolioID
	}
	return result
}

func accountFromProto(a *apiv1.Account) *entity.Account {
	result := &entity.Account{
		ID:     a.Id,
		UserID: a.UserId,
		Name:   a.Name,
		Type:   entity.AccountType(a.Type),
		Data:   a.Data,
	}
	if a.Description != nil {
		result.Description = *a.Description
	}
	return result
}

func accountToProto(a *entity.Account) *apiv1.Account {
	result := &apiv1.Account{
		Id:        a.ID,
		UserId:    a.UserID,
		Name:      a.Name,
		Type:      apiv1.AccountType(a.Type),
		Data:      a.Data,
		CreatedAt: timestamppb.New(a.CreatedAt),
		UpdatedAt: timestamppb.New(a.UpdatedAt),
	}
	if a.Description != "" {
		result.Description = &a.Description
	}
	return result
}

func transactionFromProto(t *apiv1.Transaction) *entity.Transaction {
	return &entity.Transaction{
		ID:        t.Id,
		Type:      entity.TransactionType(t.Type),
		Status:    entity.TransactionStatus(t.Status),
		AccountID: t.AccountId,
		Data:      t.Data,
	}
}

func transactionToProto(t *entity.Transaction) *apiv1.Transaction {
	return &apiv1.Transaction{
		Id:        t.ID,
		Type:      apiv1.TransactionType(t.Type),
		Status:    apiv1.TransactionStatus(t.Status),
		AccountId: t.AccountID,
		Data:      t.Data,
		CreatedAt: timestamppb.New(t.CreatedAt),
		UpdatedAt: timestamppb.New(t.UpdatedAt),
	}
}
