//go:build smoke

package smoke_test

import (
	"context"
	"testing"

	"connectrpc.com/connect"
	v1 "github.com/foxcool/greedy-eye/api/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPortfolioCRUD(t *testing.T) {
	resetDB(t)
	ctx := context.Background()
	client := newPortfolioClient(smokeTestUserID)

	// Create
	createResp, err := client.CreatePortfolio(ctx, connect.NewRequest(&v1.CreatePortfolioRequest{
		Portfolio: &v1.Portfolio{Name: "My Crypto"},
	}))
	require.NoError(t, err)
	portID := createResp.Msg.GetId()
	require.NotEmpty(t, portID)
	assert.Equal(t, "My Crypto", createResp.Msg.GetName())
	assert.NotEmpty(t, createResp.Msg.GetUserId(), "user_id must be set from X-User-Id")

	// Get
	getResp, err := client.GetPortfolio(ctx, connect.NewRequest(&v1.GetPortfolioRequest{Id: portID}))
	require.NoError(t, err)
	assert.Equal(t, portID, getResp.Msg.GetId())

	// Update name
	desc := "Updated description"
	_, err = client.UpdatePortfolio(ctx, connect.NewRequest(&v1.UpdatePortfolioRequest{
		Portfolio: &v1.Portfolio{Id: portID, Name: "My Crypto", Description: &desc},
	}))
	require.NoError(t, err)

	// List — user sees their own portfolio
	listResp, err := client.ListPortfolios(ctx, connect.NewRequest(&v1.ListPortfoliosRequest{}))
	require.NoError(t, err)
	assert.Len(t, listResp.Msg.GetPortfolios(), 1)

	// User isolation: a different user sees no portfolios
	otherClient := newPortfolioClient(smokeTestOtherUserID)
	otherResp, err := otherClient.ListPortfolios(ctx, connect.NewRequest(&v1.ListPortfoliosRequest{}))
	require.NoError(t, err)
	assert.Empty(t, otherResp.Msg.GetPortfolios(), "other user must not see portfolios they don't own")

	// Delete
	_, err = client.DeletePortfolio(ctx, connect.NewRequest(&v1.DeletePortfolioRequest{Id: portID}))
	require.NoError(t, err)

	_, err = client.GetPortfolio(ctx, connect.NewRequest(&v1.GetPortfolioRequest{Id: portID}))
	require.Error(t, err)
	var connectErr *connect.Error
	require.ErrorAs(t, err, &connectErr)
	assert.Equal(t, connect.CodeNotFound, connectErr.Code())
}

func TestAccountCRUD(t *testing.T) {
	resetDB(t)
	ctx := context.Background()
	client := newPortfolioClient(smokeTestUserID)

	// Create a portfolio to attach the account to
	portResp, err := client.CreatePortfolio(ctx, connect.NewRequest(&v1.CreatePortfolioRequest{
		Portfolio: &v1.Portfolio{Name: "Test Portfolio"},
	}))
	require.NoError(t, err)
	portID := portResp.Msg.GetId()

	// Create wallet account
	walletAddr := "0xd8dA6BF26964aF9D7eEd9e03E53415D37aA96045"
	createResp, err := client.CreateAccount(ctx, connect.NewRequest(&v1.CreateAccountRequest{
		Account: &v1.Account{
			Name:        "Vitalik's Wallet",
			Type:        v1.AccountType_ACCOUNT_TYPE_WALLET,
			PortfolioId: &portID,
			Data:        map[string]string{"address": walletAddr},
		},
	}))
	require.NoError(t, err)
	accountID := createResp.Msg.GetId()
	require.NotEmpty(t, accountID)
	assert.Equal(t, walletAddr, createResp.Msg.GetData()["address"])

	// Get
	getResp, err := client.GetAccount(ctx, connect.NewRequest(&v1.GetAccountRequest{Id: accountID}))
	require.NoError(t, err)
	assert.Equal(t, accountID, getResp.Msg.GetId())

	// Update name
	_, err = client.UpdateAccount(ctx, connect.NewRequest(&v1.UpdateAccountRequest{
		Account: &v1.Account{Id: accountID, Name: "Vitalik's ETH Wallet", Type: v1.AccountType_ACCOUNT_TYPE_WALLET},
	}))
	require.NoError(t, err)

	// List accounts for this user
	listResp, err := client.ListAccounts(ctx, connect.NewRequest(&v1.ListAccountsRequest{}))
	require.NoError(t, err)
	assert.Len(t, listResp.Msg.GetAccounts(), 1)

	// Delete
	_, err = client.DeleteAccount(ctx, connect.NewRequest(&v1.DeleteAccountRequest{Id: accountID}))
	require.NoError(t, err)

	_, err = client.GetAccount(ctx, connect.NewRequest(&v1.GetAccountRequest{Id: accountID}))
	require.Error(t, err)
	var connectErr *connect.Error
	require.ErrorAs(t, err, &connectErr)
	assert.Equal(t, connect.CodeNotFound, connectErr.Code())
}
