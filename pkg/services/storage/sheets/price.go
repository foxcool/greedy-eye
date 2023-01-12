package sheets

import (
	"context"
	"errors"
	"fmt"

	"github.com/foxcool/greedy-eye/pkg/entities"
	"github.com/foxcool/greedy-eye/pkg/services/storage"
	"github.com/shopspring/decimal"
	"google.golang.org/api/sheets/v4"
)

type PriceStorage struct {
	PriceChan     chan entities.Price
	SheetsSvc     *sheets.Service
	SpreadsheetID string
	PricesRange   string
}

func NewPriceStorage(sheetsSvc *sheets.Service, spreadsheetID, pricesRange string) (storage.PriceStorage, error) {
	s := &PriceStorage{
		SheetsSvc:     sheetsSvc,
		SpreadsheetID: spreadsheetID,
		PricesRange:   pricesRange,
	}
	return s, nil
}

func (s *PriceStorage) Get(params map[string]interface{}) (*entities.Price, error) {
	if params[storage.GetParamAsset] == nil {
		return nil, errors.New("asset param is mandatory")
	}

	asset := params[storage.GetParamAsset].(entities.Asset)

	req := s.SheetsSvc.Spreadsheets.Values.Get(s.SpreadsheetID, s.PricesRange)
	resp, err := req.Do()
	if err != nil {
		return nil, err
	}

	for _, row := range resp.Values {
		if row[0] == string(asset) {
			price, err := decimal.NewFromString(row[1].(string))
			if err != nil {
				return nil, err
			}
			return &entities.Price{Asset: asset, Price: price}, nil
		}
	}
	return nil, fmt.Errorf("asset %s not found", asset)
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
	var values [][]interface{}
	for _, price := range in {
		values = append(values, []interface{}{price.Asset, price.Price.String()})
	}
	body := &sheets.ValueRange{Values: values}

	_, err := s.SheetsSvc.Spreadsheets.Values.Append(s.SpreadsheetID, s.PricesRange, body).ValueInputOption("RAW").Do()
	return err
}

func (s *PriceStorage) Work(ctx context.Context, priceChan chan entities.Price, errorChan chan error) {
	if priceChan != nil {
		s.PriceChan = priceChan
	}
	if s.PriceChan == nil {
		errorChan <- errors.New("can't start sheets worker: price channel is mandatory")
		return
	}

	for {
		select {
		case in := <-s.PriceChan:
			err := s.set(&in)
			if err != nil {
				errorChan <- fmt.Errorf("failed to set price in sheets storage: %e", err)
			}
		case <-ctx.Done():
			return
		}
	}
}
