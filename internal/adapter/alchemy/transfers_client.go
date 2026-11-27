package alchemy

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	neturl "net/url"
	"strconv"
	"time"

	"github.com/foxcool/greedy-eye/internal/adapter/internal/urlerr"
)

// maxTransfersPerPage is maxCount of alchemy_getAssetTransfers: 0x3e8, the
// API's default and the largest page its documentation names.
const maxTransfersPerPage = 1000

// maxBlocksPerBatch bounds one JSON-RPC batch of block reads. Each item is
// metered on its own, and a batch of 100 (about 1600 CU) went past the free
// tier's throughput at once: the node refused items and the calls after it.
// 20 is about 320 CU.
const maxBlocksPerBatch = 20

// blockBatchAttempts is how often a batch item the node refused is asked
// again, blockRetryPause apart, before the read fails naming the node's answer.
const blockBatchAttempts = 3

var blockRetryPause = time.Second

// TransferQuery is one alchemy_getAssetTransfers request. Exactly one of
// FromAddress and ToAddress is set: the API matches both only as AND.
type TransferQuery struct {
	FromBlock, ToBlock uint64
	FromAddress        string
	ToAddress          string
	Categories         []string
	// WithMetadata asks for block timestamps, which the API serves only on
	// some networks (see network.transferTimes).
	WithMetadata bool
	PageKey      string
}

// RawTransfer is one transfer as alchemy_getAssetTransfers reports it.
type RawTransfer struct {
	BlockNum    string  `json:"blockNum"`
	UniqueID    string  `json:"uniqueId"`
	Hash        string  `json:"hash"`
	From        string  `json:"from"`
	To          *string `json:"to"`
	Asset       *string `json:"asset"`
	Category    string  `json:"category"`
	RawContract struct {
		// Value is the raw amount as a hex quantity.
		Value *string `json:"value"`
		// Address is null for the chain's own coin.
		Address *string `json:"address"`
		// Decimal is a hex quantity, null when the API does not know it.
		Decimal *string `json:"decimal"`
	} `json:"rawContract"`
	Metadata *struct {
		BlockTimestamp string `json:"blockTimestamp"`
	} `json:"metadata"`
}

