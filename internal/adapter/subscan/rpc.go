package subscan

import (
	"bytes"
	"context"
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
