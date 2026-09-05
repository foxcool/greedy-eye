package provider

import (
	"slices"

	alchemyadapter "github.com/foxcool/greedy-eye/internal/adapter/alchemy"
	binanceadapter "github.com/foxcool/greedy-eye/internal/adapter/binance"
	blockchairadapter "github.com/foxcool/greedy-eye/internal/adapter/blockchair"
	cbradapter "github.com/foxcool/greedy-eye/internal/adapter/cbr"
	"github.com/foxcool/greedy-eye/internal/adapter/coingecko"
	cosmosadapter "github.com/foxcool/greedy-eye/internal/adapter/cosmos"
	esploraadapter "github.com/foxcool/greedy-eye/internal/adapter/esplora"
	gateioadapter "github.com/foxcool/greedy-eye/internal/adapter/gateio"
	moexadapter "github.com/foxcool/greedy-eye/internal/adapter/moex"
	moralisadapter "github.com/foxcool/greedy-eye/internal/adapter/moralis"
	"github.com/foxcool/greedy-eye/internal/adapter/ratelimit"
	solanaadapter "github.com/foxcool/greedy-eye/internal/adapter/solana"
	subscanadapter "github.com/foxcool/greedy-eye/internal/adapter/subscan"
	tinvestadapter "github.com/foxcool/greedy-eye/internal/adapter/tinvest"
	tonapiadapter "github.com/foxcool/greedy-eye/internal/adapter/tonapi"
	tzktadapter "github.com/foxcool/greedy-eye/internal/adapter/tzkt"
	"github.com/foxcool/greedy-eye/internal/entity"
	"github.com/foxcool/greedy-eye/internal/provider/catalog"
)

// describeAll states what each registered provider is and what it needs before
// it can work. Written beside the factories it describes; a test asserts that
// every registered slug appears here and that nothing appears here twice.
//
// Chains come from the adapters themselves rather than being retyped: a chain
// added to an adapter's list must not require remembering to add it again here.
func describeAll() []catalog.Descriptor {
	all := []catalog.Descriptor{
		{
			Slug:        coingecko.ProviderName,
			Title:       "CoinGecko",
			Kinds:       []catalog.Kind{catalog.KindPrice},
			NeedsAPIKey: false,
			Keyless:     false,
		},
		{
			Slug:           binanceadapter.ProviderName,
			Title:          "Binance",
			Kinds:          []catalog.Kind{catalog.KindExchange, catalog.KindPrice},
			NeedsAPIKey:    true,
			NeedsAPISecret: true,
		},
		{
			// Exchange only; pricing the venue is personal-nzir.
			Slug:           gateioadapter.ProviderName,
			Title:          "Gate.io",
			Kinds:          []catalog.Kind{catalog.KindExchange},
			NeedsAPIKey:    true,
			NeedsAPISecret: true,
		},
		{
			Slug:    cbradapter.ProviderName,
			Title:   "Bank of Russia (FX rates)",
			Kinds:   []catalog.Kind{catalog.KindPrice},
			Keyless: true,
		},
		{
			Slug:    moexadapter.ProviderName,
			Title:   "MOEX ISS",
			Kinds:   []catalog.Kind{catalog.KindPrice},
			Keyless: true,
		},
		{
			Slug:        tinvestadapter.ProviderName,
			Title:       "T-Invest",
			Kinds:       []catalog.Kind{catalog.KindPrice},
			NeedsAPIKey: true,
			Fields: []catalog.Field{{
				Key:       "root_ca",
				Title:     "Root certificate (PEM)",
				Help:      "The host's chain ends in a root no standard store carries. Without this the account is skipped and nothing it would price gets a price. Not needed when the base URL is plain http, which has no certificate to verify.",
				Required:  true,
				Multiline: true,
			}, {
				Key:   "base_url",
				Title: "Base URL",
				// The help says what the field is FOR, because the obvious
				// guess is wrong: the vendor's sandbox lives on another host
				// but answers different methods, so pointing this at it does
				// not produce a working second environment.
				Help:      "Leave empty for the broker's live gateway. Set it to send this account elsewhere — a local server replaying captured responses, so a change can be exercised without the live broker. Not a sandbox switch: the vendor's sandbox serves different methods.",
				Required:  false,
				Multiline: false,
			}},
		},
		{
			Slug:        alchemyadapter.ProviderName,
			Title:       "Alchemy",
			Kinds:       []catalog.Kind{catalog.KindWallet},
			NeedsAPIKey: true,
			Chains:      alchemyadapter.SupportedChains(),
		},
		{
			// Kept in the catalogue although its free tier ended on
			// 2026-09-01: an instance whose operator pays for Moralis is a
			// real instance, and removing the choice would strand the accounts
			// already configured for it. What it must not be is the only EVM
			// option — see Alchemy above.
			Slug:        moralisadapter.ProviderName,
			Title:       "Moralis",
			Kinds:       []catalog.Kind{catalog.KindWallet},
			NeedsAPIKey: true,
			Chains:      moralisadapter.SupportedChains(),
		},
		{
			Slug:        subscanadapter.ProviderName,
			Title:       "Subscan",
			Kinds:       []catalog.Kind{catalog.KindWallet},
			NeedsAPIKey: true,
			Chains:      subscanadapter.SupportedChains(),
		},
		{
			Slug:        solanaadapter.ProviderName,
			Title:       "Helius",
			Kinds:       []catalog.Kind{catalog.KindWallet},
			NeedsAPIKey: true,
			Chains:      solanaadapter.SupportedChains(),
		},
		{
			// Answers without a key on a much tighter allowance; a key raises it.
			Slug:    tonapiadapter.ProviderName,
			Title:   "TON API",
			Kinds:   []catalog.Kind{catalog.KindWallet},
			Keyless: true,
			Chains:  tonapiadapter.SupportedChains(),
		},
		{
			Slug:    blockchairadapter.ProviderName,
			Title:   "Blockchair",
			Kinds:   []catalog.Kind{catalog.KindWallet},
			Keyless: true,
			Chains:  blockchairadapter.SupportedChains(),
		},
		{
			Slug:    esploraadapter.ProviderName,
			Title:   "Esplora (mempool.space)",
			Kinds:   []catalog.Kind{catalog.KindWallet},
			Keyless: true,
			Chains:  esploraadapter.SupportedChains(),
		},
		{
			Slug:    cosmosadapter.ProviderName,
			Title:   "Cosmos LCD",
			Kinds:   []catalog.Kind{catalog.KindWallet},
			Keyless: true,
			Chains:  cosmosadapter.SupportedChains(),
		},
		{
			Slug:    tzktadapter.ProviderName,
			Title:   "TzKT",
			Kinds:   []catalog.Kind{catalog.KindWallet},
			Keyless: true,
			Chains:  tzktadapter.SupportedChains(),
		},
	}

	for i := range all {
		all[i].Capabilities = capabilitiesFor(all[i].Kinds)
		all[i].Tiers = tiersFor(all[i].Slug)
	}
	slices.SortFunc(all, func(a, b catalog.Descriptor) int {
		switch {
		case a.Slug < b.Slug:
			return -1
		case a.Slug > b.Slug:
			return 1
		default:
			return 0
		}
	})
	return all
}

