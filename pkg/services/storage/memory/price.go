package memory

import (
	"context"
	"errors"
	"sync"

	"github.com/foxcool/greedy-eye/pkg/entities"
	"github.com/foxcool/greedy-eye/pkg/services/storage"
)

type IndexPriceStorage struct {
	PriceChan chan entities.Price

	storage map[entities.Asset]*entities.Price
	m       sync.RWMutex
}

func NewPriceStorage() (storage.PriceStorage, error) {
	s := &IndexPriceStorage{
		storage: make(map[entities.Asset]*entities.Price)}

	return s, nil
}

func (s *IndexPriceStorage) Get(params map[string]interface{}) (*entities.Price, error) {
	s.m.RLock()
	defer s.m.RUnlock()

	if params[storage.GetParamAsset] == nil {
		return nil, errors.New("asset param is mandatory")
	}

	return s.storage[params[storage.GetParamAsset].(entities.Asset)], nil
}

func (s *IndexPriceStorage) Set(in *entities.Price) error {
	if s.PriceChan != nil && in != nil {
		s.PriceChan <- *in

		return nil
	}

	return s.set(in)
}

func (s *IndexPriceStorage) set(in *entities.Price) error {
	s.m.Lock()
	defer s.m.Unlock()

	s.storage[in.Asset] = in

	return nil
}

func (s *IndexPriceStorage) Work(ctx context.Context, priceChan chan entities.Price) {
	for {
		select {
		case in := <-s.PriceChan:
			s.set(&in)
		case <-ctx.Done():
			return
		}
	}
}
