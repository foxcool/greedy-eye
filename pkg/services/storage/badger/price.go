package badger

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/dgraph-io/badger/v3"
	"github.com/foxcool/greedy-eye/pkg/entities"
	"github.com/foxcool/greedy-eye/pkg/services/storage"
)

type PriceStorage struct {
	PriceChan chan entities.Price
	DB        *badger.DB
}

func NewPriceStorage(path string) (storage.PriceStorage, error) {
	if path == "" {
		path = "/tmp/prices"
	}
	db, err := badger.Open(badger.DefaultOptions(path))
	if err != nil {
		return nil, err
	}

	s := &PriceStorage{DB: db}

	return s, nil
}

func (s *PriceStorage) Get(params map[string]interface{}) (*entities.Price, error) {
	if params[storage.GetParamAsset] == nil {
		return nil, errors.New("asset param is mandatory")
	}

	entity := &entities.Price{}
	err := s.DB.View(func(txn *badger.Txn) error {
		item, err := txn.Get([]byte(params[storage.GetParamAsset].(entities.Asset)))
		if err != nil {
			return err
		}

		err = item.Value(func(val []byte) error {
			err = json.Unmarshal(val, entity)
			if err != nil {
				return err
			}

			return nil
		})
		if err != nil {
			return err
		}

		return nil
	})
	if err != nil {
		return nil, err
	}

	return entity, nil
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
	for _, price := range in {
		err := s.DB.Update(func(txn *badger.Txn) error {
			value, err := json.Marshal(price)
			if err != nil {
				return err
			}
			return txn.Set([]byte(price.Asset), value)
		})
		if err != nil {
			return err
		}
	}

	return nil
}

func (s *PriceStorage) Work(ctx context.Context, priceChan chan entities.Price, errorChan chan error) {
	if priceChan != nil {
		s.PriceChan = priceChan
	}
	if s.PriceChan == nil {
		errorChan <- errors.New("can't start badger worker: price channel is mandatory")
		return
	}

	for {
		select {
		case in := <-s.PriceChan:
			err := s.set(&in)
			if err != nil {
				errorChan <- fmt.Errorf("failed to set price in badger storage: %e", err)
			}
		case <-ctx.Done():
			return
		}
	}
}
