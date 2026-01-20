package storage

import (
	"context"
	"encoding/base64"
	"log/slog"
	"time"

	"github.com/foxcool/greedy-eye/internal/api/models"
	"github.com/foxcool/greedy-eye/internal/api/services"
	"github.com/foxcool/greedy-eye/internal/services/storage/ent"
	"github.com/foxcool/greedy-eye/internal/services/storage/ent/asset"
	"github.com/foxcool/greedy-eye/internal/services/storage/ent/price"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/emptypb"
)

// CreatePrice creates a single price record.
func (s *StorageService) CreatePrice(ctx context.Context, req *services.CreatePriceRequest) (*models.Price, error) {
	if req.Price == nil {
		return nil, status.Errorf(codes.InvalidArgument, "price is required")
	}

	if req.Price.AssetId == "" || req.Price.BaseAssetId == "" || req.Price.SourceId == "" {
		return nil, status.Errorf(codes.InvalidArgument, "asset_id, base_asset_id, and source_id are required")
	}

	assetUUID, err := stringToUUID(req.Price.AssetId)
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "bad asset_id")
	}

	baseUUID, err := stringToUUID(req.Price.BaseAssetId)
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "bad base_asset_id")
	}

	entAsset, err := s.dbClient.Asset.Query().Where(asset.UUID(assetUUID)).Only(ctx)
	if err != nil {
		return nil, status.Errorf(codes.NotFound, "asset not found")
	}

	baseAssetEnt, err := s.dbClient.Asset.Query().Where(asset.UUID(baseUUID)).Only(ctx)
	if err != nil {
		return nil, status.Errorf(codes.NotFound, "base asset not found")
	}

	newPrice := s.dbClient.Price.
		Create().
		SetSourceID(req.Price.SourceId).
		SetAsset(entAsset).
		SetBaseAsset(baseAssetEnt).
		SetInterval(req.Price.Interval).
		SetLast(req.Price.Last).
		SetDecimals(req.Price.Decimals)

	if req.Price.Open != nil {
		newPrice.SetOpen(*req.Price.Open)
	}

	if req.Price.High != nil {
		newPrice.SetHigh(*req.Price.High)
	}

	if req.Price.Low != nil {
		newPrice.SetLow(*req.Price.Low)
	}

	if req.Price.Close != nil {
		newPrice.SetClose(*req.Price.Close)
	}

	if req.Price.Volume != nil {
		newPrice.SetVolume(*req.Price.Volume)
	}

	if req.Price.Timestamp != nil {
		ts := req.Price.Timestamp.AsTime()
		newPrice.SetTimestamp(ts)
	}

	entPrice, err := newPrice.Save(ctx)
	if err != nil {
		s.log.Error("Failed to create price", slog.Any("error",err))
		if ent.IsConstraintError(err) {
			return nil, status.Errorf(codes.AlreadyExists, "price constraint failed: %v", err)
		}
		return nil, status.Errorf(codes.Internal, "failed to create price: %v", err)
	}

	entPrice, err = s.dbClient.Price.Query().Where(price.UUID(entPrice.UUID)).WithAsset().WithBaseAsset().Only(ctx)
	if err != nil {
		s.log.Error("Failed to get created price", slog.Any("error",err))
		return nil, status.Errorf(codes.Internal, "failed to retrieve price: %v", err)
	}

	protoPrice, err := entPriceToProtoPrice(entPrice)
	if err != nil {
		s.log.Error("Failed to convert price to proto", slog.Any("error",err))
		return nil, status.Errorf(codes.Internal, "failed to convert price to proto: %v", err)
	}

	return protoPrice, nil
}

// CreatePrices creates multiple prices in bulk.
func (s *StorageService) CreatePrices(ctx context.Context, req *services.CreatePricesRequest) (*services.CreatePricesResponse, error) {
	count := 0
	for _, p := range req.Prices {
		_, err := s.CreatePrice(ctx, &services.CreatePriceRequest{Price: p})
		if err == nil {
			count++
		}
		// Errors are logged/warned, not failing all
	}
	return &services.CreatePricesResponse{CreatedCount: int32(count)}, nil
}

