package portfolio

import (
	"context"
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
	answer := map[string]bool{testAssetID: true, testHoldingID: false}

	s := new(mockStore)
	s.On("UnsolicitedAssets", mock.Anything, ids).Return(answer, nil)
	h := newHandler(s)

	resp, err := asServiceCaller(t, h, ids)
	require.NoError(t, err)
	// A bought asset must arrive as false, not vanish: absent means held by nobody.
	assert.Equal(t, answer, resp.Msg.Unsolicited)

	resp, err = h.ListUnsolicitedAssets(ctxWithAdmin(testUserID),
		connect.NewRequest(&apiv1.ListUnsolicitedAssetsRequest{AssetIds: ids}))
	require.NoError(t, err)
	assert.Equal(t, answer, resp.Msg.Unsolicited)
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

func TestListUnsolicitedAssets_RejectsBadIDs(t *testing.T) {
	h := newHandler(new(mockStore))

	_, err := asServiceCaller(t, h, slices.Repeat([]string{testAssetID}, maxUnsolicitedAssetIDs+1))
	assert.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))

	_, err = asServiceCaller(t, h, []string{"not-a-uuid"})
	assert.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))
}
