package marketdata

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"connectrpc.com/connect"
	apiv1 "github.com/foxcool/greedy-eye/internal/api/v1"
	"github.com/foxcool/greedy-eye/internal/api/v1/apiv1connect"
	"github.com/foxcool/greedy-eye/internal/entity"
	"github.com/foxcool/greedy-eye/internal/store"
	"google.golang.org/protobuf/types/known/emptypb"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// PriceProvider fetches prices from an external source.
type PriceProvider interface {
	// FetchPrices fetches current prices for the given assets.
	// Returned StoredPrice.BaseAssetID is intentionally empty — the handler resolves
	// the base asset UUID from BaseAssetSymbol() before persisting.
	FetchPrices(ctx context.Context, assets []*entity.Asset) ([]entity.StoredPrice, error)
	// BaseAssetSymbol returns the ticker of the quote currency (e.g. "USD", "USDT").
	// Used by FetchExternalPrices to resolve or create the base asset on demand.
	BaseAssetSymbol() string
}

// Handler implements apiv1connect.MarketDataServiceHandler.
type Handler struct {
	apiv1connect.UnimplementedMarketDataServiceHandler
	store     Store
	providers map[string]PriceProvider // keyed by source name e.g. "coingecko"
	log       *slog.Logger
}

func NewHandler(store Store, log *slog.Logger) *Handler {
	return &Handler{store: store, log: log}
}

// WithProvider returns a new Handler with the named price provider added.
func (h *Handler) WithProvider(name string, p PriceProvider) *Handler {
	providers := make(map[string]PriceProvider, len(h.providers)+1)
	for k, v := range h.providers {
		providers[k] = v
	}
	providers[name] = p
	return &Handler{
		UnimplementedMarketDataServiceHandler: h.UnimplementedMarketDataServiceHandler,
		store:                                 h.store,
		providers:                             providers,
		log:                                   h.log,
	}
}

// CreateAsset creates a new asset.
func (h *Handler) CreateAsset(ctx context.Context, req *connect.Request[apiv1.CreateAssetRequest]) (*connect.Response[apiv1.Asset], error) {
	if req.Msg.Asset == nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("asset is required"))
	}

	asset := assetFromProto(req.Msg.Asset)
	created, err := h.store.CreateAsset(ctx, asset)
	if err != nil {
		return nil, toConnectError(err)
	}

	return connect.NewResponse(assetToProto(created)), nil
}

// GetAsset retrieves an asset by ID.
func (h *Handler) GetAsset(ctx context.Context, req *connect.Request[apiv1.GetAssetRequest]) (*connect.Response[apiv1.Asset], error) {
	if req.Msg.Id == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("asset ID is required"))
	}

	asset, err := h.store.GetAsset(ctx, req.Msg.Id)
	if err != nil {
		return nil, toConnectError(err)
	}

	return connect.NewResponse(assetToProto(asset)), nil
}

// UpdateAsset updates an asset.
func (h *Handler) UpdateAsset(ctx context.Context, req *connect.Request[apiv1.UpdateAssetRequest]) (*connect.Response[apiv1.Asset], error) {
	if req.Msg.Asset == nil || req.Msg.Asset.Id == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("asset with ID is required"))
	}

	var fields []string
	if req.Msg.UpdateMask != nil {
		fields = req.Msg.UpdateMask.Paths
	}

	asset := assetFromProto(req.Msg.Asset)
	updated, err := h.store.UpdateAsset(ctx, asset, fields)
	if err != nil {
		return nil, toConnectError(err)
	}

	return connect.NewResponse(assetToProto(updated)), nil
}

// DeleteAsset deletes an asset by ID.
func (h *Handler) DeleteAsset(ctx context.Context, req *connect.Request[apiv1.DeleteAssetRequest]) (*connect.Response[emptypb.Empty], error) {
	if req.Msg.Id == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("asset ID is required"))
	}

	if err := h.store.DeleteAsset(ctx, req.Msg.Id); err != nil {
		return nil, toConnectError(err)
	}

	return connect.NewResponse(&emptypb.Empty{}), nil
}

// ListAssets lists assets with pagination and optional tag filtering.
func (h *Handler) ListAssets(ctx context.Context, req *connect.Request[apiv1.ListAssetsRequest]) (*connect.Response[apiv1.ListAssetsResponse], error) {
	opts := ListAssetsOpts{
		Tags: req.Msg.Tags,
	}
	if req.Msg.PageSize != nil {
		opts.PageSize = int(*req.Msg.PageSize)
	}
	if req.Msg.PageToken != nil {
		opts.PageToken = *req.Msg.PageToken
	}

	assets, nextPageToken, err := h.store.ListAssets(ctx, opts)
	if err != nil {
		return nil, toConnectError(err)
	}

	protoAssets := make([]*apiv1.Asset, 0, len(assets))
	for _, a := range assets {
		protoAssets = append(protoAssets, assetToProto(a))
	}

	return connect.NewResponse(&apiv1.ListAssetsResponse{
		Assets:        protoAssets,
		NextPageToken: nextPageToken,
	}), nil
}

