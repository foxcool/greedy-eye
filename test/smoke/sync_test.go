//go:build smoke

package smoke_test

import (
	"context"
	"os"
	"testing"

	"connectrpc.com/connect"
	v1 "github.com/foxcool/greedy-eye/api/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestSyncAccount_NoMoralisKey verifies that SyncAccount returns CodeUnimplemented
// when the server was started without a Moralis API key (walletSyncer == nil).
func TestSyncAccount_NoMoralisKey(t *testing.T) {
	if os.Getenv("EYE_MORALIS_APIKEY") != "" {
		t.Skip("Moralis key present — run TestSyncAccount_WithMoralis instead")
	}

	resetDB(t)
	ctx := context.Background()
	client := newPortfolioClient(smokeTestUserID)

	portResp, err := client.CreatePortfolio(ctx, connect.NewRequest(&v1.CreatePortfolioRequest{
		Portfolio: &v1.Portfolio{Name: "Test Portfolio"},
	}))
	require.NoError(t, err)
	portID := portResp.Msg.GetId()

	walletAddr := "0xd8dA6BF26964aF9D7eEd9e03E53415D37aA96045"
	acctResp, err := client.CreateAccount(ctx, connect.NewRequest(&v1.CreateAccountRequest{
		Account: &v1.Account{
			Name:        "Vitalik's Wallet",
			Type:        v1.AccountType_ACCOUNT_TYPE_WALLET,
			PortfolioId: &portID,
			Data:        map[string]string{"address": walletAddr},
		},
	}))
	require.NoError(t, err)
	accountID := acctResp.Msg.GetId()

	_, err = client.SyncAccount(ctx, connect.NewRequest(&v1.SyncAccountRequest{
		AccountId: accountID,
	}))
	require.Error(t, err)
	var connectErr *connect.Error
	require.ErrorAs(t, err, &connectErr)
	assert.Equal(t, connect.CodeUnimplemented, connectErr.Code(),
		"SyncAccount without walletSyncer should return CodeUnimplemented")
}

// TestSyncAccount_WithMoralis requires EYE_MORALIS_APIKEY and tests the full sync flow
// against vitalik.eth — a public wallet with known token holdings on Ethereum mainnet.
//
// All operations are READ-ONLY: only wallet balances are queried, nothing is changed on-chain.
func TestSyncAccount_WithMoralis(t *testing.T) {
	if os.Getenv("EYE_MORALIS_APIKEY") == "" {
		t.Skip("EYE_MORALIS_APIKEY not set — skipping Moralis sync test")
	}

	resetDB(t)
	ctx := context.Background()
	client := newPortfolioClient(smokeTestUserID)

	portResp, err := client.CreatePortfolio(ctx, connect.NewRequest(&v1.CreatePortfolioRequest{
		Portfolio: &v1.Portfolio{Name: "Vitalik Portfolio"},
	}))
	require.NoError(t, err)
	portID := portResp.Msg.GetId()

	// vitalik.eth — public Ethereum wallet with significant on-chain activity.
	// READ-ONLY: SyncAccount only queries balances via Moralis API.
	walletAddr := "0xd8dA6BF26964aF9D7eEd9e03E53415D37aA96045"
	acctResp, err := client.CreateAccount(ctx, connect.NewRequest(&v1.CreateAccountRequest{
		Account: &v1.Account{
			Name:        "vitalik.eth",
			Type:        v1.AccountType_ACCOUNT_TYPE_WALLET,
			PortfolioId: &portID,
			Data:        map[string]string{"address": walletAddr},
		},
	}))
	require.NoError(t, err)
	accountID := acctResp.Msg.GetId()

	syncResp, err := client.SyncAccount(ctx, connect.NewRequest(&v1.SyncAccountRequest{
		AccountId: accountID,
	}))
	require.NoError(t, err)
	// Some tokens may fail (e.g. missing name in Moralis metadata) — that's acceptable.
	// Key assertion: at least some holdings were synced successfully.
	assert.Greater(t, syncResp.Msg.GetHoldingsUpserted(), int32(0),
		"vitalik.eth should have at least one token holding on Ethereum mainnet")

	// Verify holdings were stored — list them for the portfolio
	holdingsResp, err := client.ListHoldings(ctx, connect.NewRequest(&v1.ListHoldingsRequest{
		PortfolioId: &portID,
	}))
	require.NoError(t, err)
	assert.Greater(t, len(holdingsResp.Msg.GetHoldings()), 0,
		"at least one holding should be stored after sync")

	// Verify assets were created for discovered tokens
	mdClient := newMDClient(smokeTestUserID)
	assetsResp, err := mdClient.ListAssets(ctx, connect.NewRequest(&v1.ListAssetsRequest{}))
	require.NoError(t, err)
	assert.Greater(t, len(assetsResp.Msg.GetAssets()), 0,
		"assets should be created for tokens found in vitalik.eth wallet")
}
