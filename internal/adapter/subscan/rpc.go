package subscan

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"strings"

	"github.com/shopspring/decimal"
	"golang.org/x/crypto/blake2b"
)

// RPCBudget is the rate-limit slug for requests to chain nodes. They are not
// Subscan's: spending its plan on them, or sending its key along, would both
// be wrong.
const RPCBudget = "substrate-rpc"

// systemAccountPrefix is twox128("System") ++ twox128("Account"): the storage
// prefix of the map every Substrate chain keeps its native balances in. It is a
// constant of the FRAME layout, not of any one chain, so it is written out
// rather than hashed at runtime (which would mean a dependency for xxhash).
const systemAccountPrefix = "26aa394eea5630e07c48ae0c9558cef7b99d880ec681799c0cf30e8886371da9"

// tokensAccountsPrefix is twox128("Tokens") ++ twox128("Accounts"): the orml
// tokens double map (account Blake2_128Concat, asset id Twox64Concat) where
// Hydration keeps every non-native balance.
//
// #nosec G101 -- a public storage-key hash, the same on every node
const tokensAccountsPrefix = "99971b5749ac43e0235e41b0d37869188ee7418a6531173d60d1f6a82d8f4d51" // gitleaks:allow

// assetRegistryPrefix is twox128("AssetRegistry") ++ twox128("Assets"), keyed
// by asset id under Blake2_128Concat.
const assetRegistryPrefix = "6e9a9b71050cd23f2d7d1b72e8c1a625682a59d51ab9e48a8c8cc418ff9708d2"

// tokenAccountLen is the SCALE size of orml AccountData: free, reserved and
// frozen as u128, with the same meaning as the native coin's.
const tokenAccountLen = 3 * 16

// keysPageSize is how many storage keys one state_getKeysPaged call asks for.
const keysPageSize = 100

// maxKeyPages caps the pages read for one account. Hydration registers a few
// hundred assets in total, so an account that fills fifty pages is a node
// misbehaving, not a wallet.
const maxKeyPages = 50

// accountInfoLen is the SCALE size of AccountInfo<u32, AccountData<u128>>:
// nonce, consumers, providers and sufficients as u32, then free, reserved,
// frozen and flags as u128.
const accountInfoLen = 4*4 + 4*16

// NativeState is a chain's own coin for one address, read from chain state
// rather than from an indexer.
//
// Free and Reserved are disjoint in the current balances pallet and add up to
// the holding. Frozen is a floor over Free, not a third part: it is how much of
// Free may not leave the account, the largest of its locks, not their sum.
type NativeState struct {
	Free     decimal.Decimal
	Reserved decimal.Decimal
	Frozen   decimal.Decimal
	Decimals int32
	Symbol   string
}

// GetNativeState reads an address's native balance from the chain's own RPC.
//
// It exists because an indexer can stop serving a chain without the chain
// stopping: Subscan has answered 404 for Hydration since 2026-08-27 while the
// chain kept producing blocks, and a position there went unrefreshed for seven
// weeks (personal-feb.16). Chain state cannot disappear that way.
//
// An address the chain has never seen is not an error: the storage read
// answers null, which is a zero balance.
func (c *Client) GetNativeState(ctx context.Context, chain, address string) (NativeState, error) {
	net, ok := networks[chain]
	if !ok || net.rpc == "" {
		return NativeState{}, fmt.Errorf("no RPC endpoint for chain %q", chain)
	}
	pub, err := ss58PublicKey(address)
	if err != nil {
		return NativeState{}, fmt.Errorf("address for %s: %w", chain, err)
	}

	// The precision is asked of the chain, not taken from the network table,
	// for the same reason the Subscan path reads it from the response: a table
	// that disagrees with the chain silently rescales a position.
	var props struct {
		TokenDecimals json.RawMessage `json:"tokenDecimals"`
		TokenSymbol   json.RawMessage `json:"tokenSymbol"`
	}
	if err := c.rpcCall(ctx, chain, net.rpc, "system_properties", nil, &props); err != nil {
		return NativeState{}, err
	}
	var decimals int32
	if err := firstOf(props.TokenDecimals, &decimals); err != nil {
		return NativeState{}, fmt.Errorf("%s: chain states no readable decimals: %w", chain, err)
	}
	var symbol string
	if err := firstOf(props.TokenSymbol, &symbol); err != nil || symbol == "" {
		symbol = net.symbol
	}

	key := "0x" + systemAccountPrefix + blake2128Concat(pub)

	var raw *string
	if err := c.rpcCall(ctx, chain, net.rpc, "state_getStorage", []any{key}, &raw); err != nil {
		return NativeState{}, err
	}
	state := NativeState{Free: decimal.Zero, Reserved: decimal.Zero, Frozen: decimal.Zero, Decimals: decimals, Symbol: symbol}
	if raw == nil {
		return state, nil
	}

	b, err := hex.DecodeString(strings.TrimPrefix(*raw, "0x"))
	if err != nil {
		return NativeState{}, fmt.Errorf("%s: account storage is not hex: %w", chain, err)
	}
	if len(b) < accountInfoLen {
		// A shorter value is a different layout (an older AccountData without
		// frozen, or a chain with its own). Reading it at these offsets would
		// produce a number, and a wrong one.
		return NativeState{}, fmt.Errorf("%s: account storage is %d bytes, want %d", chain, len(b), accountInfoLen)
	}
	state.Free = u128(b[16:32])
	state.Reserved = u128(b[32:48])
	state.Frozen = u128(b[48:64])
	return state, nil
}

