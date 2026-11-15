package evmswap

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// rpcStub answers eth_call by the target address with a fixed result or a
// revert payload, and records what was asked.
type rpcStub struct {
	results map[Address][]byte
	reverts map[Address][]byte
	asked   []map[string]string
}

func (s *rpcStub) serve(t *testing.T) *httptest.Server {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Method string            `json:"method"`
			Params []json.RawMessage `json:"params"`
		}
		require.NoError(t, json.NewDecoder(r.Body).Decode(&req))
		require.Equal(t, "eth_call", req.Method)
		var msg map[string]string
		require.NoError(t, json.Unmarshal(req.Params[0], &msg))
		s.asked = append(s.asked, msg)
		to, err := ParseAddress(msg["to"])
		require.NoError(t, err)
		if data, ok := s.reverts[to]; ok {
			_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": 1, "error": map[string]any{
				"code": 3, "message": "execution reverted", "data": "0x" + hex.EncodeToString(data),
			}})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": 1, "result": "0x" + hex.EncodeToString(s.results[to])})
	}))
	t.Cleanup(srv.Close)
	return srv
}

func words(ws ...[]byte) []byte {
	var out []byte
	for _, w := range ws {
		out = append(out, w...)
	}
	return out
}

func errorString(msg string) []byte {
	return words(selErrorString[:], wordUint64(wordSize), encodeBytes([]byte(msg)))
}

func TestQuoteReadsAmountOut(t *testing.T) {
	stub := &rpcStub{results: map[Address][]byte{
		Base.Quoter: words(wordUint64(7_500_000_000_000_000), wordUint64(1), wordUint64(2), wordUint64(90_000)),
	}}
	c := NewClient(Config{Endpoint: stub.serve(t).URL})

	out, err := c.Quote(context.Background(), Base, usdc, weth, big.NewInt(25_000_000), 500)
	require.NoError(t, err)
	assert.Equal(t, "7500000000000000", out.String())
	require.Len(t, stub.asked, 1)
	assert.Equal(t, "0x"+hex.EncodeToString(selQuoteExactInSingle[:]), stub.asked[0]["data"][:10])
}

// TestBestQuoteSkipsMissingPools: a fee tier without a pool reverts; the best
// of the tiers that answered wins, and a node error is not taken for a
// missing pool.
func TestBestQuoteSkipsMissingPools(t *testing.T) {
	answers := map[uint32]string{500: "7500", 3000: "7600"} // 100 and 10000 revert
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Params []json.RawMessage `json:"params"`
		}
		require.NoError(t, json.NewDecoder(r.Body).Decode(&req))
		var msg map[string]string
		require.NoError(t, json.Unmarshal(req.Params[0], &msg))
		data, _ := hex.DecodeString(msg["data"][2:])
		fee := new(big.Int).SetBytes(data[4+3*wordSize : 4+4*wordSize]).Uint64()
		if v, ok := answers[uint32(fee)]; ok {
			n, _ := new(big.Int).SetString(v, 10)
			w2, _ := wordUint(n)
			_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": 1, "result": "0x" + hex.EncodeToString(words(w2, wordUint64(0), wordUint64(0), wordUint64(0)))})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": 1, "error": map[string]any{"code": 3, "message": "execution reverted", "data": "0x"}})
	}))
	t.Cleanup(srv.Close)

	fee, out, err := NewClient(Config{Endpoint: srv.URL}).BestQuote(context.Background(), Base, usdc, weth, big.NewInt(25_000_000))
	require.NoError(t, err)
	assert.Equal(t, uint32(3000), fee)
	assert.Equal(t, "7600", out.String())

	down := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	}))
	t.Cleanup(down.Close)
	_, _, err = NewClient(Config{Endpoint: down.URL}).BestQuote(context.Background(), Base, usdc, weth, big.NewInt(1))
	require.Error(t, err)
	var revert *RevertError
	assert.NotErrorAs(t, err, &revert, "a node that is down is not a pool that is missing")
}

