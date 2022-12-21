package sora

import (
	"encoding/json"
	"sync/atomic"

	"github.com/gorilla/websocket"

	"github.com/shopspring/decimal"
)

// Dirty

type JsonRPCRequest struct {
	ID      int64         `json:"id"`
	JSONRPC string        `json:"jsonrpc"`
	Method  string        `json:"method"`
	Params  []interface{} `json:"params"`
}

func (req JsonRPCRequest) Bytes() []byte {
	b, err := json.Marshal(req)
	if err != nil {
		panic(err)
	}
	return b
}

type JsonRPCResponse struct {
	ID     int64                  `json:"id"`
	Result map[string]interface{} `json:"result"`
}

// ToDo: drop if not needed
// func (s *Service) registerRespHandler(id int64, job *entities.ExplorationJob) {
// 	s.jobMapMutex.Lock()
// 	s.jobMap[id] = job
// 	s.jobMapMutex.Unlock()
// }

func (s *Service) SendPriceRequest(addressFrom, addressTo string, amount decimal.Decimal) (int64, error) {
	n := atomic.AddInt64(s.lastReqID, 1)
	req := JsonRPCRequest{
		ID:      n,
		JSONRPC: "2.0",
		Method:  "liquidityProxy_quote",

		Params: []interface{}{
			0,
			addressFrom,
			addressTo,
			amount.Shift(18).Round(0).String(),
			"WithDesiredInput",
			[]string{},
			"Disabled",
		},
	}

	return n, s.conn.WriteMessage(websocket.TextMessage, req.Bytes())
}

func (s *Service) decodeResponse(message []byte) (*JsonRPCResponse, error) {
	var resp JsonRPCResponse
	err := json.Unmarshal(message, &resp)
	if err != nil {
		return nil, err
	}

	return &resp, nil
}