// TokenState is one non-native asset an address holds, read from the Tokens
// pallet, with symbol and precision from the chain's asset registry.
type TokenState struct {
	AssetID  uint32
	Symbol   string
	Decimals int32
	Free     decimal.Decimal
	Reserved decimal.Decimal
	Frozen   decimal.Decimal
}

// GetTokenStates reads every non-native balance an address holds on a chain
// with an orml Tokens pallet (Hydration).
//
// Zero balances are dropped: an asset once held and spent keeps its storage
// entry. An asset the registry cannot describe is not dropped silently either:
// it comes back in skipped, beside the positions that did read, the same way
// the Subscan path names a refused token entry.
//
// Liquidity tokens of the EVM money market (GIGAHDX and the other aTokens)
// live in EVM storage, not here, so they are not read and cannot double the
// native HDX they are backed by.
func (c *Client) GetTokenStates(ctx context.Context, chain, address string) ([]TokenState, []string, error) {
	net, ok := networks[chain]
	if !ok || net.rpc == "" {
		return nil, nil, fmt.Errorf("no RPC endpoint for chain %q", chain)
	}
	pub, err := ss58PublicKey(address)
	if err != nil {
		return nil, nil, fmt.Errorf("address for %s: %w", chain, err)
	}

	prefix := "0x" + tokensAccountsPrefix + blake2128Concat(pub)
	var keys []string
	start := prefix
	for pages := 0; ; pages++ {
		if pages == maxKeyPages {
			return nil, nil, fmt.Errorf("%s: more than %d token entries for one account", chain, maxKeyPages*keysPageSize)
		}
		var page []string
		if err := c.rpcCall(ctx, chain, net.rpc, "state_getKeysPaged", []any{prefix, keysPageSize, start}, &page); err != nil {
			return nil, nil, err
		}
		// Keys come back sorted and after start. A node that repeats a page
		// or strays outside the prefix would otherwise loop until the sync
		// deadline, or read other accounts' balances as this one's.
		for _, k := range page {
			if !strings.HasPrefix(k, prefix) || k <= start {
				return nil, nil, fmt.Errorf("%s: state_getKeysPaged returned key %s out of order or outside the account", chain, k)
			}
			start = k
		}
		keys = append(keys, page...)
		if len(page) < keysPageSize {
			break
		}
	}

	var (
		out     []TokenState
		skipped []string
	)
	for _, key := range keys {
		id, err := tokenKeyAssetID(prefix, key)
		if err != nil {
			return nil, nil, fmt.Errorf("%s: %w", chain, err)
		}

		var raw *string
		if err := c.rpcCall(ctx, chain, net.rpc, "state_getStorage", []any{key}, &raw); err != nil {
			return nil, nil, err
		}
		if raw == nil {
			continue // removed between the two reads
		}
		b, err := hex.DecodeString(strings.TrimPrefix(*raw, "0x"))
		if err != nil {
			return nil, nil, fmt.Errorf("%s: token %d storage is not hex: %w", chain, id, err)
		}
		if len(b) != tokenAccountLen {
			return nil, nil, fmt.Errorf("%s: token %d storage is %d bytes, want %d", chain, id, len(b), tokenAccountLen)
		}
		state := TokenState{AssetID: id, Free: u128(b[0:16]), Reserved: u128(b[16:32]), Frozen: u128(b[32:48])}
		if state.Free.Add(state.Reserved).IsZero() {
			continue
		}

		var details *string
		regKey := "0x" + assetRegistryPrefix + blake2128Concat(binary.LittleEndian.AppendUint32(nil, id))
		if err := c.rpcCall(ctx, chain, net.rpc, "state_getStorage", []any{regKey}, &details); err != nil {
			return nil, nil, err
		}
		if details == nil {
			skipped = append(skipped, fmt.Sprintf("%s: token %d is not in the asset registry", chain, id))
			continue
		}
		db, err := hex.DecodeString(strings.TrimPrefix(*details, "0x"))
		if err != nil {
			skipped = append(skipped, fmt.Sprintf("%s: token %d registry entry is not hex", chain, id))
			continue
		}
		symbol, decimals, err := decodeAssetDetails(db)
		if err != nil {
			skipped = append(skipped, fmt.Sprintf("%s: token %d: %v", chain, id, err))
			continue
		}
		state.Symbol, state.Decimals = symbol, decimals
		out = append(out, state)
	}
	return out, skipped, nil
}

