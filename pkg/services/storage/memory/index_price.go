package memory

import (
	"errors"
	"sync"

	"github.com/foxcool/greedy-eye/pkg/entities"
	"github.com/foxcool/greedy-eye/pkg/services/storage"
)

type IndexPriceStorage struct {
	storage map[entities.Asset]*entities.IndexPrice
	m       sync.RWMutex
}

func NewIndexPriceStorage() (storage.IndexPriceStorage, error) {
	s := &IndexPriceStorage{storage: make(map[entities.Asset]*entities.IndexPrice)}

	return s, nil
}

func (s *IndexPriceStorage) Set(in *entities.IndexPrice) error {
	s.m.Lock()
	defer s.m.Unlock()

	s.storage[in.Asset] = in

	return nil
}

func (s *IndexPriceStorage) Get(params map[string]interface{}) (*entities.IndexPrice, error) {
	s.m.RLock()
	defer s.m.RUnlock()

	if params[storage.GetParamAsset] == nil {
		return nil, errors.New("asset param is mandatory")
	}

	return s.storage[params[storage.GetParamAsset].(entities.Asset)], nil
}