// GetLatestPrice returns the most recent price for asset/base/source.
func (s *StorageService) GetLatestPrice(ctx context.Context, req *services.GetLatestPriceRequest) (*models.Price, error) {
	if req.AssetId == "" || req.BaseAssetId == "" {
		return nil, status.Errorf(codes.InvalidArgument, "asset_id and base_asset_id required")
	}

	assetUUID, err := stringToUUID(req.AssetId)
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "bad asset_id")
	}
	entAsset, err := s.dbClient.Asset.Query().Where(asset.UUID(assetUUID)).Only(ctx)
	if err != nil {
		return nil, status.Errorf(codes.NotFound, "failed to get asset: %v", err)
	}
	baseAssetUUID, err := stringToUUID(req.BaseAssetId)
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "bad base_asset_id")
	}
	entBaseAsset, err := s.dbClient.Asset.Query().Where(asset.UUID(baseAssetUUID)).Only(ctx)
	if err != nil {
		return nil, status.Errorf(codes.NotFound, "failed to get base asset: %v", err)
	}

	query := s.dbClient.Price.Query().Where(price.AssetID(entAsset.ID), price.BaseAssetID(entBaseAsset.ID))
	if req.SourceId != nil && *req.SourceId != "" {
		query = query.Where(price.SourceID(*req.SourceId))
	}
	// Latest by timestamp desc
	entPrice, err := query.Order(ent.Desc(price.FieldTimestamp)).WithAsset().WithBaseAsset().First(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return nil, status.Error(codes.NotFound, "price not found")
		}
		return nil, status.Errorf(codes.Internal, "failed to get latest price: %v", err)
	}
	return entPriceToProtoPrice(entPrice)
}

// ListPriceHistory returns prices for an asset/base in a time range with pagination.
func (s *StorageService) ListPriceHistory(ctx context.Context, req *services.ListPriceHistoryRequest) (*services.ListPriceHistoryResponse, error) {
	if req.AssetId == "" || req.BaseAssetId == "" {
		return nil, status.Errorf(codes.InvalidArgument, "asset_id and base_asset_id required")
	}

	assetUUID, err := stringToUUID(req.AssetId)
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "bad asset_id")
	}
	entAsset, err := s.dbClient.Asset.Query().Where(asset.UUID(assetUUID)).Only(ctx)
	if err != nil {
		return nil, status.Errorf(codes.NotFound, "failed to get asset: %v", err)
	}
	baseUUID, err := stringToUUID(req.BaseAssetId)
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "bad base_asset_id")
	}
	entBaseAsset, err := s.dbClient.Asset.Query().Where(asset.UUID(baseUUID)).Only(ctx)
	if err != nil {
		return nil, status.Errorf(codes.NotFound, "failed to get base asset: %v", err)
	}
	query := s.dbClient.Price.Query().
		Where(price.AssetID(entAsset.ID), price.BaseAssetID(entBaseAsset.ID)).
		Order(ent.Asc(price.FieldTimestamp)).
		WithAsset().
		WithBaseAsset()

	if req.SourceId != nil && *req.SourceId != "" {
		query = query.Where(price.SourceID(*req.SourceId))
	}
	if req.From != nil {
		query = query.Where(price.TimestampGTE(req.From.AsTime()))
	}
	if req.To != nil {
		query = query.Where(price.TimestampLTE(req.To.AsTime()))
	}

	limit := DefaultPageSize
	if req.PageSize != nil && *req.PageSize > 0 {
		limit = int(*req.PageSize)
	}
	query = query.Limit(limit + 1)

	// Pagination by page_token (base64 encoded timestamp)
	var cursorTs time.Time
	if req.PageToken != nil && *req.PageToken != "" {
		raw, _ := base64.StdEncoding.DecodeString(*req.PageToken)
		if len(raw) > 0 {
			err := cursorTs.UnmarshalText(raw)
			if err == nil {
				query = query.Where(price.TimestampGT(cursorTs))
			}
		}
	}

	entPrices, err := query.All(ctx)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to list price history: %v", err)
	}
	protoPrices := make([]*models.Price, 0, len(entPrices))
	for i, entPr := range entPrices {
		if i == limit {
			break
		}
		pp, err := entPriceToProtoPrice(entPr)
		if err == nil {
			protoPrices = append(protoPrices, pp)
		}
	}
	var nextPageToken string
	if len(entPrices) > limit {
		last := entPrices[limit-1]
		txt, _ := last.Timestamp.MarshalText()
		nextPageToken = base64.StdEncoding.EncodeToString(txt)
	}
	return &services.ListPriceHistoryResponse{
		Prices:        protoPrices,
		NextPageToken: nextPageToken,
	}, nil
}

