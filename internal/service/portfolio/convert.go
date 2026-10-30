package portfolio

import "math"

// Token decimals arrive from external chain metadata. They are tiny in practice
// (<= ~36), but their wire type (int) permits values that would overflow when
// narrowed to int32/uint32, which gosec flags as G115. These helpers clamp to
// the target range so malformed metadata can never trigger a silent integer
// overflow; entity.DecI32 is the same rule for uint32 decimals.

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
