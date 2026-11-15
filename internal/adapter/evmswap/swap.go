package evmswap

import (
	"bytes"
	"errors"
	"fmt"
	"math/big"
	"time"

	"github.com/shopspring/decimal"
)

// Network is one chain's contracts. Addresses are the official deployments:
// Uniswap's v3 deployment list for the router and quoter, Safe's
// safe-deployments (v1.4.1, canonical) for MultiSendCallOnly and
// SimulateTxAccessor.
type Network struct {
	Name        string
	ChainID     uint64
	Router      Address // Uniswap V3 SwapRouter02
	Quoter      Address // Uniswap V3 QuoterV2
	MultiSend   Address // Safe MultiSendCallOnly
	TxSimulator Address // Safe SimulateTxAccessor
}

var (
	Base = Network{
		Name:        "base",
		ChainID:     8453,
		Router:      mustAddress("0x2626664c2603336E57B271c5C0b26F421741e481"),
		Quoter:      mustAddress("0x3d4e44Eb1374240CE5F1B871ab261CD16335B76a"),
		MultiSend:   mustAddress("0x9641d764fc13c8B624c04430C7356C1C7C8102e2"),
		TxSimulator: mustAddress("0x3d4BA2E0884aa488718476ca2FB8Efc291A46199"),
	}
	BaseSepolia = Network{
		Name:        "base-sepolia",
		ChainID:     84532,
		Router:      mustAddress("0x94cC0AaC535CCDB3C01d6787D6413C739ae12bc4"),
		Quoter:      mustAddress("0xC5290058841028F1614F3A6F0F5816cAd0df5E27"),
		MultiSend:   mustAddress("0x9641d764fc13c8B624c04430C7356C1C7C8102e2"),
		TxSimulator: mustAddress("0x3d4BA2E0884aa488718476ca2FB8Efc291A46199"),
	}
)

// FeeTiers are the Uniswap V3 pool fees, in hundredths of a basis point, that
// a quote is asked for.
var FeeTiers = []uint32{100, 500, 3000, 10000}

// Swap is one exact-input swap from a Safe's balance back into the Safe.
// Amounts are in the tokens' smallest units.
type Swap struct {
	Safe     Address
	TokenIn  Address
	TokenOut Address
	Fee      uint32
	AmountIn *big.Int
	MinOut   *big.Int
	Deadline time.Time
}

// Operation is how a Safe runs its transaction's target.
type Operation uint8

const (
	OpCall         Operation = 0
	OpDelegateCall Operation = 1
)

// SafeTx is the unsigned transaction a Safe executes: the fields a proposal
// carries, before nonce and gas settings.
type SafeTx struct {
	To        Address
	Value     *big.Int
	Data      []byte
	Operation Operation
}

// MinOut is the least output a swap accepts: the quote less the slippage
// ceiling, rounded down so the ceiling is never exceeded.
func MinOut(quoted *big.Int, slippageBps uint32) (*big.Int, error) {
	if quoted == nil || quoted.Sign() <= 0 {
		return nil, errors.New("evmswap: no positive quote to take slippage from")
	}
	if slippageBps >= 10000 {
		return nil, fmt.Errorf("evmswap: slippage of %d bps leaves nothing to protect", slippageBps)
	}
	out := new(big.Int).Mul(quoted, big.NewInt(int64(10000-slippageBps)))
	return out.Quo(out, big.NewInt(10000)), nil
}

func (s Swap) validate() error {
	switch {
	case s.AmountIn == nil || s.AmountIn.Sign() <= 0:
		return errors.New("evmswap: amount in must be positive")
	case s.MinOut == nil || s.MinOut.Sign() <= 0:
		// A zero minimum signs away the whole output to whoever moves the pool.
		return errors.New("evmswap: minimum out must be positive")
	case s.TokenIn == s.TokenOut:
		return errors.New("evmswap: token in and token out are the same")
	case s.Fee == 0 || s.Fee >= 1<<24:
		return fmt.Errorf("evmswap: fee %d is not a uint24 pool fee", s.Fee)
	case s.Deadline.Unix() <= 0:
		return errors.New("evmswap: deadline is not set")
	}
	return nil
}