// simulatePayload is simulateAndRevert's revert data around simulate's
// (estimate, success, returnData).
func simulatePayload(ok bool, gas uint64, returnData []byte) []byte {
	success := uint64(0)
	if ok {
		success = 1
	}
	ret := words(wordUint64(gas), wordUint64(success), wordUint64(3*wordSize), encodeBytes(returnData))
	return words(wordUint64(1), wordUint64(uint64(len(ret))), ret)
}

func TestSimulate(t *testing.T) {
	tx := mustBuild(t, Base, testSwap())

	stub := &rpcStub{reverts: map[Address][]byte{safe: simulatePayload(true, 180_000, nil)}}
	sim, err := NewClient(Config{Endpoint: stub.serve(t).URL}).Simulate(context.Background(), Base, safe, tx)
	require.NoError(t, err)
	assert.True(t, sim.OK)
	assert.Equal(t, int64(180_000), sim.GasUsed.Int64())
	require.Len(t, stub.asked, 1)
	assert.Equal(t, safe.String(), stub.asked[0]["to"], "the simulation runs on the Safe itself")
	assert.Equal(t, "0x"+hex.EncodeToString(selSimulateAndRevert[:]), stub.asked[0]["data"][:10])

	short := &rpcStub{reverts: map[Address][]byte{safe: simulatePayload(false, 60_000, errorString("STF"))}}
	sim, err = NewClient(Config{Endpoint: short.serve(t).URL}).Simulate(context.Background(), Base, safe, tx)
	require.NoError(t, err, "a swap that would revert is an answer, not an error")
	assert.False(t, sim.OK)
	assert.Equal(t, "STF", sim.Reason)

	notSafe := &rpcStub{results: map[Address][]byte{safe: nil}}
	_, err = NewClient(Config{Endpoint: notSafe.serve(t).URL}).Simulate(context.Background(), Base, safe, tx)
	assert.ErrorContains(t, err, "is the address a Safe")
}

func TestParseSimulationRefusesShortPayloads(t *testing.T) {
	for name, p := range map[string][]byte{
		"empty":        nil,
		"length lies":  words(wordUint64(1), wordUint64(1000)),
		"missing body": words(wordUint64(1), wordUint64(64), wordUint64(5)),
	} {
		_, err := parseSimulation(p)
		assert.Error(t, err, name)
	}
}

func TestCheckFunds(t *testing.T) {
	stub := &rpcStub{results: map[Address][]byte{usdc: wordUint64(10_000_000)}}
	c := NewClient(Config{Endpoint: stub.serve(t).URL})
	s := testSwap() // needs 25 USDC

	var short *InsufficientFundsError
	require.ErrorAs(t, c.CheckFunds(context.Background(), s), &short)
	assert.Equal(t, int64(10_000_000), short.Have.Int64())
	assert.Equal(t, int64(25_000_000), short.Need.Int64())
	assert.Equal(t, safe.String(), "0x"+stub.asked[0]["data"][34:], "asks the Safe's balance")

	s.AmountIn = big.NewInt(10_000_000)
	assert.NoError(t, c.CheckFunds(context.Background(), s), "exactly enough is enough")

	for _, bad := range []*big.Int{nil, big.NewInt(-1), big.NewInt(0)} {
		s.AmountIn = bad
		assert.Error(t, c.CheckFunds(context.Background(), s), "%v is not an amount to check", bad)
	}
}

func TestSimulateNamesAnEmptyRevert(t *testing.T) {
	stub := &rpcStub{reverts: map[Address][]byte{safe: simulatePayload(false, 130_000, nil)}}
	sim, err := NewClient(Config{Endpoint: stub.serve(t).URL}).Simulate(context.Background(), Base, safe, mustBuild(t, Base, testSwap()))
	require.NoError(t, err)
	assert.False(t, sim.OK)
	assert.Equal(t, noReason, sim.Reason)
}
