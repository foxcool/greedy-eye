package execution

import (
	"testing"

	apiv1 "github.com/foxcool/greedy-eye/api/v1"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParsePolicy(t *testing.T) {
	wallet := apiv1.AccountType_ACCOUNT_TYPE_WALLET
	manual := apiv1.AccountType_ACCOUNT_TYPE_MANUAL

	p, err := ParsePolicy(manual, nil)
	require.NoError(t, err, "paper without a policy has no limits")
	assert.NoError(t, p.Check(Trade{QuoteAmount: decimal.NewFromInt(1_000_000), AssetID: "any", QuoteAssetID: "thing"}))

	for name, data := range map[string]map[string]string{
		"real account without a policy": nil,
		"maximum without assets":        {PolicyMaxTradeAmount: "50"},
		"assets without a maximum":      {PolicyAllowedAssets: "weth,usdc"},
		"maximum not a number":          {PolicyMaxTradeAmount: "lots", PolicyAllowedAssets: "weth"},
		"maximum zero":                  {PolicyMaxTradeAmount: "0", PolicyAllowedAssets: "weth"},
		"assets list empty":             {PolicyMaxTradeAmount: "50", PolicyAllowedAssets: " , "},
		"slippage over the ceiling":     {PolicyMaxTradeAmount: "50", PolicyAllowedAssets: "weth", PolicyMaxSlippageBps: "301"},
		"slippage not a number":         {PolicyMaxTradeAmount: "50", PolicyAllowedAssets: "weth", PolicyMaxSlippageBps: "0.5%"},
	} {
		_, err := ParsePolicy(wallet, data)
		assert.Error(t, err, name)
	}

	p, err = ParsePolicy(wallet, map[string]string{PolicyMaxTradeAmount: "50", PolicyAllowedAssets: "weth, usdc"})
	require.NoError(t, err)
	assert.Equal(t, uint32(DefaultSlippageBps), p.SlippageBps)

	p, err = ParsePolicy(wallet, map[string]string{PolicyMaxTradeAmount: "50", PolicyAllowedAssets: "weth", PolicyMaxSlippageBps: "300"})
	require.NoError(t, err)
	assert.Equal(t, uint32(300), p.SlippageBps, "the ceiling itself is allowed")
}

func TestPolicyCheck(t *testing.T) {
	p, err := ParsePolicy(apiv1.AccountType_ACCOUNT_TYPE_WALLET, map[string]string{
		PolicyMaxTradeAmount: "50", PolicyAllowedAssets: "weth,usdc",
	})
	require.NoError(t, err)
	buy := func(amount int64, asset, quote string) Trade {
		return Trade{Side: SideBuy, AssetID: asset, QuoteAssetID: quote, QuoteAmount: decimal.NewFromInt(amount)}
	}

	assert.NoError(t, p.Check(buy(50, "weth", "usdc")), "exactly the maximum is allowed")
	assert.ErrorContains(t, p.Check(buy(51, "weth", "usdc")), PolicyMaxTradeAmount)
	assert.ErrorContains(t, p.Check(buy(10, "pepe", "usdc")), "pepe")
	assert.ErrorContains(t, p.Check(buy(10, "weth", "dai")), "dai", "the quote asset is checked too")
}
