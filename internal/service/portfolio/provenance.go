package portfolio

import (
	"context"
	"errors"
	"fmt"

	"connectrpc.com/connect"
	"github.com/google/uuid"

	apiv1 "github.com/foxcool/greedy-eye/api/v1"
	"github.com/foxcool/greedy-eye/internal/middleware"
)

// maxUnsolicitedAssetIDs bounds one provenance lookup.
const maxUnsolicitedAssetIDs = 1000

// ListUnsolicitedAssets answers identity scoring in a marketdata process
// that cannot read holdings itself.
func (h *Handler) ListUnsolicitedAssets(
	ctx context.Context,
	req *connect.Request[apiv1.ListUnsolicitedAssetsRequest],
) (*connect.Response[apiv1.ListUnsolicitedAssetsResponse], error) {
	if err := requireServiceOrAdmin(ctx); err != nil {
		return nil, err
	}
	ids := req.Msg.AssetIds
	if len(ids) > maxUnsolicitedAssetIDs {
		return nil, connect.NewError(connect.CodeInvalidArgument,
			fmt.Errorf("at most %d asset_ids per request", maxUnsolicitedAssetIDs))
	}
	for _, id := range ids {
		if _, err := uuid.Parse(id); err != nil {
			return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("asset_id %q is not a UUID", id))
		}
	}
	m, err := h.store.UnsolicitedAssets(ctx, ids)
	if err != nil {
		return nil, toConnectError(err)
	}
	return connect.NewResponse(&apiv1.ListUnsolicitedAssetsResponse{Unsolicited: m}), nil
}

// requireServiceOrAdmin admits a sibling service with a valid token, which
// may come without a user and without SyncAuthority (the nightly rescore),
// and an admin.
func requireServiceOrAdmin(ctx context.Context) error {
	if middleware.ServiceCaller(ctx) {
		return nil
	}
	user, ok := middleware.UserFromContext(ctx)
	if !ok {
		return connect.NewError(connect.CodeUnauthenticated, errors.New("user not found in context"))
	}
	if !user.IsAdmin() {
		return connect.NewError(connect.CodePermissionDenied, errors.New("holding provenance is for services and admins"))
	}
	return nil
}