// CreatePrice creates a new price record.
func (h *Handler) CreatePrice(ctx context.Context, req *connect.Request[apiv1.CreatePriceRequest]) (*connect.Response[apiv1.Price], error) {
	if req.Msg.Price == nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("price is required"))
	}

	price := priceFromProto(req.Msg.Price)
	created, err := h.store.CreatePrice(ctx, price)
	if err != nil {
		return nil, toConnectError(err)
	}

	return connect.NewResponse(priceToProto(created)), nil
}

// CreatePrices creates multiple price records in bulk.
func (h *Handler) CreatePrices(ctx context.Context, req *connect.Request[apiv1.CreatePricesRequest]) (*connect.Response[apiv1.CreatePricesResponse], error) {
	prices := make([]*entity.StoredPrice, 0, len(req.Msg.Prices))
	for _, p := range req.Msg.Prices {
		prices = append(prices, priceFromProto(p))
	}

	count, err := h.store.CreatePrices(ctx, prices)
	if err != nil {
		return nil, toConnectError(err)
	}

	return connect.NewResponse(&apiv1.CreatePricesResponse{
		CreatedCount: int32(count),
	}), nil
}

// GetLatestPrice returns the most recent price for an asset pair.
func (h *Handler) GetLatestPrice(ctx context.Context, req *connect.Request[apiv1.GetLatestPriceRequest]) (*connect.Response[apiv1.Price], error) {
	if req.Msg.AssetId == "" || req.Msg.BaseAssetId == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("asset_id and base_asset_id are required"))
	}

	baseAssetID, err := h.resolveAssetID(ctx, req.Msg.BaseAssetId)
	if err != nil {
		return nil, toConnectError(err)
	}

	var sourceID string
	if req.Msg.SourceId != nil {
		sourceID = *req.Msg.SourceId
	}

	price, err := h.store.GetLatestPrice(ctx, req.Msg.AssetId, baseAssetID, sourceID)
	if err != nil {
		return nil, toConnectError(err)
	}

	return connect.NewResponse(priceToProto(price)), nil
}

// resolveAssetID returns id unchanged if it is a valid UUID, otherwise treats it
// as a symbol and looks up the asset by symbol. This allows callers to pass either
// a UUID or a well-known ticker (e.g. "USD", "usd") interchangeably.
func (h *Handler) resolveAssetID(ctx context.Context, id string) (string, error) {
	if _, err := uuid.Parse(id); err == nil {
		return id, nil
	}
	asset, err := h.store.GetAssetBySymbol(ctx, strings.ToUpper(id))
	if err != nil {
		return "", fmt.Errorf("resolve asset %q: %w", id, err)
	}
	return asset.ID, nil
}

// ListPriceHistory returns price history for an asset pair.
func (h *Handler) ListPriceHistory(ctx context.Context, req *connect.Request[apiv1.ListPriceHistoryRequest]) (*connect.Response[apiv1.ListPriceHistoryResponse], error) {
	if req.Msg.AssetId == "" || req.Msg.BaseAssetId == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("asset_id and base_asset_id are required"))
	}

	baseAssetID, err := h.resolveAssetID(ctx, req.Msg.BaseAssetId)
	if err != nil {
		return nil, toConnectError(err)
	}

	opts := ListPriceHistoryOpts{
		AssetID:     req.Msg.AssetId,
		BaseAssetID: baseAssetID,
	}
	if req.Msg.SourceId != nil {
		opts.SourceID = *req.Msg.SourceId
	}
	if req.Msg.From != nil {
		t := req.Msg.From.AsTime()
		opts.From = &t
	}
	if req.Msg.To != nil {
		t := req.Msg.To.AsTime()
		opts.To = &t
	}
	if req.Msg.PageSize != nil {
		opts.PageSize = int(*req.Msg.PageSize)
	}
	if req.Msg.PageToken != nil {
		opts.PageToken = *req.Msg.PageToken
	}

	prices, nextPageToken, err := h.store.ListPriceHistory(ctx, opts)
	if err != nil {
		return nil, toConnectError(err)
	}

	protoPrices := make([]*apiv1.Price, 0, len(prices))
	for _, p := range prices {
		protoPrices = append(protoPrices, priceToProto(p))
	}

	return connect.NewResponse(&apiv1.ListPriceHistoryResponse{
		Prices:        protoPrices,
		NextPageToken: nextPageToken,
	}), nil
}

