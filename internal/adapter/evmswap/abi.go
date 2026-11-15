// Package evmswap builds, checks and simulates a token swap that a Safe
// executes: one Safe transaction that approves the exact input amount to the
// Uniswap V3 router and swaps it with a minimum output and a deadline.
//
// Only that one shape is built and only that one shape is decoded. A
// transaction GE proposes for signature must be describable in words that the
// hardware wallet screen can confirm, so arbitrary calldata is never produced
// and never accepted.
package evmswap

import (
	"encoding/hex"
	"errors"
	"fmt"
	"math/big"
	"strings"

	"golang.org/x/crypto/sha3"
)

// Address is a 20-byte EVM account or contract address.
type Address [20]byte

// ParseAddress reads a 0x-prefixed, 40-hex-digit address. Case is not checked:
// EIP-55 checksums are a typing aid, and every address here comes from config
// or from the catalogue rather than from a person's keyboard.
func ParseAddress(s string) (Address, error) {
	var a Address
	h, ok := strings.CutPrefix(s, "0x")
	if !ok || len(h) != 40 {
		return a, fmt.Errorf("evmswap: %q is not a 0x-prefixed 20-byte address", s)
	}
	if _, err := hex.Decode(a[:], []byte(h)); err != nil {
		return a, fmt.Errorf("evmswap: %q is not a hex address: %w", s, err)
	}
	return a, nil
}

func mustAddress(s string) Address {
	a, err := ParseAddress(s)
	if err != nil {
		panic(err)
	}
	return a
}

func (a Address) String() string { return "0x" + hex.EncodeToString(a[:]) }

// selector is the first four bytes of the keccak-256 of a function signature.
// Computed rather than written down, so a typo in a constant cannot route a
// call to a different function.
func selector(signature string) [4]byte {
	h := sha3.NewLegacyKeccak256()
	h.Write([]byte(signature))
	var s [4]byte
	copy(s[:], h.Sum(nil))
	return s
}

var (
	selApprove            = selector("approve(address,uint256)")
	selExactInputSingle   = selector("exactInputSingle((address,address,uint24,address,uint256,uint256,uint160))")
	selMulticallDeadline  = selector("multicall(uint256,bytes[])")
	selMultiSend          = selector("multiSend(bytes)")
	selQuoteExactInSingle = selector("quoteExactInputSingle((address,address,uint256,uint24,uint160))")
	selSimulateAndRevert  = selector("simulateAndRevert(address,bytes)")
	selSimulate           = selector("simulate(address,uint256,bytes,uint8)")
)

const wordSize = 32

var maxUint256 = new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), 256), big.NewInt(1))

// wordUint encodes a non-negative integer of at most 256 bits.
func wordUint(v *big.Int) ([]byte, error) {
	if v == nil || v.Sign() < 0 || v.Cmp(maxUint256) > 0 {
		return nil, fmt.Errorf("evmswap: %v does not fit uint256", v)
	}
	w := make([]byte, wordSize)
	v.FillBytes(w)
	return w, nil
}

func wordUint64(v uint64) []byte {
	w, _ := wordUint(new(big.Int).SetUint64(v))
	return w
}

func wordAddress(a Address) []byte {
	w := make([]byte, wordSize)
	copy(w[12:], a[:])
	return w
}

// encodeBytes is the tail of a dynamic `bytes` value: its length, then the
// data padded to a whole number of words.
func encodeBytes(b []byte) []byte {
	out := wordUint64(uint64(len(b)))
	padded := make([]byte, (len(b)+wordSize-1)/wordSize*wordSize)
	copy(padded, b)
	return append(out, padded...)
}

// reader walks ABI-encoded data and fails on anything out of range, so a
// decoder never panics on a hostile payload.
type reader struct{ b []byte }

var errShort = errors.New("evmswap: encoded data is shorter than its layout")

func (r reader) word(i int) ([]byte, error) {
	start := i * wordSize
	if i < 0 || start+wordSize > len(r.b) {
		return nil, errShort
	}
	return r.b[start : start+wordSize], nil
}

func (r reader) uint(i int) (*big.Int, error) {
	w, err := r.word(i)
	if err != nil {
		return nil, err
	}
	return new(big.Int).SetBytes(w), nil
}

// small reads a word that must be a length or an offset: anything that does
// not fit the payload is refused before it can be used as an index.
func (r reader) small(i int) (int, error) {
	v, err := r.uint(i)
	if err != nil {
		return 0, err
	}
	if !v.IsInt64() || v.Int64() > int64(len(r.b)) {
		return 0, fmt.Errorf("evmswap: length or offset %v exceeds the payload", v)
	}
	return int(v.Int64()), nil
}

func (r reader) address(i int) (Address, error) {
	var a Address
	w, err := r.word(i)
	if err != nil {
		return a, err
	}
	for _, b := range w[:12] {
		if b != 0 {
			return a, errors.New("evmswap: address word has non-zero high bytes")
		}
	}
	copy(a[:], w[12:])
	return a, nil
}

// bytesAt reads a dynamic `bytes` whose offset (relative to this reader) is
// stored in word i.
func (r reader) bytesAt(i int) ([]byte, error) {
	off, err := r.small(i)
	if err != nil {
		return nil, err
	}
	if off%wordSize != 0 {
		return nil, errors.New("evmswap: misaligned offset")
	}
	tail := reader{r.b[off:]}
	n, err := tail.small(0)
	if err != nil {
		return nil, err
	}
	if wordSize+n > len(tail.b) {
		return nil, errShort
	}
	return tail.b[wordSize : wordSize+n], nil
}

// cutSelector splits a call into its selector and arguments, refusing any
// selector other than want.
func cutSelector(data []byte, want [4]byte, name string) (reader, error) {
	if len(data) < 4 || [4]byte(data[:4]) != want {
		return reader{}, fmt.Errorf("evmswap: not a %s call", name)
	}
	return reader{data[4:]}, nil
}
