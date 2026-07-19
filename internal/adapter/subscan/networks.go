package subscan

import "slices"

// network describes one Substrate chain served by Subscan. Each chain has its
// own API host, native token and decimal precision — none of it is derivable
// from the address, which is why the account config names the chain.
type network struct {
	// host is the Subscan subdomain (https://<host>.api.subscan.io).
	host string

	symbol   string
	name     string
	decimals int
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