// ListPricesByInterval aggregates prices by interval (stub for MVP).
func (h *Handler) ListPricesByInterval(ctx context.Context, req *connect.Request[apiv1.ListPricesByIntervalRequest]) (*connect.Response[apiv1.ListPriceHistoryResponse], error) {
	// MVP: fallback to ListPriceHistory
	return h.ListPriceHistory(ctx, connect.NewRequest(&apiv1.ListPriceHistoryRequest{
		AssetId:     req.Msg.AssetId,
		BaseAssetId: req.Msg.BaseAssetId,
		From:        req.Msg.From,
		To:          req.Msg.To,
		SourceId:    req.Msg.SourceId,
		PageSize:    req.Msg.PageSize,
		PageToken:   req.Msg.PageToken,
	}))
}

// DeletePrice deletes a price record by ID.
func (h *Handler) DeletePrice(ctx context.Context, req *connect.Request[apiv1.DeletePriceRequest]) (*connect.Response[emptypb.Empty], error) {
	if req.Msg.Id == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("price ID is required"))
	}

	if err := h.store.DeletePrice(ctx, req.Msg.Id); err != nil {
		return nil, toConnectError(err)
	}

	return connect.NewResponse(&emptypb.Empty{}), nil
}

// DeletePrices deletes price records by criteria.
func (h *Handler) DeletePrices(ctx context.Context, req *connect.Request[apiv1.DeletePricesRequest]) (*connect.Response[emptypb.Empty], error) {
	opts := DeletePricesOpts{}
	if req.Msg.AssetId != nil {
		opts.AssetID = *req.Msg.AssetId
	}
	if req.Msg.BaseAssetId != nil {
		opts.BaseAssetID = *req.Msg.BaseAssetId
	}
	if req.Msg.SourceId != nil {
		opts.SourceID = *req.Msg.SourceId
	}
	if req.Msg.From != nil {
		t := req.Msg.From.AsTime()
		opts.From = &t
	}
	if req.Msg.To != nil {
		t := req.Msg.To.AsTime()
		opts.To = &t
	}

	if err := h.store.DeletePrices(ctx, opts); err != nil {
		return nil, toConnectError(err)
	}

	return connect.NewResponse(&emptypb.Empty{}), nil
}

// EnrichAssetData enriches asset with external data (stub).
func (h *Handler) EnrichAssetData(ctx context.Context, req *connect.Request[apiv1.EnrichAssetDataRequest]) (*connect.Response[apiv1.Asset], error) {
	// TODO: Implement with external data sources
	return nil, connect.NewError(connect.CodeUnimplemented, errors.New("EnrichAssetData not implemented"))
}

// FindSimilarAssets finds similar assets (stub).
func (h *Handler) FindSimilarAssets(ctx context.Context, req *connect.Request[apiv1.FindSimilarAssetsRequest]) (*connect.Response[apiv1.ListAssetsResponse], error) {
	// TODO: Implement similarity search
	return nil, connect.NewError(connect.CodeUnimplemented, errors.New("FindSimilarAssets not implemented"))
}

// FetchExternalPrices fetches prices from configured providers and stores them.
// If source_ids is specified in the request, only those providers are called.
func (h *Handler) FetchExternalPrices(ctx context.Context, req *connect.Request[apiv1.FetchExternalPricesRequest]) (*connect.Response[apiv1.FetchExternalPricesResponse], error) {
	if len(h.providers) == 0 {
		return nil, connect.NewError(connect.CodeUnimplemented, errors.New("no price providers configured"))
	}

	// Load all assets; filter to the requested IDs if specified.
	allAssets, _, err := h.store.ListAssets(ctx, ListAssetsOpts{PageSize: 500})
	if err != nil {
		return nil, toConnectError(err)
	}

	assets := allAssets
	if len(req.Msg.AssetIds) > 0 {
		requested := make(map[string]bool, len(req.Msg.AssetIds))
		for _, id := range req.Msg.AssetIds {
			requested[id] = true
		}
		filtered := make([]*entity.Asset, 0, len(req.Msg.AssetIds))
		for _, a := range allAssets {
			if requested[a.ID] {
				filtered = append(filtered, a)
			}
		}
		assets = filtered
	}

	if len(assets) == 0 {
		return connect.NewResponse(&apiv1.FetchExternalPricesResponse{}), nil
	}

	var allPrices []*entity.StoredPrice
	var fetchErrs []string
	var totalFetched int

	// Cache base asset UUIDs per symbol to avoid repeated lookups across providers.
	baseAssetCache := map[string]string{} // symbol → UUID

	for name, provider := range h.providers {
		if len(req.Msg.SourceIds) > 0 && !slices.Contains(req.Msg.SourceIds, name) {
			continue
		}
		results, err := provider.FetchPrices(ctx, assets)
		if err != nil {
			fetchErrs = append(fetchErrs, fmt.Sprintf("%s: %v", name, err))
			continue
		}

		// Resolve base asset UUID for this provider (create the asset if it doesn't exist yet).
		sym := strings.ToUpper(provider.BaseAssetSymbol())
		baseID, ok := baseAssetCache[sym]
		if !ok {
			baseAsset, err := h.store.GetOrCreateAssetBySymbol(ctx, sym, sym, entity.AssetTypeForex)
			if err != nil {
				fetchErrs = append(fetchErrs, fmt.Sprintf("%s: resolve base asset %s: %v", name, sym, err))
				continue
			}
			baseID = baseAsset.ID
			baseAssetCache[sym] = baseID
		}

		totalFetched += len(results)
		for i := range results {
			results[i].BaseAssetID = baseID
			allPrices = append(allPrices, &results[i])
		}
	}

	stored, err := h.store.CreatePrices(ctx, allPrices)
	if err != nil {
		// Partial failures are non-fatal: surface them in the response errors field.
		h.log.Warn("some prices failed to store", "error", err)
		fetchErrs = append(fetchErrs, err.Error())
	}

	return connect.NewResponse(&apiv1.FetchExternalPricesResponse{
		PricesFetched: int32(totalFetched),
		PricesStored:  int32(stored),
		Errors:        fetchErrs,
	}), nil
}

