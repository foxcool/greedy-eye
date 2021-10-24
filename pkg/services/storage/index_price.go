package storage

import "github.com/foxcool/greedy-eye/pkg/entities"

type IndexPriceStorage interface {
	Set(in *entities.IndexPrice) error
	Get(params map[string]interface{}) (*entities.IndexPrice, error)
}

const (
	GetParamAsset = "asset"
)
