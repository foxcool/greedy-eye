package alchemy

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"regexp"
	"slices"
	"strings"

	"github.com/foxcool/greedy-eye/internal/entity"
)

// network maps this system's chain identifier to Alchemy's network slug and
// the metadata of the coin that pays for gas there.
//
// THE CHAIN ID IS PART OF AN ASSET'S IDENTITY: the same contract address on two
// chains is two assets, and a balance stamped with the wrong chain merges them.
// So the response is translated back through this table rather than by trimming
// "-mainnet" off whatever the API said — a slug this build does not know must
// fail loudly, not become a plausible-looking chain name.
var network = map[string]struct {
	slug          string
	symbol, title string
}{
	"eth":       {"eth-mainnet", "ETH", "Ethereum"},
	"base":      {"base-mainnet", "ETH", "Ethereum"},
	"arbitrum":  {"arb-mainnet", "ETH", "Ethereum"},
	"optimism":  {"opt-mainnet", "ETH", "Ethereum"},
	"linea":     {"linea-mainnet", "ETH", "Ethereum"},
	"zksync":    {"zksync-mainnet", "ETH", "Ethereum"},
	"scroll":    {"scroll-mainnet", "ETH", "Ethereum"},
	"polygon":   {"polygon-mainnet", "POL", "Polygon"},
	"bsc":       {"bnb-mainnet", "BNB", "BNB Chain"},
	"avalanche": {"avax-mainnet", "AVAX", "Avalanche"},

	// Fantom is deliberately absent. Moralis read it; Alchemy has deprecated
	// it, and claiming a chain this adapter cannot read would put a wallet's
	// fantom holdings out of the sum with nothing saying why. An account whose
	// chain list names it is routed elsewhere by the registry, which is the
	// honest outcome: no source, rather than a silent zero.
}

const nativeDecimals = 18

// evmAddress matches a 20-byte hex address.
var evmAddress = regexp.MustCompile(`^0x[0-9a-fA-F]{40}$`)

// HandlesAddress reports whether an address belongs to an EVM chain, routing
// accounts that name no chain (auto-discovery) to this adapter.
func HandlesAddress(address string) bool {
	return evmAddress.MatchString(address)
}

// SupportedChains returns the chain identifiers this adapter can sync.
func SupportedChains() []string {
	chains := make([]string, 0, len(network))
	for chain := range network {
		chains = append(chains, chain)
	}
	slices.Sort(chains) // map iteration order is random; keep the result stable
	return chains
}

// chainOf reverses the slug table.
func chainOf(slug string) (string, bool) {
	for chain, n := range network {
		if n.slug == slug {
			return chain, true
		}
	}
	return "", false
}

// WalletSyncerAdapter adapts *Client to entity.WalletSyncer.
type WalletSyncerAdapter struct {
	client *Client
}

// NewWalletSyncer wraps a *Client as an entity.WalletSyncer.
func NewWalletSyncer(c *Client) *WalletSyncerAdapter {
	return &WalletSyncerAdapter{client: c}
}

// SyncWallet fetches native and token balances across the given chains.
//
// An empty chain set means every chain this adapter supports. That IS the
// discovery step here: the Portfolio API answers five networks per call, so
// asking all ten costs two requests, and asking is cheaper than a separate
// endpoint that guesses which chains a wallet has touched — the previous
// source had one, and it refused to probe three of the chains it could
// otherwise read, hiding those balances from any account that relied on it.
//
// Per-chain failures are returned alongside the balances that were read, never
// instead of them: a chain that could not be reached is a hole to disclose, not
// a reason to discard the nine that answered.
func (a *WalletSyncerAdapter) SyncWallet(ctx context.Context, address string, chains []string) ([]entity.WalletBalance, error) {
	if len(chains) == 0 {
		chains = SupportedChains()
	}

	slugs, errs := a.slugsFor(chains)
	if len(slugs) == 0 {
		return nil, errors.Join(errs...)
	}

	var result []entity.WalletBalance
	for _, batch := range slices.Collect(slices.Chunk(slugs, maxNetworksPerCall)) {
		tokens, netErrs, err := a.client.TokensByAddress(ctx, address, batch)
		if err != nil {
			errs = append(errs, fmt.Errorf("chains %s: %w", strings.Join(batch, ","), err))
			continue
		}
		for _, ne := range netErrs {
			errs = append(errs, fmt.Errorf("chain %s: %s", ne.Network, ne.Message))
		}

		balances, converted := a.convert(tokens)
		result = append(result, balances...)
		errs = append(errs, converted...)
	}

	return result, errors.Join(errs...)
}

