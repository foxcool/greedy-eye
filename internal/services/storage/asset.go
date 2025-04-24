package storage

import (
	"context"
	"encoding/base64"
	"encoding/json"

	"entgo.io/ent/dialect/sql"
	"github.com/foxcool/greedy-eye/internal/api/models"
	"github.com/foxcool/greedy-eye/internal/api/services"
	"github.com/foxcool/greedy-eye/internal/services/storage/ent"
	"github.com/foxcool/greedy-eye/internal/services/storage/ent/asset"
	"github.com/foxcool/greedy-eye/internal/services/storage/ent/predicate"
	"go.uber.org/zap"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/emptypb"
)

func (s *StorageService) CreateAsset(ctx context.Context, req *services.CreateAssetRequest) (*models.Asset, error) {
	if req.Asset == nil {
		return nil, status.Errorf(codes.InvalidArgument, "asset information is required")
	}

	// Validate input data (basic)
	if req.Asset.Name == "" || req.Asset.Type == models.AssetType_ASSET_TYPE_UNSPECIFIED {
		return nil, status.Errorf(codes.InvalidArgument, "asset name and type are required")
	}

	entAssetType, err := protoAssetTypeToEnt(req.Asset.Type)
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid asset type: %v", req.Asset.Type)
	}

	var symbol string
	if req.Asset.Symbol != nil {
		symbol = *req.Asset.Symbol
	}

	createdEntAsset, err := s.dbClient.Asset.
		Create().
		SetSymbol(symbol).
		SetName(req.Asset.Name).
		SetType(entAssetType).
		SetTags(req.Asset.Tags).
		Save(ctx)
	if err != nil {
		s.log.Error("Failed to create asset", zap.Error(err))

		if ent.IsConstraintError(err) {
			return nil, status.Errorf(codes.AlreadyExists, "asset creation constraint failed: %v", err)
		}
		return nil, status.Errorf(codes.Internal, "failed to create asset: %v", err)
	}

	protoAsset, err := entAssetToProtoAsset(createdEntAsset)
	if err != nil {
		s.log.Error("Failed to convert asset to proto", zap.Error(err))
		return nil, status.Errorf(codes.Internal, "failed to convert asset to proto: %v", err)
	}

	s.log.Info("Asset created successfully", zap.String("uuid", createdEntAsset.UUID.String()))
	return protoAsset, nil
}

func (s *StorageService) GetAsset(ctx context.Context, req *services.GetAssetRequest) (*models.Asset, error) {
	if req.Id == "" {
		return nil, status.Errorf(codes.InvalidArgument, "asset ID is required")
	}

	parsedUUID, err := stringToUUID(req.Id)
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid asset ID format: %v", err)
	}

	entAsset, err := s.dbClient.Asset.
		Query().
		Where(asset.UUID(parsedUUID)).
		Only(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			s.log.Warn("Asset not found", zap.String("uuid", req.Id))
			return nil, status.Errorf(codes.NotFound, "asset with ID %s not found", req.Id)
		}
		s.log.Error("Failed to get asset", zap.String("uuid", req.Id), zap.Error(err))
		return nil, status.Errorf(codes.Internal, "failed to retrieve asset: %v", err)
	}

	protoAsset, err := entAssetToProtoAsset(entAsset)
	if err != nil {
		s.log.Error("Failed to convert asset to proto", zap.Error(err))
		return nil, status.Errorf(codes.Internal, "failed to convert asset to proto: %v", err)
	}

	return protoAsset, nil
}

func (s *StorageService) UpdateAsset(ctx context.Context, req *services.UpdateAssetRequest) (*models.Asset, error) {
	if req.Asset == nil || req.Asset.Id == "" {
		return nil, status.Errorf(codes.InvalidArgument, "asset with ID is required")
	}
	if req.UpdateMask == nil || len(req.UpdateMask.Paths) == 0 {
		return nil, status.Errorf(codes.InvalidArgument, "update mask is required for update operation")
	}

	parsedUUID, err := stringToUUID(req.Asset.Id)
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid asset ID format: %v", err)
	}

	entAsset, err := s.dbClient.Asset.Query().Where(asset.UUID(parsedUUID)).Only(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			s.log.Warn("Asset not found", zap.String("uuid", req.Asset.Id))
			return nil, status.Errorf(codes.NotFound, "asset with ID %s not found for update", req.Asset.Id)
		}
		s.log.Error("Failed to get asset", zap.String("uuid", req.Asset.Id), zap.Error(err))
		return nil, status.Errorf(codes.Internal, "failed to retrieve asset: %v", err)
	}

	mutation := entAsset.Update()
	// Apply changes based on FieldMask
	for _, path := range req.UpdateMask.Paths {
		switch path {
		case "symbol":
			if req.Asset.Symbol == nil {
				return nil, status.Errorf(codes.InvalidArgument, "symbol cannot be empty if included in mask")
			}
			mutation.SetSymbol(*req.Asset.Symbol)
		case "name":
			if req.Asset.Name == "" {
				return nil, status.Errorf(codes.InvalidArgument, "name cannot be empty if included in mask")
			}
			mutation.SetName(req.Asset.Name)
		case "type":
			entAssetType, err := protoAssetTypeToEnt(req.Asset.Type)
			if err != nil {
				return nil, status.Errorf(codes.InvalidArgument, "invalid asset type provided in mask: %v", req.Asset.Type)
			}
			mutation.SetType(entAssetType)
		case "tags":
			mutation.SetTags(req.Asset.Tags)
		default:
			s.log.Warn("UpdateAsset requested with unknown field in mask", zap.String("path", path))
		}
	}
	if _, err := mutation.Save(ctx); err != nil {
		s.log.Error("Failed to update asset", zap.String("uuid", req.Asset.Id), zap.Error(err))
		return nil, status.Errorf(codes.Internal, "failed to update asset: %v", err)
	}
	entAsset, err = s.dbClient.Asset.Query().Where(asset.UUID(parsedUUID)).Only(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			s.log.Error("Asset not found", zap.String("uuid", req.Asset.Id))
			return nil, status.Errorf(codes.NotFound, "asset with ID %s not found after update", req.Asset.Id)
		}
		s.log.Error("Failed to get asset after update", zap.String("uuid", req.Asset.Id), zap.Error(err))
		return nil, status.Errorf(codes.Internal, "failed to retrieve asset: %v", err)
	}
	protoAsset, err := entAssetToProtoAsset(entAsset)
	if err != nil {
		s.log.Error("Failed to convert asset to proto after update", zap.String("uuid", req.Asset.Id), zap.Error(err))
		return nil, status.Errorf(codes.Internal, "failed to convert asset to proto: %v", err)
	}

	return protoAsset, nil
}

