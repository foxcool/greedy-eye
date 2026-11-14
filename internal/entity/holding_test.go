package entity

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHolding_ExclusionSourceNeedsExcluded(t *testing.T) {
	var h Holding
	h.SetExcluded(false, ExcludedByUser)

	assert.False(t, h.Excluded())
	assert.Equal(t, ExclusionUnrecorded, h.ExcludedSource(), "an author without an exclusion is not recorded")
}

func TestHolding_ClearingExcludedClearsSource(t *testing.T) {
	for _, by := range []ExclusionSource{ExcludedByUser, ExcludedByQuarantine} {
		var h Holding
		h.SetExcluded(true, by)
		require.True(t, h.Excluded())
		require.Equal(t, by, h.ExcludedSource())

		h.SetExcluded(false, by)
		assert.False(t, h.Excluded())
		assert.Equal(t, ExclusionUnrecorded, h.ExcludedSource())
	}
}

func TestHolding_SourceImmutableAfterStamp(t *testing.T) {
	var h Holding
	require.NoError(t, h.StampSource(SourceSync))
	assert.Equal(t, SourceSync, h.Source())

	require.NoError(t, h.StampSource(SourceSync), "restamping the same provenance is not a change")
	assert.ErrorIs(t, h.StampSource(SourceManual), ErrSourceImmutable)
	assert.Equal(t, SourceSync, h.Source())
}
