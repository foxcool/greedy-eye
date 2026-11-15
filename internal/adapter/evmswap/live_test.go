//go:build integration

package evmswap

import (
	"context"
	"math/big"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// These run read-only eth_calls against a real Base mainnet node: no key, no
// transaction, nothing that costs gas. EVMSWAP_BASE_RPC names the endpoint,
// for example https://mainnet.base.org.
func liveClient(t *testing.T) *Client {
	t.Helper()
	url := os.Getenv("EVMSWAP_BASE_RPC")
	if url == "" {
		t.Skip("EVMSWAP_BASE_RPC is not set")
	}
	return NewClient(Config{Endpoint: url})
}

// TestLiveBestQuote: the encoding reaches the deployed QuoterV2 and its answer
// is a plausible WETH amount for 25 USDC.
func TestLiveBestQuote(t *testing.T) {
	c := liveClient(t)
	fee, out, err := c.BestQuote(context.Background(), Base, usdc, weth, big.NewInt(25_000_000))
	require.NoError(t, err)
	t.Logf("25 USDC -> %s wei WETH at fee %d", out, fee)
	// Between 0.0001 and 1 WETH: a sanity band, not a price check.
	assert.Positive(t, out.Cmp(big.NewInt(100_000_000_000_000)))
	assert.Negative(t, out.Cmp(new(big.Int).Exp(big.NewInt(10), big.NewInt(18), nil)))
}

// safeSingletonL2 is the SafeL2 v1.4.1 singleton (safe-deployments,
// canonical). It holds no USDC, and simulateAndRevert runs on it like on any
// Safe, so a swap simulated from it must fail in the swap, not in the
// simulation machinery.
var safeSingletonL2 = mustAddress("0x29fcB43b46531BcA003ddC8FCB67FFE91900C762")

// TestLiveSimulateWithoutFunds: the batch built for a Safe with no input token
// runs through the real Safe, MultiSendCallOnly and router, and comes back as
// a failed simulation with the router's reason.
func TestLiveSimulateWithoutFunds(t *testing.T) {
	c := liveClient(t)
	ctx := context.Background()
	fee, quoted, err := c.BestQuote(ctx, Base, usdc, weth, big.NewInt(25_000_000))
	require.NoError(t, err)
	minOut, err := MinOut(quoted, 50)
	require.NoError(t, err)

	tx, err := Build(Base, Swap{
		Safe: safeSingletonL2, TokenIn: usdc, TokenOut: weth, Fee: fee,
		AmountIn: big.NewInt(25_000_000), MinOut: minOut, Deadline: time.Now().Add(time.Hour),
	})
	require.NoError(t, err)

	sim, err := c.Simulate(ctx, Base, safeSingletonL2, tx)
	require.NoError(t, err)
	t.Logf("simulation: ok=%v gas=%v reason=%q", sim.OK, sim.GasUsed, sim.Reason)
	assert.False(t, sim.OK, "a Safe without USDC cannot swap it")
	assert.Equal(t, noReason, sim.Reason, "MultiSendCallOnly reverts empty")

	// So the funds check, not the simulation, is what names the cause.
	var short *InsufficientFundsError
	require.ErrorAs(t, c.CheckFunds(ctx, Swap{Safe: safeSingletonL2, TokenIn: usdc, AmountIn: big.NewInt(25_000_000)}), &short)
	// Not zero: someone has sent the singleton dust. Less than the swap needs.
	assert.Negative(t, short.Have.Cmp(short.Need))
}
