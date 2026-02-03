package marketdata

import (
	"context"
	"errors"
	"log/slog"

	"connectrpc.com/connect"
	apiv1 "github.com/foxcool/greedy-eye/internal/api/v1"
	"github.com/foxcool/greedy-eye/internal/api/v1/apiv1connect"
	"github.com/foxcool/greedy-eye/internal/entity"
	"github.com/foxcool/greedy-eye/internal/store"
	"google.golang.org/protobuf/types/known/emptypb"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// Handler implements apiv1connect.MarketDataServiceHandler.
type Handler struct {
	apiv1connect.UnimplementedMarketDataServiceHandler
	store Store
	log   *slog.Logger
}

func NewHandler(store Store, log *slog.Logger) *Handler {
	return &Handler{store: store, log: log}
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

	var sourceID string
	if req.Msg.SourceId != nil {
		sourceID = *req.Msg.SourceId
	}

	price, err := h.store.GetLatestPrice(ctx, req.Msg.AssetId, req.Msg.BaseAssetId, sourceID)
	if err != nil {
		return nil, toConnectError(err)
	}

	return connect.NewResponse(priceToProto(price)), nil
}

// ListPriceHistory returns price history for an asset pair.
func (h *Handler) ListPriceHistory(ctx context.Context, req *connect.Request[apiv1.ListPriceHistoryRequest]) (*connect.Response[apiv1.ListPriceHistoryResponse], error) {
	if req.Msg.AssetId == "" || req.Msg.BaseAssetId == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("asset_id and base_asset_id are required"))
	}

	opts := ListPriceHistoryOpts{
		AssetID:     req.Msg.AssetId,
		BaseAssetID: req.Msg.BaseAssetId,
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

// FetchExternalPrices fetches prices from external sources (stub).
func (h *Handler) FetchExternalPrices(ctx context.Context, req *connect.Request[apiv1.FetchExternalPricesRequest]) (*connect.Response[apiv1.FetchExternalPricesResponse], error) {
	// TODO: Implement with external price providers
	return nil, connect.NewError(connect.CodeUnimplemented, errors.New("FetchExternalPrices not implemented"))
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

func priceFromProto(p *apiv1.Price) *entity.StoredPrice {
	price := &entity.StoredPrice{
		ID:          p.Id,
		SourceID:    p.SourceId,
		AssetID:     p.AssetId,
		BaseAssetID: p.BaseAssetId,
		Interval:    p.Interval,
		Decimals:    p.Decimals,
		Last:        p.Last,
		Open:        p.Open,
		High:        p.High,
		Low:         p.Low,
		Close:       p.Close,
		Volume:      p.Volume,
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
		Last:        e.Last,
		Open:        e.Open,
		High:        e.High,
		Low:         e.Low,
		Close:       e.Close,
		Volume:      e.Volume,
		Timestamp:   timestamppb.New(e.Timestamp),
	}
}
