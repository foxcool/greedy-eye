// Package entitytest builds entities for tests in other packages.
package entitytest

import "github.com/foxcool/greedy-eye/internal/entity"

// HoldingState is the part of a holding that only its methods may write.
type HoldingState struct {
	Source         entity.ProvenanceSource
	Excluded       bool
	ExcludedSource entity.ExclusionSource
}

// Holding returns h with s applied through the holding's own methods, so a
// fixture cannot hold a combination production code could not produce.
func Holding(h entity.Holding, s HoldingState) *entity.Holding {
	h.SetExcluded(s.Excluded, s.ExcludedSource)
	if err := h.StampSource(s.Source); err != nil {
		panic(err)
	}
	return &h
}
