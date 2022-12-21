package airtable

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/foxcool/greedy-eye/pkg/entities"
	"github.com/foxcool/greedy-eye/pkg/services/storage"
	"github.com/mehanizm/airtable"
	"github.com/shopspring/decimal"
)

const tableName = "prices"

type PriceStorage struct {
	PriceChan  chan entities.Price
	Client     *airtable.Client
	DatabaseID string
	APIKey     string
}

func NewPriceStorage(apiKey, databaseID string) (storage.PriceStorage, error) {
	if apiKey == "" && databaseID == "" {
		return nil, fmt.Errorf("airtable API key and database ID must be provided")
	}

	s := &PriceStorage{
		APIKey:     apiKey,
		DatabaseID: databaseID,
		Client:     airtable.NewClient(apiKey),
	}

	return s, nil
}

func (s *PriceStorage) Get(params map[string]interface{}) (*entities.Price, error) {
	if params[storage.GetParamAsset] == nil {
		return nil, errors.New("asset param is mandatory")
	}

	table := s.Client.GetTable(s.DatabaseID, tableName)
	records, err := table.GetRecords().
		WithFilterFormula(fmt.Sprintf("{Asset}='%s'", params[storage.GetParamAsset])).
		WithSort(struct {
			FieldName string
			Direction string
		}{
			FieldName: "Time",
			Direction: "desc",
		}).
		MaxRecords(1).
		ReturnFields("Source", "Asset", "Price", "Time").
		InStringFormat("Europe/Moscow", "ru").
		Do()
	if err != nil {
		return nil, fmt.Errorf("can't get price: %e", err)
	}

	price := entities.Price{
		Source: records.Records[0].Fields["Source"].(string),
		Asset:  records.Records[0].Fields["Asset"].(entities.Asset),
		Price:  records.Records[0].Fields["Price"].(decimal.Decimal),
		Time:   records.Records[0].Fields["Time"].(time.Time),
	}

	return &price, nil
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
	table := s.Client.GetTable(s.DatabaseID, tableName)
	records := new(airtable.Records)

	for _, price := range in {
		records.Records = append(
			records.Records,
			&airtable.Record{
				Fields: map[string]interface{}{
					"Source": price.Source,
					"Asset":  price.Asset,
					"Price":  price.Price,
					"Time":   price.Time,
				},
			},
		)
	}

	_, err := table.AddRecords(records)
	if err != nil {
		return fmt.Errorf("can't get price: %e", err)
	}
	return nil
}

func (s *PriceStorage) Work(ctx context.Context, priceChan chan entities.Price, errorChan chan error) {
	if priceChan != nil {
		s.PriceChan = priceChan
	}
	if s.PriceChan == nil {
		errorChan <- errors.New("can't start airtable worker: price channel is mandatory")
		return
	}

	// ToDo: use batch save
	for {
		select {
		case in := <-s.PriceChan:
			err := s.set(&in)
			if err != nil {
				errorChan <- fmt.Errorf("can't set price on airtable storage: %e", err)
			}
		case <-ctx.Done():
			return
		}
	}
}