// toConnectError converts store errors to Connect errors.
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

// Conversion helpers

func assetFromProto(p *apiv1.Asset) *entity.Asset {
	var symbol string
	if p.Symbol != nil {
		symbol = *p.Symbol
	}
	return &entity.Asset{
		ID:     p.Id,
		Name:   p.Name,
		Symbol: symbol,
		Type:   entity.AssetType(p.Type),
		Tags:   p.Tags,
	}
}

func assetToProto(e *entity.Asset) *apiv1.Asset {
	var symbol *string
	if e.Symbol != "" {
		symbol = &e.Symbol
	}
	return &apiv1.Asset{
		Id:        e.ID,
		Name:      e.Name,
		Symbol:    symbol,
		Type:      apiv1.AssetType(e.Type),
		Tags:      e.Tags,
		CreatedAt: timestamppb.New(e.CreatedAt),
		UpdatedAt: timestamppb.New(e.UpdatedAt),
	}
}

// parseDecimal parses a raw integer decimal string, returning zero on empty/invalid input.
func parseDecimal(s string) decimal.Decimal {
	d, err := decimal.NewFromString(s)
	if err != nil {
		return decimal.Zero
	}
	return d
}

// parseNullDecimal converts an optional proto string into a NullDecimal.
func parseNullDecimal(s *string) decimal.NullDecimal {
	if s == nil {
		return decimal.NullDecimal{}
	}
	d, err := decimal.NewFromString(*s)
	if err != nil {
		return decimal.NullDecimal{}
	}
	return decimal.NullDecimal{Decimal: d, Valid: true}
}

// nullDecimalToProto converts a NullDecimal into an optional proto string.
func nullDecimalToProto(d decimal.NullDecimal) *string {
	if !d.Valid {
		return nil
	}
	s := d.Decimal.String()
	return &s
}

func priceFromProto(p *apiv1.Price) *entity.StoredPrice {
	price := &entity.StoredPrice{
		ID:          p.Id,
		SourceID:    p.SourceId,
		AssetID:     p.AssetId,
		BaseAssetID: p.BaseAssetId,
		Interval:    p.Interval,
		Decimals:    p.Decimals,
		Last:        parseDecimal(p.Last),
		Open:        parseNullDecimal(p.Open),
		High:        parseNullDecimal(p.High),
		Low:         parseNullDecimal(p.Low),
		Close:       parseNullDecimal(p.Close),
		Volume:      parseNullDecimal(p.Volume),
	}
	if p.Timestamp != nil {
		price.Timestamp = p.Timestamp.AsTime()
	}
	return price
}

func priceToProto(e *entity.StoredPrice) *apiv1.Price {
	return &apiv1.Price{
		Id:          e.ID,
		SourceId:    e.SourceID,
		AssetId:     e.AssetID,
		BaseAssetId: e.BaseAssetID,
		Interval:    e.Interval,
		Decimals:    e.Decimals,
		Last:        e.Last.String(),
		Open:        nullDecimalToProto(e.Open),
		High:        nullDecimalToProto(e.High),
		Low:         nullDecimalToProto(e.Low),
		Close:       nullDecimalToProto(e.Close),
		Volume:      nullDecimalToProto(e.Volume),
		Timestamp:   timestamppb.New(e.Timestamp),
	}
}
