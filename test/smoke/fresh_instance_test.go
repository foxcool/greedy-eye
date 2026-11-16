//go:build smoke

package smoke_test

import (
	"context"
	"testing"

	"connectrpc.com/connect"
	v1 "github.com/foxcool/greedy-eye/api/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// On an empty catalogue nothing has created the display currency yet: no price
// has landed. A new user's first portfolio still has to value, and the
// dashboard's heatmap still has to load, instead of both answering
// "resolve asset USD: not found".
func TestFreshInstanceValuesAnEmptyPortfolio(t *testing.T) {
	resetDB(t)
	ctx := context.Background()
	pf, err := newPortfolioClient(smokeTestUserID).CreatePortfolio(ctx, connect.NewRequest(&v1.CreatePortfolioRequest{
		Portfolio: &v1.Portfolio{Name: "First"},
	}))
	require.NoError(t, err)

	value, err := newPortfolioClient(smokeTestUserID).CalculatePortfolioValue(ctx, connect.NewRequest(&v1.CalculatePortfolioValueRequest{
		PortfolioId: pf.Msg.GetId(),
	}))
	require.NoError(t, err, "an empty portfolio on an empty catalogue must value")
	assert.Equal(t, "0", value.Msg.GetTotalValueAmount())
	assert.NotEmpty(t, value.Msg.GetQuoteAssetId(), "the total is expressed in a currency the catalogue now holds")

	heatmap, err := newAnalyticsClient(smokeTestUserID).GetHeatmap(ctx, connect.NewRequest(&v1.GetHeatmapRequest{
		Scope: v1.HeatmapScope_HEATMAP_SCOPE_BALANCE,
	}))
	require.NoError(t, err, "the dashboard heatmap must load on a fresh instance")
	assert.Empty(t, heatmap.Msg.GetNodes())
}
