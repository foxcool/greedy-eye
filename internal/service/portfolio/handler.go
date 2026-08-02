package portfolio

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math/big"
	"slices"
	"strings"
	"time"

	"connectrpc.com/connect"
	apiv1 "github.com/foxcool/greedy-eye/api/v1"
	"github.com/foxcool/greedy-eye/api/v1/apiv1connect"
	"github.com/foxcool/greedy-eye/internal/entity"
	"github.com/foxcool/greedy-eye/internal/middleware"
	"github.com/foxcool/greedy-eye/internal/scamfilter"
	"github.com/foxcool/greedy-eye/internal/store"
	"github.com/shopspring/decimal"
	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/emptypb"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// defaultQuoteAsset is the symbol used when the caller omits quote_asset_id.
// The marketdata handler resolves it to a UUID via GetAssetBySymbol.
const defaultQuoteAsset = "USD"

// WalletSyncerSource resolves a wallet syncer from stored account credentials
// for a given user, able to sync the requested chains (see
// internal/service/credentials). An empty chains list means auto-discovery,
// which is routed by the address's shape.
type WalletSyncerSource interface {
	WalletSyncerFor(ctx context.Context, userID, address string, chains []string) (entity.WalletSyncer, error)
}

// ExchangeSyncerSource builds an exchange syncer from a specific account's own
// stored credentials (see internal/service/credentials).
type ExchangeSyncerSource interface {
	ExchangeSyncerForAccount(a *entity.Account) (entity.ExchangeSyncer, error)
}

// Handler implements apiv1connect.PortfolioServiceHandler.
type Handler struct {
	apiv1connect.UnimplementedPortfolioServiceHandler
	store          Store
	mdClient       MarketDataClient     // optional; nil if not configured
	walletSyncer   entity.WalletSyncer  // optional; nil if not configured
	syncerSource   WalletSyncerSource   // optional; takes precedence over walletSyncer
	exchangeSource ExchangeSyncerSource // optional; resolves per-account exchange syncers
	log            *slog.Logger
}

func NewHandler(store Store, log *slog.Logger) *Handler {
	return &Handler{store: store, log: log}
}

func (h *Handler) clone() *Handler {
	copied := *h
	return &copied
}

// WithMarketDataClient returns a new Handler with the MarketData client injected.
func (h *Handler) WithMarketDataClient(mc MarketDataClient) *Handler {
	copied := h.clone()
	copied.mdClient = mc
	return copied
}

// WithWalletSyncer returns a new Handler with the wallet syncer injected.
func (h *Handler) WithWalletSyncer(ws entity.WalletSyncer) *Handler {
	copied := h.clone()
	copied.walletSyncer = ws
	return copied
}

// WithWalletSyncerSource returns a new Handler resolving wallet syncers from
// stored account credentials, with walletSyncer as the fallback.
func (h *Handler) WithWalletSyncerSource(src WalletSyncerSource) *Handler {
	copied := h.clone()
	copied.syncerSource = src
	return copied
}

// WithExchangeSyncerSource returns a new Handler resolving exchange syncers from
// stored account credentials.
func (h *Handler) WithExchangeSyncerSource(src ExchangeSyncerSource) *Handler {
	copied := h.clone()
	copied.exchangeSource = src
	return copied
}

// --- Portfolio CRUD ---

// ownedPortfolio loads a portfolio and enforces ownership.
func (h *Handler) ownedPortfolio(ctx context.Context, id string) (*entity.Portfolio, error) {
	p, err := h.store.GetPortfolio(ctx, id)
	if err != nil {
		return nil, toConnectError(err)
	}
	if err := middleware.EnsureOwner(ctx, p.UserID); err != nil {
		return nil, err
	}
	return p, nil
}

// ownedAccount loads an account and enforces ownership.
func (h *Handler) ownedAccount(ctx context.Context, id string) (*entity.Account, error) {
	a, err := h.store.GetAccount(ctx, id)
	if err != nil {
		return nil, toConnectError(err)
	}
	if err := middleware.EnsureOwner(ctx, a.UserID); err != nil {
		return nil, err
	}
	return a, nil
}

// ownedHolding loads a holding and enforces ownership via its account.
func (h *Handler) ownedHolding(ctx context.Context, id string) (*entity.Holding, error) {
	hld, err := h.store.GetHolding(ctx, id)
	if err != nil {
		return nil, toConnectError(err)
	}
	if _, err := h.ownedAccount(ctx, hld.AccountID); err != nil {
		return nil, err
	}
	return hld, nil
}

// ownedTransaction loads a transaction and enforces ownership via its account.
func (h *Handler) ownedTransaction(ctx context.Context, id string) (*entity.Transaction, error) {
	t, err := h.store.GetTransaction(ctx, id)
	if err != nil {
		return nil, toConnectError(err)
	}
	if _, err := h.ownedAccount(ctx, t.AccountID); err != nil {
		return nil, err
	}
	return t, nil
}

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

	p, err := h.ownedPortfolio(ctx, req.Msg.Id)
	if err != nil {
		return nil, err
	}

	return connect.NewResponse(portfolioToProto(p)), nil
}

