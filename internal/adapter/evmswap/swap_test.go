package evmswap

import (
	"encoding/hex"
	"math/big"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var (
	safe = mustAddress("0x00000000000000000000000000000000000000a1")
	usdc = mustAddress("0x833589fCD6eDb6E08f4c7C32D4f71b54bdA02913")
	weth = mustAddress("0x4200000000000000000000000000000000000006")
)

func testSwap() Swap {
	return Swap{
		Safe: safe, TokenIn: usdc, TokenOut: weth, Fee: 500,
		AmountIn: big.NewInt(25_000_000),            // 25 USDC
		MinOut:   big.NewInt(7_400_000_000_000_000), // 0.0074 WETH
		Deadline: time.Date(2026, 11, 16, 12, 0, 0, 0, time.UTC),
	}
}

// TestSelectors: the selectors are computed from signatures; these are the
// values the deployed contracts answer to, so a wrong signature fails here
// rather than on chain.
func TestSelectors(t *testing.T) {
	for name, tc := range map[string]struct {
		got  [4]byte
		want string
	}{
		"approve":               {selApprove, "095ea7b3"},
		"exactInputSingle":      {selExactInputSingle, "04e45aaf"},
		"multicall(deadline)":   {selMulticallDeadline, "5ae401dc"},
		"multiSend":             {selMultiSend, "8d80ff0a"},
		"quoteExactInputSingle": {selQuoteExactInSingle, "c6a5026a"},
		"Error(string)":         {selErrorString, "08c379a0"},
	} {
		assert.Equal(t, tc.want, hex.EncodeToString(tc.got[:]), name)
	}
}

func TestBuildDecodeRoundTrip(t *testing.T) {
	s := testSwap()
	tx, err := Build(Base, s)
	require.NoError(t, err)
	assert.Equal(t, Base.MultiSend, tx.To)
	assert.Equal(t, OpDelegateCall, tx.Operation)
	assert.Zero(t, tx.Value.Sign())

	again, err := Build(Base, s)
	require.NoError(t, err)
	assert.Equal(t, tx.Data, again.Data, "the same swap builds the same bytes")

	got, err := Decode(Base, safe, tx)
	require.NoError(t, err)
	assert.Equal(t, s.TokenIn, got.TokenIn)
	assert.Equal(t, s.TokenOut, got.TokenOut)
	assert.Equal(t, s.Fee, got.Fee)
	assert.Equal(t, s.Safe, got.Safe)
	assert.Zero(t, s.AmountIn.Cmp(got.AmountIn))
	assert.Zero(t, s.MinOut.Cmp(got.MinOut))
	assert.True(t, s.Deadline.Equal(got.Deadline))

	assert.Equal(t, "swap 25 USDC -> >=0.0074 WETH on Uniswap V3 (fee 0.05%), until 2026-11-16T12:00:00Z",
		Describe(got, Token{Symbol: "USDC", Decimals: 6}, Token{Symbol: "WETH", Decimals: 18}))
}

func TestBuildRefusesAnUnprotectedSwap(t *testing.T) {
	for name, mutate := range map[string]func(*Swap){
		"zero minimum out": func(s *Swap) { s.MinOut = big.NewInt(0) },
		"zero amount":      func(s *Swap) { s.AmountIn = big.NewInt(0) },
		"no deadline":      func(s *Swap) { s.Deadline = time.Time{} },
		"same token":       func(s *Swap) { s.TokenOut = s.TokenIn },
		"fee not uint24":   func(s *Swap) { s.Fee = 1 << 24 },
		"zero Safe burns":  func(s *Swap) { s.Safe = Address{} },
		"zero token":       func(s *Swap) { s.TokenOut = Address{} },
	} {
		s := testSwap()
		mutate(&s)
		_, err := Build(Base, s)
		assert.Error(t, err, name)
	}
}

// TestDecodeRefusesAnythingElse: whatever Decode accepts is what the summary
// describes, so every way the bytes could say something else must fail.
func TestDecodeRefusesAnythingElse(t *testing.T) {
	s := testSwap()
	good, err := Build(Base, s)
	require.NoError(t, err)

	other := mustAddress("0x00000000000000000000000000000000000000b2")
	rebuild := func(mutate func(*Swap)) SafeTx {
		v := testSwap()
		mutate(&v)
		tx, err := Build(Base, v)
		require.NoError(t, err)
		return tx
	}
	withData := func(data []byte) SafeTx {
		tx := good
		tx.Data = data
		return tx
	}
	packedWith := func(calls ...[]byte) SafeTx {
		var packed []byte
		for _, c := range calls {
			packed = append(packed, c...)
		}
		data := append(selMultiSend[:], wordUint64(wordSize)...)
		return withData(append(data, encodeBytes(packed)...))
	}
	approveFor := func(spender Address, amount *big.Int) []byte {
		b, err := encodeApprove(spender, amount)
		require.NoError(t, err)
		return b
	}
	swapData, err := encodeSwap(Base, s)
	require.NoError(t, err)

	for name, tx := range map[string]SafeTx{
		"pays out elsewhere":   rebuild(func(v *Swap) { v.Safe = other }),
		"other network":        mustBuild(t, BaseSepolia, s),
		"plain call":           func() SafeTx { tx := good; tx.Operation = OpCall; return tx }(),
		"sends value":          func() SafeTx { tx := good; tx.Value = big.NewInt(1); return tx }(),
		"other target":         func() SafeTx { tx := good; tx.To = other; return tx }(),
		"approve for more":     packedWith(packCall(usdc, approveFor(Base.Router, big.NewInt(26_000_000))), packCall(Base.Router, swapData)),
		"approve to other":     packedWith(packCall(usdc, approveFor(other, s.AmountIn)), packCall(Base.Router, swapData)),
		"approve other token":  packedWith(packCall(weth, approveFor(Base.Router, s.AmountIn)), packCall(Base.Router, swapData)),
		"swap only":            packedWith(packCall(Base.Router, swapData)),
		"extra call":           packedWith(packCall(usdc, approveFor(Base.Router, s.AmountIn)), packCall(Base.Router, swapData), packCall(other, []byte{1})),
		"swap to other target": packedWith(packCall(usdc, approveFor(Base.Router, s.AmountIn)), packCall(other, swapData)),
		"truncated":            withData(good.Data[:len(good.Data)-40]),
		"empty":                withData(nil),
	} {
		_, err := Decode(Base, safe, tx)
		assert.Error(t, err, name)
	}
}

func mustBuild(t *testing.T, n Network, s Swap) SafeTx {
	t.Helper()
	tx, err := Build(n, s)
	require.NoError(t, err)
	return tx
}

func TestMinOut(t *testing.T) {
	got, err := MinOut(big.NewInt(10_000), 50)
	require.NoError(t, err)
	assert.Equal(t, int64(9_950), got.Int64())

	got, err = MinOut(big.NewInt(999), 1)
	require.NoError(t, err)
	assert.Equal(t, int64(998), got.Int64(), "rounded down, never above the ceiling")

	_, err = MinOut(big.NewInt(0), 50)
	assert.Error(t, err)
	_, err = MinOut(big.NewInt(100), 10_000)
	assert.Error(t, err, "100% slippage protects nothing")
}