func (s *StorageService) DeleteAsset(ctx context.Context, req *services.DeleteAssetRequest) (*emptypb.Empty, error) {
	if req.Id == "" {
		return nil, status.Errorf(codes.InvalidArgument, "asset ID is required")
	}

	parsedUUID, err := stringToUUID(req.Id)
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid asset ID format: %v", err)
	}

	deletedCount, err := s.dbClient.Asset.
		Delete().
		Where(asset.UUID(parsedUUID)).
		Exec(ctx)
	if err != nil {
		// Handle constraint error
		if ent.IsConstraintError(err) {
			s.log.Error("Failed to delete asset due to constraint", zap.String("uuid", req.Id), zap.Error(err))
			return nil, status.Errorf(codes.FailedPrecondition, "cannot delete asset due to existing dependencies: %v", err)
		}
		s.log.Error("Failed to delete asset", zap.String("uuid", req.Id), zap.Error(err))
		return nil, status.Errorf(codes.Internal, "failed to delete asset: %v", err)
	}

	if deletedCount == 0 {
		s.log.Warn("Attempted to delete non-existent asset", zap.String("uuid", req.Id))
		return nil, status.Errorf(codes.NotFound, "asset with ID %s not found", req.Id)
	}

	s.log.Info("Asset deleted successfully", zap.String("uuid", req.Id))
	return &emptypb.Empty{}, nil
}

func (s *StorageService) ListAssets(ctx context.Context, req *services.ListAssetsRequest) (*services.ListAssetsResponse, error) {
	// Set limit of page size
	limit := DefaultPageSize
	if req.PageSize != nil && *req.PageSize > 0 {
		limit = int(*req.PageSize)
	}
	// Create query
	// limit + 1 - is needed to detect last page
	query := s.dbClient.Asset.Query().Order(ent.Asc(asset.FieldUUID)).Limit(limit + 1)
	// Decode cursor
	var cursorUUID string
	if req.PageToken != nil && *req.PageToken != "" {
		decodedCursor, err := base64.StdEncoding.DecodeString(*req.PageToken)
		if err != nil {
			return nil, status.Errorf(codes.InvalidArgument, "invalid page_token: %v", err)
		}
		cursorUUID = string(decodedCursor)
	}
	// Apply cursor filtering
	if cursorUUID != "" {
		uuidVal, err := stringToUUID(cursorUUID)
		if err != nil {
			return nil, status.Errorf(codes.InvalidArgument, "invalid cursor UUID: %v", err)
		}
		query = query.Where(asset.UUIDGT(uuidVal))
	}
	// Apply tags filtering: asset must have all tags of the request
	if len(req.Tags) > 0 {
		query = query.Where(assetTagsContain(req.Tags))
	}

	assets, err := query.All(ctx)
	if err != nil {
		s.log.Error("Failed to list assets", zap.Error(err))
		return nil, status.Errorf(codes.Internal, "failed to list assets: %v", err)
	}

	protoAssets := make([]*models.Asset, 0, len(assets))
	for i, entAsset := range assets {
		// paginate over limit+1 for next page check
		if i == limit {
			break
		}
		protoAsset, err := entAssetToProtoAsset(entAsset)
		if err != nil {
			s.log.Error("Failed to convert asset to proto", zap.String("uuid", entAsset.UUID.String()), zap.Error(err))
			continue
		}
		protoAssets = append(protoAssets, protoAsset)
	}

	var nextPageToken string
	if len(assets) > limit {
		lastAsset := assets[limit-1]
		nextPageToken = base64.StdEncoding.EncodeToString([]byte(lastAsset.UUID.String()))
	}

	return &services.ListAssetsResponse{
		Assets:        protoAssets,
		NextPageToken: nextPageToken,
	}, nil
}

func assetTagsContain(tags []string) predicate.Asset {
	jsonTags, err := json.Marshal(tags)
	if err != nil {
		// This should ideally not happen for a []string.
		// Return a predicate that always evaluates to false in case of an error.
		return predicate.Asset(func(s *sql.Selector) {
			s.Where(sql.False()) // Ensures the query returns no results on marshalling error
		})
	}

	// Return the custom predicate using sql.P
	return predicate.Asset(func(s *sql.Selector) {
		s.Where(sql.P(func(b *sql.Builder) {
			b.Ident(asset.FieldTags). // Target the 'tags' column
							WriteString(" @> ").   // Use the JSONB contains operator
							Arg(string(jsonTags)). // Pass the JSON array string as an argument
							WriteString("::jsonb") // Explicitly cast the argument to jsonb
		}))
	})
}