func (h *Handler) UpdatePortfolio(ctx context.Context, req *connect.Request[apiv1.UpdatePortfolioRequest]) (*connect.Response[apiv1.Portfolio], error) {
	if req.Msg.Portfolio == nil || req.Msg.Portfolio.Id == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("portfolio with ID is required"))
	}

	if _, err := h.ownedPortfolio(ctx, req.Msg.Portfolio.Id); err != nil {
		return nil, err
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

	if _, err := h.ownedPortfolio(ctx, req.Msg.Id); err != nil {
		return nil, err
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
	if req.Msg.UserId != nil && *req.Msg.UserId != "" && user.IsAdmin() {
		opts.UserID = *req.Msg.UserId // explicit override is admin-only
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

	if _, err := h.ownedPortfolio(ctx, req.Msg.PortfolioId); err != nil {
		return nil, err
	}

	quoteAssetID := req.Msg.QuoteAssetId
	if quoteAssetID == "" {
		quoteAssetID = defaultQuoteAsset
	}

	// Fetch all holdings (excluded included) and partition in code so the total
	// can exclude the quarantined ones while still disclosing them — a silently
	// shrunk total looks like a real outflow.
	holdings, _, err := h.store.ListHoldings(ctx, ListHoldingsOpts{
		PortfolioID: req.Msg.PortfolioId,
		PageSize:    1000,
	})
	if err != nil {
		return nil, toConnectError(err)
	}

	const resultDecimals = 2
	total := decimal.Zero
	excludedTotal := decimal.Zero
	var excludedCount uint32
	coverage := &apiv1.ValuationCoverage{}
	var unpriced []*entity.Holding

	for _, hld := range holdings {
		unit, ok, err := h.unitPrice(ctx, hld.AssetID, quoteAssetID)
		if err != nil {
			return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("get price for %s: %w", hld.AssetID, err))
		}
		if hld.Excluded {
			// Count every quarantined holding; add its value only when priced.
			// Quarantine is its own axis: an excluded holding is out of the total
			// by decision, not for lack of a quote, so it stays out of coverage.
			excludedCount++
			if ok {
				excludedTotal = excludedTotal.Add(hld.Amount.Shift(-decI32(hld.Decimals)).Mul(unit))
			}
			continue
		}
		if !ok {
			// No price path: report the holding instead of letting it contribute
			// zero. Zero is an assertion about the market; this is missing data.
			coverage.UnpricedCount++
			unpriced = append(unpriced, hld)
			continue
		}
		coverage.PricedCount++

		// value = (amount / 10^holding.Decimals) * unitPrice
		holdingValue := hld.Amount.Shift(-decI32(hld.Decimals)).Mul(unit)
		total = total.Add(holdingValue)
	}

	coverage.Unpriced, coverage.UnpricedTruncated = h.describeUnpriced(ctx, unpriced)

	// Convert to result decimals (e.g., 2 for USD cents) as a raw integer decimal string.
	scale := decimal.New(1, int32(resultDecimals))
	resultAmount := total.Mul(scale).Round(0).String()
	excludedAmount := excludedTotal.Mul(scale).Round(0).String()

	return connect.NewResponse(&apiv1.PortfolioValueResponse{
		PortfolioId:         req.Msg.PortfolioId,
		QuoteAssetId:        quoteAssetID,
		TotalValueAmount:    resultAmount,
		Decimals:            resultDecimals,
		CalculationTime:     timestamppb.New(time.Now()),
		ExcludedCount:       excludedCount,
		ExcludedValueAmount: excludedAmount,
		Coverage:            coverage,
	}), nil
}

// maxUnpricedDisclosed caps the per-holding detail in a coverage block: the
// count is always exact, the list is a sample bounded by read size.
const maxUnpricedDisclosed = 50

// describeUnpriced labels unpriced holdings for disclosure, up to the cap, and
// reports whether the list was truncated.
//
// A failed asset lookup degrades to an empty symbol rather than failing the
// valuation: the asset_id still identifies the position, and losing the whole
// total over a display label would be a worse trade than a missing name.
func (h *Handler) describeUnpriced(ctx context.Context, holdings []*entity.Holding) ([]*apiv1.UnpricedHolding, bool) {
	if len(holdings) == 0 {
		return nil, false
	}
	listed := holdings
	truncated := false
	if len(listed) > maxUnpricedDisclosed {
		listed = listed[:maxUnpricedDisclosed]
		truncated = true
	}

	out := make([]*apiv1.UnpricedHolding, 0, len(listed))
	for _, hld := range listed {
		item := &apiv1.UnpricedHolding{HoldingId: hld.ID, AssetId: hld.AssetID}
		resp, err := h.mdClient.GetAsset(ctx, connect.NewRequest(&apiv1.GetAssetRequest{Id: hld.AssetID}))
		if err != nil {
			h.log.WarnContext(ctx, "unpriced holding: asset lookup failed, reporting without a symbol",
				"asset_id", hld.AssetID, "holding_id", hld.ID, "error", err)
		} else {
			item.Symbol = resp.Msg.GetSymbol()
		}
		out = append(out, item)
	}
	return out, truncated
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
	return resp.Msg.BaseAssetId, last.Shift(-decI32(resp.Msg.Decimals)), true, nil
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
	return last.Shift(-decI32(resp.Msg.Decimals)), true, nil
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

	if _, err := h.ownedPortfolio(ctx, req.Msg.PortfolioId); err != nil {
		return nil, err
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

		divisorCurrent := decimal.New(1, decI32(hld.Decimals)+decI32(latestPrice.Decimals))
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

		divisorFrom := decimal.New(1, decI32(hld.Decimals)+decI32(fromPrice.Decimals))
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
	if holding.Amount.IsNegative() {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("amount must not be negative"))
	}
	// Provenance is server-stamped: everything created through this RPC is manual.
	holding.Source = entity.SourceManual
	// The target account (and portfolio, when set) must belong to the caller.
	if _, err := h.ownedAccount(ctx, holding.AccountID); err != nil {
		return nil, err
	}
	if holding.PortfolioID != "" {
		if _, err := h.ownedPortfolio(ctx, holding.PortfolioID); err != nil {
			return nil, err
		}
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

	holding, err := h.ownedHolding(ctx, req.Msg.Id)
	if err != nil {
		return nil, err
	}

	return connect.NewResponse(holdingToProto(holding)), nil
}

func (h *Handler) UpdateHolding(ctx context.Context, req *connect.Request[apiv1.UpdateHoldingRequest]) (*connect.Response[apiv1.Holding], error) {
	if req.Msg.Holding == nil || req.Msg.Holding.Id == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("holding with ID is required"))
	}

	if _, err := h.ownedHolding(ctx, req.Msg.Holding.Id); err != nil {
		return nil, err
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

func (h *Handler) DeleteHolding(ctx context.Context, req *connect.Request[apiv1.DeleteHoldingRequest]) (*connect.Response[emptypb.Empty], error) {
	if req.Msg.Id == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("holding ID is required"))
	}

	if _, err := h.ownedHolding(ctx, req.Msg.Id); err != nil {
		return nil, err
	}

	if err := h.store.DeleteHolding(ctx, req.Msg.Id); err != nil {
		return nil, toConnectError(err)
	}

	return connect.NewResponse(&emptypb.Empty{}), nil
}

