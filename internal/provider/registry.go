// Package provider is the one place that knows which external adapters exist.
//
// Before it, that knowledge lived in a table inside main.go, which made it
// unreachable to everything else: a form asking a person to pick a provider had
// to carry a second copy of the list, and a second copy of a fact is how the
// two drift apart. Here the factory and the description of what it needs are
// written side by side, and a test asserts that neither exists without the
// other.
//
// Nothing above this package imports an adapter. Services take the interfaces
// they need (credentials.WalletProvider, marketdata.PriceProvider); the account
// form takes catalog.Descriptor, which carries no adapter code at all.
package provider

import (
	"net/http"

	binanceadapter "github.com/foxcool/greedy-eye/internal/adapter/binance"
	blockchairadapter "github.com/foxcool/greedy-eye/internal/adapter/blockchair"
	cbradapter "github.com/foxcool/greedy-eye/internal/adapter/cbr"
	"github.com/foxcool/greedy-eye/internal/adapter/coingecko"
	cosmosadapter "github.com/foxcool/greedy-eye/internal/adapter/cosmos"
	esploraadapter "github.com/foxcool/greedy-eye/internal/adapter/esplora"
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
	"github.com/foxcool/greedy-eye/internal/service/credentials"
	"github.com/foxcool/greedy-eye/internal/service/marketdata"
)

// Registry holds every adapter this build can construct, keyed by the slug an
// account names in data["provider"].
type Registry struct {
	limits *ratelimit.Registry
}

// New builds the registry over a rate-limit registry. Every client it makes
// draws on the budget of the credential it was built from — clients are
// short-lived, budgets are not.
func New(limits *ratelimit.Registry) *Registry {
	return &Registry{limits: limits}
}

// WalletSyncers are chain readers built from an account's credentials, routed
// by chain: an account naming "polkadot" must never reach an EVM-only syncer.
func (r *Registry) WalletSyncers() map[string]credentials.WalletProvider {
	return map[string]credentials.WalletProvider{
		moralisadapter.ProviderName: {
			Factory: func(a *entity.Account) (entity.WalletSyncer, error) {
				return moralisadapter.NewWalletSyncer(
					moralisadapter.NewClient(moralisadapter.Config{
						APIKey:    a.Data["api_key"],
						Transport: r.transport(moralisadapter.ProviderName, a),
					}),
				), nil
			},
			Chains:         moralisadapter.SupportedChains(),
			HandlesAddress: moralisadapter.HandlesAddress,
		},
		subscanadapter.ProviderName: {
			Factory: func(a *entity.Account) (entity.WalletSyncer, error) {
				return subscanadapter.NewWalletSyncer(
					subscanadapter.NewClient(subscanadapter.Config{
						APIKey:    a.Data["api_key"],
						Transport: r.transport(subscanadapter.ProviderName, a),
					}),
				), nil
			},
			Chains:         subscanadapter.SupportedChains(),
			HandlesAddress: subscanadapter.HandlesAddress,
		},
		tonapiadapter.ProviderName: {
			Factory: func(a *entity.Account) (entity.WalletSyncer, error) {
				return tonapiadapter.NewWalletSyncer(
					tonapiadapter.NewClient(tonapiadapter.Config{
						APIKey:    a.Data["api_key"],
						Transport: r.transport(tonapiadapter.ProviderName, a),
					}),
				), nil
			},
			Chains:         tonapiadapter.SupportedChains(),
			HandlesAddress: tonapiadapter.HandlesAddress,
		},
		solanaadapter.ProviderName: {
			Factory: func(a *entity.Account) (entity.WalletSyncer, error) {
				return solanaadapter.NewWalletSyncer(
					solanaadapter.NewClient(solanaadapter.Config{
						APIKey:    a.Data["api_key"],
						Transport: r.transport(solanaadapter.ProviderName, a),
					}),
				), nil
			},
			Chains:         solanaadapter.SupportedChains(),
			HandlesAddress: solanaadapter.HandlesAddress,
		},
		// Esplora needs no credentials; the account exists only so the registry
		// has something to route to.
		//
		// The endpoint is not configurable per account on purpose. accounts.data
		// is user-supplied, and a client whose base URL comes from it would make
		// the server issue requests to any address its caller names — cloud
		// metadata, loopback, anything inside the trust boundary — with the
		// response surfacing through sync errors. If a self-hosted instance is
		// ever needed it belongs in operator config, not in user data.
		esploraadapter.ProviderName: r.keylessWallet(esploraadapter.ProviderName),
		cosmosadapter.ProviderName:  r.keylessWallet(cosmosadapter.ProviderName),
		tzktadapter.ProviderName:    r.keylessWallet(tzktadapter.ProviderName),
		blockchairadapter.ProviderName: {
			Factory: func(a *entity.Account) (entity.WalletSyncer, error) {
				return blockchairadapter.NewWalletSyncer(
					blockchairadapter.NewClient(blockchairadapter.Config{
						APIKey:    a.Data["api_key"],
						Transport: r.transport(blockchairadapter.ProviderName, a),
					}),
				), nil
			},
			Chains:         blockchairadapter.SupportedChains(),
			HandlesAddress: blockchairadapter.HandlesAddress,
		},
	}
}