// Build turns a swap into the Safe transaction that executes it: a delegate
// call to MultiSendCallOnly running an ERC-20 approve of exactly the input
// amount to the router, then the router's multicall with the deadline around
// one exactInputSingle paying out to the Safe. Approve and swap land together
// or revert together.
func Build(n Network, s Swap) (SafeTx, error) {
	if err := s.validate(); err != nil {
		return SafeTx{}, err
	}
	approve, err := encodeApprove(n.Router, s.AmountIn)
	if err != nil {
		return SafeTx{}, err
	}
	swap, err := encodeSwap(n, s)
	if err != nil {
		return SafeTx{}, err
	}
	packed := append(packCall(s.TokenIn, approve), packCall(n.Router, swap)...)
	data := append(selMultiSend[:], wordUint64(wordSize)...)
	data = append(data, encodeBytes(packed)...)
	return SafeTx{To: n.MultiSend, Value: new(big.Int), Data: data, Operation: OpDelegateCall}, nil
}

func encodeApprove(spender Address, amount *big.Int) ([]byte, error) {
	w, err := wordUint(amount)
	if err != nil {
		return nil, err
	}
	out := append(selApprove[:], wordAddress(spender)...)
	return append(out, w...), nil
}

func encodeSwap(n Network, s Swap) ([]byte, error) {
	amountIn, err := wordUint(s.AmountIn)
	if err != nil {
		return nil, err
	}
	minOut, err := wordUint(s.MinOut)
	if err != nil {
		return nil, err
	}
	inner := append([]byte{}, selExactInputSingle[:]...)
	for _, w := range [][]byte{
		wordAddress(s.TokenIn), wordAddress(s.TokenOut), wordUint64(uint64(s.Fee)),
		wordAddress(s.Safe), amountIn, minOut, wordUint64(0), // no price limit: minOut bounds the trade
	} {
		inner = append(inner, w...)
	}

	// multicall(uint256 deadline, bytes[] data) with a single element.
	out := append([]byte{}, selMulticallDeadline[:]...)
	out = append(out, wordUint64(uint64(s.Deadline.Unix()))...) // #nosec G115 -- validate refuses a deadline at or before the epoch
	out = append(out, wordUint64(2*wordSize)...)                // offset of the array
	out = append(out, wordUint64(1)...)                         // array length
	out = append(out, wordUint64(wordSize)...)                  // offset of element 0, after the offsets
	out = append(out, encodeBytes(inner)...)
	return out, nil
}

// packCall is one MultiSend entry: operation, target, value, data length and
// data, tightly packed. MultiSendCallOnly runs plain calls only, and nothing
// here sends ETH.
func packCall(to Address, data []byte) []byte {
	out := []byte{byte(OpCall)}
	out = append(out, to[:]...)
	out = append(out, wordUint64(0)...)
	out = append(out, wordUint64(uint64(len(data)))...)
	return append(out, data...)
}

// Decode reads a Safe transaction back into the swap it performs, and refuses
// anything that is not exactly what Build produces for this network and Safe:
// another target, another operation, an approve for more than the swap
// spends or to another spender, a recipient other than the Safe, a price
// limit, or any extra call.
func Decode(n Network, safe Address, tx SafeTx) (Swap, error) {
	var s Swap
	if tx.To != n.MultiSend || tx.Operation != OpDelegateCall || (tx.Value != nil && tx.Value.Sign() != 0) {
		return s, errors.New("evmswap: not a MultiSendCallOnly delegate call without value")
	}
	args, err := cutSelector(tx.Data, selMultiSend, "multiSend")
	if err != nil {
		return s, err
	}
	packed, err := args.bytesAt(0)
	if err != nil {
		return s, err
	}
	calls, err := unpackCalls(packed)
	if err != nil {
		return s, err
	}
	if len(calls) != 2 {
		return s, fmt.Errorf("evmswap: %d calls in the batch, want approve and swap", len(calls))
	}
	approve, swap := calls[0], calls[1]
	if swap.to != n.Router {
		return s, errors.New("evmswap: second call is not to the router")
	}
	if s, err = decodeSwap(swap.data); err != nil {
		return s, err
	}
	if s.Safe != safe {
		return s, fmt.Errorf("evmswap: swap pays out to %s, not to the Safe", s.Safe)
	}
	if approve.to != s.TokenIn {
		return s, errors.New("evmswap: approve is not on the token the swap spends")
	}
	spender, amount, err := decodeApprove(approve.data)
	if err != nil {
		return s, err
	}
	if spender != n.Router || amount.Cmp(s.AmountIn) != 0 {
		return s, errors.New("evmswap: approve is not exactly the swap amount to the router")
	}
	return s, s.validate()
}