func (h *Handler) ListHoldings(ctx context.Context, req *connect.Request[apiv1.ListHoldingsRequest]) (*connect.Response[apiv1.ListHoldingsResponse], error) {
	user, ok := middleware.UserFromContext(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("user not found in context"))
	}

	opts := ListHoldingsOpts{UserID: user.ID}
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
	for k, v := range account.Data {
		if strings.HasPrefix(v, maskPrefix) {
			return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("data key %q holds a masked value; send the real secret", k))
		}
	}
	if len(account.SystemScopes) > 0 && !user.IsAdmin() {
		return nil, connect.NewError(connect.CodePermissionDenied, errors.New("only admins may set system scopes"))
	}
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

	account, err := h.ownedAccount(ctx, req.Msg.Id)
	if err != nil {
		return nil, err
	}

	return connect.NewResponse(accountToProto(account)), nil
}

func (h *Handler) UpdateAccount(ctx context.Context, req *connect.Request[apiv1.UpdateAccountRequest]) (*connect.Response[apiv1.Account], error) {
	if req.Msg.Account == nil || req.Msg.Account.Id == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("account with ID is required"))
	}

	existing, err := h.ownedAccount(ctx, req.Msg.Account.Id)
	if err != nil {
		return nil, err
	}

	// system_scopes is deliberately absent from the defaults: it is only
	// updated when explicitly named in the update_mask, and only by admins.
	fields := []string{"name", "description", "type", "data", "portfolio_id", "capabilities"}
	if req.Msg.UpdateMask != nil && len(req.Msg.UpdateMask.Paths) > 0 {
		fields = req.Msg.UpdateMask.Paths
	}

	if slices.Contains(fields, "system_scopes") {
		user, ok := middleware.UserFromContext(ctx)
		if !ok {
			return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("user not found in context"))
		}
		if !user.IsAdmin() {
			return nil, connect.NewError(connect.CodePermissionDenied, errors.New("only admins may change system scopes"))
		}
	}

	account := accountFromProto(req.Msg.Account)
	if slices.Contains(fields, "portfolio_id") && account.PortfolioID != "" && account.PortfolioID != existing.PortfolioID {
		if _, err := h.ownedPortfolio(ctx, account.PortfolioID); err != nil {
			return nil, err
		}
	}
	if slices.Contains(fields, "data") {
		if err := restoreMaskedSecrets(account, existing); err != nil {
			return nil, err
		}
	}
	updated, err := h.store.UpdateAccount(ctx, account, fields)
	if err != nil {
		return nil, toConnectError(err)
	}

	return connect.NewResponse(accountToProto(updated)), nil
}

// restoreMaskedSecrets implements write-only secret semantics on update:
// masked incoming values are replaced with the currently stored ones, so
// clients can echo back the masked data map without wiping credentials.
func restoreMaskedSecrets(account, existing *entity.Account) error {
	for k, v := range account.Data {
		if !strings.HasPrefix(v, maskPrefix) {
			continue
		}
		stored, ok := existing.Data[k]
		if !ok {
			return connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("data key %q holds a masked value but no stored secret exists", k))
		}
		account.Data[k] = stored
	}
	return nil
}

