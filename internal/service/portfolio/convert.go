package portfolio

import "math"

// Token decimals arrive from external chain metadata. They are tiny in practice
// (<= ~36), but their wire types (uint32, int) permit values that would overflow
// when narrowed to int32/uint32, which gosec flags as G115. These helpers clamp
// to the target range so malformed metadata can never trigger a silent integer
// overflow. Clamping rather than erroring is safe: an absurd decimals value
// already yields meaningless amounts downstream, so the clamp only ever rewrites
// input that was broken to begin with.

func decI32(d uint32) int32 {
	if d > math.MaxInt32 {
		return math.MaxInt32
	}
	return int32(d)
}

func intToI32(n int) int32 {
	if n > math.MaxInt32 {
		return math.MaxInt32
	}
	if n < math.MinInt32 {
		return math.MinInt32
	}
	return int32(n)
}

func intToU32(n int) uint32 {
	if n < 0 {
		return 0
	}
	if int64(n) > math.MaxUint32 {
		return math.MaxUint32
	}
	return uint32(n)
}