type packedCall struct {
	to   Address
	data []byte
}

func unpackCalls(b []byte) ([]packedCall, error) {
	var calls []packedCall
	for len(b) > 0 {
		const head = 1 + 20 + wordSize + wordSize
		if len(b) < head {
			return nil, errShort
		}
		if Operation(b[0]) != OpCall {
			return nil, errors.New("evmswap: batch holds a delegate call")
		}
		var c packedCall
		copy(c.to[:], b[1:21])
		if new(big.Int).SetBytes(b[21:53]).Sign() != 0 {
			return nil, errors.New("evmswap: batch call sends value")
		}
		n := new(big.Int).SetBytes(b[53:85])
		if !n.IsInt64() || n.Int64() > int64(len(b)-head) {
			return nil, errShort
		}
		end := head + int(n.Int64())
		c.data = b[head:end]
		calls = append(calls, c)
		b = b[end:]
	}
	return calls, nil
}

func decodeApprove(data []byte) (Address, *big.Int, error) {
	r, err := cutSelector(data, selApprove, "approve")
	if err != nil {
		return Address{}, nil, err
	}
	if len(r.b) != 2*wordSize {
		return Address{}, nil, errors.New("evmswap: approve has extra data")
	}
	spender, err := r.address(0)
	if err != nil {
		return Address{}, nil, err
	}
	amount, err := r.uint(1)
	return spender, amount, err
}

func decodeSwap(data []byte) (Swap, error) {
	var s Swap
	r, err := cutSelector(data, selMulticallDeadline, "multicall(deadline)")
	if err != nil {
		return s, err
	}
	deadline, err := r.uint(0)
	if err != nil {
		return s, err
	}
	if !deadline.IsInt64() {
		return s, errors.New("evmswap: deadline out of range")
	}
	s.Deadline = time.Unix(deadline.Int64(), 0).UTC()

	arrOff, err := r.small(1)
	if err != nil {
		return s, err
	}
	arr := reader{r.b[arrOff:]}
	if count, err := arr.small(0); err != nil || count != 1 {
		return s, errors.New("evmswap: multicall must carry exactly one call")
	}
	elems := reader{arr.b[wordSize:]}
	inner, err := elems.bytesAt(0)
	if err != nil {
		return s, err
	}

	p, err := cutSelector(inner, selExactInputSingle, "exactInputSingle")
	if err != nil {
		return s, err
	}
	if len(p.b) != 7*wordSize {
		return s, errors.New("evmswap: exactInputSingle has extra data")
	}
	if s.TokenIn, err = p.address(0); err != nil {
		return s, err
	}
	if s.TokenOut, err = p.address(1); err != nil {
		return s, err
	}
	fee, err := p.uint(2)
	if err != nil || !fee.IsUint64() || fee.Uint64() >= 1<<24 {
		return s, errors.New("evmswap: fee is not a uint24")
	}
	s.Fee = uint32(fee.Uint64()) // #nosec G115 -- checked below 1<<24 above
	if s.Safe, err = p.address(3); err != nil {
		return s, err
	}
	if s.AmountIn, err = p.uint(4); err != nil {
		return s, err
	}
	if s.MinOut, err = p.uint(5); err != nil {
		return s, err
	}
	limit, err := p.word(6)
	if err != nil {
		return s, err
	}
	if !bytes.Equal(limit, make([]byte, wordSize)) {
		return s, errors.New("evmswap: swap sets a price limit")
	}
	return s, nil
}

// Token is what a description needs to name an amount.
type Token struct {
	Symbol   string
	Decimals int32
}

// Describe is the one-line summary shown next to a proposal; it must read the
// same as what the Safe App and the hardware wallet show for the transaction.
func Describe(s Swap, in, out Token) string {
	return fmt.Sprintf("swap %s %s -> >=%s %s on Uniswap V3 (fee %s%%), until %s",
		decimal.NewFromBigInt(s.AmountIn, -in.Decimals).String(), in.Symbol,
		decimal.NewFromBigInt(s.MinOut, -out.Decimals).String(), out.Symbol,
		decimal.New(int64(s.Fee), -4).String(), s.Deadline.UTC().Format(time.RFC3339))
}