func (h *Handler) DeleteAccount(ctx context.Context, req *connect.Request[apiv1.DeleteAccountRequest]) (*connect.Response[emptypb.Empty], error) {
	if req.Msg.Id == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("account ID is required"))
	}

	if _, err := h.ownedAccount(ctx, req.Msg.Id); err != nil {
		return nil, err
	}

	del := h.store.DeleteAccount
	if req.Msg.Cascade {
		del = h.store.DeleteAccountWithHoldings
	}

	if err := del(ctx, req.Msg.Id); err != nil {
		// Holdings and transactions reference the account, so deleting one that
		// still owns rows is refused by the database. "Existing dependencies"
		// does not tell the caller what to clear, so name the positions.
		if errors.Is(err, store.ErrConstraint) {
			// With cascade the holdings are already gone, so whatever remains
			// is transaction history — which this path never removes.
			if req.Msg.Cascade {
				return nil, connect.NewError(connect.CodeFailedPrecondition,
					errors.New("account has transaction history, which is never deleted automatically; remove the transactions first"))
			}
			if n, countErr := h.countHoldings(ctx, req.Msg.Id); countErr == nil && n > 0 {
				return nil, connect.NewError(connect.CodeFailedPrecondition,
					fmt.Errorf("account still holds %d position(s); delete them before deleting the account", n))
			}
			return nil, connect.NewError(connect.CodeFailedPrecondition,
				errors.New("account still has transactions; they must be removed before deleting the account"))
		}
		return nil, toConnectError(err)
	}

	return connect.NewResponse(&emptypb.Empty{}), nil
}

// countHoldings reports how many holdings the account still owns, for error
// messages only — a best-effort count, never a gate on the delete itself.
func (h *Handler) countHoldings(ctx context.Context, accountID string) (int, error) {
	var total int
	pageToken := ""
	for {
		holdings, next, err := h.store.ListHoldings(ctx, ListHoldingsOpts{
			AccountID: accountID,
			PageSize:  100,
			PageToken: pageToken,
		})
		if err != nil {
			return 0, err
		}
		total += len(holdings)
		if next == "" {
			return total, nil
		}
		pageToken = next
	}
}

