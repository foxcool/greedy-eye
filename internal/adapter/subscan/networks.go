package subscan

import (
	"bytes"
	"errors"
	"fmt"
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

	// rpc, when set, is the chain's own JSON-RPC node, and the native balance
	// is read from chain state there instead of from Subscan. It is fixed here,
	// never taken from account data: a URL a user can name is a URL the server
	// can be made to call.
	rpc string

	// tokens marks a chain whose non-native balances sit in an orml Tokens
	// pallet with an asset registry beside it, read over rpc.
	tokens bool

	// evmAddresses marks a chain that identifies accounts by EVM H160 rather
	// than SS58. Such a chain cannot be reached by sweeping an SS58 address and
	// has to be named explicitly.
	evmAddresses bool
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
// The assets themselves ride in the same /api/scan/account/tokens response
// this adapter already reads, under builtin and assets, and are read as
// positions of their own (personal-feb.10). The erc20 group is not: see
// tokensResponse for why an unmeasured field shape is not worth a parser.
//
// symbol and decimals are fallbacks only. Live balances carry their own symbol
// and precision, which always win: a table that disagrees with the chain is
// how a position gets silently rescaled (personal-feb.12).
var networks = map[string]network{
	"polkadot":          {host: "polkadot", symbol: "DOT", name: "Polkadot", decimals: 10},
	"kusama":            {host: "kusama", symbol: "KSM", name: "Kusama", decimals: 12},
	"assethub-polkadot": {host: "assethub-polkadot", symbol: "DOT", name: "Polkadot Asset Hub", decimals: 10},
	"assethub-kusama":   {host: "assethub-kusama", symbol: "KSM", name: "Kusama Asset Hub", decimals: 12},
	// Hydration has no Subscan API host since 2026-08-27 (measured again
	// 2026-10-17 under both names, hydration and hydradx: 404, while polkadot
	// answers), so it is read from the chain itself, native HDX and the Tokens
	// pallet both.
	//
	// shortcut: one node is a single point of failure; add a fallback node
	// when it is seen failing (personal-ai9x).
	"hydration": {rpc: "https://rpc.hydradx.cloud", tokens: true, symbol: "HDX", name: "Hydration", decimals: 12},
	"astar":     {host: "astar", symbol: "ASTR", name: "Astar", decimals: 18},
	"moonbeam":  {host: "moonbeam", symbol: "GLMR", name: "Moonbeam", decimals: 18, evmAddresses: true},
}

// sweepChains returns the chains worth probing for an address whose account
// names none. Chains addressed by EVM H160 are left out: an SS58 address can
// never resolve there, so probing them spends a request to collect a
// "Record Not Found" that surfaces to the user as a sync error on every run.
func sweepChains() []string {
	chains := make([]string, 0, len(networks))
	for chain, net := range networks {
		if net.evmAddresses {
			continue
		}
		chains = append(chains, chain)
	}
	slices.Sort(chains)
	return chains
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
	_, err := ss58PublicKey(address)
	return err == nil
}

// ss58PublicKey returns the 32-byte public key an SS58 address encodes, after
// verifying its checksum. The key is what chain state is keyed by; the network
// prefix around it only says which chain the string was rendered for.
func ss58PublicKey(address string) ([]byte, error) {
	if !ss58Shape.MatchString(address) {
		return nil, errors.New("not an SS58 address")
	}

	decoded, err := base58.Decode(address)
	if err != nil {
		return nil, fmt.Errorf("not an SS58 address: %w", err)
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
		return nil, errors.New("not an SS58 address: wrong length")
	}
	if prefixLen == 1 && decoded[0] >= 64 {
		return nil, errors.New("not an SS58 address: one-byte prefix out of range") // ids from 64 up must use the two-byte form
	}

	body := decoded[:prefixLen+32]
	want := decoded[prefixLen+32:]

	hash := blake2b.Sum512(append(append([]byte{}, ss58Prefix...), body...))
	if !bytes.Equal(hash[:2], want) {
		return nil, errors.New("SS58 checksum mismatch")
	}
	return decoded[prefixLen : prefixLen+32], nil
}
