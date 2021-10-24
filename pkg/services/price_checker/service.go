package price_checker

import (
	"errors"

	"github.com/foxcool/greedy-eye/pkg/entities"
)

type Service struct {
	// channel with trading opportunities
	opportunityChan chan entities.TradingOpportunity
	// the channel to which the service sends errors
	errorChan chan interface{}
	// exchange on which prices will be checked
	exchange *entities.Exchange
}

func NewService(opportunityChan chan entities.TradingOpportunity, errorChan chan interface{}, exchange *entities.Exchange) (*Service, error) {
	if errorChan == nil {
		return nil, errors.New("missing errorChan")
	}
	if opportunityChan == nil {
		return nil, errors.New("missing opportunityChan")
	}
	if exchange == nil {
		return nil, errors.New("missing exchange")
	}

	return &Service{
		opportunityChan: opportunityChan,
		errorChan:       errorChan,
		exchange:        exchange,
	}, nil
}