func (h *Handler) ListAccounts(ctx context.Context, req *connect.Request[apiv1.ListAccountsRequest]) (*connect.Response[apiv1.ListAccountsResponse], error) {
	user, ok := middleware.UserFromContext(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("user not found in context"))
	}

	opts := ListAccountsOpts{UserID: user.ID}
	if req.Msg.UserId != nil && *req.Msg.UserId != "" && user.IsAdmin() {
		opts.UserID = *req.Msg.UserId // explicit override is admin-only
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

// syncedBalance is the provider-agnostic balance shape the upsert path consumes.
// Wallet and exchange syncers both normalize into this.
type syncedBalance struct {
	symbol          string
	name            string
	amount          string // raw integer string scaled by decimals
	decimals        int
	contractAddress string           // token contract/mint; empty for exchange/native
	chain           string           // network the balance is on; empty for exchange
	liquidity       entity.Liquidity // how reachable it is; empty when the source cannot say
	// providerSpam / contractVerified carry a source's identity signals for scam
	// scoring at intake; nil when the source does not report them.
	providerSpam     *bool
	contractVerified *bool
}

// SyncAccount fetches external holdings for a wallet or exchange account and
// upserts assets+holdings.
func (h *Handler) SyncAccount(ctx context.Context, req *connect.Request[apiv1.SyncAccountRequest]) (*connect.Response[apiv1.SyncAccountResponse], error) {
	if req.Msg.AccountId == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("account_id is required"))
	}
	if h.mdClient == nil {
		return nil, connect.NewError(connect.CodeUnimplemented, errors.New("account sync not configured"))
	}

	account, err := h.ownedAccount(ctx, req.Msg.AccountId)
	if err != nil {
		return nil, err
	}

	var balances []syncedBalance
	var syncErrors []string
	switch account.Type {
	case entity.AccountTypeWallet:
		balances, syncErrors, err = h.syncWalletBalances(ctx, account)
	case entity.AccountTypeExchange:
		balances, syncErrors, err = h.syncExchangeBalances(ctx, account)
	case entity.AccountTypeManual:
		// FailedPrecondition (not InvalidArgument): the account exists and is fine,
		// this kind is just never syncable — positions are entered manually.
		return nil, connect.NewError(connect.CodeFailedPrecondition, errors.New("manual account has nothing to sync: positions are entered manually"))
	default:
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("only wallet and exchange accounts can be synced"))
	}
	if err != nil {
		return nil, err
	}

	assetsUpserted, holdingsUpserted, syncedAssetIDs, upsertErrors, err := h.upsertSyncedBalances(ctx, account, balances)
	if err != nil {
		return nil, err
	}
	syncErrors = append(syncErrors, upsertErrors...)

	// Price what this sync touched, so CalculatePortfolioValue returns current
	// values. Naming the assets matters: an unfiltered request re-prices the
	// whole catalogue, which on a monthly provider quota costs as much as an
	// hour of the cron sweep and buys nothing the sync changed.
	if len(syncedAssetIDs) > 0 {
		if _, err := h.mdClient.FetchExternalPrices(ctx, connect.NewRequest(&apiv1.FetchExternalPricesRequest{
			AssetIds: syncedAssetIDs,
		})); err != nil {
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

// syncWalletBalances resolves the wallet syncer for the account owner and
// returns its balances normalized to syncedBalance. A partial failure surfaces
// as a sync error string, not a hard error.
func (h *Handler) syncWalletBalances(ctx context.Context, account *entity.Account) ([]syncedBalance, []string, error) {
	// An account normally holds one address, but UTXO chains spread a wallet
	// over many, so "addresses" accepts a list. Every address on an account
	// belongs to the same ecosystem: the syncer is resolved once, from the
	// first, and reused for the rest.
	addresses := splitAddresses(account.Data["addresses"])
	if len(addresses) == 0 {
		if single := strings.TrimSpace(account.Data["address"]); single != "" {
			addresses = []string{single}
		}
	}
	if len(addresses) == 0 {
		return nil, nil, connect.NewError(connect.CodeInvalidArgument,
			errors.New("account.data.address or account.data.addresses is required for wallet sync"))
	}
	address := addresses[0]

	// Resolve which chains to sync from the account config.
	// Empty or "auto" → let the syncer auto-discover (pass nil).
	// Otherwise a comma-separated list: "eth,base,arbitrum".
	var chains []string
	if chainRaw := strings.TrimSpace(account.Data["chain"]); chainRaw != "" && chainRaw != "auto" {
		chains = splitChains(chainRaw)
	}

	// Resolve the syncer routed by the account's chains: each provider covers
	// one ecosystem, so a Substrate account must not land on an EVM syncer.
	// A wired source is the sole authority — it knows every provider's chains
	// and carries the env-configured syncer as its own fallback. The statically
	// wired syncer only serves setups without a source (tests, minimal configs)
	// and is assumed EVM-only.
	walletSyncer := h.walletSyncer
	if h.syncerSource != nil {
		resolved, err := h.syncerSource.WalletSyncerFor(ctx, account.UserID, address, chains)
		if err != nil {
			return nil, nil, toConnectError(err)
		}
		walletSyncer = resolved
	}
	if walletSyncer == nil {
		if len(chains) > 0 {
			return nil, nil, connect.NewError(connect.CodeUnimplemented,
				fmt.Errorf("no wallet syncer configured for chain(s) %s", strings.Join(chains, ",")))
		}
		return nil, nil, connect.NewError(connect.CodeUnimplemented, errors.New("wallet sync not configured"))
	}

	// The syncer owns all provider mechanics (discovery, fan-out, native vs token).
	// Partial failures arrive as a joined error alongside the balances gathered so far.
	// Balances from several addresses are concatenated here and merged
	// downstream per (asset, chain), so a wallet split across addresses reports
	// one holding per asset per chain — several addresses on one chain are one
	// place, two chains are not.
	var (
		syncErrors []string
		balances   []entity.WalletBalance
	)
	for _, addr := range addresses {
		got, err := walletSyncer.SyncWallet(ctx, addr, chains)
		if err != nil {
			// Name the address: with several in play, an unqualified provider
			// error says nothing about which part of the wallet went missing.
			if len(addresses) > 1 {
				syncErrors = append(syncErrors, fmt.Sprintf("%s: %s", addr, err.Error()))
			} else {
				syncErrors = append(syncErrors, err.Error())
			}
		}
		balances = append(balances, got...)
	}

	result := make([]syncedBalance, 0, len(balances))
	for _, b := range balances {
		result = append(result, syncedBalance{
			symbol:           b.Symbol,
			name:             b.Name,
			amount:           b.Amount,
			decimals:         b.Decimals,
			contractAddress:  b.ContractAddress,
			chain:            b.Chain,
			liquidity:        b.Liquidity,
			providerSpam:     b.ProviderSpam,
			contractVerified: b.ContractVerified,
		})
	}
	return result, syncErrors, nil
}

// syncExchangeBalances builds the exchange syncer from the account's own stored
// credentials and returns its balances normalized to syncedBalance.
func (h *Handler) syncExchangeBalances(ctx context.Context, account *entity.Account) ([]syncedBalance, []string, error) {
	if h.exchangeSource == nil {
		return nil, nil, connect.NewError(connect.CodeUnimplemented, errors.New("exchange sync not configured"))
	}
	syncer, err := h.exchangeSource.ExchangeSyncerForAccount(account)
	if err != nil {
		return nil, nil, toConnectError(err)
	}
	if syncer == nil {
		return nil, nil, connect.NewError(connect.CodeInvalidArgument, errors.New("no exchange adapter for account provider; set account.data.provider (e.g. \"binance\")"))
	}

	var syncErrors []string
	balances, err := syncer.SyncExchange(ctx)
	if err != nil {
		syncErrors = append(syncErrors, err.Error())
	}

	result := make([]syncedBalance, 0, len(balances))
	for _, b := range balances {
		result = append(result, syncedBalance{
			symbol:   b.Symbol,
			name:     b.Symbol, // exchanges report only the ticker; use it as the name
			amount:   b.Amount,
			decimals: b.Decimals,
		})
	}
	return result, syncErrors, nil
}

// upsertSyncedBalances resolves each balance to an asset and upserts holdings
// for the account. Resolution is by contract identity (external ref) first and
// by symbol otherwise, so a scam clone of a real ticker resolves to its own
// asset while cross-chain instances of the same asset collapse onto one
// asset_id.
//
// Positions are keyed by (asset, chain, liquidity), not by asset alone.
// Collapsing chains destroyed the only copy of where an amount sits: USDC on
// Base and USDC on Arbitrum became one row, summed across mismatched decimals
// (USDC is 6 on Ethereum and 18 on BSC) and stored at the largest scale seen.
// Liquidity is the same argument in time rather than space: staked ATOM and
// bank-balance ATOM are one asset on one chain, but only one of them can be
// spent today. Within one chain and one liquidity state, several addresses of
// the same wallet still merge — that is the same place, so quantities are
// summed as real amounts (raw / 10^decimals).
//
// syncedAssetIDs lists the assets whose holdings this sync actually wrote. It
// is what the caller prices afterwards: a provider quota is monthly, and
// re-pricing the whole catalogue on every sync spends it on data the sync did
// not touch.
func (h *Handler) upsertSyncedBalances(ctx context.Context, account *entity.Account, balances []syncedBalance) (assetsUpserted, holdingsUpserted int32, syncedAssetIDs []string, syncErrors []string, err error) {
	type positionKey struct {
		assetID string
		chain   string // empty for venues with no chain of their own (exchanges)
		// liquidity is empty unless the adapter partitioned the balance. Staked
		// and liquid value on one chain are different money — one is spendable
		// today and the other is not — so they are different rows.
		liquidity entity.Liquidity
	}
	type accumulated struct {
		qty      decimal.Decimal // real token quantity, decimals applied
		decimals int             // max decimals seen on this chain → stored scale
		excluded bool            // derived from a scam/impersonation verdict
	}
	byPosition := make(map[positionKey]*accumulated)
	// Insertion order, so a resync writes rows in a stable order and the legacy
	// row below is adopted by the first chain seen rather than by map luck.
	var order []positionKey

	for _, b := range balances {
		amt, ok := new(big.Int).SetString(strings.TrimSpace(b.amount), 10)
		if !ok {
			syncErrors = append(syncErrors, fmt.Sprintf("parse amount %q for %s", b.amount, b.symbol))
			continue
		}
		if amt.Sign() == 0 {
			continue
		}

		assetID, created, verdict, rerr := h.resolveSyncedAsset(ctx, b)
		if rerr != nil {
			syncErrors = append(syncErrors, fmt.Sprintf("resolve asset %s: %v", b.symbol, rerr))
			continue
		}
		if created {
			assetsUpserted++
		}

		qty := decimal.NewFromBigInt(amt, -intToI32(b.decimals)) // raw / 10^decimals
		key := positionKey{assetID: assetID, chain: b.chain, liquidity: b.liquidity}
		if entry, ok := byPosition[key]; ok {
			entry.qty = entry.qty.Add(qty)
			if b.decimals > entry.decimals {
				entry.decimals = b.decimals
			}
			entry.excluded = entry.excluded || isQuarantineVerdict(verdict)
		} else {
			byPosition[key] = &accumulated{
				qty:      qty,
				decimals: b.decimals,
				excluded: isQuarantineVerdict(verdict),
			}
			order = append(order, key)
		}
	}

	// Build existing holdings map for this account
	existingHoldings, _, err := h.store.ListHoldings(ctx, ListHoldingsOpts{AccountID: account.ID, PageSize: 1000})
	if err != nil {
		return 0, 0, nil, nil, toConnectError(err)
	}
	holdingByPosition := make(map[positionKey]*entity.Holding, len(existingHoldings))
	// Rows written before positions carried a chain or a liquidity state: one
	// summed row per asset, both fields empty. The first position of that asset
	// adopts the row instead of leaving it beside the new ones, where it would
	// double the holding until someone deleted it by hand. Adoption keeps the
	// row's id, provenance, portfolio assignment and manual excluded override.
	//
	// Provenance is deliberately NOT a filter here. Before positions had
	// dimensions, sync updated whatever row it found for the asset — including
	// an imported one — so an imported ATOM position was refreshed in place.
	// Adopting only sync-written rows looked tidier and doubled that position on
	// the first resync: caught on dev, where an llm_import row of 66.54 ATOM sat
	// beside a freshly synced 21.84 liquid + 45.00 staked.
	legacyByAsset := make(map[string][]*entity.Holding)
	for _, hld := range existingHoldings {
		holdingByPosition[positionKey{assetID: hld.AssetID, chain: hld.Chain, liquidity: hld.Liquidity}] = hld
		if hld.Chain == "" && hld.Liquidity == "" {
			legacyByAsset[hld.AssetID] = append(legacyByAsset[hld.AssetID], hld)
		}
	}

	defaultPortfolioID := account.PortfolioID

	for _, key := range order {
		entry := byPosition[key]
		assetID := key.assetID
		decimals := intToU32(entry.decimals)
		// holdings.amount is NUMERIC: store the merged quantity as a raw integer at the
		// holding's decimals scale (exact — qty has at most `decimals` fractional digits).
		amount := entry.qty.Shift(intToI32(entry.decimals))

		existing, ok := holdingByPosition[key]
		adopted := false
		if !ok && (key.chain != "" || key.liquidity != "") {
			if legacy := legacyByAsset[assetID]; len(legacy) > 0 {
				existing, ok, adopted = legacy[0], true, true
				legacyByAsset[assetID] = legacy[1:]
			}
		}

		if ok {
			// Update existing holding: only refresh amount/decimals (and the
			// chain when adopting a pre-chain row); never touch portfolio assignment.
			existing.Amount = amount
			existing.Decimals = decimals
			fields := []string{"amount", "decimals"}
			if adopted {
				existing.Chain = key.chain
				existing.Liquidity = key.liquidity
				fields = append(fields, "chain", "liquidity")
			}
			if _, err := h.store.UpdateHolding(ctx, existing, fields); err != nil {
				syncErrors = append(syncErrors, fmt.Sprintf("update holding for asset %s: %v", assetID, err))
				continue
			}
		} else {
			// Create new holding; inherit account's default portfolio if configured.
			// A scam/impersonation verdict on the asset excludes the new holding
			// from the sums; the position still syncs (no frozen holding), it is
			// just quarantined. An existing holding's excluded flag is left alone
			// on update so a user's manual override survives resync.
			_, err := h.store.CreateHolding(ctx, &entity.Holding{
				AssetID:     assetID,
				AccountID:   account.ID,
				PortfolioID: defaultPortfolioID,
				Amount:      amount,
				Decimals:    decimals,
				Chain:       key.chain,
				Liquidity:   key.liquidity,
				Source:      entity.SourceSync,
				Excluded:    entry.excluded,
			})
			if err != nil {
				syncErrors = append(syncErrors, fmt.Sprintf("create holding for asset %s: %v", assetID, err))
				continue
			}
		}
		holdingsUpserted++
		syncedAssetIDs = append(syncedAssetIDs, assetID)
	}

	return assetsUpserted, holdingsUpserted, syncedAssetIDs, syncErrors, nil
}

// resolveSyncedAsset resolves (creating when needed) the asset for one synced
// balance through the MarketData service. A balance that carries a contract is
// resolved by its on-chain identity (external ref), so a token is matched by
// contract before symbol and cross-chain instances of the same asset collapse
// onto one asset_id.
func (h *Handler) resolveSyncedAsset(ctx context.Context, b syncedBalance) (assetID string, created bool, verdict string, err error) {
	msg := &apiv1.FindOrCreateAssetRequest{
		Symbol:           b.symbol,
		Type:             apiv1.AssetType_ASSET_TYPE_CRYPTOCURRENCY,
		ProviderSpam:     b.providerSpam,
		ContractVerified: b.contractVerified,
	}
	if b.name != "" {
		msg.Name = &b.name
	}
	if b.contractAddress != "" && b.chain != "" {
		source := entity.OnchainSource(b.chain)
		contract := b.contractAddress
		msg.ExternalRefSource = &source
		msg.ExternalRef = &contract
	}

	resp, err := h.mdClient.FindOrCreateAsset(ctx, connect.NewRequest(msg))
	if err != nil {
		return "", false, "", err
	}
	if resp.Msg.Asset == nil {
		return "", false, "", fmt.Errorf("marketdata returned no asset for %s", b.symbol)
	}
	return resp.Msg.Asset.Id, resp.Msg.Created, resp.Msg.Asset.GetIdentityVerdict(), nil
}

// isQuarantineVerdict reports whether an identity verdict excludes a synced
// holding from the sums: a scam or an impersonation is not the user's money to
// count, while a real asset's situational risk (a separate axis) never excludes.
func isQuarantineVerdict(verdict string) bool {
	return verdict == string(scamfilter.VerdictScam) ||
		verdict == string(scamfilter.VerdictImpersonation)
}

// --- Transaction CRUD ---

func (h *Handler) CreateTransaction(ctx context.Context, req *connect.Request[apiv1.CreateTransactionRequest]) (*connect.Response[apiv1.Transaction], error) {
	if req.Msg.Transaction == nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("transaction is required"))
	}

	tx := transactionFromProto(req.Msg.Transaction)
	// Provenance is server-stamped: everything created through this RPC is manual.
	tx.Source = entity.SourceManual
	// The target account must belong to the caller.
	if _, err := h.ownedAccount(ctx, tx.AccountID); err != nil {
		return nil, err
	}
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

	tx, err := h.ownedTransaction(ctx, req.Msg.Id)
	if err != nil {
		return nil, err
	}

	return connect.NewResponse(transactionToProto(tx)), nil
}

