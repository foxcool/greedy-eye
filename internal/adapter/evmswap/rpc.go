package evmswap

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"strings"
	"time"

	"github.com/foxcool/greedy-eye/internal/adapter/internal/urlerr"
)

// Client reads one chain over JSON-RPC: quotes and simulations, never a
// transaction that changes state.
type Client struct {
	endpoint   string
	httpClient *http.Client
}

// Config holds the endpoint and, optionally, the shared rate budget's
// transport. The endpoint may carry a key in its path; it is kept out of
// every error.
type Config struct {
	Endpoint  string
	Transport http.RoundTripper
}

func NewClient(cfg Config) *Client {
	return &Client{
		endpoint:   cfg.Endpoint,
		httpClient: &http.Client{Timeout: 30 * time.Second, Transport: cfg.Transport},
	}
}

// RevertError is an eth_call that the chain executed and reverted. It is an
// answer about the call, not a failure to reach the node.
type RevertError struct {
	Data []byte
}

func (e *RevertError) Error() string {
	if reason, ok := revertReason(e.Data); ok {
		return "evmswap: reverted: " + reason
	}
	return "evmswap: reverted"
}

type callResponse struct {
	Result string `json:"result"`
	Error  *struct {
		Code    int             `json:"code"`
		Message string          `json:"message"`
		Data    json.RawMessage `json:"data"`
	} `json:"error"`
}

// call runs eth_call against the latest block. A revert comes back as a
// *RevertError carrying the revert data.
func (c *Client) call(ctx context.Context, from *Address, to Address, data []byte) ([]byte, error) {
	msg := map[string]string{"to": to.String(), "data": "0x" + hex.EncodeToString(data)}
	if from != nil {
		msg["from"] = from.String()
	}
	body, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "eth_call", "params": []any{msg, "latest"},
	})
	if err != nil {
		return nil, fmt.Errorf("evmswap: encode request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("evmswap: create request: %w", urlerr.Strip(err))
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("evmswap: eth_call: %w", urlerr.Strip(err))
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("evmswap: read response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("evmswap: eth_call: HTTP %d", resp.StatusCode)
	}
	var out callResponse
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("evmswap: decode response: %w", err)
	}
	if out.Error != nil {
		// Nodes report a revert as an error whose data is the revert payload.
		var hexData string
		if json.Unmarshal(out.Error.Data, &hexData) == nil && strings.HasPrefix(hexData, "0x") {
			if b, err := hex.DecodeString(hexData[2:]); err == nil {
				return nil, &RevertError{Data: b}
			}
		}
		return nil, fmt.Errorf("evmswap: eth_call: %d %s", out.Error.Code, out.Error.Message)
	}
	b, err := hex.DecodeString(strings.TrimPrefix(out.Result, "0x"))
	if err != nil {
		return nil, fmt.Errorf("evmswap: result is not hex: %w", err)
	}
	return b, nil
}

// Quote asks QuoterV2 what one pool pays for an exact input. A pool that does
// not exist reverts, which comes back as an error.
func (c *Client) Quote(ctx context.Context, n Network, tokenIn, tokenOut Address, amountIn *big.Int, fee uint32) (*big.Int, error) {
	amount, err := wordUint(amountIn)
	if err != nil {
		return nil, err
	}
	data := append([]byte{}, selQuoteExactInSingle[:]...)
	for _, w := range [][]byte{
		wordAddress(tokenIn), wordAddress(tokenOut), amount, wordUint64(uint64(fee)), wordUint64(0),
	} {
		data = append(data, w...)
	}
	res, err := c.call(ctx, nil, n.Quoter, data)
	if err != nil {
		return nil, err
	}
	return reader{res}.uint(0)
}

// BestQuote asks every standard fee tier and keeps the largest output. Tiers
// whose pool is missing or empty revert and are skipped; an error comes back
// only when no tier answered, and it is the last one seen.
func (c *Client) BestQuote(ctx context.Context, n Network, tokenIn, tokenOut Address, amountIn *big.Int) (fee uint32, out *big.Int, err error) {
	var lastErr error
	for _, f := range FeeTiers {
		q, err := c.Quote(ctx, n, tokenIn, tokenOut, amountIn, f)
		if err != nil {
			var revert *RevertError
			if !errors.As(err, &revert) {
				return 0, nil, err // the node, not the pool: asking other tiers will not help
			}
			lastErr = err
			continue
		}
		if out == nil || q.Cmp(out) > 0 {
			fee, out = f, q
		}
	}
	if out == nil {
		return 0, nil, fmt.Errorf("evmswap: no fee tier quotes %s -> %s: %w", tokenIn, tokenOut, lastErr)
	}
	return fee, out, nil
}

// Simulation is what running a Safe transaction against the latest block did.
type Simulation struct {
	OK      bool
	GasUsed *big.Int
	// Reason is the decoded revert reason when OK is false and the target
	// gave one.
	Reason string
}

