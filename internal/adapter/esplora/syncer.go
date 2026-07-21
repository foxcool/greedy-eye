package esplora

import (
	"context"
	"fmt"
	"regexp"
	"strconv"

	"github.com/foxcool/greedy-eye/internal/adapter/internal/base58"
	"github.com/foxcool/greedy-eye/internal/entity"
)

// SupportedChains returns the chains this adapter serves, for registration in
// the chain-keyed syncer registry.
func SupportedChains() []string { return []string{Chain} }

// bech32Address matches the native SegWit form: the "bc1" human-readable part
// followed by the bech32 alphabet (no 1, b, i or o).
var bech32Address = regexp.MustCompile(`^bc1[02-9ac-hj-np-z]{11,71}$`)

// Legacy address version bytes: P2PKH pays to a key hash, P2SH to a script
// hash. They are what separates Bitcoin from the other base58 chains, whose
// addresses are byte-identical in structure and differ only here.
const (
	versionP2PKH = 0x00
	versionP2SH  = 0x05
)

// legacyLen is the decoded size of a base58 address: version byte, 20-byte
// hash, 4-byte checksum.
const legacyLen = 25

// HandlesAddress reports whether an address is a Bitcoin one, routing accounts
// that name no chain to this adapter.
//
// Legacy addresses are checked by version byte rather than by prefix character.
// Dash and Dogecoin use the same 25-byte base58 layout and are told apart only
// by that byte, so a prefix rule would hand their addresses to Bitcoin, where
// they resolve to an unused address and report an empty wallet.
func HandlesAddress(address string) bool {
	if bech32Address.MatchString(address) {
		return true
	}

	decoded, err := base58.Decode(address)
	if err != nil || len(decoded) != legacyLen {
		return false
	}
	return decoded[0] == versionP2PKH || decoded[0] == versionP2SH
}

// WalletSyncerAdapter adapts *Client to entity.WalletSyncer.
type WalletSyncerAdapter struct {
	client *Client
}

// NewWalletSyncer wraps a *Client as an entity.WalletSyncer.
func NewWalletSyncer(c *Client) *WalletSyncerAdapter {
	return &WalletSyncerAdapter{client: c}
}

// SyncWallet returns the confirmed BTC balance of one address.
//
// A Bitcoin wallet usually spans many addresses; the fan-out lives on the
// account (accounts.data.addresses) rather than here, so each call stays a
// single request. Deriving addresses from an xpub is out of scope.
func (a *WalletSyncerAdapter) SyncWallet(ctx context.Context, address string, chains []string) ([]entity.WalletBalance, error) {
	for _, c := range chains {
		if c != Chain {
			return nil, fmt.Errorf("esplora: unsupported chain %q", c)
		}
	}

	sats, err := a.client.GetBalance(ctx, address)
	if err != nil {
		return nil, err
	}
	if sats <= 0 {
		return nil, nil // an address that holds nothing yields no position
	}

	return []entity.WalletBalance{{
		Symbol:   nativeSymbol,
		Name:     nativeName,
		Amount:   strconv.FormatInt(sats, 10),
		Decimals: nativeDecimals,
		Chain:    Chain,
	}}, nil
}
