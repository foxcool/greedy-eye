package subscan

import (
	"bytes"
	"regexp"
	"slices"

	"golang.org/x/crypto/blake2b"

	"github.com/foxcool/greedy-eye/internal/adapter/internal/base58"
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
// The Asset Hubs are where the ecosystem's non-native assets live (USDT is
// asset id 1984, USDC 1337): a relay chain carries only its own token and has
// no pallet-assets, so nothing else can sit there. Their native balance is the
// same DOT or KSM as the relay's and merges into one position by symbol, which
// is correct — it is the same asset, held on another chain.
//
// Reading the assets themselves needs a different endpoint and is not done yet.
var networks = map[string]network{
	"polkadot":          {host: "polkadot", symbol: "DOT", name: "Polkadot", decimals: 10},
	"kusama":            {host: "kusama", symbol: "KSM", name: "Kusama", decimals: 12},
	"assethub-polkadot": {host: "assethub-polkadot", symbol: "DOT", name: "Polkadot Asset Hub", decimals: 10},
	"assethub-kusama":   {host: "assethub-kusama", symbol: "KSM", name: "Kusama Asset Hub", decimals: 12},
	"hydration":         {host: "hydration", symbol: "HDX", name: "Hydration", decimals: 12},
	"astar":             {host: "astar", symbol: "ASTR", name: "Astar", decimals: 18},
	"moonbeam":          {host: "moonbeam", symbol: "GLMR", name: "Moonbeam", decimals: 18},
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

// ss58Shape is a cheap pre-filter for the SS58 form every Substrate chain
// shares: base58 over a network prefix, a 32-byte public key and a checksum,
// which lands at 46-48 characters. The same key yields a different string per
// network (generic "5…", Polkadot "1…", Kusama "C…"), so the prefix is not
// pinned. The checksum below is the actual authority.
//
// Moonbeam is the exception: it is in this adapter's chain list but addresses
// there are EVM H160, so it cannot be reached by auto-discovery and needs its
// chain named explicitly.
var ss58Shape = regexp.MustCompile(`^[1-9A-HJ-NP-Za-km-z]{46,48}$`)

// ss58Prefix is the domain separator Substrate hashes before the payload.
var ss58Prefix = []byte("SS58PRE")

// HandlesAddress reports whether an address is SS58, routing accounts that
// name no chain to this adapter.
//
// The shape alone is not enough to decide. A Solana pubkey is base58 over the
// same alphabet and lands at 43-44 characters — only two short of the SS58
// range — and both formats are otherwise indistinguishable by length. Routing
// a Solana address here would not fail loudly: Subscan would answer "no such
// account" and the position would silently drop to zero. So the address is
// decoded and its checksum verified, which no foreign format can satisfy by
// accident.
func HandlesAddress(address string) bool {
	if !ss58Shape.MatchString(address) {
		return false
	}

	decoded, err := base58.Decode(address)
	if err != nil {
		return false
	}

	// 1-byte prefix (network ids below 64) or 2-byte prefix, plus a 32-byte
	// public key and a 2-byte checksum.
	var prefixLen int
	switch len(decoded) {
	case 35:
		prefixLen = 1
	case 36:
		prefixLen = 2
	default:
		return false
	}
	if prefixLen == 1 && decoded[0] >= 64 {
		return false // ids from 64 up must use the two-byte form
	}

	body := decoded[:prefixLen+32]
	want := decoded[prefixLen+32:]

	hash := blake2b.Sum512(append(append([]byte{}, ss58Prefix...), body...))
	return bytes.Equal(hash[:2], want)
}
