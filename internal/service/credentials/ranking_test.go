package credentials

import (
	"context"
	"testing"
	"time"

	"github.com/foxcool/greedy-eye/internal/entity"
	"github.com/foxcool/greedy-eye/internal/service/marketdata"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// rankingProvider is a price provider that can also rank the market.
type rankingProvider struct{ fakeProvider }

func (*rankingProvider) TopByMarketCap(context.Context, int) (entity.MarketRanking, error) {
	return entity.MarketRanking{}, nil
}
func (*rankingProvider) RankedCoinID(*entity.Asset) (string, bool) { return "", false }

func rankerNames(rankers []entity.MarketRanker) []string {
	var out []string
	for _, r := range rankers {
		out = append(out, r.(*rankingProvider).name)
	}
	return out
}

// A provider that cannot rank is not offered.
func TestMarketRankersFollowThePriceRegistry(t *testing.T) {
	now := time.Now()
	owned := account("own", "coingecko", now)
	owned.UserID = "u1"
	r := NewResolver(Config{
		Source: &fakeSource{
			user:   map[string][]*entity.Account{"u1": {owned}},
			system: []*entity.Account{account("shared", "coingecko", now), account("moex-1", "moex", now)},
		},
		PriceProviders: map[string]PriceProviderFactory{
			"coingecko": func(a *entity.Account) (marketdata.PriceProvider, error) {
				return &rankingProvider{fakeProvider{name: a.ID}}, nil
			},
			"moex": func(a *entity.Account) (marketdata.PriceProvider, error) {
				return &fakeProvider{name: a.ID}, nil
			},
		},
	})

	got, err := r.MarketRankersFor(context.Background(), "u1")
	require.NoError(t, err)
	assert.Equal(t, []string{"own", "shared"}, rankerNames(got))
}

func TestMarketRankersFallBackToKeyless(t *testing.T) {
	r := NewResolver(Config{
		Source: &fakeSource{},
		KeylessPriceProviders: map[string]marketdata.PriceProvider{
			"coingecko": &rankingProvider{fakeProvider{name: "keyless"}},
			"cbr":       &fakeProvider{name: "cbr"},
		},
	})

	got, err := r.MarketRankersFor(context.Background(), "u1")
	require.NoError(t, err)
	assert.Equal(t, []string{"keyless"}, rankerNames(got))
}