func (h *Handler) UpdateTransaction(ctx context.Context, req *connect.Request[apiv1.UpdateTransactionRequest]) (*connect.Response[apiv1.Transaction], error) {
	if req.Msg.Transaction == nil || req.Msg.Transaction.Id == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("transaction with ID is required"))
	}

	if _, err := h.ownedTransaction(ctx, req.Msg.Transaction.Id); err != nil {
		return nil, err
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
	user, ok := middleware.UserFromContext(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("user not found in context"))
	}

	// Admins see everything; everyone else only their own accounts' transactions.
	opts := ListTransactionsOpts{}
	if !user.IsAdmin() {
		opts.UserID = user.ID
	}
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
	// Provenance fields (source, import_id) are deliberately not mapped:
	// they are output-only and stamped by the server.
	result := &entity.Holding{
		ID:        h.Id,
		Amount:    amount,
		Decimals:  h.Decimals,
		AssetID:   h.AssetId,
		AccountID: h.AccountId,
		Chain:     h.Chain,
		Liquidity: entity.Liquidity(h.Liquidity),
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
		Chain:     h.Chain,
		Liquidity: string(h.Liquidity),
		Excluded:  h.Excluded,
		Source:    provenanceToProto(h.Source),
		CreatedAt: timestamppb.New(h.CreatedAt),
		UpdatedAt: timestamppb.New(h.UpdatedAt),
	}
	if h.PortfolioID != "" {
		result.PortfolioId = &h.PortfolioID
	}
	if h.ImportID != "" {
		result.ImportId = &h.ImportID
	}
	return result
}

