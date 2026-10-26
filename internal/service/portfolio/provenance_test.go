package portfolio

import (
	"context"
	"maps"
	"slices"
	"testing"

	"connectrpc.com/connect"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	apiv1 "github.com/foxcool/greedy-eye/api/v1"
	"github.com/foxcool/greedy-eye/internal/middleware"
)

// asServiceCaller runs a call the way a sibling's background job arrives:
// a valid token, no user, no SyncAuthority.
func asServiceCaller(t *testing.T, h *Handler, ids []string) (*connect.Response[apiv1.ListUnsolicitedAssetsResponse], error) {
	t.Helper()
	const token = "sibling-token"
	req := connect.NewRequest(&apiv1.ListUnsolicitedAssetsRequest{AssetIds: ids})
	req.Header().Set(middleware.HeaderServiceToken, token)
	call := middleware.ServiceIdentityInterceptor(token)(func(ctx context.Context, r connect.AnyRequest) (connect.AnyResponse, error) {
		return h.ListUnsolicitedAssets(ctx, r.(*connect.Request[apiv1.ListUnsolicitedAssetsRequest]))
	})
	resp, err := call(context.Background(), req)
	if err != nil {
		return nil, err
	}
	return resp.(*connect.Response[apiv1.ListUnsolicitedAssetsResponse]), nil
}

func TestListUnsolicitedAssets_ServiceAndAdminGetStoreAnswer(t *testing.T) {
	ids := []string{testAssetID, testHoldingID}
	want := map[string]bool{testAssetID: true, testHoldingID: false}

	s := new(mockStore)
	// A copy, so an entry dropped in place cannot vanish from want as well.
	s.On("UnsolicitedAssets", mock.Anything, ids).Return(maps.Clone(want), nil)
	h := newHandler(s)

	resp, err := asServiceCaller(t, h, ids)
	require.NoError(t, err)
	// A false entry must arrive, not vanish: absent means held by nobody.
	assert.Equal(t, want, resp.Msg.Unsolicited)

	resp, err = h.ListUnsolicitedAssets(ctxWithAdmin(testUserID),
		connect.NewRequest(&apiv1.ListUnsolicitedAssetsRequest{AssetIds: ids}))
	require.NoError(t, err)
	assert.Equal(t, want, resp.Msg.Unsolicited)
}

func TestListUnsolicitedAssets_RegularUserDenied(t *testing.T) {
	h := newHandler(new(mockStore))
	req := connect.NewRequest(&apiv1.ListUnsolicitedAssetsRequest{AssetIds: []string{testAssetID}})

	_, err := h.ListUnsolicitedAssets(ctxWithUser(testUserID), req)
	assert.Equal(t, connect.CodePermissionDenied, connect.CodeOf(err))

	// In-process SyncAuthority is not a service identity.
	_, err = h.ListUnsolicitedAssets(middleware.WithSyncAuthority(ctxWithUser(testUserID)), req)
	assert.Equal(t, connect.CodePermissionDenied, connect.CodeOf(err))

	_, err = h.ListUnsolicitedAssets(context.Background(), req)
	assert.Equal(t, connect.CodeUnauthenticated, connect.CodeOf(err))
}

func TestListUnsolicitedAssets_IDLimitAndFormat(t *testing.T) {
	full := slices.Repeat([]string{testAssetID}, maxUnsolicitedAssetIDs)
	s := new(mockStore)
	s.On("UnsolicitedAssets", mock.Anything, full).Return(map[string]bool{}, nil)
	h := newHandler(s)

	_, err := asServiceCaller(t, h, full)
	require.NoError(t, err)

	_, err = asServiceCaller(t, h, append(full, testAssetID))
	assert.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))

	_, err = asServiceCaller(t, h, []string{"not-a-uuid"})
	assert.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))
}
