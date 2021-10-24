package sora

import (
	"errors"
	"fmt"
	"net/http"
	"sync"

	"github.com/shopspring/decimal"

	"github.com/agrea/ptr"
	"github.com/foxcool/greedy-eye/pkg/entities"
	"github.com/foxcool/greedy-eye/pkg/services/storage"
	"github.com/gorilla/websocket"
)

type Service struct {
	URL             string
	Storage         storage.IndexPriceStorage
	JobChan         chan entities.ExplorationJob
	OpportunityChan chan entities.TradingOpportunity
	ErrorChan       chan error

	conn        *websocket.Conn
	connMutex   sync.RWMutex
	jobMap      map[int64]*entities.ExplorationJob
	jobMapMutex sync.Mutex
	lastReqID   *int64
}

var (
	header = http.Header{
		"Sec-WebSocket-Version":    []string{"13"},
		"Origin":                   []string{"https://polkaswap.io"},
		"User-Agent":               []string{"Mozilla/5.0 (X11; Ubuntu; Linux x86_64; rv:88.0) Gecko/20100101 Firefox/88.0"},
		"Sec-WebSocket-Extensions": []string{"permessage-deflate"},
	}
	xor              = entities.Asset("XOR")
	defaultValueStep = decimal.NewFromInt(100)
	xorBasicFee      = decimal.NewFromFloat(0.0007)
)

func (s *Service) WaitJobs() {
	for job := range s.JobChan {
		s.processJob(job)
	}
}

func (s *Service) WaitResponses() {
	for {
		if s.conn == nil {
			s.connect()
		}

		_, message, err := s.conn.ReadMessage()
		if err != nil {
			s.SendError(fmt.Errorf("can't read response: %e", err))

			err = s.conn.Close()
			if err != nil {
				s.SendError(fmt.Errorf("can't close connection: %e", err))
			}
			s.conn = nil

			continue
		}

		resp, err := s.decodeResponse(message)
		if err != nil {
			s.SendError(fmt.Errorf("can't decode response: %e", err))
		}

		job := s.jobFromResponse(resp)
		if job.BestOpportunity != nil && job.CurrentOpportunity.Profit.GreaterThan(job.BestOpportunity.Profit) {
			// Make new testing amount step
			job.BestOpportunity = job.CurrentOpportunity
			job.CurrentOpportunity = nil

			s.JobChan <- *job
		} else {
			// Send final result
			s.OpportunityChan <- *job.BestOpportunity
		}
	}
}

func (s *Service) SendError(err error) {
	s.ErrorChan <- fmt.Errorf("sora %s client: %e", s.URL, err)
}

func (s *Service) connect() {
	if s.ErrorChan == nil {
		panic("ErrorChan is mandatory")
	}
	if s.URL == "" || s.Storage == nil || s.JobChan == nil || s.OpportunityChan == nil || s.ErrorChan == nil {
		s.SendError(errors.New("URL, IndexPriceStorage, JobChan and OpportunityChan are mandatory"))
	}

	s.connMutex.Lock()
	defer s.connMutex.Unlock()

	var err error

	s.jobMap = make(map[int64]*entities.ExplorationJob)
	s.lastReqID = ptr.Int64(0)

	s.conn, _, err = websocket.DefaultDialer.Dial(s.URL, header)
	if err != nil {
		s.SendError(fmt.Errorf("can't connect to server: %e", err))
	}
}

func (s *Service) processJob(job entities.ExplorationJob) {
	if job.FromAmountStep == nil {
		fromAssetIndexPrice, err := s.Storage.Get(map[string]interface{}{storage.GetParamAsset: job.FromAsset})
		if err != nil {
			s.ErrorChan <- fmt.Errorf("can't calculate the step of the value of the sold asset: %e", err)

			return
		}
		fromAmountStep := defaultValueStep.Div(fromAssetIndexPrice.Price)
		job.FromAmountStep = &fromAmountStep
	}

	job.CurrentOpportunity = &entities.TradingOpportunity{
		From: job.FromAsset,
		To:   job.ToAsset,
	}

	// Set sold token amount
	if job.BestOpportunity != nil {
		job.CurrentOpportunity.FromAmount = job.BestOpportunity.FromAmount.Add(*job.FromAmountStep)
	} else {
		job.CurrentOpportunity.FromAmount = *job.FromAmountStep
	}

	// If we have limiting the maximum amount, check this
	if job.MaximumFromAmount != nil && job.CurrentOpportunity.FromAmount.GreaterThan(*job.MaximumFromAmount) {
		job.CurrentOpportunity.FromAmount = *job.MaximumFromAmount
	}

	reqID, err := s.SendPriceRequest(TokenAddresses[string(*job.FromAsset)], TokenAddresses[string(*job.ToAsset)], job.CurrentOpportunity.FromAmount)
	if err != nil {
		s.ErrorChan <- fmt.Errorf("can't make price request: %e", err)

		return
	}

	// Store job for response handler
	s.setJob(reqID, &job)
}

func (s *Service) jobFromResponse(resp *JsonRPCResponse) *entities.ExplorationJob {
	job := s.getJob(resp.ID)
	if job == nil {
		s.ErrorChan <- fmt.Errorf("can't find job for response: %d", resp.ID)

		return nil
	}

	// Get destination token amount
	amountString, exist := resp.Result["amount"]
	if !exist {
		return nil
	}
	amount, err := decimal.NewFromString(amountString.(string))
	if err != nil {
		return nil
	}
	job.CurrentOpportunity.ToAmount = amount.Shift(-18)

	fee, err := decimal.NewFromString(resp.Result["fee"].(string))
	if err != nil {
		s.ErrorChan <- fmt.Errorf("can't calculate fee for job: %e", err)

		return nil
	}
	xorPrice, err := s.Storage.Get(map[string]interface{}{storage.GetParamAsset: xor})
	if err != nil {
		s.ErrorChan <- fmt.Errorf("can't calculate fee for job: %e", err)

		return nil
	}
	job.CurrentOpportunity.Fee = fee.Shift(-18).Add(xorBasicFee).Mul(xorPrice.Price)

	// Calculate possible profit
	soldAssetPrice, err := s.Storage.Get(map[string]interface{}{storage.GetParamAsset: job.FromAsset})
	if err != nil {
		s.ErrorChan <- fmt.Errorf("can't get sold asset price: %e", err)

		return nil
	}
	purchasedAssetPrice, err := s.Storage.Get(map[string]interface{}{storage.GetParamAsset: job.ToAsset})
	if err != nil {
		s.ErrorChan <- fmt.Errorf("can't get sold asset price: %e", err)

		return nil
	}
	soldAssetValue := job.CurrentOpportunity.FromAmount.Mul(soldAssetPrice.Price)
	purchasedAssetValue := job.CurrentOpportunity.FromAmount.Mul(purchasedAssetPrice.Price)

	job.CurrentOpportunity.Profit = purchasedAssetValue.Sub(soldAssetValue).Sub(job.CurrentOpportunity.Fee)

	return job
}

func (s *Service) getJob(reqID int64) *entities.ExplorationJob {
	s.jobMapMutex.Lock()
	defer s.jobMapMutex.Unlock()

	job, exist := s.jobMap[reqID]
	if !exist {
		return nil
	}

	delete(s.jobMap, reqID)

	return job
}

func (s *Service) setJob(reqID int64, job *entities.ExplorationJob) {
	s.jobMapMutex.Lock()
	defer s.jobMapMutex.Unlock()

	s.jobMap[reqID] = job
}
