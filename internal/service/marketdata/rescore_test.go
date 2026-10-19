package marketdata

import (
	"context"
	"log/slog"
	"testing"

	"connectrpc.com/connect"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	apiv1 "github.com/foxcool/greedy-eye/api/v1"
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

type stubProvenance struct {
	unsolicited map[string]bool
	err         error
}

func (p stubProvenance) UnsolicitedAssets(_ context.Context, _ []string) (map[string]bool, error) {
	return p.unsolicited, p.err
}

// TestRescoreAssets_Unsolicited: an unlisted asset nobody ever bought is
// queued for review, not condemned; the same asset bought once stays legit,
// and one nobody holds is not judged on provenance at all.
func TestRescoreAssets_Unsolicited(t *testing.T) {
	ctx := context.Background()
	airdrop := &entity.Asset{ID: "11111111-1111-1111-1111-111111111111", Symbol: "MNEP", Name: "Minereum Polygon"}
	bought := &entity.Asset{ID: "22222222-2222-2222-2222-222222222222", Symbol: "MNEP", Name: "Minereum Polygon"}
	unheld := &entity.Asset{ID: "33333333-3333-3333-3333-333333333333", Symbol: "MNEP", Name: "Minereum Polygon"}

	verdicts := map[string]string{}
	signals := map[string]map[string]float64{}
	s := &mockStore{}
	s.On("ListAssets", ctx, mock.Anything).Return([]*entity.Asset{airdrop, bought, unheld}, "", nil).Once()
	s.On("FindTickerIncumbent", ctx, mock.Anything).Return("", store.ErrNotFound).Maybe()
	s.On("GetLatestPrice", ctx, mock.Anything, "", "").Return(nil, store.ErrNotFound)
	s.On("SetAssetVerdict", ctx, mock.Anything, mock.Anything, mock.Anything, mock.Anything, rescoreVerdictSource).
		Run(func(args mock.Arguments) {
			verdicts[args.String(1)] = args.String(2)
			signals[args.String(1)] = args.Get(4).(map[string]float64)
		}).Return(true, nil)

	h := NewHandler(s, slog.Default()).WithHoldingProvenance(stubProvenance{
		unsolicited: map[string]bool{airdrop.ID: true, bought.ID: false},
	})
	_, err := h.RescoreAssets(ctx)
	require.NoError(t, err)

	assert.Equal(t, "suspect", verdicts[airdrop.ID])
	assert.Contains(t, signals[airdrop.ID], "unsolicited")
	assert.Equal(t, "legit", verdicts[bought.ID])
	assert.NotContains(t, signals[bought.ID], "unsolicited")
	assert.Equal(t, "legit", verdicts[unheld.ID])
	assert.NotContains(t, signals[unheld.ID], "unsolicited")
}

// TestRescoreAssets_ProvenanceFailureJudgesNothing: a failed lookup leaves the
// signal unjudged rather than failing the pass or guessing.
func TestRescoreAssets_ProvenanceFailureJudgesNothing(t *testing.T) {
	ctx := context.Background()
	a := &entity.Asset{ID: "11111111-1111-1111-1111-111111111111", Symbol: "MNEP", Name: "Minereum Polygon"}

	var got map[string]float64
	s := &mockStore{}
	s.On("ListAssets", ctx, mock.Anything).Return([]*entity.Asset{a}, "", nil).Once()
	s.On("FindTickerIncumbent", ctx, mock.Anything).Return("", store.ErrNotFound).Maybe()
	s.On("GetLatestPrice", ctx, a.ID, "", "").Return(nil, store.ErrNotFound)
	s.On("SetAssetVerdict", ctx, a.ID, "legit", mock.Anything, mock.Anything, rescoreVerdictSource).
		Run(func(args mock.Arguments) { got = args.Get(4).(map[string]float64) }).Return(true, nil)

	h := NewHandler(s, slog.Default()).WithHoldingProvenance(stubProvenance{err: assert.AnError})
	_, err := h.RescoreAssets(ctx)
	require.NoError(t, err)
	assert.NotContains(t, got, "unsolicited")
	s.AssertExpectations(t)
}

// TestFindOrCreateAsset_IntakeScoresStoredState: a sync re-scoring a known asset
// sees the same listing and provenance the nightly rescore does, so the hourly
// sync does not undo the nightly verdict. A just-created asset is not asked:
// nothing could have priced or bought it yet.
func TestFindOrCreateAsset_IntakeScoresStoredState(t *testing.T) {
	newStore := func() (*mockStore, *string) {
		var verdict string
		s := &mockStore{}
		s.On("FindTickerIncumbent", mock.Anything, mock.Anything).Return("", store.ErrNotFound).Maybe()
		s.On("GetLatestPrice", mock.Anything, mock.Anything, "", "").Return(nil, store.ErrNotFound).Maybe()
		s.On("SetAssetVerdict", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, rescoreVerdictSource).
			Run(func(args mock.Arguments) { verdict = args.String(2) }).Return(true, nil)
		return s, &verdict
	}
	provenance := stubProvenance{unsolicited: map[string]bool{"id-1": true, "id-2": true}}

	existing, verdict := newStore()
	existing.On("FindAssetByIdentity", mock.Anything, "BTC", "crypto", entity.AssetTypeCryptocurrency).
		Return(testAsset("id-1"), nil)
	_, err := newHandler(existing).WithHoldingProvenance(provenance).
		FindOrCreateAsset(syncContext(), connect.NewRequest(&apiv1.FindOrCreateAssetRequest{Symbol: "BTC"}))
	require.NoError(t, err)
	assert.Equal(t, "suspect", *verdict, "unlisted and never bought")

	created, verdict := newStore()
	created.On("FindAssetByIdentity", mock.Anything, "NEW", "crypto", entity.AssetTypeCryptocurrency).
		Return(nil, store.ErrNotFound)
	created.On("CreateAsset", mock.Anything, mock.Anything).Return(testAsset("id-2"), nil)
	_, err = newHandler(created).WithHoldingProvenance(provenance).
		FindOrCreateAsset(syncContext(), connect.NewRequest(&apiv1.FindOrCreateAssetRequest{Symbol: "NEW"}))
	require.NoError(t, err)
	assert.Equal(t, "legit", *verdict)
	created.AssertNotCalled(t, "GetLatestPrice", mock.Anything, mock.Anything, mock.Anything, mock.Anything)
}
