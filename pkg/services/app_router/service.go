package app_router

import (
	"errors"

	"github.com/foxcool/greedy-eye/pkg/entities"
)

type Service struct {
	// channel with trading jobs
	jobChan chan entities.ExplorationJob
	// channel with trading opportunities
	opportunityChan chan entities.TradingOpportunity
	// the channel to which the service sends errors
	errorChan chan error
	// channel for notifications
	sendMessageChan chan interface{}
}

func NewService(jobChan chan entities.ExplorationJob, opportunityChan chan entities.TradingOpportunity, sendMessageChan chan interface{}, errorChan chan error) (*Service, error) {
	if errorChan == nil {
		return nil, errors.New("missing errorChan")
	}
	if opportunityChan == nil {
		return nil, errors.New("missing opportunityChan")
	}

	return &Service{
		jobChan:         jobChan,
		opportunityChan: opportunityChan,
		sendMessageChan: sendMessageChan,
		errorChan:       errorChan,
	}, nil
}

func (s *Service) Work() {
	select {
	case opportunity := <-s.opportunityChan:
		s.sendMessageChan <- opportunity
	case err := <-s.errorChan:
		s.sendMessageChan <- err
	}
}
