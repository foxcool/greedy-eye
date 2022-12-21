package storage

import (
	"context"

	"github.com/foxcool/greedy-eye/pkg/entities"
)

type PriceStorage interface {
	Set(in *entities.Price) error
	Get(params map[string]interface{}) (*entities.Price, error)
	Work(ctx context.Context, priceChan chan entities.Price)
}

const (
	GetParamAsset = "asset"
)