// Simulate runs tx as the Safe would, without a signature: through the Safe's
// simulateAndRevert into SimulateTxAccessor, which executes the transaction
// in the Safe's context and reverts with the outcome. A plain eth_call to
// MultiSend would not do: the batch must run as a delegate call from the Safe,
// or the approve would be the MultiSend contract's, not the Safe's.
func (c *Client) Simulate(ctx context.Context, n Network, safe Address, tx SafeTx) (Simulation, error) {
	value := tx.Value
	if value == nil {
		value = new(big.Int)
	}
	v, err := wordUint(value)
	if err != nil {
		return Simulation{}, err
	}
	inner := append([]byte{}, selSimulate[:]...)
	inner = append(inner, wordAddress(tx.To)...)
	inner = append(inner, v...)
	inner = append(inner, wordUint64(4*wordSize)...) // offset of data
	inner = append(inner, wordUint64(uint64(tx.Operation))...)
	inner = append(inner, encodeBytes(tx.Data)...)

	outer := append([]byte{}, selSimulateAndRevert[:]...)
	outer = append(outer, wordAddress(n.TxSimulator)...)
	outer = append(outer, wordUint64(2*wordSize)...)
	outer = append(outer, encodeBytes(inner)...)

	_, err = c.call(ctx, nil, safe, outer)
	var revert *RevertError
	if !errors.As(err, &revert) {
		if err == nil {
			return Simulation{}, errors.New("evmswap: simulateAndRevert returned instead of reverting; is the address a Safe?")
		}
		return Simulation{}, err
	}
	return parseSimulation(revert.Data)
}

// parseSimulation reads simulateAndRevert's revert payload: the success of
// its delegate call into the accessor, the length of what the accessor
// returned, then simulate's (uint256 estimate, bool success, bytes returnData).
func parseSimulation(payload []byte) (Simulation, error) {
	r := reader{payload}
	ran, err := r.uint(0)
	if err != nil {
		return Simulation{}, err
	}
	n, err := r.small(1)
	if err != nil {
		return Simulation{}, err
	}
	if 2*wordSize+n > len(payload) {
		return Simulation{}, errShort
	}
	ret := reader{payload[2*wordSize : 2*wordSize+n]}
	if ran.Sign() == 0 {
		reason, _ := revertReason(ret.b)
		return Simulation{}, fmt.Errorf("evmswap: the Safe could not run the simulation: %s", reason)
	}
	gas, err := ret.uint(0)
	if err != nil {
		return Simulation{}, err
	}
	ok, err := ret.uint(1)
	if err != nil {
		return Simulation{}, err
	}
	sim := Simulation{OK: ok.Sign() != 0, GasUsed: gas}
	if !sim.OK {
		data, err := ret.bytesAt(2)
		if err != nil {
			return Simulation{}, err
		}
		var ok bool
		if sim.Reason, ok = revertReason(data); !ok {
			sim.Reason = noReason
		}
	}
	return sim, nil
}

var selBalanceOf = selector("balanceOf(address)")

// InsufficientFundsError is a swap whose input the Safe does not hold.
type InsufficientFundsError struct {
	Token Address
	Have  *big.Int
	Need  *big.Int
}

func (e *InsufficientFundsError) Error() string {
	return fmt.Sprintf("evmswap: the Safe holds %s of %s, the swap needs %s", e.Have, e.Token, e.Need)
}

// CheckFunds reads the Safe's balance of the swap's input token and refuses a
// swap it cannot pay for, naming both amounts. It runs before Simulate because
// a failing batch reverts without a reason: MultiSendCallOnly does not pass the
// router's reason on, so the most common failure would otherwise read as
// "reverted" and nothing more.
func (c *Client) CheckFunds(ctx context.Context, s Swap) error {
	res, err := c.call(ctx, nil, s.TokenIn, append(selBalanceOf[:], wordAddress(s.Safe)...))
	if err != nil {
		return err
	}
	have, err := reader{res}.uint(0)
	if err != nil {
		return err
	}
	if have.Cmp(s.AmountIn) < 0 {
		return &InsufficientFundsError{Token: s.TokenIn, Have: have, Need: s.AmountIn}
	}
	return nil
}

// noReason is the reason of a simulation that reverted without data, which is
// how every failing MultiSend batch reverts.
const noReason = "the batch reverted without a reason (MultiSendCallOnly does not pass one on)"

var selErrorString = selector("Error(string)")

// revertReason decodes a Solidity Error(string) revert; false for anything
// else (a custom error, an empty revert).
func revertReason(data []byte) (string, bool) {
	r, err := cutSelector(data, selErrorString, "Error(string)")
	if err != nil {
		return "", false
	}
	s, err := r.bytesAt(0)
	if err != nil {
		return "", false
	}
	return string(s), true
}
