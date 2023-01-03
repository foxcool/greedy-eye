package coingecko

import (
	"testing"
	"time"

	"github.com/foxcool/greedy-eye/pkg/entities"
	"github.com/stretchr/testify/assert"
)

func TestGet(t *testing.T) {
	// Call the Get function
	prices, err := Get()
	assert.NoError(t, err)

	// Check that the returned prices are correct
	for _, price := range prices {
		assert.Equal(t, "coingecko", price.Source)
		assert.IsType(t, entities.Asset(""), price.Asset)
		assert.IsType(t, time.Time{}, price.Time)
	}
}
