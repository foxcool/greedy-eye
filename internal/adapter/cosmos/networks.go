package cosmos

import "slices"

// network describes one Cosmos zone. The prefix is what makes an address
// belong to the chain; the denom is the on-chain name of its native token,
// which is the micro-unit (uatom = 10^-6 ATOM).
type network struct {
	// hrp is the bech32 human-readable part of the chain's addresses.
	hrp string
	// lcdChain is the path segment on the LCD proxy.
	lcdChain string

	denom  string
	symbol string
	name   string

	decimals int
}

// networks maps the chain identifier stored on an account to its parameters.
// Keys are the chain names used in accounts.data.chain and in the syncer
// registry (cmd/eye/main.go).
//
// The set is deliberately small: one entry per zone actually held. Adding a
// zone is one row, since every Cosmos chain answers the same LCD endpoints.
var networks = map[string]network{
	"cosmos":  {hrp: "cosmos", lcdChain: "cosmoshub", denom: "uatom", symbol: "ATOM", name: "Cosmos Hub", decimals: 6},
	"akash":   {hrp: "akash", lcdChain: "akash", denom: "uakt", symbol: "AKT", name: "Akash Network", decimals: 6},
	"osmosis": {hrp: "osmo", lcdChain: "osmosis", denom: "uosmo", symbol: "OSMO", name: "Osmosis", decimals: 6},
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

// networkByHRP finds the chain whose addresses carry this prefix.
func networkByHRP(hrp string) (network, bool) {
	for _, net := range networks {
		if net.hrp == hrp {
			return net, true
		}
	}
	return network{}, false
}
