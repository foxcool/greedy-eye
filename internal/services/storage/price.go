package storage

import (
	"context"

	"github.com/foxcool/greedy-eye/internal/entities"
)

type PriceStorage interface {
	Set(in ...*entities.Price) error
	Get(params map[string]interface{}) (*entities.Price, error)
	Work(ctx context.Context, priceChan chan entities.Price, errorChan chan error)
}

const (
	GetParamAsset = "asset"
)
