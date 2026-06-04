package portfolio

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math/big"
	"strings"
	"time"

	"connectrpc.com/connect"
	apiv1 "github.com/foxcool/greedy-eye/internal/api/v1"
	"github.com/foxcool/greedy-eye/internal/api/v1/apiv1connect"
	"github.com/foxcool/greedy-eye/internal/entity"
	"github.com/foxcool/greedy-eye/internal/middleware"
	"github.com/foxcool/greedy-eye/internal/store"
	"github.com/shopspring/decimal"
	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/emptypb"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// defaultQuoteAsset is the symbol used when the caller omits quote_asset_id.
// The marketdata handler resolves it to a UUID via GetAssetBySymbol.
const defaultQuoteAsset = "USD"

// Handler implements apiv1connect.PortfolioServiceHandler.
type Handler struct {
	apiv1connect.UnimplementedPortfolioServiceHandler
	store        Store
	mdClient     MarketDataClient    // optional; nil if not configured
	walletSyncer entity.WalletSyncer // optional; nil if not configured
	log          *slog.Logger
}

func NewHandler(store Store, log *slog.Logger) *Handler {
	return &Handler{store: store, log: log}
}

// WithMarketDataClient returns a new Handler with the MarketData client injected.
func (h *Handler) WithMarketDataClient(mc MarketDataClient) *Handler {
	return &Handler{
		UnimplementedPortfolioServiceHandler: h.UnimplementedPortfolioServiceHandler,
		store:                                h.store,
		mdClient:                             mc,
		walletSyncer:                         h.walletSyncer,
		log:                                  h.log,
	}
}

// WithWalletSyncer returns a new Handler with the wallet syncer injected.
func (h *Handler) WithWalletSyncer(ws entity.WalletSyncer) *Handler {
	return &Handler{
		UnimplementedPortfolioServiceHandler: h.UnimplementedPortfolioServiceHandler,
		store:                                h.store,
		mdClient:                             h.mdClient,
		walletSyncer:                         ws,
		log:                                  h.log,
	}
}

// --- Portfolio CRUD ---

func (h *Handler) CreatePortfolio(ctx context.Context, req *connect.Request[apiv1.CreatePortfolioRequest]) (*connect.Response[apiv1.Portfolio], error) {
	if req.Msg.Portfolio == nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("portfolio is required"))
	}

	user, ok := middleware.UserFromContext(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("user not found in context"))
	}

	p := portfolioFromProto(req.Msg.Portfolio)
	p.UserID = user.ID
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

	fields := []string{"name", "description", "data"}
	if req.Msg.UpdateMask != nil && len(req.Msg.UpdateMask.Paths) > 0 {
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
	user, ok := middleware.UserFromContext(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("user not found in context"))
	}

	opts := ListPortfoliosOpts{UserID: user.ID}
	if req.Msg.UserId != nil && *req.Msg.UserId != "" {
		opts.UserID = *req.Msg.UserId // allow explicit override (admin)
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
	if h.mdClient == nil {
		return nil, connect.NewError(connect.CodeUnimplemented, errors.New("market data client not configured"))
	}
	if req.Msg.PortfolioId == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("portfolio_id is required"))
	}

	quoteAssetID := req.Msg.QuoteAssetId
	if quoteAssetID == "" {
		quoteAssetID = defaultQuoteAsset
	}

	holdings, _, err := h.store.ListHoldings(ctx, ListHoldingsOpts{
		PortfolioID:  req.Msg.PortfolioId,
		PageSize:     1000,
		HideExcluded: true,
	})
	if err != nil {
		return nil, toConnectError(err)
	}

	const resultDecimals = 2
	total := decimal.Zero

	for _, hld := range holdings {
		unit, ok, err := h.unitPrice(ctx, hld.AssetID, quoteAssetID)
		if err != nil {
			return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("get price for %s: %w", hld.AssetID, err))
		}
		if !ok {
			continue // no price path for this asset → skip
		}

		// value = (amount / 10^holding.Decimals) * unitPrice
		holdingValue := hld.Amount.Shift(-int32(hld.Decimals)).Mul(unit)
		total = total.Add(holdingValue)
	}

	// Convert to result decimals (e.g., 2 for USD cents) as a raw integer decimal string.
	resultAmount := total.Mul(decimal.New(1, int32(resultDecimals))).Round(0).String()

	return connect.NewResponse(&apiv1.PortfolioValueResponse{
		PortfolioId:      req.Msg.PortfolioId,
		QuoteAssetId:     quoteAssetID,
		TotalValueAmount: resultAmount,
		Decimals:         resultDecimals,
		CalculationTime:  timestamppb.New(time.Now()),
	}), nil
}