func provenanceToProto(s entity.ProvenanceSource) apiv1.ProvenanceSource {
	switch s {
	case entity.SourceSync:
		return apiv1.ProvenanceSource_PROVENANCE_SOURCE_SYNC
	case entity.SourceManual:
		return apiv1.ProvenanceSource_PROVENANCE_SOURCE_MANUAL
	case entity.SourceLLMImport:
		return apiv1.ProvenanceSource_PROVENANCE_SOURCE_LLM_IMPORT
	default:
		return apiv1.ProvenanceSource_PROVENANCE_SOURCE_UNSPECIFIED
	}
}

func accountFromProto(a *apiv1.Account) *entity.Account {
	result := &entity.Account{
		ID:           a.Id,
		UserID:       a.UserId,
		Name:         a.Name,
		Type:         entity.AccountType(a.Type),
		Data:         a.Data,
		Capabilities: capabilitiesFromProto(a.Capabilities),
		SystemScopes: capabilitiesFromProto(a.SystemScopes),
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
		Id:           a.ID,
		UserId:       a.UserID,
		Name:         a.Name,
		Type:         apiv1.AccountType(a.Type),
		Data:         maskSecrets(a.Data),
		Capabilities: capabilitiesToProto(a.Capabilities),
		SystemScopes: capabilitiesToProto(a.SystemScopes),
		CreatedAt:    timestamppb.New(a.CreatedAt),
		UpdatedAt:    timestamppb.New(a.UpdatedAt),
	}
	if a.Description != "" {
		result.Description = &a.Description
	}
	if a.PortfolioID != "" {
		result.PortfolioId = &a.PortfolioID
	}
	return result
}

