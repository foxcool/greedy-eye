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

// TestSyncAccount_NoProviderAccount verifies that SyncAccount returns
// CodeUnimplemented when nobody has configured a wallet provider: since v0.7.0
// the key lives in a provider account, and an empty database has none.
func TestSyncAccount_NoProviderAccount(t *testing.T) {
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
		"SyncAccount without a wallet provider account should return CodeUnimplemented")
}