// unitPrice returns the per-token price of assetID expressed in quoteAssetID as a
// real-unit decimal (i.e. already divided by the price's decimals).
//
// A position is priced in whatever pair it actually trades in (USDT, RUB, BTC, …); the
// quote currency is not assumed. Resolution:
//  1. direct  — a price quoted straight in quoteAssetID
//  2. cross   — the asset's latest price in its own base B, converted B→quote
//
// ok is false when no price path exists; a non-nil error signals an unexpected failure.
func (h *Handler) unitPrice(ctx context.Context, assetID, quoteAssetID string) (decimal.Decimal, bool, error) {
	if p, ok, err := h.realPrice(ctx, assetID, quoteAssetID); err != nil || ok {
		return p, ok, err
	}

	// The asset's actual traded pair: latest price in whatever base it has.
	baseID, value, ok, err := h.latestAnyBase(ctx, assetID)
	if err != nil || !ok {
		return decimal.Zero, false, err
	}

	rate, ok, err := h.crossRate(ctx, baseID, quoteAssetID)
	if err != nil || !ok {
		return decimal.Zero, false, err
	}
	return value.Mul(rate), true, nil
}

// crossRate returns how many units of quoteID one unit of baseID is worth, using a
// direct baseID/quoteID price or, failing that, the inverse quoteID/baseID price.
func (h *Handler) crossRate(ctx context.Context, baseID, quoteID string) (decimal.Decimal, bool, error) {
	if r, ok, err := h.realPrice(ctx, baseID, quoteID); err != nil || ok {
		return r, ok, err
	}
	inv, ok, err := h.realPrice(ctx, quoteID, baseID)
	if err != nil || !ok || inv.IsZero() {
		return decimal.Zero, false, err
	}
	return decimal.NewFromInt(1).Div(inv), true, nil
}

// latestAnyBase returns the asset's most recent price regardless of base, as the base
// asset ID and the real-unit value. ok is false when no price exists.
func (h *Handler) latestAnyBase(ctx context.Context, assetID string) (string, decimal.Decimal, bool, error) {
	resp, err := h.mdClient.GetLatestPrice(ctx, connect.NewRequest(&apiv1.GetLatestPriceRequest{
		AssetId: assetID, // BaseAssetId omitted → latest in any base
	}))
	if err != nil {
		if connect.CodeOf(err) == connect.CodeNotFound {
			return "", decimal.Zero, false, nil
		}
		return "", decimal.Zero, false, err
	}
	last, err := decimal.NewFromString(resp.Msg.Last)
	if err != nil {
		h.log.Warn("skip price with unparseable last",
			"asset_id", assetID, "base_asset_id", resp.Msg.BaseAssetId, "last", resp.Msg.Last, "error", err)
		return "", decimal.Zero, false, nil
	}
	return resp.Msg.BaseAssetId, last.Shift(-int32(resp.Msg.Decimals)), true, nil
}

// realPrice returns the latest price of assetID in baseID as a real-unit decimal
// (value = last / 10^decimals). ok is false when no price exists (NotFound) or the
// stored value is unparseable; a non-nil error is returned only for unexpected failures.
func (h *Handler) realPrice(ctx context.Context, assetID, baseID string) (decimal.Decimal, bool, error) {
	resp, err := h.mdClient.GetLatestPrice(ctx, connect.NewRequest(&apiv1.GetLatestPriceRequest{
		AssetId: assetID, BaseAssetId: baseID,
	}))
	if err != nil {
		if connect.CodeOf(err) == connect.CodeNotFound {
			return decimal.Zero, false, nil
		}
		return decimal.Zero, false, err
	}
	last, err := decimal.NewFromString(resp.Msg.Last)
	if err != nil {
		h.log.Warn("skip price with unparseable last",
			"asset_id", assetID, "base_asset_id", baseID, "last", resp.Msg.Last, "error", err)
		return decimal.Zero, false, nil
	}
	return last.Shift(-int32(resp.Msg.Decimals)), true, nil
}