// KeylessWalletSyncers are chain readers registered without any account.
//
// They matter more than they look. A syncer is chosen from the accounts
// carrying onchain_lookup, and a wallet account holds only an address — it
// never names its reader. Without these a fresh instance reads no chain at all
// until somebody hand-creates a service row per ecosystem, silently. Seeding
// one instead is not available: accounts.user_id is NOT NULL, so before the
// first user there is nobody to own the row.
//
// TON answers without a key on a much tighter allowance ("tonapi:keyless" in
// the limits table). An account carrying a key still overrides this and gets
// the keyed rate.
func (r *Registry) KeylessWalletSyncers() map[string]credentials.WalletProvider {
	return map[string]credentials.WalletProvider{
		esploraadapter.ProviderName: r.keylessWallet(esploraadapter.ProviderName),
		cosmosadapter.ProviderName:  r.keylessWallet(cosmosadapter.ProviderName),
		tzktadapter.ProviderName:    r.keylessWallet(tzktadapter.ProviderName),
		tonapiadapter.ProviderName:  r.keylessWallet(tonapiadapter.ProviderName),
	}
}

// keylessWallet builds the reader for a slug that needs no credential. The
// account, if any, is ignored beyond its budget: these adapters have nothing to
// authenticate with.
func (r *Registry) keylessWallet(slug string) credentials.WalletProvider {
	cred := ratelimit.Credential{Provider: slug}
	switch slug {
	case esploraadapter.ProviderName:
		return credentials.WalletProvider{
			Factory: func(*entity.Account) (entity.WalletSyncer, error) {
				return esploraadapter.NewWalletSyncer(esploraadapter.NewClient(esploraadapter.Config{
					Transport: r.limits.Transport(cred, nil),
				})), nil
			},
			Chains:         esploraadapter.SupportedChains(),
			HandlesAddress: esploraadapter.HandlesAddress,
		}
	case cosmosadapter.ProviderName:
		return credentials.WalletProvider{
			Factory: func(*entity.Account) (entity.WalletSyncer, error) {
				return cosmosadapter.NewWalletSyncer(cosmosadapter.NewClient(cosmosadapter.Config{
					Transport: r.limits.Transport(cred, nil),
				})), nil
			},
			Chains:         cosmosadapter.SupportedChains(),
			HandlesAddress: cosmosadapter.HandlesAddress,
		}
	case tzktadapter.ProviderName:
		return credentials.WalletProvider{
			Factory: func(*entity.Account) (entity.WalletSyncer, error) {
				return tzktadapter.NewWalletSyncer(tzktadapter.NewClient(tzktadapter.Config{
					Transport: r.limits.Transport(cred, nil),
				})), nil
			},
			Chains:         tzktadapter.SupportedChains(),
			HandlesAddress: tzktadapter.HandlesAddress,
		}
	case tonapiadapter.ProviderName:
		return credentials.WalletProvider{
			Factory: func(*entity.Account) (entity.WalletSyncer, error) {
				return tonapiadapter.NewWalletSyncer(tonapiadapter.NewClient(tonapiadapter.Config{
					Transport: r.limits.Transport(cred, nil),
				})), nil
			},
			Chains:         tonapiadapter.SupportedChains(),
			HandlesAddress: tonapiadapter.HandlesAddress,
		}
	default:
		return credentials.WalletProvider{}
	}
}

// ExchangeSyncers read positions held at a venue. The account being synced
// holds the key itself, so there is no user→system fallback here.
func (r *Registry) ExchangeSyncers() map[string]credentials.ExchangeSyncerFactory {
	return map[string]credentials.ExchangeSyncerFactory{
		binanceadapter.ProviderName: func(a *entity.Account) (entity.ExchangeSyncer, error) {
			return binanceadapter.NewExchangeSyncer(binanceadapter.NewClient(binanceadapter.Config{
				APIKey:    a.Data["api_key"],
				APISecret: a.Data["api_secret"],
				Transport: r.transport(binanceadapter.ProviderName, a),
			})), nil
		},
	}
}