// tokenKeyAssetID takes the asset id off the end of a Tokens.Accounts key:
// the address prefix, then Twox64Concat(u32) — an 8-byte hash and the id.
func tokenKeyAssetID(prefix, key string) (uint32, error) {
	rest, ok := strings.CutPrefix(key, prefix)
	if !ok || len(rest) != 2*(8+4) {
		return 0, fmt.Errorf("unexpected Tokens.Accounts key %s", key)
	}
	b, err := hex.DecodeString(rest[16:])
	if err != nil {
		return 0, fmt.Errorf("unexpected Tokens.Accounts key %s: %w", key, err)
	}
	return binary.LittleEndian.Uint32(b), nil
}

// decodeAssetDetails reads symbol and decimals from a Hydration AssetRegistry
// entry: name Option<Vec<u8>>, asset_type (a unit enum, one byte),
// existential_deposit u128, symbol Option<Vec<u8>>, decimals Option<u8>,
// xcm_rate_limit Option<u128>, is_sufficient bool.
//
// The tail is checked for its exact length, so a layout change fails here
// instead of shifting every later field. Absent decimals is a refusal, not a
// zero: read as whole units, six-decimal USDT would be a million times over.
func decodeAssetDetails(b []byte) (string, int32, error) {
	r := scaleReader{b: b}
	name := r.optBytes()
	r.skip(1 + 16) // asset_type, existential_deposit
	symbol := r.optBytes()
	var decimals byte
	hasDecimals := r.byte() != 0
	if hasDecimals {
		decimals = r.byte()
	}
	if r.err != nil {
		return "", 0, fmt.Errorf("asset registry entry: %w", r.err)
	}
	switch tail := len(b) - r.pos; {
	case tail == 1+1 && b[r.pos] == 0, tail == 1+16+1 && b[r.pos] == 1:
	default:
		return "", 0, fmt.Errorf("asset registry entry has an unexpected layout (%d trailing bytes)", tail)
	}
	if !hasDecimals {
		return "", 0, fmt.Errorf("asset registry states no decimals")
	}
	if symbol == nil {
		symbol = name
	}
	if len(symbol) == 0 {
		return "", 0, fmt.Errorf("asset registry states no symbol or name")
	}
	return string(symbol), int32(decimals), nil
}

// scaleReader reads the few SCALE shapes the asset registry uses. The first
// error sticks, so a caller checks once after a run of reads.
type scaleReader struct {
	b   []byte
	pos int
	err error
}