// GetPortfolioPerformance calculates return over a time range using stored price history.
// If no `from` is set, defaults to 30 days ago. Requires marketStore.
func (h *Handler) GetPortfolioPerformance(ctx context.Context, req *connect.Request[apiv1.GetPortfolioPerformanceRequest]) (*connect.Response[apiv1.PortfolioPerformanceResponse], error) {
	if h.mdClient == nil {
		return nil, connect.NewError(connect.CodeUnimplemented, errors.New("market data client not configured"))
	}
	if req.Msg.PortfolioId == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("portfolio_id is required"))
	}

	from := time.Now().AddDate(0, 0, -30) // default: 30 days
	if req.Msg.From != nil {
		from = req.Msg.From.AsTime()
	}

	quoteAssetID := defaultQuoteAsset
	if req.Msg.BenchmarkAssetId != "" {
		quoteAssetID = req.Msg.BenchmarkAssetId
	}

	holdings, _, err := h.store.ListHoldings(ctx, ListHoldingsOpts{PortfolioID: req.Msg.PortfolioId, PageSize: 1000})
	if err != nil {
		return nil, toConnectError(err)
	}

	var currentValue, fromValue decimal.Decimal

	for _, hld := range holdings {
		// Current price
		latestResp, err := h.mdClient.GetLatestPrice(ctx, connect.NewRequest(&apiv1.GetLatestPriceRequest{
			AssetId: hld.AssetID, BaseAssetId: quoteAssetID,
		}))
		if err != nil {
			if connect.CodeOf(err) == connect.CodeNotFound {
				continue
			}
			return nil, err
		}
		latestPrice := latestResp.Msg

		latestLast, err := decimal.NewFromString(latestPrice.Last)
		if err != nil {
			h.log.Warn("skip price with unparseable last", "asset_id", hld.AssetID, "last", latestPrice.Last, "error", err)
			continue
		}

		divisorCurrent := decimal.New(1, int32(hld.Decimals)+int32(latestPrice.Decimals))
		holdingCurrent := hld.Amount.
			Mul(latestLast).
			Div(divisorCurrent)
		currentValue = currentValue.Add(holdingCurrent)

		// Historical price (first available at or after `from`)
		pageSize := int32(1)
		histResp, err := h.mdClient.ListPriceHistory(ctx, connect.NewRequest(&apiv1.ListPriceHistoryRequest{
			AssetId: hld.AssetID, BaseAssetId: quoteAssetID,
			From:     timestamppb.New(from),
			PageSize: &pageSize,
		}))
		if err != nil {
			return nil, err
		}
		if len(histResp.Msg.Prices) == 0 {
			// No historical data — use current price as baseline (0% return for this asset)
			fromValue = fromValue.Add(holdingCurrent)
			continue
		}
		fromPrice := histResp.Msg.Prices[0]

		fromLast, err := decimal.NewFromString(fromPrice.Last)
		if err != nil {
			h.log.Warn("skip historical price with unparseable last", "asset_id", hld.AssetID, "last", fromPrice.Last, "error", err)
			fromValue = fromValue.Add(holdingCurrent)
			continue
		}

		divisorFrom := decimal.New(1, int32(hld.Decimals)+int32(fromPrice.Decimals))
		holdingFrom := hld.Amount.
			Mul(fromLast).
			Div(divisorFrom)
		fromValue = fromValue.Add(holdingFrom)
	}

	var returnPct float64
	if !fromValue.IsZero() {
		returnPct, _ = currentValue.Sub(fromValue).Div(fromValue).Mul(decimal.NewFromInt(100)).Float64()
	}

	return connect.NewResponse(&apiv1.PortfolioPerformanceResponse{
		PortfolioId:      req.Msg.PortfolioId,
		ReturnPercentage: returnPct,
		// Volatility and SharpeRatio require daily price series — not yet implemented
	}), nil
}

// --- Holding CRUD ---

