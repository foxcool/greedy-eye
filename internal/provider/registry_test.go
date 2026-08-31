package provider

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/foxcool/greedy-eye/internal/adapter/coingecko"
	"github.com/foxcool/greedy-eye/internal/adapter/ratelimit"
	tinvestadapter "github.com/foxcool/greedy-eye/internal/adapter/tinvest"
	"github.com/foxcool/greedy-eye/internal/entity"
	"github.com/foxcool/greedy-eye/internal/provider/catalog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func testRegistry() *Registry {
	return New(ratelimit.NewRegistry(nil))
}

// registeredSlugs is every slug this build can construct a client for.
func registeredSlugs(r *Registry) map[string]bool {
	slugs := map[string]bool{}
	for slug := range r.WalletSyncers() {
		slugs[slug] = true
	}
	for slug := range r.KeylessWalletSyncers() {
		slugs[slug] = true
	}
	for slug := range r.ExchangeSyncers() {
		slugs[slug] = true
	}
	for slug := range r.PriceProviders() {
		slugs[slug] = true
	}
	for slug := range r.KeylessPriceProviders() {
		slugs[slug] = true
	}
	return slugs
}

// TestCatalogueCoversTheRegistry is the guard that makes a served catalogue
// safe to trust.
//
// The catalogue exists so a form stops carrying its own copy of the provider
// list. That only helps while the copy that remains is complete: a registered
// adapter missing from the catalogue is one nobody can configure, and a
// described provider with no factory is a choice that silently does nothing
// once saved.
func TestCatalogueCoversTheRegistry(t *testing.T) {
	r := testRegistry()
	registered := registeredSlugs(r)

	described := map[string]bool{}
	for _, d := range r.Descriptors() {
		assert.False(t, described[d.Slug], "provider %q described twice", d.Slug)
		described[d.Slug] = true
		assert.True(t, registered[d.Slug], "provider %q is described but nothing can build it", d.Slug)
	}

	for slug := range registered {
		assert.True(t, described[slug], "provider %q is registered but not described", slug)
	}
}

// TestDescriptorsAreOrdered: a form that renders a different order on every
// request is a form nobody can point at over someone's shoulder.
func TestDescriptorsAreOrdered(t *testing.T) {
	first := testRegistry().Descriptors()
	second := testRegistry().Descriptors()
	require.Equal(t, first, second)

	for i := 1; i < len(first); i++ {
		assert.Less(t, first[i-1].Slug, first[i].Slug, "descriptors are not sorted by slug")
	}
}

// TestTiersComeFromTheLimiter: a tier offered in a form has to be one the rate
// limiter can look up. A tier only the form knows about would quietly fall back
// to the free plan's numbers, so the person picking it would be told they are
// on a paid plan while being metered as if they were not.
func TestTiersComeFromTheLimiter(t *testing.T) {
	var cg catalog.Descriptor
	for _, d := range testRegistry().Descriptors() {
		if d.Slug == coingecko.ProviderName {
			cg = d
		}
	}
	require.NotEmpty(t, cg.Tiers, "CoinGecko is metered, so it has plans to offer")

	names := make([]string, 0, len(cg.Tiers))
	byName := map[string]catalog.Tier{}
	for _, tier := range cg.Tiers {
		names = append(names, tier.Name)
		byName[tier.Name] = tier
	}
	assert.Equal(t, "", names[0], "the free keyed plan reads as the default, so it comes first")
	assert.Contains(t, byName, "pro")

	// The numbers are the limiter's own, not a second copy.
	plans := ratelimit.Plans(coingecko.ProviderName)
	assert.Equal(t, plans["pro"], byName["pro"].Limit)
	assert.Positive(t, byName["pro"].Limit.Quota, "a volume-metered plan carries its allowance")
}

// TestUnmeteredProviderOffersNoTier: a provider absent from the limits table is
// metered by the fallback. Offering "" as a plan would suggest a published free
// tier that nobody wrote down.
func TestUnmeteredProviderOffersNoTier(t *testing.T) {
	assert.Empty(t, ratelimit.Plans("no-such-provider"))
}

// TestTInvestDeclaresItsTrustAnchor: "credential" is not always an API key.
// Reaching this API means an operator deciding to trust an authority no
// standard store carries, and a form that only offers a key field leaves the
// account unusable in a way that surfaces hours later, inside a sweep.
func TestTInvestDeclaresItsTrustAnchor(t *testing.T) {
	var ti catalog.Descriptor
	for _, d := range testRegistry().Descriptors() {
		if d.Slug == tinvestadapter.ProviderName {
			ti = d
		}
	}
	fields := map[string]catalog.Field{}
	for _, f := range ti.Fields {
		fields[f.Key] = f
	}

	anchor, ok := fields["root_ca"]
	require.True(t, ok, "the trust anchor must be offered by the form")
	assert.True(t, anchor.Required)
	assert.True(t, anchor.Multiline, "a PEM block is not a line")
	assert.True(t, ti.NeedsAPIKey)

	// The base URL is optional and stays optional: empty means the live
	// gateway, so a form that demanded it would make every existing account
	// invalid to save.
	base, ok := fields["base_url"]
	require.True(t, ok, "an instance must be able to point an account elsewhere")
	assert.False(t, base.Required, "empty means the production gateway")
	assert.False(t, base.Multiline)
}