// capabilitiesFor maps what a provider does onto the capabilities an account
// must carry for the resolver to reach it.
//
// Derived rather than written down per provider: the mapping is a property of
// the kind, and a person filling in a form should not have to know that a price
// feed is reached through "market_data".
func capabilitiesFor(kinds []catalog.Kind) []string {
	var caps []string
	for _, k := range kinds {
		switch k {
		case catalog.KindPrice:
			caps = appendUnique(caps, string(entity.CapabilityMarketData))
		case catalog.KindWallet:
			caps = appendUnique(caps, string(entity.CapabilityOnchainLookup))
		case catalog.KindExchange:
			caps = appendUnique(caps, string(entity.CapabilityPortfolioSync))
		}
	}
	return caps
}

func appendUnique(list []string, v string) []string {
	if slices.Contains(list, v) {
		return list
	}
	return append(list, v)
}

// tiersFor reads the plans out of the limits table, so a tier offered in a form
// is one the limiter can actually look up. A tier that only the form knows
// about would silently fall back to the free plan's numbers.
func tiersFor(slug string) []catalog.Tier {
	plans := ratelimit.Plans(slug)
	tiers := make([]catalog.Tier, 0, len(plans))
	for name, limit := range plans {
		tiers = append(tiers, catalog.Tier{Name: name, Limit: limit})
	}
	// Free keyed plan first, then named ones alphabetically: "" reads as the
	// default, which it is.
	slices.SortFunc(tiers, func(a, b catalog.Tier) int {
		switch {
		case a.Name == b.Name:
			return 0
		case a.Name == "":
			return -1
		case b.Name == "":
			return 1
		case a.Name < b.Name:
			return -1
		default:
			return 1
		}
	})
	return tiers
}
