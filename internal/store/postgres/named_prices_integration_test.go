//go:build integration

package postgres

import (
	"context"
	"log/slog"
	"testing"

	"connectrpc.com/connect"
	apiv1 "github.com/foxcool/greedy-eye/api/v1"
	"github.com/foxcool/greedy-eye/internal/entity"
	"github.com/foxcool/greedy-eye/internal/middleware"
	"github.com/foxcool/greedy-eye/internal/service/marketdata"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// askedProvider records what it is asked for and prices nothing.
type askedProvider struct {
	market string
	asked  []string
}

func (p *askedProvider) FetchPrices(_ context.Context, assets []*entity.Asset) ([]entity.StoredPrice, error) {
	for _, a := range assets {
		p.asked = append(p.asked, a.ID)
	}
	return nil, nil
}

func (p *askedProvider) BaseAssetSymbol() string         { return "USD" }
func (p *askedProvider) BaseAssetType() entity.AssetType { return entity.AssetTypeForex }
func (p *askedProvider) Binding() entity.PriceBinding {
	return entity.PriceBinding{Markets: []string{p.market}}
}

// A mistyped id that is not even a uuid is named back like an id with no
// asset, and the asset named next to it is still priced. Cast into the query,
// it failed the whole call.
func TestFetchExternalPrices_NonUUIDIDDoesNotFailTheCall(t *testing.T) {
	s := NewMarketDataStore(getTestPool(t))
	real := createTestAsset(t, s, "NonUUIDNeighbour")

	p := &askedProvider{market: entity.NormalizeMarket(real.Market)}
	h := marketdata.NewHandler(s, slog.New(slog.DiscardHandler)).WithProvider("probe", p)

	resp, err := h.FetchExternalPrices(middleware.WithSyncAuthority(context.Background()),
		connect.NewRequest(&apiv1.FetchExternalPricesRequest{AssetIds: []string{real.ID, "not-a-uuid"}}))
	require.NoError(t, err)
	assert.Contains(t, resp.Msg.GetErrors(), "unknown asset_ids: not-a-uuid")
	assert.Equal(t, []string{real.ID}, p.asked)
}

// Only ids that are not uuids match nothing; they must not drop the filter and
// read the whole catalogue.
func TestListAssets_NonUUIDIDsMatchNothing(t *testing.T) {
	s := NewMarketDataStore(getTestPool(t))
	ctx := context.Background()
	want := createTestAsset(t, s, "NonUUIDWanted")
	createTestAsset(t, s, "NonUUIDOther")

	got, _, err := s.ListAssets(ctx, marketdata.ListAssetsOpts{IDs: []string{"not-a-uuid", want.ID}})
	require.NoError(t, err)
	assert.Equal(t, []string{want.ID}, assetIDs(got))

	// uuid.Parse also takes the urn form, which the uuid cast does not.
	got, _, err = s.ListAssets(ctx, marketdata.ListAssetsOpts{IDs: []string{"urn:uuid:" + want.ID}})
	require.NoError(t, err)
	assert.Equal(t, []string{want.ID}, assetIDs(got))

	got, next, err := s.ListAssets(ctx, marketdata.ListAssetsOpts{IDs: []string{"not-a-uuid"}})
	require.NoError(t, err)
	assert.Empty(t, got)
	assert.Empty(t, next)
}
