package marketdata

import (
	"context"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/foxcool/greedy-eye/internal/entity"
	"github.com/foxcool/greedy-eye/internal/store"
)

func TestRescoreAssets(t *testing.T) {
	ctx := context.Background()

	assets := []*entity.Asset{
		{ID: "11111111-1111-1111-1111-111111111111", Symbol: "BTC", Name: "Bitcoin"},
		{ID: "22222222-2222-2222-2222-222222222222", Symbol: "AAVE-SR", Name: "VISIT [AAVE-SR.XYZ] AND CLAIM SPECIAL REWARDS"},
		// A user verdict is terminal: this row must be skipped entirely.
		{ID: "33333333-3333-3333-3333-333333333333", Symbol: "USDT", Name: "Tether", VerdictSource: "user:42"},
	}

	s := &mockStore{}
	s.On("ListAssets", ctx, mock.Anything).Return(assets, "", nil).Once()
	s.On("FindTickerIncumbent", ctx, mock.Anything).Return("", store.ErrNotFound).Maybe()

	// BTC resolves a price (listed); the scam does not (unlisted signal).
	s.On("GetLatestPrice", ctx, assets[0].ID, "", "").Return(&entity.StoredPrice{}, nil).Once()
	s.On("GetLatestPrice", ctx, assets[1].ID, "", "").Return(nil, store.ErrNotFound).Once()

	s.On("SetAssetVerdict", ctx, assets[0].ID, "legit", mock.Anything, mock.Anything, rescoreVerdictSource).Return(true, nil).Once()
	s.On("SetAssetVerdict", ctx, assets[1].ID, "scam", mock.Anything, mock.Anything, rescoreVerdictSource).Return(true, nil).Once()

	h := NewHandler(s, slog.Default())
	report, err := h.RescoreAssets(ctx)
	require.NoError(t, err)

	assert.Equal(t, 2, report.Scored, "user-verdict asset must not be scored")
	assert.Equal(t, 2, report.Written)
	assert.Equal(t, 1, report.ByVerdict["scam"])
	assert.Equal(t, 1, report.ByVerdict["legit"])
	require.Len(t, report.Flagged, 1)
	assert.Equal(t, assets[1].ID, report.Flagged[0].AssetID)
	assert.Equal(t, "scam", report.Flagged[0].Verdict)

	// The skipped user-verdict asset triggered neither a price lookup nor a write.
	s.AssertNotCalled(t, "GetLatestPrice", ctx, assets[2].ID, "", "")
	s.AssertNotCalled(t, "SetAssetVerdict", ctx, assets[2].ID, mock.Anything, mock.Anything, mock.Anything, mock.Anything)
	s.AssertExpectations(t)
}

// TestRescoreAssets_UserVerdictPreserved documents that a machine write which
// loses to a user verdict at the store (written=false) is not counted as
// changed, even though the asset was scored.
func TestRescoreAssets_WrittenCountReflectsStore(t *testing.T) {
	ctx := context.Background()
	assets := []*entity.Asset{
		{ID: "11111111-1111-1111-1111-111111111111", Symbol: "BTC", Name: "Bitcoin"},
	}
	s := &mockStore{}
	s.On("ListAssets", ctx, mock.Anything).Return(assets, "", nil).Once()
	s.On("GetLatestPrice", ctx, assets[0].ID, "", "").Return(&entity.StoredPrice{}, nil).Once()
	s.On("FindTickerIncumbent", ctx, mock.Anything).Return("", store.ErrNotFound).Maybe()
	s.On("SetAssetVerdict", ctx, assets[0].ID, "legit", mock.Anything, mock.Anything, rescoreVerdictSource).Return(false, nil).Once()

	h := NewHandler(s, slog.Default())
	report, err := h.RescoreAssets(ctx)
	require.NoError(t, err)
	assert.Equal(t, 1, report.Scored)
	assert.Equal(t, 0, report.Written)
	s.AssertExpectations(t)
}
