package memory

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/foxcool/greedy-eye/pkg/entities"
	"github.com/foxcool/greedy-eye/pkg/services/storage"
)

type PriceStorage struct {
	PriceChan chan entities.Price

	storage map[entities.Asset]*entities.Price
	m       sync.RWMutex
}

func NewPriceStorage() (storage.PriceStorage, error) {
	s := &PriceStorage{
		storage: make(map[entities.Asset]*entities.Price)}

	return s, nil
}

func (s *PriceStorage) Get(params map[string]interface{}) (*entities.Price, error) {
	s.m.RLock()
	defer s.m.RUnlock()

	if params[storage.GetParamAsset] == nil {
		return nil, errors.New("asset param is mandatory")
	}

	return s.storage[params[storage.GetParamAsset].(entities.Asset)], nil
}

func (s *PriceStorage) Set(in ...*entities.Price) error {
	if s.PriceChan == nil {
		return s.set(in...)
	}

	for _, price := range in {
		s.PriceChan <- *price
	}

	return nil
}

func (s *PriceStorage) set(in ...*entities.Price) error {
	s.m.Lock()
	defer s.m.Unlock()

	for _, price := range in {
		s.storage[price.Asset] = price
	}

	return nil
}

func (s *PriceStorage) Work(ctx context.Context, priceChan chan entities.Price, errorChan chan error) {
	for {
		select {
		case in := <-s.PriceChan:
			err := s.set(&in)
			if err != nil {
				errorChan <- fmt.Errorf("can't set price on memory storage: %e", err)
			}
		case <-ctx.Done():
			return
		}
	}
}
