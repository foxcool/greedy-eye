package moralis

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"slices"

	"github.com/foxcool/greedy-eye/internal/entity"
)

// nativeToken maps a Moralis chain identifier to its native coin metadata.
var nativeToken = map[string]struct{ symbol, name string }{
	"eth":       {"ETH", "Ethereum"},
	"base":      {"ETH", "Ethereum"},
	"arbitrum":  {"ETH", "Ethereum"},
	"optimism":  {"ETH", "Ethereum"},
	"linea":     {"ETH", "Ethereum"},
	"zksync":    {"ETH", "Ethereum"},
	"scroll":    {"ETH", "Ethereum"},
	"polygon":   {"POL", "Polygon"},
	"bsc":       {"BNB", "BNB Chain"},
	"avalanche": {"AVAX", "Avalanche"},
	"fantom":    {"FTM", "Fantom"},
}

const nativeDecimals = 18

// evmAddress matches a 20-byte hex address, the only form Moralis accepts.
var evmAddress = regexp.MustCompile(`^0x[0-9a-fA-F]{40}$`)

// HandlesAddress reports whether an address belongs to an EVM chain, routing
// accounts that name no chain (auto-discovery) to this adapter.
func HandlesAddress(address string) bool {
	return evmAddress.MatchString(address)
}

// SupportedChains returns the chain identifiers this adapter can sync. The
// syncer registry uses it to route an account to the right provider, so it
// must list every chain nativeToken knows — including the ones the chains
// endpoint refuses to probe (scroll/zksync/fantom): balance lookups work for
// them, only auto-discovery does not.
func SupportedChains() []string {
	chains := make([]string, 0, len(nativeToken))
	for chain := range nativeToken {
		chains = append(chains, chain)
	}
	slices.Sort(chains) // map iteration order is random; keep the result stable
	return chains
}

// WalletSyncerAdapter adapts *Client to entity.WalletSyncer.
type WalletSyncerAdapter struct {
	client *Client
}

// NewWalletSyncer wraps a *Client as an entity.WalletSyncer.
func NewWalletSyncer(c *Client) *WalletSyncerAdapter {
	return &WalletSyncerAdapter{client: c}
}

// SyncWallet fetches native and token balances across the given chains, returning a flat
// normalized list. When chains is empty it auto-discovers chains with activity, falling
// back to "eth" if discovery fails or yields nothing. Per-chain failures are joined into
// the returned error while the balances gathered so far are still returned.
func (a *WalletSyncerAdapter) SyncWallet(ctx context.Context, address string, chains []string) ([]entity.WalletBalance, error) {
	if len(chains) == 0 {
		chains = a.resolveChains(ctx, address)
	}

	var (
		result []entity.WalletBalance
		errs   []error
	)
	for _, chain := range chains {
		tokens, err := a.tokenBalances(ctx, chain, address)
		if err != nil {
			errs = append(errs, fmt.Errorf("chain %s tokens: %w", chain, err))
		} else {
			result = append(result, tokens...)
		}

		native, err := a.nativeBalance(ctx, chain, address)
		if err != nil {
			errs = append(errs, fmt.Errorf("chain %s native: %w", chain, err))
		} else if native != nil {
			result = append(result, *native)
		}
	}

	return result, errors.Join(errs...)
}

// resolveChains discovers chains with activity, falling back to "eth".
func (a *WalletSyncerAdapter) resolveChains(ctx context.Context, address string) []string {
	discovered, err := a.client.GetActiveChains(ctx, address)
	if err != nil || len(discovered) == 0 {
		return []string{"eth"}
	}
	return discovered
}

// tokenBalances returns ERC-20 token balances for a single chain.
func (a *WalletSyncerAdapter) tokenBalances(ctx context.Context, chain, address string) ([]entity.WalletBalance, error) {
	balances, err := a.client.GetWalletTokenBalances(ctx, chain, address)
	if err != nil {
		return nil, err
	}
	result := make([]entity.WalletBalance, 0, len(balances))
	for _, b := range balances {
		// Scam tokens clone real symbols (fake USDT airdrops etc.) and would
		// merge into legitimate holdings downstream — drop at the source.
		// Moralis misses some scams in possible_spam (a fake USDT passed), so
		// unverified contracts are dropped too; majors are all verified.
		if b.PossibleSpam || !b.VerifiedContract {
			continue
		}
		result = append(result, entity.WalletBalance{
			Symbol:          b.Symbol,
			Name:            b.Name,
			Amount:          b.Balance,
			Decimals:        b.Decimals,
			ContractAddress: b.TokenAddress,
			Chain:           chain,
		})
	}
	return result, nil
}

// nativeBalance returns the native coin balance for a single chain, or nil when the
// balance is zero. Unknown chains are skipped (nil, nil) rather than erroring, so a
// chain set that mixes supported and unsupported networks still syncs the rest.
func (a *WalletSyncerAdapter) nativeBalance(ctx context.Context, chain, address string) (*entity.WalletBalance, error) {
	native, ok := nativeToken[chain]
	if !ok {
		return nil, nil
	}

	raw, err := a.client.GetWalletBalance(ctx, chain, address)
	if err != nil {
		return nil, err
	}
	if raw == "" || raw == "0" {
		return nil, nil
	}
	return &entity.WalletBalance{
		Symbol:   native.symbol,
		Name:     native.name,
		Amount:   raw,
		Decimals: nativeDecimals,
		Chain:    chain,
	}, nil
}