func (r *scaleReader) byte() byte {
	if r.err == nil && r.pos >= len(r.b) {
		r.err = fmt.Errorf("truncated at byte %d", r.pos)
	}
	if r.err != nil {
		return 0
	}
	v := r.b[r.pos]
	r.pos++
	return v
}

func (r *scaleReader) skip(n int) {
	if r.err == nil && r.pos+n > len(r.b) {
		r.err = fmt.Errorf("truncated at byte %d", r.pos)
	}
	if r.err == nil {
		r.pos += n
	}
}

// optBytes reads Option<Vec<u8>>, returning nil for None. Only the one- and
// two-byte compact length modes are read; a registry string is never 16 KiB.
func (r *scaleReader) optBytes() []byte {
	if r.byte() == 0 || r.err != nil {
		return nil
	}
	first := r.byte()
	var n int
	switch first & 3 {
	case 0:
		n = int(first >> 2)
	case 1:
		n = int(binary.LittleEndian.Uint16([]byte{first, r.byte()}) >> 2)
	default:
		if r.err == nil {
			r.err = fmt.Errorf("compact length mode %d at byte %d", first&3, r.pos-1)
		}
		return nil
	}
	start := r.pos
	r.skip(n)
	if r.err != nil {
		return nil
	}
	return r.b[start : start+n]
}

// blake2128Concat is the Blake2_128Concat hasher of FRAME storage maps: the
// 16-byte digest followed by the key itself.
func blake2128Concat(key []byte) string {
	h, _ := blake2b.New(16, nil) // only fails for a size over 64 or a long key
	_, _ = h.Write(key)          // hash.Hash.Write never returns an error
	return hex.EncodeToString(h.Sum(nil)) + hex.EncodeToString(key)
}

// u128 reads a little-endian unsigned 128-bit integer.
func u128(le []byte) decimal.Decimal {
	be := make([]byte, len(le))
	for i, v := range le {
		be[len(le)-1-i] = v
	}
	return decimal.NewFromBigInt(new(big.Int).SetBytes(be), 0)
}

// firstOf decodes a chain property that is a scalar on single-token chains and
// an array on multi-token ones, where the first entry is the native token.
func firstOf[T any](raw json.RawMessage, out *T) error {
	if len(raw) == 0 || string(raw) == "null" {
		return fmt.Errorf("absent")
	}
	if raw[0] == '[' {
		var list []T
		if err := json.Unmarshal(raw, &list); err != nil {
			return err
		}
		if len(list) == 0 {
			return fmt.Errorf("empty list")
		}
		*out = list[0]
		return nil
	}
	return json.Unmarshal(raw, out)
}

// rpcCall makes one JSON-RPC 2.0 request over HTTP.
//
// It goes through rpcHTTP, never httpClient: the latter carries the Subscan
// budget, and the request here must not carry the Subscan key either — the
// endpoint is a third party's node.
func (c *Client) rpcCall(ctx context.Context, chain, endpoint, method string, params []any, out any) error {
	if c.rpcURLOverride != "" {
		endpoint = c.rpcURLOverride
	}
	if params == nil {
		params = []any{}
	}
	body, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": method, "params": params})
	if err != nil {
		return fmt.Errorf("encode %s: %w", method, err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("create %s request: %w", method, err)
	}
	req.Header.Set("Content-Type", "application/json")
	// Measured 2026-10-17: the public Hydration node answers 403 to a request
	// without a User-Agent, as behind a default bot filter.
	req.Header.Set("User-Agent", "greedy-eye")

	resp, err := c.rpcHTTP.Do(req)
	if err != nil {
		return fmt.Errorf("%s %s: %w", chain, method, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%s RPC status %d for %s", chain, resp.StatusCode, method)
	}

	var envelope struct {
		Result json.RawMessage `json:"result"`
		Error  *struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&envelope); err != nil {
		return fmt.Errorf("%s %s: decode: %w", chain, method, err)
	}
	if envelope.Error != nil {
		return fmt.Errorf("%s %s: RPC error %d: %s", chain, method, envelope.Error.Code, envelope.Error.Message)
	}
	if err := json.Unmarshal(envelope.Result, out); err != nil {
		return fmt.Errorf("%s %s: decode result: %w", chain, method, err)
	}
	return nil
}
