//go:build smoke

package smoke_test

import (
	"context"
	"net/http"
	"testing"

	"connectrpc.com/connect"
	v1 "github.com/foxcool/greedy-eye/internal/api/v1"
	"github.com/foxcool/greedy-eye/internal/api/v1/apiv1connect"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/fieldmaskpb"
)

func TestAssetCRUD(t *testing.T) {
	resetDB(t)
	ctx := context.Background()
	client := newMDClient(smokeTestUserID)

	sym := "BTC"

	// Create
	createResp, err := client.CreateAsset(ctx, connect.NewRequest(&v1.CreateAssetRequest{
		Asset: &v1.Asset{
			Name:   "Bitcoin",
			Symbol: &sym,
			Type:   v1.AssetType_ASSET_TYPE_CRYPTOCURRENCY,
			Tags:   []string{"crypto", "pow"},
		},
	}))
	require.NoError(t, err)
	btcID := createResp.Msg.GetId()
	require.NotEmpty(t, btcID)
	assert.Equal(t, "Bitcoin", createResp.Msg.GetName())
	assert.Equal(t, "BTC", createResp.Msg.GetSymbol())

	// Get
	getResp, err := client.GetAsset(ctx, connect.NewRequest(&v1.GetAssetRequest{Id: btcID}))
	require.NoError(t, err)
	assert.Equal(t, btcID, getResp.Msg.GetId())
	assert.Equal(t, "Bitcoin", getResp.Msg.GetName())

	// Update name via UpdateMask
	updatedName := "Bitcoin (Updated)"
	updateResp, err := client.UpdateAsset(ctx, connect.NewRequest(&v1.UpdateAssetRequest{
		Asset: &v1.Asset{
			Id:   btcID,
			Name: updatedName,
		},
		UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"name"}},
	}))
	require.NoError(t, err)
	assert.Equal(t, updatedName, updateResp.Msg.GetName())

	// List — create two more, then paginate
	ethSym := "ETH"
	_, err = client.CreateAsset(ctx, connect.NewRequest(&v1.CreateAssetRequest{
		Asset: &v1.Asset{Name: "Ethereum", Symbol: &ethSym, Type: v1.AssetType_ASSET_TYPE_CRYPTOCURRENCY},
	}))
	require.NoError(t, err)

	solSym := "SOL"
	_, err = client.CreateAsset(ctx, connect.NewRequest(&v1.CreateAssetRequest{
		Asset: &v1.Asset{Name: "Solana", Symbol: &solSym, Type: v1.AssetType_ASSET_TYPE_CRYPTOCURRENCY},
	}))
	require.NoError(t, err)

	pageSize := int32(2)
	listResp, err := client.ListAssets(ctx, connect.NewRequest(&v1.ListAssetsRequest{
		PageSize: &pageSize,
	}))
	require.NoError(t, err)
	assert.Len(t, listResp.Msg.GetAssets(), 2)
	assert.NotEmpty(t, listResp.Msg.GetNextPageToken(), "expect more pages")

	// Delete
	_, err = client.DeleteAsset(ctx, connect.NewRequest(&v1.DeleteAssetRequest{Id: btcID}))
	require.NoError(t, err)

	// Get deleted — expect NotFound
	_, err = client.GetAsset(ctx, connect.NewRequest(&v1.GetAssetRequest{Id: btcID}))
	require.Error(t, err)
	var connectErr *connect.Error
	require.ErrorAs(t, err, &connectErr)
	assert.Equal(t, connect.CodeNotFound, connectErr.Code())
}

func TestAssetCRUD_NoUserHeader(t *testing.T) {
	resetDB(t)
	ctx := context.Background()

	sym := "BTC"
	bare := apiv1connect.NewMarketDataServiceClient(http.DefaultClient, serverURL)
	_, err := bare.CreateAsset(ctx, connect.NewRequest(&v1.CreateAssetRequest{
		Asset: &v1.Asset{Name: "Bitcoin", Symbol: &sym, Type: v1.AssetType_ASSET_TYPE_CRYPTOCURRENCY},
	}))
	require.Error(t, err)
	var connectErr *connect.Error
	require.ErrorAs(t, err, &connectErr)
	assert.Equal(t, connect.CodeUnauthenticated, connectErr.Code())
}