// slugsFor translates chain identifiers, naming the ones this adapter cannot
// read rather than dropping them: an account configured for a chain with no
// source must produce an error somebody can see, not a shorter answer.
func (a *WalletSyncerAdapter) slugsFor(chains []string) ([]string, []error) {
	var (
		slugs []string
		errs  []error
	)
	for _, chain := range chains {
		n, ok := network[chain]
		if !ok {
			errs = append(errs, fmt.Errorf("chain %s: alchemy has no network for it", chain))
			continue
		}
		slugs = append(slugs, n.slug)
	}
	return slugs, errs
}

// convert turns API rows into balances, returning the rows it refused.
//
// Refusal, not omission, is the point of the second return value. Three things
// get refused and every one of them would otherwise be a wrong NUMBER rather
// than a missing one:
//
//   - a row the API itself marked failed: its metadata is unknown, so its
//     scale is unknown;
//   - a token with no decimals: the raw amount would be read as whole units,
//     which is how a balance becomes a thousandfold overstatement;
//   - a balance that is not a hex quantity: unparseable is not zero.
//
// A zero balance is dropped silently and that is different: zero is a fact the
// API stated, and a holding that goes to zero is zeroed by the sync from the
// snapshot's completeness, not from a row saying so.
func (a *WalletSyncerAdapter) convert(tokens []Token) ([]entity.WalletBalance, []error) {
	var (
		result []entity.WalletBalance
		errs   []error
	)
	for _, t := range tokens {
		chain, ok := chainOf(t.Network)
		if !ok {
			errs = append(errs, fmt.Errorf("network %s: unknown to this build, so a balance on it was not read", t.Network))
			continue
		}
		if t.Err != "" {
			errs = append(errs, fmt.Errorf("chain %s, token %s: %s", chain, tokenLabel(t), t.Err))
			continue
		}

		amount, ok := hexToDecimal(t.TokenBalance)
		if !ok {
			errs = append(errs, fmt.Errorf("chain %s, token %s: balance %q is not a hex quantity", chain, tokenLabel(t), t.TokenBalance))
			continue
		}
		if amount == "0" {
			continue
		}

		native := t.TokenAddress == ""
		if native {
			n := network[chain]
			result = append(result, entity.WalletBalance{
				Symbol:   n.symbol,
				Name:     n.title,
				Amount:   amount,
				Decimals: nativeDecimals,
				Chain:    chain,
			})
			continue
		}

		if t.Decimals == nil {
			errs = append(errs, fmt.Errorf("chain %s, token %s: no decimals reported, so the amount has no scale", chain, tokenLabel(t)))
			continue
		}

		// ProviderSpam and ContractVerified stay nil: Alchemy reports neither.
		// nil means "this source makes no statement", which the scorer treats
		// as no evidence — as opposed to false, which would be evidence of
		// innocence this adapter has not got. Losing those two signals is a
		// real cost of leaving Moralis, and it is carried here rather than
		// papered over.
		result = append(result, entity.WalletBalance{
			Symbol:          t.Symbol,
			Name:            t.Name,
			Amount:          amount,
			Decimals:        *t.Decimals,
			ContractAddress: t.TokenAddress,
			Chain:           chain,
		})
	}
	return result, errs
}

// tokenLabel names a row for an error message, preferring the contract address
// because a symbol is attacker-controlled and two rows can share one.
func tokenLabel(t Token) string {
	if t.TokenAddress != "" {
		return t.TokenAddress
	}
	if t.Symbol != "" {
		return t.Symbol
	}
	return "native"
}

// hexToDecimal converts a hex quantity string to a decimal integer string.
//
// The wire format differs from the previous source's: Moralis sent decimal
// strings, Alchemy sends "0x..." quantities. Reading one as the other does not
// fail, it produces a different number — which is why this is a conversion with
// a boolean rather than a cast.
func hexToDecimal(hex string) (string, bool) {
	trimmed := strings.TrimPrefix(strings.TrimPrefix(hex, "0x"), "0X")
	if trimmed == "" {
		return "", false
	}
	n, ok := new(big.Int).SetString(trimmed, 16)
	if !ok {
		return "", false
	}
	return n.String(), true
}
