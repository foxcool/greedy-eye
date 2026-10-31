package portfolio

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"connectrpc.com/connect"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	apiv1 "github.com/foxcool/greedy-eye/api/v1"
	"github.com/foxcool/greedy-eye/internal/adapter/coingecko"
	"github.com/foxcool/greedy-eye/internal/adapter/ratelimit"
	"github.com/foxcool/greedy-eye/internal/entity"
	"github.com/foxcool/greedy-eye/internal/service/credentials"
	"github.com/foxcool/greedy-eye/internal/service/marketdata"
	"github.com/foxcool/greedy-eye/internal/spend"
)

// keyAccounts serves the same accounts as the caller's own market-data
// accounts and nothing else.
type keyAccounts []*entity.Account

func (k keyAccounts) ListUserAccountsByCapability(context.Context, string, entity.AccountCapability) ([]*entity.Account, error) {
	return k, nil
}

func (keyAccounts) ListSystemAccountsByCapability(context.Context, entity.AccountCapability) ([]*entity.Account, error) {
	return nil, nil
}

func (keyAccounts) ListCapabilityOwners(context.Context, entity.AccountCapability) ([]string, error) {
	return nil, nil
}

// revokedKeyProvider answers 401 to the key named revoked and an empty list to
// any other, standing in for CoinGecko behind the real rate limiter.
type revokedKeyProvider string

func (k revokedKeyProvider) RoundTrip(req *http.Request) (*http.Response, error) {
	status, body := http.StatusOK, "[]"
	if req.Header.Get("x-cg-demo-api-key") == string(k) {
		status, body = http.StatusUnauthorized, `{"status":{"error_code":10002}}`
	}
	return &http.Response{StatusCode: status, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(body)), Request: req}, nil
}

// TestAccountHealthNamesARejectedKey: the "shadowed" signal that named an
// account standing behind a dead one went with the failover change, and a
// revoked first key read as OK while its own account priced nothing. The
// provider's refusal is now kept on the key, and the account that holds it says so.
func TestAccountHealthNamesARejectedKey(t *testing.T) {
	accounts := keyAccounts{}
	for _, id := range []string{"revoked", "fresh"} {
		a := healthAccount(id, entity.AccountTypeService)
		a.Capabilities = []entity.AccountCapability{entity.CapabilityMarketData}
		a.Data = map[string]string{credentials.DataProviderKey: "coingecko", "api_key": "k-" + id}
		accounts = append(accounts, a)
	}

	limits := ratelimit.NewRegistry(nil)
	resolver := credentials.NewResolver(credentials.Config{
		Source: accounts,
		PriceProviders: map[string]credentials.PriceProviderFactory{
			"coingecko": func(a *entity.Account) (marketdata.PriceProvider, error) {
				cred := ratelimit.Credential{Provider: "coingecko", APIKey: a.Data["api_key"], Account: a.ID}
				return coingecko.NewProvider(coingecko.NewClient(coingecko.Config{
					APIKey:    a.Data["api_key"],
					Transport: limits.Transport(cred, revokedKeyProvider("k-revoked")),
					Budget:    limits.Budget(cred),
				})), nil
			},
		},
	})

	// The sweep asks every key once.
	inv, err := resolver.PriceInventoryFor(context.Background(), testUserID)
	require.NoError(t, err)
	btc := []*entity.Asset{{ID: "btc", Symbol: "BTC", Market: entity.MarketCrypto}}
	for _, k := range inv.Keys["coingecko"] {
		_, err := k.Provider.FetchPrices(context.Background(), btc)
		if k.AccountID == "revoked" {
			require.ErrorIs(t, err, spend.ErrCredentialRejected)
		} else {
			require.NoError(t, err)
		}
	}

	store := newHealthStore()
	store.On("ListAccounts", mock.Anything, mock.Anything).Return([]*entity.Account(accounts), "", nil)
	store.On("ListSyncDeferrals", mock.Anything, testUserID, "").Return(nil, nil)
	resp, err := newHandler(store).WithPriceSourceHealth(resolver).
		GetAccountHealth(ctxWithUser(testUserID), connect.NewRequest(&apiv1.GetAccountHealthRequest{}))
	require.NoError(t, err)

	got := byAccount(resp.Msg)
	revoked := got["revoked"]
	assert.Equal(t, apiv1.HealthState_HEALTH_STATE_UNUSABLE, revoked.State, "a dead key is not OK")
	require.Len(t, revoked.Reasons, 1)
	assert.Contains(t, revoked.Reasons[0].Message, "rejected this credential")
	assert.Equal(t, apiv1.HealthState_HEALTH_STATE_OK, got["fresh"].State)
	assert.Equal(t, apiv1.HealthState_HEALTH_STATE_OK, bySource(resp.Msg)["coingecko"].State,
		"the source still has a key that answers")
}
