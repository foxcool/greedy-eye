package blockchair

import "slices"

// network describes one chain served by Blockchair's dashboards API. Every
// UTXO chain here answers the same shape; only the smallest unit differs in
// name, never in scale.
type network struct {
	// path is the chain segment in the API URL.
	path string

	// version is the base58 version byte of the chain's addresses, which is
	// the only thing distinguishing them from Bitcoin's.
	version byte

	symbol string
	name   string

	decimals int
}

// networks maps the chain identifier stored on an account to its parameters.
// Keys are the chain names used in accounts.data.chain and in the syncer
// registry (cmd/eye/main.go).
//
// Filecoin is deliberately absent. It is not a UTXO chain, its balances are
// attoFIL at 18 decimals rather than the 8 every chain here shares, and the
// response could not be verified against the live API — Blockchair answers
// this network's requests with 430 until a key is presented. Shipping an
// unverified scale is how a position silently ends up off by 10^n.
var networks = map[string]network{
	"dash":     {path: "dash", version: 0x4c, symbol: "DASH", name: "Dash", decimals: 8},
	"dogecoin": {path: "dogecoin", version: 0x1e, symbol: "DOGE", name: "Dogecoin", decimals: 8},
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