// ListPricesByInterval aggregates prices in the given interval (stub implementation).
func (s *StorageService) ListPricesByInterval(ctx context.Context, req *services.ListPricesByIntervalRequest) (*services.ListPriceHistoryResponse, error) {
	// Actual timebucket/candle aggregation needs DB-side logic (SQL/TimescaleDB)
	// For MVP — fallback to ListPriceHistory, optionally filter by interval
	return s.ListPriceHistory(ctx, &services.ListPriceHistoryRequest{
		AssetId:     req.AssetId,
		BaseAssetId: req.BaseAssetId,
		From:        req.From,
		To:          req.To,
		SourceId:    req.SourceId,
		PageSize:    req.PageSize,
		PageToken:   req.PageToken,
	})
}

// DeletePrice deletes a price record by ID.
func (s *StorageService) DeletePrice(ctx context.Context, req *services.DeletePriceRequest) (*emptypb.Empty, error) {
	if req.Id == "" {
		return nil, status.Errorf(codes.InvalidArgument, "price ID is required")
	}
	uuidVal, err := stringToUUID(req.Id)
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "bad price ID")
	}
	count, err := s.dbClient.Price.Delete().Where(price.UUID(uuidVal)).Exec(ctx)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to delete price: %v", err)
	}
	if count == 0 {
		return nil, status.Errorf(codes.NotFound, "price with ID %s not found", req.Id)
	}
	return &emptypb.Empty{}, nil
}

// DeletePrices deletes price records by criteria.
func (s *StorageService) DeletePrices(ctx context.Context, req *services.DeletePricesRequest) (*emptypb.Empty, error) {
	query := s.dbClient.Price.Delete()
	if req.AssetId != nil && *req.AssetId != "" {
		assetUUID, err := stringToUUID(*req.AssetId)
		if err != nil {
			return nil, status.Errorf(codes.InvalidArgument, "bad asset_id")
		}
		entAsset, err := s.dbClient.Asset.Query().Where(asset.UUID(assetUUID)).Only(ctx)
		if err != nil {
			return nil, status.Errorf(codes.NotFound, "failed to get asset: %v", err)
		}
		query = query.Where(price.AssetID(entAsset.ID))
	}
	if req.BaseAssetId != nil && *req.BaseAssetId != "" {
		baseUUID, err := stringToUUID(*req.BaseAssetId)
		if err != nil {
			return nil, status.Errorf(codes.InvalidArgument, "bad base_asset_id")
		}
		entBaseAsset, err := s.dbClient.Asset.Query().Where(asset.UUID(baseUUID)).Only(ctx)
		if err != nil {
			return nil, status.Errorf(codes.NotFound, "failed to get base asset: %v", err)
		}
		query = query.Where(price.BaseAssetID(entBaseAsset.ID))
	}
	if req.From != nil {
		query = query.Where(price.TimestampGTE(req.From.AsTime()))
	}
	if req.To != nil {
		query = query.Where(price.TimestampLTE(req.To.AsTime()))
	}
	if req.SourceId != nil && *req.SourceId != "" {
		query = query.Where(price.SourceID(*req.SourceId))
	}
	count, err := query.Exec(ctx)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to delete prices: %v", err)
	}
	if count == 0 {
		return nil, status.Errorf(codes.NotFound, "no prices matching criteria found")
	}
	return &emptypb.Empty{}, nil
}
