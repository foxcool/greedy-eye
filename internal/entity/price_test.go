package entity

import (
	"math"
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestDecI32 pins the narrowing every amount scale goes through: real decimals
// pass unchanged, and a wire value past int32 clamps instead of wrapping negative.
func TestDecI32(t *testing.T) {
	assert.Equal(t, int32(0), DecI32(0))
	assert.Equal(t, int32(18), DecI32(18))
	assert.Equal(t, int32(math.MaxInt32), DecI32(math.MaxInt32))
	assert.Equal(t, int32(math.MaxInt32), DecI32(math.MaxInt32+1))
	assert.Equal(t, int32(math.MaxInt32), DecI32(math.MaxUint32))
}