func (h *Handler) CreateHolding(ctx context.Context, req *connect.Request[apiv1.CreateHoldingRequest]) (*connect.Response[apiv1.Holding], error) {
	if req.Msg.Holding == nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("holding is required"))
	}

	holding, err := holdingFromProto(req.Msg.Holding)
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
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

	fields := []string{"amount", "decimals", "portfolio_id", "excluded"}
	if req.Msg.UpdateMask != nil && len(req.Msg.UpdateMask.Paths) > 0 {
		fields = req.Msg.UpdateMask.Paths
	}

	holding, err := holdingFromProto(req.Msg.Holding)
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
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

	user, ok := middleware.UserFromContext(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("user not found in context"))
	}

	account := accountFromProto(req.Msg.Account)
	account.UserID = user.ID
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

	fields := []string{"name", "description", "type", "data", "portfolio_id"}
	if req.Msg.UpdateMask != nil && len(req.Msg.UpdateMask.Paths) > 0 {
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
	user, ok := middleware.UserFromContext(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("user not found in context"))
	}

	opts := ListAccountsOpts{UserID: user.ID}
	if req.Msg.UserId != nil && *req.Msg.UserId != "" {
		opts.UserID = *req.Msg.UserId // allow explicit override (admin)
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

// --- Account sync ---

// SyncAccount fetches external holdings for a wallet account and upserts assets+holdings.
func (h *Handler) SyncAccount(ctx context.Context, req *connect.Request[apiv1.SyncAccountRequest]) (*connect.Response[apiv1.SyncAccountResponse], error) {
	if req.Msg.AccountId == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("account_id is required"))
	}
	if h.walletSyncer == nil || h.mdClient == nil {
		return nil, connect.NewError(connect.CodeUnimplemented, errors.New("wallet sync not configured"))
	}

	account, err := h.store.GetAccount(ctx, req.Msg.AccountId)
	if err != nil {
		return nil, toConnectError(err)
	}
	if account.Type != entity.AccountTypeWallet {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("only wallet accounts can be synced"))
	}

	address, ok := account.Data["address"]
	if !ok || address == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("account.data.address is required for wallet sync"))
	}

	// Resolve which chains to sync from the account config.
	// Empty or "auto" → let the syncer auto-discover (pass nil).
	// Otherwise a comma-separated list: "eth,base,arbitrum".
	var syncErrors []string
	var chains []string
	if chainRaw := strings.TrimSpace(account.Data["chain"]); chainRaw != "" && chainRaw != "auto" {
		chains = splitChains(chainRaw)
	}

	// The syncer owns all provider mechanics (discovery, fan-out, native vs token).
	// Partial failures arrive as a joined error alongside the balances gathered so far.
	balances, err := h.walletSyncer.SyncWallet(ctx, address, chains)
	if err != nil {
		syncErrors = append(syncErrors, err.Error())
	}

	// Merge same-symbol tokens across chains. Raw balances are arbitrary-precision
	// integers (uint256 on-chain), summed as big.Int and stored as a decimal string.
	type accumulated struct {
		bal   entity.WalletBalance
		total *big.Int
	}
	bySymbol := make(map[string]*accumulated)

	for _, b := range balances {
		amt, ok := new(big.Int).SetString(strings.TrimSpace(b.Amount), 10)
		if !ok {
			syncErrors = append(syncErrors, fmt.Sprintf("parse amount %q for %s", b.Amount, b.Symbol))
			continue
		}
		if amt.Sign() == 0 {
			continue
		}
		if entry, ok := bySymbol[b.Symbol]; ok {
			entry.total.Add(entry.total, amt)
		} else {
			bySymbol[b.Symbol] = &accumulated{bal: b, total: amt}
		}
	}

	// Build symbol → asset ID map from existing assets
	pageSize := int32(1000)
	listResp, err := h.mdClient.ListAssets(ctx, connect.NewRequest(&apiv1.ListAssetsRequest{
		PageSize: &pageSize,
	}))
	if err != nil {
		return nil, err
	}
	symbolToAssetID := make(map[string]string, len(listResp.Msg.Assets))
	for _, a := range listResp.Msg.Assets {
		if a.Symbol != nil {
			symbolToAssetID[*a.Symbol] = a.Id
		}
	}

	// Build existing holdings map for this account
	existingHoldings, _, err := h.store.ListHoldings(ctx, ListHoldingsOpts{AccountID: req.Msg.AccountId, PageSize: 1000})
	if err != nil {
		return nil, toConnectError(err)
	}
	holdingByAssetID := make(map[string]*entity.Holding, len(existingHoldings))
	for _, hld := range existingHoldings {
		holdingByAssetID[hld.AssetID] = hld
	}

	defaultPortfolioID := account.PortfolioID

	var assetsUpserted, holdingsUpserted int32

	for _, entry := range bySymbol {
		bal := entry.bal
		symbol := bal.Symbol

		// holdings.amount is NUMERIC; store the full uint256 sum as a decimal integer.
		amount := decimal.NewFromBigInt(entry.total, 0)

		// Ensure asset exists
		assetID, exists := symbolToAssetID[symbol]
		if !exists {
			// Store contract address as a tag so price providers can look up by address.
			var tags []string
			if bal.ContractAddress != "" {
				tags = append(tags, "contract:"+bal.ContractAddress)
			}
			createResp, err := h.mdClient.CreateAsset(ctx, connect.NewRequest(&apiv1.CreateAssetRequest{
				Asset: &apiv1.Asset{
					Name:   bal.Name,
					Symbol: &symbol,
					Type:   apiv1.AssetType_ASSET_TYPE_CRYPTOCURRENCY,
					Tags:   tags,
				},
			}))
			if err != nil {
				syncErrors = append(syncErrors, fmt.Sprintf("create asset %s: %v", symbol, err))
				continue
			}
			assetID = createResp.Msg.Id
			symbolToAssetID[symbol] = assetID
			assetsUpserted++
		}

		if existing, ok := holdingByAssetID[assetID]; ok {
			// Update existing holding: only refresh amount/decimals; never touch portfolio assignment
			existing.Amount = amount
			existing.Decimals = uint32(bal.Decimals)
			if _, err := h.store.UpdateHolding(ctx, existing, []string{"amount", "decimals"}); err != nil {
				syncErrors = append(syncErrors, fmt.Sprintf("update holding %s: %v", symbol, err))
				continue
			}
		} else {
			// Create new holding; inherit account's default portfolio if configured
			_, err := h.store.CreateHolding(ctx, &entity.Holding{
				AssetID:     assetID,
				AccountID:   req.Msg.AccountId,
				PortfolioID: defaultPortfolioID,
				Amount:      amount,
				Decimals:    uint32(bal.Decimals),
			})
			if err != nil {
				syncErrors = append(syncErrors, fmt.Sprintf("create holding %s: %v", symbol, err))
				continue
			}
		}
		holdingsUpserted++
	}

	// Fetch fresh prices for all synced assets so CalculatePortfolioValue returns current values.
	if assetsUpserted > 0 || holdingsUpserted > 0 {
		if _, err := h.mdClient.FetchExternalPrices(ctx, connect.NewRequest(&apiv1.FetchExternalPricesRequest{})); err != nil {
			h.log.Warn("fetch prices after sync failed", "error", err)
		}
	}

	return connect.NewResponse(&apiv1.SyncAccountResponse{
		AccountId:        req.Msg.AccountId,
		AssetsUpserted:   assetsUpserted,
		HoldingsUpserted: holdingsUpserted,
		Errors:           syncErrors,
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

	fields := []string{"status", "data"}
	if req.Msg.UpdateMask != nil && len(req.Msg.UpdateMask.Paths) > 0 {
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

func holdingFromProto(h *apiv1.Holding) (*entity.Holding, error) {
	// Empty amount is treated as unset (zero) so partial updates that omit it still work;
	// a non-empty but malformed amount is rejected rather than silently coerced to zero.
	amount := decimal.Zero
	if h.Amount != "" {
		var err error
		amount, err = decimal.NewFromString(h.Amount)
		if err != nil {
			return nil, fmt.Errorf("invalid amount %q: %w", h.Amount, err)
		}
	}
	result := &entity.Holding{
		ID:        h.Id,
		Amount:    amount,
		Decimals:  h.Decimals,
		AssetID:   h.AssetId,
		AccountID: h.AccountId,
		Excluded:  h.Excluded,
	}
	if h.PortfolioId != nil {
		result.PortfolioID = *h.PortfolioId
	}
	return result, nil
}

func holdingToProto(h *entity.Holding) *apiv1.Holding {
	result := &apiv1.Holding{
		Id:        h.ID,
		Amount:    h.Amount.String(),
		Decimals:  h.Decimals,
		AssetId:   h.AssetID,
		AccountId: h.AccountID,
		Excluded:  h.Excluded,
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
	if a.PortfolioId != nil {
		result.PortfolioID = *a.PortfolioId
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
	if a.PortfolioID != "" {
		result.PortfolioId = &a.PortfolioID
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

// splitChains splits a comma-separated chain string, defaulting to ["eth"] when empty.
func splitChains(raw string) []string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return []string{"eth"}
	}
	parts := strings.FieldsFunc(raw, func(r rune) bool { return r == ',' || r == ' ' })
	if len(parts) == 0 {
		return []string{"eth"}
	}
	result := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			result = append(result, p)
		}
	}
	return result
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