// TransferPage is one page of transfers and the key to the next; an empty
// PageKey ends the walk.
type TransferPage struct {
	Transfers []RawTransfer
	PageKey   string
	// Raw holds each transfer's JSON as received, index-aligned with Transfers.
	Raw []json.RawMessage
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// postRPC sends one JSON-RPC body (a single call or a batch) to a network and
// returns the response body.
func (c *Client) postRPC(ctx context.Context, slug, method string, payload any) ([]byte, error) {
	url := fmt.Sprintf("%s/%s", c.rpcURL(slug), neturl.PathEscape(c.apiKey))
	return c.post(ctx, url, method+" on "+slug, payload)
}

// post sends a JSON body and returns the response body. The key is a path
// segment of url, so url never reaches an error: a transport error from Do
// quotes it itself, so that error is stripped of it (urlerr).
func (c *Client) post(ctx context.Context, url, what string, payload any) ([]byte, error) {
	encoded, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("alchemy: encode request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(encoded))
	if err != nil {
		return nil, fmt.Errorf("alchemy: create request: %w", urlerr.Strip(err))
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("alchemy: do request: %w", urlerr.Strip(err))
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("alchemy: API status %d for %s", resp.StatusCode, what)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("alchemy: read response: %w", err)
	}
	return body, nil
}

// rpcCall is a single JSON-RPC request. No params is an empty list on the
// wire, never null: a strict endpoint refuses null.
func rpcCall(method string, params ...any) rpcRequest {
	if params == nil {
		params = []any{}
	}
	return rpcRequest{JSONRPC: "2.0", ID: 1, Method: method, Params: params}
}

// BlockNumber returns the network's head as the API's node sees it.
func (c *Client) BlockNumber(ctx context.Context, slug string) (uint64, error) {
	body, err := c.postRPC(ctx, slug, "eth_blockNumber", rpcCall("eth_blockNumber"))
	if err != nil {
		return 0, err
	}
	var parsed struct {
		Result string    `json:"result"`
		Error  *rpcError `json:"error"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return 0, fmt.Errorf("alchemy: decode eth_blockNumber: %w", err)
	}
	if parsed.Error != nil {
		return 0, fmt.Errorf("alchemy: eth_blockNumber on %s: %s", slug, parsed.Error.Message)
	}
	head, ok := hexToUint(parsed.Result)
	if !ok {
		return 0, fmt.Errorf("alchemy: eth_blockNumber on %s answered %q, not a block number", slug, parsed.Result)
	}
	return head, nil
}

// AssetTransfers fetches one page of alchemy_getAssetTransfers, oldest first.
func (c *Client) AssetTransfers(ctx context.Context, slug string, q TransferQuery) (TransferPage, error) {
	params := map[string]any{
		"fromBlock":        hexQuantity(q.FromBlock),
		"toBlock":          hexQuantity(q.ToBlock),
		"category":         q.Categories,
		"order":            "asc",
		"withMetadata":     q.WithMetadata,
		"excludeZeroValue": true,
		"maxCount":         hexQuantity(maxTransfersPerPage),
	}
	if q.FromAddress != "" {
		params["fromAddress"] = q.FromAddress
	}
	if q.ToAddress != "" {
		params["toAddress"] = q.ToAddress
	}
	if q.PageKey != "" {
		params["pageKey"] = q.PageKey
	}
	body, err := c.postRPC(ctx, slug, "alchemy_getAssetTransfers", rpcCall("alchemy_getAssetTransfers", params))
	if err != nil {
		return TransferPage{}, err
	}
	var parsed struct {
		Result *struct {
			Transfers []json.RawMessage `json:"transfers"`
			PageKey   string            `json:"pageKey"`
		} `json:"result"`
		Error *rpcError `json:"error"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return TransferPage{}, fmt.Errorf("alchemy: decode asset transfers: %w", err)
	}
	if parsed.Error != nil {
		return TransferPage{}, fmt.Errorf("alchemy: alchemy_getAssetTransfers on %s: %s", slug, parsed.Error.Message)
	}
	// No result is not an empty page: read as one, it would cover the range.
	if parsed.Result == nil {
		return TransferPage{}, fmt.Errorf("alchemy: alchemy_getAssetTransfers on %s answered no result", slug)
	}
	page := TransferPage{
		PageKey:   parsed.Result.PageKey,
		Transfers: make([]RawTransfer, len(parsed.Result.Transfers)),
		Raw:       parsed.Result.Transfers,
	}
	for i, raw := range parsed.Result.Transfers {
		if err := json.Unmarshal(raw, &page.Transfers[i]); err != nil {
			return TransferPage{}, fmt.Errorf("alchemy: decode transfer %d: %w", i, err)
		}
	}
	return page, nil
}

// BlockTimes reads the timestamps of the given blocks in JSON-RPC batches.
// An item the node refused (throughput) is asked again; a block still without
// an answer is an error, because an event without its time cannot be written.
func (c *Client) BlockTimes(ctx context.Context, slug string, blocks []uint64) (map[uint64]time.Time, error) {
	out := make(map[uint64]time.Time, len(blocks))
	for start := 0; start < len(blocks); start += maxBlocksPerBatch {
		pending := blocks[start:min(start+maxBlocksPerBatch, len(blocks))]
		var why string
		for attempt := 0; len(pending) > 0; attempt++ {
			if attempt == blockBatchAttempts {
				return nil, fmt.Errorf("alchemy: block %d on %s has no answer: %s", pending[0], slug, why)
			}
			if attempt > 0 {
				select {
				case <-ctx.Done():
					return nil, ctx.Err()
				case <-time.After(blockRetryPause):
				}
			}
			var err error
			if pending, why, err = c.blockBatch(ctx, slug, pending, out); err != nil {
				return nil, err
			}
		}
	}
	return out, nil
}

// blockBatch reads one batch into out and returns the blocks left without an
// answer, with the node's last word on them.
func (c *Client) blockBatch(ctx context.Context, slug string, chunk []uint64, out map[uint64]time.Time) ([]uint64, string, error) {
	batch := make([]rpcRequest, len(chunk))
	for i, b := range chunk {
		batch[i] = rpcRequest{JSONRPC: "2.0", ID: i, Method: "eth_getBlockByNumber", Params: []any{hexQuantity(b), false}}
	}
	body, err := c.postRPC(ctx, slug, "eth_getBlockByNumber", batch)
	if err != nil {
		return nil, "", err
	}
	var parsed []struct {
		ID     int `json:"id"`
		Result *struct {
			Timestamp string `json:"timestamp"`
		} `json:"result"`
		Error *rpcError `json:"error"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, "", fmt.Errorf("alchemy: decode block batch: %w", err)
	}
	why := "no item in the answer"
	for _, r := range parsed {
		if r.ID < 0 || r.ID >= len(chunk) {
			return nil, "", fmt.Errorf("alchemy: block batch on %s answered unknown id %d", slug, r.ID)
		}
		block := chunk[r.ID]
		switch {
		case r.Error != nil:
			why = r.Error.Message
			continue
		case r.Result == nil:
			why = "the node has no such block"
			continue
		}
		ts, ok := hexToUint(r.Result.Timestamp)
		if !ok || ts > math.MaxInt64 {
			return nil, "", fmt.Errorf("alchemy: block %d on %s has timestamp %q", block, slug, r.Result.Timestamp)
		}
		out[block] = time.Unix(int64(ts), 0).UTC() // #nosec G115 -- checked against MaxInt64 above
	}
	var missing []uint64
	for _, b := range chunk {
		if _, ok := out[b]; !ok {
			missing = append(missing, b)
		}
	}
	return missing, why, nil
}

func hexQuantity(n uint64) string { return "0x" + strconv.FormatUint(n, 16) }

// hexToUint reads a "0x…" quantity that must fit 64 bits (blocks, times).
func hexToUint(hex string) (uint64, bool) {
	if len(hex) < 3 || (hex[:2] != "0x" && hex[:2] != "0X") {
		return 0, false
	}
	n, err := strconv.ParseUint(hex[2:], 16, 64)
	return n, err == nil
}
