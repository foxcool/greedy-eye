package subscan

import (
	"regexp"
	"slices"
)

// network describes one Substrate chain served by Subscan. Each chain has its
// own API host, native token and decimal precision — none of it is derivable
// from the address, which is why the account config names the chain.
type network struct {
	// host is the Subscan subdomain (https://<host>.api.subscan.io).
	host string

	symbol string
	name   string

	// decimals is int32 to match decimal.Decimal.Shift, the only place it is
	// used for arithmetic; entity.WalletBalance widens it back to int.
	decimals int32
}

// networks maps the chain identifier stored on an account to its parameters.
// Keys are the chain names used in accounts.data.chain and in the syncer
// registry (cmd/eye/main.go).
//
// Moonbeam is here rather than with the EVM chains on purpose: it is
// EVM-compatible but Moralis's chains endpoint does not serve it, so Subscan
// is the only route to its balances.
var networks = map[string]network{
	"polkadot":  {host: "polkadot", symbol: "DOT", name: "Polkadot", decimals: 10},
	"kusama":    {host: "kusama", symbol: "KSM", name: "Kusama", decimals: 12},
	"hydration": {host: "hydration", symbol: "HDX", name: "Hydration", decimals: 12},
	"astar":     {host: "astar", symbol: "ASTR", name: "Astar", decimals: 18},
	"moonbeam":  {host: "moonbeam", symbol: "GLMR", name: "Moonbeam", decimals: 18},
}

// SupportedChains returns the chains this adapter can sync, for registration
// in the chain-keyed syncer registry.
func SupportedChains() []string {
	chains := make([]string, 0, len(networks))
	for chain := range networks {
		chains = append(chains, chain)
	}
	slices.Sort(chains) // map iteration order is random; keep the result stable
	return chains
}

// ss58Address matches the SS58 form every Substrate chain shares: base58 over
// a one-byte network prefix, a 32-byte public key and a checksum, which lands
// at 47-48 characters. The same key yields a different string per network
// (generic "5…", Polkadot "1…", Kusama "C…"), so the prefix is not pinned.
//
// Moonbeam is the exception: it is in this adapter's chain list but addresses
// there are EVM H160, so it cannot be reached by auto-discovery and needs its
// chain named explicitly.
var ss58Address = regexp.MustCompile(`^[1-9A-HJ-NP-Za-km-z]{46,48}$`)

// HandlesAddress reports whether an address is SS58, routing accounts that
// name no chain to this adapter.
func HandlesAddress(address string) bool {
	return ss58Address.MatchString(address)
}