// TestCapabilitiesFollowTheKind: the resolver reaches a provider through the
// capability on its account, and which capability that is follows from what the
// provider does. A person filling in a form should not have to know that a
// price feed is reached through "market_data".
func TestCapabilitiesFollowTheKind(t *testing.T) {
	for _, d := range testRegistry().Descriptors() {
		require.NotEmpty(t, d.Capabilities, "provider %q reaches nothing", d.Slug)
		for _, kind := range d.Kinds {
			switch kind {
			case catalog.KindPrice:
				assert.Contains(t, d.Capabilities, string(entity.CapabilityMarketData), d.Slug)
			case catalog.KindWallet:
				assert.Contains(t, d.Capabilities, string(entity.CapabilityOnchainLookup), d.Slug)
			case catalog.KindExchange:
				assert.Contains(t, d.Capabilities, string(entity.CapabilityPortfolioSync), d.Slug)
			}
		}
	}
}

// TestKeylessProvidersBuildWithoutAnAccount: these are what a fresh instance
// reads chains with before anybody has entered a key. Handing them a nil
// account is exactly what the resolver does.
func TestKeylessProvidersBuildWithoutAnAccount(t *testing.T) {
	r := testRegistry()
	for slug, wallet := range r.KeylessWalletSyncers() {
		require.NotNil(t, wallet.Factory, "keyless wallet %q has no factory", slug)
		syncer, err := wallet.Factory(nil)
		require.NoError(t, err, "keyless wallet %q needs an account after all", slug)
		assert.NotNil(t, syncer)
		assert.NotEmpty(t, wallet.Chains, "a keyless reader still has to say what it reads")
	}
	assert.NotEmpty(t, r.KeylessPriceProviders())
}

// TestAccountBackedFactoriesReadTheirCredentials guards the wiring the move
// could have broken silently: a factory that ignores the account it is handed
// builds a client with no key, and the provider answers as if the account had
// never been created.
func TestAccountBackedFactoriesReadTheirCredentials(t *testing.T) {
	r := testRegistry()
	account := &entity.Account{ID: "acc-1", Data: map[string]string{
		"provider": coingecko.ProviderName, "api_key": "CG-x", "tier": "pro",
	}}

	factory, ok := r.PriceProviders()[coingecko.ProviderName]
	require.True(t, ok)
	client, err := factory(account)
	require.NoError(t, err)
	require.NotNil(t, client)

	// T-Invest refuses rather than falling back to the system trust store: a
	// connection that silently trusts the wrong roots is worse than no price.
	tinvest := r.PriceProviders()[tinvestadapter.ProviderName]
	require.NotNil(t, tinvest)
	_, err = tinvest(&entity.Account{ID: "acc-2", Data: map[string]string{"api_key": "t"}})
	assert.Error(t, err, "no root CA means no client")
}

// TestTInvestReachesTheAccountsHost is the one that pins the feature rather
// than its guards: the account's base_url must be where the requests actually
// go. Without it, dropping `BaseURL: baseURL` from the Config passes every
// other test in this file — the errors still behave, and the client quietly
// talks to the live broker instead of the replay server.
func TestTInvestReachesTheAccountsHost(t *testing.T) {
	var got []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = append(got, r.URL.Path)
		_, _ = w.Write([]byte(`{}`))
	}))
	t.Cleanup(srv.Close)

	tinvest := testRegistry().PriceProviders()[tinvestadapter.ProviderName]
	require.NotNil(t, tinvest)

	p, err := tinvest(&entity.Account{ID: "acc-replay", Data: map[string]string{
		"api_key": "t", "base_url": srv.URL,
	}})
	require.NoError(t, err, "an http replay host needs no trust anchor")

	// Any call that leaves the process will do: the claim under test is where
	// it lands, not what comes back.
	_, _ = p.FetchPrices(context.Background(), []*entity.Asset{{
		ID: "a1", Symbol: "SBER", Market: "moex", Type: entity.AssetTypeStock,
		ExternalRefs: []entity.AssetExternalRef{{
			Source: tinvestadapter.RefSource, Ref: "BBG004730N88", Origin: entity.RefOriginAuto,
		}},
	}})

	assert.NotEmpty(t, got, "the account's base_url never received a request")
}

// TestTInvestBaseURLComesFromTheAccount pins the whole point of data[base_url]:
// an instance can be sent to a local replay of captured responses instead of
// the live broker. The vendor's sandbox is not that — it answers different
// methods — so this field is the only way to exercise the path offline.
func TestTInvestBaseURLComesFromTheAccount(t *testing.T) {
	tinvest := testRegistry().PriceProviders()[tinvestadapter.ProviderName]
	require.NotNil(t, tinvest)

	build := func(data map[string]string) error {
		data["api_key"] = "t"
		_, err := tinvest(&entity.Account{ID: "acc-base", Data: data})
		return err
	}

	// A plaintext replay server presents no certificate, so demanding a trust
	// anchor for it would refuse the one configuration this field exists for.
	assert.NoError(t, build(map[string]string{"base_url": "http://127.0.0.1:8081/rest"}),
		"an http base URL has no certificate to anchor")

	// https keeps the existing rule exactly: the anchor is about TLS, and
	// pointing elsewhere over TLS still needs one.
	assert.ErrorIs(t, build(map[string]string{"base_url": "https://example.invalid/rest"}),
		tinvestadapter.ErrNoRootCA, "https elsewhere still needs an anchor")

	// A malformed value must read as configuration. Without this it is
	// concatenated onto a service path and fails later as something that looks
	// like the broker being down.
	for _, bad := range []string{
		"invest-public-api.tbank.ru/rest", // no scheme
		"ftp://example.invalid",           // wrong scheme
		"https://",                        // no host
		"http://127.0.0.1:8081?x=1",       // a service path cannot follow a query
	} {
		assert.ErrorIs(t, build(map[string]string{"base_url": bad}),
			tinvestadapter.ErrBadBaseURL, "base_url %q must fail as configuration", bad)
	}
}