func capabilitiesFromProto(caps []string) []entity.AccountCapability {
	if len(caps) == 0 {
		return nil
	}
	result := make([]entity.AccountCapability, len(caps))
	for i, c := range caps {
		result[i] = entity.AccountCapability(c)
	}
	return result
}

func capabilitiesToProto(caps []entity.AccountCapability) []string {
	if len(caps) == 0 {
		return nil
	}
	result := make([]string, len(caps))
	for i, c := range caps {
		result[i] = string(c)
	}
	return result
}

// maskPrefix marks a secret value as masked in API responses; an incoming
// value with this prefix means "keep the stored secret".
const maskPrefix = "••••"

// nonSecretDataKeys are accounts.data keys that look secret-ish by name but
// are safe to return as is.
var nonSecretDataKeys = map[string]bool{
	"provider":  true,
	"address":   true,
	"addresses": true,
	"chain":     true,
	"pro":       true,
}

// isSecretKey classifies accounts.data keys by name: anything containing
// key/secret/token/password is treated as a credential unless explicitly
// allowlisted. Fail-safe: an unknown provider's credential key gets masked
// without code changes.
func isSecretKey(key string) bool {
	if nonSecretDataKeys[key] {
		return false
	}
	k := strings.ToLower(key)
	for _, marker := range []string{"key", "secret", "token", "password"} {
		if strings.Contains(k, marker) {
			return true
		}
	}
	return false
}

// maskSecrets returns a copy of data with secret values replaced by
// maskPrefix+last4 (or just maskPrefix for short values). The input map is
// shared with the credentials resolver and must not be mutated.
func maskSecrets(data map[string]string) map[string]string {
	if data == nil {
		return nil
	}
	masked := make(map[string]string, len(data))
	for k, v := range data {
		if isSecretKey(k) {
			// last4 only when it reveals at most half of the secret
			if len(v) >= 8 {
				v = maskPrefix + v[len(v)-4:]
			} else {
				v = maskPrefix
			}
		}
		masked[k] = v
	}
	return masked
}

func transactionFromProto(t *apiv1.Transaction) *entity.Transaction {
	// Provenance fields (source, import_id) are deliberately not mapped:
	// they are output-only and stamped by the server.
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

// splitAddresses reads the comma- or space-separated address list an account
// may carry instead of a single address. Unlike splitChains there is no default:
// an absent list means the account uses the single-address form.
func splitAddresses(raw string) []string {
	parts := strings.FieldsFunc(raw, func(r rune) bool { return r == ',' || r == ' ' })
	result := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			result = append(result, p)
		}
	}
	return result
}

func transactionToProto(t *entity.Transaction) *apiv1.Transaction {
	result := &apiv1.Transaction{
		Id:        t.ID,
		Type:      apiv1.TransactionType(t.Type),
		Status:    apiv1.TransactionStatus(t.Status),
		AccountId: t.AccountID,
		Data:      t.Data,
		Source:    provenanceToProto(t.Source),
		CreatedAt: timestamppb.New(t.CreatedAt),
		UpdatedAt: timestamppb.New(t.UpdatedAt),
	}
	if t.ImportID != "" {
		result.ImportId = &t.ImportID
	}
	return result
}
