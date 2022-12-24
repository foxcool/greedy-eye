package badger

import (
	"context"
	"testing"
	"time"

	"github.com/foxcool/greedy-eye/pkg/entities"
	"github.com/foxcool/greedy-eye/pkg/services/storage"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
)

func TestPriceStorage_Work(t *testing.T) {
	s, err := NewPriceStorage("/tmp/test_badger_prices")
	assert.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	errorChan := make(chan error, 1)
	priceChan := make(chan entities.Price, 1)

	go s.Work(ctx, priceChan, errorChan)

	t.Run("sets the price in the database when it is received on the price channel", func(t *testing.T) {
		price := entities.Price{Asset: entities.Asset("BTC"), Price: decimal.NewFromFloat(123.45)}
		priceChan <- price

		time.Sleep(1 * time.Second)

		got, err := s.Get(map[string]interface{}{storage.GetParamAsset: price.Asset})
		assert.NoError(t, err)
		assert.Equal(t, &price, got)
	})

	t.Run("sends an error to the error channel if it fails to set the price in the database", func(t *testing.T) {
		s, ok := s.(*PriceStorage)
		assert.True(t, ok)
		s.DB.Close()

		price := entities.Price{Asset: entities.Asset("ETH"), Price: decimal.NewFromFloat(67.89)}
		err := s.Set(&price)
		assert.NoError(t, err)

		select {
		case err := <-errorChan:
			assert.Contains(t, err.Error(), "failed to set price in badger storage")
		case <-ctx.Done():
			assert.Fail(t, "timed out waiting for error on error channel")
		}
	})
}
