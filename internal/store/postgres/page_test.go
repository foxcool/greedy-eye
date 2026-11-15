package postgres

import (
	"math"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestPageLimit(t *testing.T) {
	assert.Equal(t, defaultPageSize, pageLimit(0), "unset asks for the default")
	assert.Equal(t, defaultPageSize, pageLimit(-5), "a negative size is unset")
	assert.Equal(t, 7, pageLimit(7))
	assert.Equal(t, maxPageSize, pageLimit(maxPageSize), "the largest internal page passes untouched")
	assert.Equal(t, maxPageSize, pageLimit(math.MaxInt32), "a client cannot size the allocation")
}