// PriceProviders quote assets, built from an account's credentials.
func (r *Registry) PriceProviders() map[string]credentials.PriceProviderFactory {
	return map[string]credentials.PriceProviderFactory{
		coingecko.ProviderName: func(a *entity.Account) (marketdata.PriceProvider, error) {
			cred := accountCred(coingecko.ProviderName, a)
			return coingecko.NewProvider(coingecko.NewClient(coingecko.Config{
				APIKey: a.Data["api_key"],
				// accountCred already folded the legacy "pro" flag into the
				// tier; reading data["pro"] separately here is what let the
				// host and the plan disagree.
				Tier:      cred.Tier,
				Transport: r.limits.Transport(cred, nil),
				Budget:    r.limits.Budget(cred),
			})), nil
		},
		binanceadapter.ProviderName: func(a *entity.Account) (marketdata.PriceProvider, error) {
			return binanceadapter.NewProvider(binanceadapter.NewClient(binanceadapter.Config{
				APIKey:    a.Data["api_key"],
				APISecret: a.Data["api_secret"],
				Transport: r.transport(binanceadapter.ProviderName, a),
			})), nil
		},
		// The feed needs no credential, but an account is still how a provider
		// gets throttled or given a share of a shared plan, so the slug is
		// registered on both paths.
		cbradapter.ProviderName: func(a *entity.Account) (marketdata.PriceProvider, error) {
			return cbradapter.NewProvider(cbradapter.NewClient(cbradapter.Config{
				Transport: r.transport(cbradapter.ProviderName, a),
			})), nil
		},
		moexadapter.ProviderName: func(a *entity.Account) (marketdata.PriceProvider, error) {
			return moexadapter.NewProvider(moexadapter.NewClient(moexadapter.Config{
				Transport: r.transport(moexadapter.ProviderName, a),
			})), nil
		},
		// Registered whether or not a root CA was supplied. Without one the
		// factory refuses and the resolver skips this account with a warning
		// naming what is missing, so the account exists, says what it needs, and
		// costs nothing else — every other provider in the sweep keeps working.
		// Leaving the slug unregistered instead would report the provider as
		// simply absent, which is the silence that let stale env credentials
		// serve the sweep for days (personal-cpw).
		//
		// The trust anchor travels with the account, in data["root_ca"]. The
		// host's chain terminates in a root no standard store carries, so
		// reaching this API means an operator deciding to trust that authority —
		// a decision that belongs beside the token it is used with, not in the
		// service's own configuration.
		tinvestadapter.ProviderName: func(a *entity.Account) (marketdata.PriceProvider, error) {
			// The verified transport is built first and the rate limiter wraps
			// it. The other order would hand NewClient a ready Transport, which
			// takes precedence over the anchor and would leave the connection
			// trusting only the system store.
			base, err := tinvestadapter.TLSTransport([]byte(a.Data["root_ca"]))
			if err != nil {
				return nil, err
			}
			client, err := tinvestadapter.NewClient(tinvestadapter.Config{
				Token:     a.Data["api_key"],
				Transport: r.limits.Transport(accountCred(tinvestadapter.ProviderName, a), base),
			})
			if err != nil {
				return nil, err
			}
			return tinvestadapter.NewProvider(client), nil
		},
	}
}

// KeylessPriceProviders are feeds anyone may read, registered with no account
// at all: without the CBR rate a rouble-quoted instrument has a price and still
// contributes nothing to a USD total, and a portfolio holding Russian shares
// reads zero without MOEX. An account naming the same slug still wins.
func (r *Registry) KeylessPriceProviders() map[string]marketdata.PriceProvider {
	return map[string]marketdata.PriceProvider{
		cbradapter.ProviderName: cbradapter.NewProvider(cbradapter.NewClient(cbradapter.Config{
			Transport: r.limits.Transport(ratelimit.Credential{Provider: cbradapter.ProviderName}, nil),
		})),
		moexadapter.ProviderName: moexadapter.NewProvider(moexadapter.NewClient(moexadapter.Config{
			Transport: r.limits.Transport(ratelimit.Credential{Provider: moexadapter.ProviderName}, nil),
		})),
	}
}

// transport is the rate-limited transport for an account-backed client.
func (r *Registry) transport(slug string, a *entity.Account) http.RoundTripper {
	return r.limits.Transport(accountCred(slug, a), nil)
}

// Descriptors returns the catalogue: what each provider is and what it needs,
// with no client built.
//
// Ordered by slug so a form renders the same list twice running.
func (r *Registry) Descriptors() []catalog.Descriptor {
	return describeAll()
}
