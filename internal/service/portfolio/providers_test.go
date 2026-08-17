package portfolio

import (
	"context"
	"log/slog"
	"os"
	"testing"

	"connectrpc.com/connect"
	apiv1 "github.com/foxcool/greedy-eye/api/v1"
	"github.com/foxcool/greedy-eye/internal/adapter/ratelimit"
	"github.com/foxcool/greedy-eye/internal/provider/catalog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type stubCatalog []catalog.Descriptor

func (s stubCatalog) Descriptors() []catalog.Descriptor { return s }

func providerHandler(pc ProviderCatalog) *Handler {
	h := NewHandler(nil, slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError})))
	if pc == nil {
		return h
	}
	return h.WithProviderCatalog(pc)
}

// TestListProvidersDescribesWithoutRevealing is the property that makes this RPC
// safe to serve to any caller: it states what a credential must look like and
// never what one is. Nothing it reads comes from an account.
func TestListProvidersDescribesWithoutRevealing(t *testing.T) {
	h := providerHandler(stubCatalog{{
		Slug:           "binance",
		Title:          "Binance",
		Kinds:          []catalog.Kind{catalog.KindExchange, catalog.KindPrice},
		Capabilities:   []string{"portfolio_sync", "market_data"},
		NeedsAPIKey:    true,
		NeedsAPISecret: true,
	}})

	resp, err := h.ListProviders(context.Background(), connect.NewRequest(&apiv1.ListProvidersRequest{}))
	require.NoError(t, err)
	require.Len(t, resp.Msg.Providers, 1)

	p := resp.Msg.Providers[0]
	assert.Equal(t, "binance", p.Slug)
	assert.True(t, p.NeedsApiKey)
	assert.True(t, p.NeedsApiSecret)
	assert.Equal(t, []apiv1.ProviderKind{
		apiv1.ProviderKind_PROVIDER_KIND_EXCHANGE,
		apiv1.ProviderKind_PROVIDER_KIND_PRICE,
	}, p.Kinds)
	assert.Equal(t, []string{"portfolio_sync", "market_data"}, p.Capabilities)
}

// TestListProvidersCarriesTheNumbersBehindAPlan: a tier is picked in a form and
// spent by the limiter, so the form has to show what picking it costs. A name
// with no numbers beside it is a choice made blind.
func TestListProvidersCarriesTheNumbersBehindAPlan(t *testing.T) {
	h := providerHandler(stubCatalog{{
		Slug:  "coingecko",
		Kinds: []catalog.Kind{catalog.KindPrice},
		Tiers: []catalog.Tier{
			{Name: "", Limit: ratelimit.Limit{RPS: 1.6, Burst: 1, Quota: 10000, Period: ratelimit.QuotaMonth}},
			{Name: "pro", Limit: ratelimit.Limit{RPS: 8, Burst: 2, Quota: 500000, Period: ratelimit.QuotaMonth}},
		},
	}})

	resp, err := h.ListProviders(context.Background(), connect.NewRequest(&apiv1.ListProvidersRequest{}))
	require.NoError(t, err)
	require.Len(t, resp.Msg.Providers[0].Tiers, 2)

	free := resp.Msg.Providers[0].Tiers[0]
	assert.Empty(t, free.Name, "the free keyed plan is the default and comes first")
	assert.InDelta(t, 1.6, free.Rps, 1e-9, "a fractional rate survives the wire")
	assert.Equal(t, int32(10000), free.Quota)
	assert.Equal(t, "month", free.QuotaPeriod)

	pro := resp.Msg.Providers[0].Tiers[1]
	assert.Equal(t, "pro", pro.Name)
	assert.Equal(t, int32(500000), pro.Quota)
}

// TestListProvidersCarriesExtraFields: "credential" is not always a key. A
// provider whose extra field goes unrendered leaves an account that looks
// complete and fails hours later, inside a sweep.
func TestListProvidersCarriesExtraFields(t *testing.T) {
	h := providerHandler(stubCatalog{{
		Slug:  "tinvest",
		Kinds: []catalog.Kind{catalog.KindPrice},
		Fields: []catalog.Field{{
			Key: "root_ca", Title: "Root certificate (PEM)",
			Help: "Without it the account is skipped", Required: true, Multiline: true,
		}},
	}})

	resp, err := h.ListProviders(context.Background(), connect.NewRequest(&apiv1.ListProvidersRequest{}))
	require.NoError(t, err)
	require.Len(t, resp.Msg.Providers[0].Fields, 1)

	f := resp.Msg.Providers[0].Fields[0]
	assert.Equal(t, "root_ca", f.Key)
	assert.True(t, f.Required)
	assert.True(t, f.Multiline)
	assert.NotEmpty(t, f.Help, "a required field has to say what happens without it")
}

// TestListProvidersWithoutACatalogueSaysSo: a deployment that composes no
// adapters must not answer with an empty list. "There are no providers" and
// "this deployment cannot tell you" lead a person to opposite conclusions, and
// only one of them is true.
func TestListProvidersWithoutACatalogueSaysSo(t *testing.T) {
	_, err := providerHandler(nil).ListProviders(context.Background(),
		connect.NewRequest(&apiv1.ListProvidersRequest{}))

	require.Error(t, err)
	assert.Equal(t, connect.CodeUnimplemented, connect.CodeOf(err))
}

// TestListProvidersKeepsCatalogueOrder: the order is the catalogue's, not the
// map iteration of whatever built it.
func TestListProvidersKeepsCatalogueOrder(t *testing.T) {
	h := providerHandler(stubCatalog{
		{Slug: "aaa", Kinds: []catalog.Kind{catalog.KindPrice}},
		{Slug: "bbb", Kinds: []catalog.Kind{catalog.KindPrice}},
		{Slug: "ccc", Kinds: []catalog.Kind{catalog.KindWallet}},
	})

	for range 5 {
		resp, err := h.ListProviders(context.Background(), connect.NewRequest(&apiv1.ListProvidersRequest{}))
		require.NoError(t, err)
		assert.Equal(t, []string{"aaa", "bbb", "ccc"}, []string{
			resp.Msg.Providers[0].Slug, resp.Msg.Providers[1].Slug, resp.Msg.Providers[2].Slug,
		})
	}
}
