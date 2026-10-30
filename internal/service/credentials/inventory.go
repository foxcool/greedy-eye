package credentials

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/foxcool/greedy-eye/internal/entity"
)

// Origin says where a provider's client comes from.
type Origin string

const (
	// OriginUnresolved: the build can talk to the provider, nothing gives it a client.
	OriginUnresolved Origin = "unresolved"
	// OriginKeyless: registered by default, no account involved.
	OriginKeyless Origin = "keyless"
	// OriginSystem: an account shared through system_scopes.
	OriginSystem Origin = "system"
	// OriginOperator: the sole operator's own account, the unattended fallback.
	OriginOperator Origin = "operator"
	// OriginPerAccount: the synced account carries its own credential, so
	// nothing resolves until such an account is synced.
	OriginPerAccount Origin = "per_account"
)

// Role is a service a provider performs, as the inventory names it.
type Role string

const (
	RolePrice    Role = "price"
	RoleWallet   Role = "wallet"
	RoleExchange Role = "exchange"
	RoleBroker   Role = "broker"
)

// RoleOrigin is one role of a provider and where it is served from.
type RoleOrigin struct {
	Role   Role
	Origin Origin
}

// ProviderStatus is how one provider resolves.
type ProviderStatus struct {
	Provider string
	Roles    []RoleOrigin // ordered by role
}

// String renders the roles as "price=system wallet=keyless".
func (p ProviderStatus) String() string {
	parts := make([]string, len(p.Roles))
	for i, ro := range p.Roles {
		parts[i] = fmt.Sprintf("%s=%s", ro.Role, ro.Origin)
	}
	return strings.Join(parts, " ")
}

// Inventory is every provider the build registers, seen by work with no user
// in context, plus the price accounts that view passed over.
type Inventory struct {
	Providers []ProviderStatus // ordered by slug
	Skipped   []Skipped
}

// Inventory resolves every registered provider the way the scheduler would.
// A list of what resolved cannot show what did not, so every slug is named,
// unresolved ones included.
func (r *Resolver) Inventory(ctx context.Context) (Inventory, error) {
	prices, err := r.PriceInventoryFor(ctx, "")
	if err != nil {
		return Inventory{}, err
	}
	walletAccounts, _, err := r.accountsFor(ctx, "", entity.CapabilityOnchainLookup)
	if err != nil {
		return Inventory{}, err
	}

	roles := map[string]map[Role]Origin{}
	set := func(slug string, role Role, o Origin) {
		if roles[slug] == nil {
			roles[slug] = map[Role]Origin{}
		}
		roles[slug][role] = o
	}

	for _, slug := range unionKeys(r.cfg.PriceProviders, r.cfg.KeylessPriceProviders) {
		switch a, ok := prices.servingAccounts[slug]; {
		case ok:
			set(slug, RolePrice, originOf(a, entity.CapabilityMarketData))
		case len(prices.Keys[slug]) > 0:
			set(slug, RolePrice, OriginKeyless)
		default:
			set(slug, RolePrice, OriginUnresolved)
		}
	}

	// WalletSyncerFor takes the first candidate naming the slug, then keyless.
	walletAccounts = firstOccurrences(walletAccounts)
	for _, slug := range unionKeys(r.cfg.WalletSyncers, r.cfg.KeylessWalletSyncers) {
		origin := OriginUnresolved
		if _, ok := r.cfg.KeylessWalletSyncers[slug]; ok {
			origin = OriginKeyless
		}
		if _, ok := r.cfg.WalletSyncers[slug]; ok {
			if i := slices.IndexFunc(walletAccounts, func(a *entity.Account) bool {
				return a.Data[DataProviderKey] == slug
			}); i >= 0 {
				origin = originOf(walletAccounts[i], entity.CapabilityOnchainLookup)
			}
		}
		set(slug, RoleWallet, origin)
	}

	for slug := range r.cfg.ExchangeSyncers {
		set(slug, RoleExchange, OriginPerAccount)
	}
	for slug := range r.cfg.BrokerSyncers {
		set(slug, RoleBroker, OriginPerAccount)
	}

	inv := Inventory{Skipped: prices.Skipped}
	for _, slug := range slices.Sorted(maps.Keys(roles)) {
		status := ProviderStatus{Provider: slug}
		for _, role := range slices.Sorted(maps.Keys(roles[slug])) {
			status.Roles = append(status.Roles, RoleOrigin{Role: role, Origin: roles[slug][role]})
		}
		inv.Providers = append(inv.Providers, status)
	}
	return inv, nil
}

// originOf names the kind of account an unattended candidate came from: one
// not shared through system_scopes can only be the sole operator's.
func originOf(a *entity.Account, capability entity.AccountCapability) Origin {
	if slices.Contains(a.SystemScopes, capability) {
		return OriginSystem
	}
	return OriginOperator
}

func unionKeys[A, B any](a map[string]A, b map[string]B) []string {
	keys := slices.Collect(maps.Keys(a))
	for k := range b {
		if _, ok := a[k]; !ok {
			keys = append(keys, k)
		}
	}
	return keys
}
