package alchemy

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// rpcFake answers JSON-RPC by method and records the transfer queries it got.
type rpcFake struct {
	mu        sync.Mutex
	head      uint64
	transfers func(params map[string]any) string // result JSON
	queries   []map[string]any
	blockReqs int
}

func (f *rpcFake) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(body) > 0 && body[0] == '[' {
		var batch []struct {
			ID     int   `json:"id"`
			Params []any `json:"params"`
		}
		_ = json.Unmarshal(body, &batch)
		f.blockReqs++
		out := "["
		for i, b := range batch {
			if i > 0 {
				out += ","
			}
			// Block n was mined at n seconds after 2026-01-01.
			n, _ := hexToUint(b.Params[0].(string))
			out += fmt.Sprintf(`{"jsonrpc":"2.0","id":%d,"result":{"timestamp":"%s"}}`, b.ID, hexQuantity(uint64(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC).Unix())+n))
		}
		writeJSON(w, out+"]")
		return
	}
	var req struct {
		Method string           `json:"method"`
		Params []map[string]any `json:"params"`
	}
	_ = json.Unmarshal(body, &req)
	switch req.Method {
	case "eth_blockNumber":
		writeJSON(w, fmt.Sprintf(`{"jsonrpc":"2.0","id":1,"result":"%s"}`, hexQuantity(f.head)))
	case "alchemy_getAssetTransfers":
		f.queries = append(f.queries, req.Params[0])
		writeJSON(w, `{"jsonrpc":"2.0","id":1,"result":`+f.transfers(req.Params[0])+`}`)
	default:
		w.WriteHeader(http.StatusBadRequest)
	}
}

func transferReaderAgainst(t *testing.T, f *rpcFake) *WalletSyncerAdapter {
	t.Helper()
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	client := NewClient(Config{APIKey: "test-key"})
	client.rpcURL = func(string) string { return srv.URL }
	return NewWalletSyncer(client)
}

const me = "0x75304308839f839a553b60b5671bb2f043420167"

// TestReadTransfers_CoversUpToConfirmedHead: the read stops `confirmations`
// behind the head the source itself reports, and says so in Next. Both
// directions are asked, a transfer to oneself is kept once, the coin and a
// token are told apart, and a token without a known scale keeps its raw
// amount with no decimals rather than a guessed one.
func TestReadTransfers_CoversUpToConfirmedHead(t *testing.T) {
	f := &rpcFake{head: 100, transfers: func(p map[string]any) string {
		if p["fromAddress"] != nil {
			return `{"transfers":[
				{"blockNum":"0x10","uniqueId":"0xaa:external","hash":"0xAA","from":"` + me + `","to":"0xbb","asset":"ETH","category":"external","rawContract":{"value":"0xde0b6b3a7640000","address":null,"decimal":"0x12"},"metadata":{"blockTimestamp":"2026-01-02T03:04:05.000Z"}},
				{"blockNum":"0x20","uniqueId":"0xcc:log:0x1","hash":"0xcc","from":"` + me + `","to":"` + me + `","asset":"USDC","category":"erc20","rawContract":{"value":"0x3e8","address":"0xA0b8","decimal":"0x6"},"metadata":{"blockTimestamp":"2026-01-03T00:00:00.000Z"}}
			]}`
		}
		return `{"transfers":[
			{"blockNum":"0x20","uniqueId":"0xcc:log:0x1","hash":"0xcc","from":"` + me + `","to":"` + me + `","asset":"USDC","category":"erc20","rawContract":{"value":"0x3e8","address":"0xA0b8","decimal":"0x6"},"metadata":{"blockTimestamp":"2026-01-03T00:00:00.000Z"}},
			{"blockNum":"0x30","uniqueId":"0xdd:log:0x0","hash":"0xdd","from":"0xee","to":"` + me + `","asset":null,"category":"erc20","rawContract":{"value":"0x5","address":"0xSPAM","decimal":null},"metadata":{"blockTimestamp":"2026-01-04T00:00:00.000Z"}}
		]}`
	}}
	batch, err := transferReaderAgainst(t, f).ReadTransfers(context.Background(), me, "eth", 5, 10)
	require.NoError(t, err)

	assert.Equal(t, uint64(91), batch.Next, "covered through head-confirmations = 90")
	assert.Empty(t, batch.Incomplete, "eth serves internal transfers")
	require.Len(t, f.queries, 2)
	for _, q := range f.queries {
		assert.Equal(t, "0x5", q["fromBlock"])
		assert.Equal(t, "0x5a", q["toBlock"])
		assert.ElementsMatch(t, []any{"external", "erc20", "internal"}, q["category"])
		assert.Equal(t, true, q["withMetadata"])
	}

	require.Len(t, batch.Transfers, 3, "the transfer to oneself is kept once")
	coin, self, spam := batch.Transfers[0], batch.Transfers[1], batch.Transfers[2]
	assert.Equal(t, "0xaa", coin.TxHash)
	assert.Empty(t, coin.ContractAddress)
	assert.Equal(t, "ETH", coin.Symbol)
	assert.Equal(t, "1000000000000000000", coin.Amount)
	require.NotNil(t, coin.Decimals)
	assert.Equal(t, 18, *coin.Decimals)
	assert.Equal(t, time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC), coin.At)

	assert.Equal(t, "0xa0b8", self.ContractAddress)
	assert.Equal(t, "1000", self.Amount)
	assert.Equal(t, 6, *self.Decimals)

	assert.Equal(t, "5", spam.Amount)
	assert.Nil(t, spam.Decimals, "an unknown scale stays unknown")
	assert.NotEmpty(t, spam.Raw)
}

// TestReadTransfers_HeadBehindCursorCoversNothing: a source whose head is not
// yet past the cursor plus the confirmations has nothing to say, and must not
// be read as having said "nothing moved".
func TestReadTransfers_HeadBehindCursorCoversNothing(t *testing.T) {
	f := &rpcFake{head: 50, transfers: func(map[string]any) string { return `{"transfers":[]}` }}
	batch, err := transferReaderAgainst(t, f).ReadTransfers(context.Background(), me, "eth", 45, 10)
	require.NoError(t, err)
	assert.Equal(t, uint64(45), batch.Next)
	assert.Empty(t, f.queries, "nothing to ask for")
}

// TestReadTransfers_PageBoundLeavesTheLastBlock: a walk stopped by the page
// bound has seen part of its last block at most, so the read ends before it
// and drops what it saw there; the next read takes the block whole.
func TestReadTransfers_PageBoundLeavesTheLastBlock(t *testing.T) {
	var page int
	f := &rpcFake{head: 1000, transfers: func(p map[string]any) string {
		if p["toAddress"] != nil {
			return `{"transfers":[]}`
		}
		page++
		return fmt.Sprintf(`{"pageKey":"k%d","transfers":[
			{"blockNum":"%s","uniqueId":"0x%x:external","hash":"0x%x","from":"%s","to":"0xbb","category":"external","rawContract":{"value":"0x1"},"metadata":{"blockTimestamp":"2026-01-02T00:00:00Z"}}
		]}`, page, hexQuantity(uint64(page*10)), page, page, me)
	}}
	batch, err := transferReaderAgainst(t, f).ReadTransfers(context.Background(), me, "eth", 0, 0)
	require.NoError(t, err)
	assert.Equal(t, uint64(maxTransferPages*10), batch.Next, "ends before the last block seen")
	assert.Len(t, batch.Transfers, maxTransferPages-1)
	for _, tr := range batch.Transfers {
		assert.Less(t, tr.Block, batch.Next)
	}
}

// TestReadTransfers_NetworkWithoutInternalOrTimes: linea serves neither internal
// transfers nor block times in the transfer call. Internal is not asked for
// and the gap is named; times are read from the blocks in one batch.
func TestReadTransfers_NetworkWithoutInternalOrTimes(t *testing.T) {
	f := &rpcFake{head: 100, transfers: func(p map[string]any) string {
		if p["toAddress"] != nil {
			return `{"transfers":[]}`
		}
		return `{"transfers":[
			{"blockNum":"0x7","uniqueId":"0x01:external","hash":"0x01","from":"` + me + `","to":"0xbb","category":"external","rawContract":{"value":"0x1"}},
			{"blockNum":"0x7","uniqueId":"0x02:external","hash":"0x02","from":"` + me + `","to":"0xbb","category":"external","rawContract":{"value":"0x1"}},
			{"blockNum":"0x9","uniqueId":"0x03:external","hash":"0x03","from":"` + me + `","to":"0xbb","category":"external","rawContract":{"value":"0x1"}}
		]}`
	}}
	batch, err := transferReaderAgainst(t, f).ReadTransfers(context.Background(), me, "linea", 0, 0)
	require.NoError(t, err)
	assert.Equal(t, incompleteInternal, batch.Incomplete)
	assert.ElementsMatch(t, []any{"external", "erc20"}, f.queries[0]["category"])
	assert.Equal(t, false, f.queries[0]["withMetadata"])
	assert.Equal(t, 1, f.blockReqs, "two distinct blocks, one batch")
	require.Len(t, batch.Transfers, 3)
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	assert.Equal(t, base.Add(7*time.Second), batch.Transfers[0].At)
	assert.Equal(t, base.Add(9*time.Second), batch.Transfers[2].At)
	assert.Equal(t, "ETH", batch.Transfers[0].Symbol)
}

// TestReadTransfers_FailureCoversNothing: an API failure on either direction
// leaves Next at the cursor.
func TestReadTransfers_FailureCoversNothing(t *testing.T) {
	f := &rpcFake{head: 100, transfers: func(p map[string]any) string {
		if p["toAddress"] != nil {
			return `null,"error":{"code":-32600,"message":"boom"}`
		}
		return `{"transfers":[]}`
	}}
	batch, err := transferReaderAgainst(t, f).ReadTransfers(context.Background(), me, "eth", 3, 0)
	require.ErrorContains(t, err, "boom")
	assert.Equal(t, uint64(3), batch.Next)
}

// TestReadTransfers_UnknownChain names the chain it cannot read.
func TestReadTransfers_UnknownChain(t *testing.T) {
	_, err := transferReaderAgainst(t, &rpcFake{}).ReadTransfers(context.Background(), me, "fantom", 0, 0)
	require.ErrorContains(t, err, "fantom")
}

// TestReadTransfers_CrowdedFirstBlockIsFinished: more pages than one walk
// takes, all in the block the read starts from. Stopping there would cover
// nothing, forever; the walk finishes the block and moves on.
func TestReadTransfers_CrowdedFirstBlockIsFinished(t *testing.T) {
	var page int
	f := &rpcFake{head: 1000, transfers: func(p map[string]any) string {
		if p["toAddress"] != nil {
			return `{"transfers":[]}`
		}
		page++
		block := uint64(5)
		if page > maxTransferPages+2 {
			block = 6
		}
		return fmt.Sprintf(`{"pageKey":"k%d","transfers":[
			{"blockNum":"%s","uniqueId":"0x%x:log:0x0","hash":"0x%x","from":"%s","to":"0xbb","category":"erc20","rawContract":{"value":"0x1","address":"0xtok","decimal":"0x0"},"metadata":{"blockTimestamp":"2026-01-02T00:00:00Z"}}
		]}`, page, hexQuantity(block), page, page, me)
	}}
	batch, err := transferReaderAgainst(t, f).ReadTransfers(context.Background(), me, "eth", 5, 0)
	require.NoError(t, err)
	assert.Equal(t, uint64(6), batch.Next)
	assert.Len(t, batch.Transfers, maxTransferPages+2, "every transfer of block 5")
}

// TestReadTransfers_BlockPastTheHigherBoundIsRefused: a block that holds more
// than the higher bound is an error that names it, not a silent stall.
func TestReadTransfers_BlockPastTheHigherBoundIsRefused(t *testing.T) {
	var page int
	f := &rpcFake{head: 1000, transfers: func(p map[string]any) string {
		page++
		return fmt.Sprintf(`{"pageKey":"k%d","transfers":[
			{"blockNum":"0x5","uniqueId":"0x%x:log:0x0","hash":"0x%x","from":"%s","to":"0xbb","category":"erc20","rawContract":{"value":"0x1","address":"0xtok","decimal":"0x0"},"metadata":{"blockTimestamp":"2026-01-02T00:00:00Z"}}
		]}`, page, page, page, me)
	}}
	batch, err := transferReaderAgainst(t, f).ReadTransfers(context.Background(), me, "eth", 5, 0)
	require.ErrorContains(t, err, "block 5 on eth holds more than")
	assert.Equal(t, uint64(5), batch.Next)
	assert.Equal(t, maxTransferPages+maxOneBlockPages, page)
}

// TestReadTransfers_EndlessEmptyPagesAreRefused: pages that carry nothing and
// never end are an error that says so, not a claim about a crowded block.
func TestReadTransfers_EndlessEmptyPagesAreRefused(t *testing.T) {
	var page int
	f := &rpcFake{head: 1000, transfers: func(map[string]any) string {
		page++
		return fmt.Sprintf(`{"pageKey":"k%d","transfers":[]}`, page)
	}}
	_, err := transferReaderAgainst(t, f).ReadTransfers(context.Background(), me, "eth", 5, 0)
	require.ErrorContains(t, err, "carried no transfer and no end")
}

// TestRPCCall_NoParamsIsAnEmptyList: a strict endpoint refuses null params.
func TestRPCCall_NoParamsIsAnEmptyList(t *testing.T) {
	encoded, err := json.Marshal(rpcCall("eth_blockNumber"))
	require.NoError(t, err)
	assert.Contains(t, string(encoded), `"params":[]`)
}

// TestReadTransfers_MissingTimeIsReadFromTheBlock: a network that serves
// times can still return a row without one; it is read from the block rather
// than lost.
func TestReadTransfers_MissingTimeIsReadFromTheBlock(t *testing.T) {
	f := &rpcFake{head: 100, transfers: func(p map[string]any) string {
		if p["toAddress"] != nil {
			return `{"transfers":[]}`
		}
		return `{"transfers":[
			{"blockNum":"0x7","uniqueId":"0x01:external","hash":"0x01","from":"` + me + `","to":"0xbb","category":"external","rawContract":{"value":"0x1"},"metadata":null}
		]}`
	}}
	batch, err := transferReaderAgainst(t, f).ReadTransfers(context.Background(), me, "eth", 0, 0)
	require.NoError(t, err)
	require.Len(t, batch.Transfers, 1)
	assert.Equal(t, time.Date(2026, 1, 1, 0, 0, 7, 0, time.UTC), batch.Transfers[0].At)
	assert.Equal(t, 1, f.blockReqs)
}

// TestReadTransfers_NoResultIsNotAnEmptyPage: a response with a null result
// read as an empty page would cover the whole range with nothing recorded.
func TestReadTransfers_NoResultIsNotAnEmptyPage(t *testing.T) {
	f := &rpcFake{head: 100, transfers: func(map[string]any) string { return `null` }}
	batch, err := transferReaderAgainst(t, f).ReadTransfers(context.Background(), me, "eth", 3, 0)
	require.ErrorContains(t, err, "answered no result")
	assert.Equal(t, uint64(3), batch.Next)
}

// blockBatchFake answers eth_getBlockByNumber batches, refusing an item as the
// node does over its throughput while refuse says so.
type blockBatchFake struct {
	mu     sync.Mutex
	sizes  []int
	refuse func(call int, block uint64) bool
	calls  int
}

func (f *blockBatchFake) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	var batch []struct {
		ID     int   `json:"id"`
		Params []any `json:"params"`
	}
	_ = json.Unmarshal(body, &batch)
	f.mu.Lock()
	f.calls++
	call := f.calls
	f.sizes = append(f.sizes, len(batch))
	f.mu.Unlock()
	out := "["
	for i, b := range batch {
		if i > 0 {
			out += ","
		}
		n, _ := hexToUint(b.Params[0].(string))
		if f.refuse != nil && f.refuse(call, n) {
			out += fmt.Sprintf(`{"jsonrpc":"2.0","id":%d,"error":{"code":429,"message":"Your app has exceeded its compute units per second capacity"}}`, b.ID)
			continue
		}
		out += fmt.Sprintf(`{"jsonrpc":"2.0","id":%d,"result":{"timestamp":"%s"}}`, b.ID, hexQuantity(1_700_000_000+n))
	}
	writeJSON(w, out+"]")
}

func blockClient(t *testing.T, f *blockBatchFake) *Client {
	t.Helper()
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	client := NewClient(Config{APIKey: "test-key"})
	client.rpcURL = func(string) string { return srv.URL }
	orig := blockRetryPause
	blockRetryPause = 0
	t.Cleanup(func() { blockRetryPause = orig })
	return client
}

// TestBlockTimes_BatchesStayUnderThroughput: one batch of block reads is
// metered item by item, so it is kept small.
func TestBlockTimes_BatchesStayUnderThroughput(t *testing.T) {
	f := &blockBatchFake{}
	blocks := make([]uint64, 45)
	for i := range blocks {
		blocks[i] = uint64(i + 1)
	}
	times, err := blockClient(t, f).BlockTimes(context.Background(), "bnb-mainnet", blocks)
	require.NoError(t, err)
	assert.Equal(t, []int{20, 20, 5}, f.sizes)
	assert.Len(t, times, 45)
}

// TestBlockTimes_RefusedItemIsAskedAgain: an item the node refused for
// throughput is asked again, alone, and the read succeeds.
func TestBlockTimes_RefusedItemIsAskedAgain(t *testing.T) {
	f := &blockBatchFake{refuse: func(call int, block uint64) bool { return call == 1 && block == 3 }}
	times, err := blockClient(t, f).BlockTimes(context.Background(), "bnb-mainnet", []uint64{1, 2, 3, 4})
	require.NoError(t, err)
	assert.Equal(t, []int{4, 1}, f.sizes, "only the refused block is asked again")
	assert.Equal(t, time.Unix(1_700_000_003, 0).UTC(), times[3])
}

// TestBlockTimes_RefusalPastTheAttemptsNamesTheNode: a block still refused
// after the attempts fails the read with what the node said.
func TestBlockTimes_RefusalPastTheAttemptsNamesTheNode(t *testing.T) {
	f := &blockBatchFake{refuse: func(_ int, block uint64) bool { return block == 2 }}
	_, err := blockClient(t, f).BlockTimes(context.Background(), "bnb-mainnet", []uint64{1, 2})
	require.ErrorContains(t, err, "block 2 on bnb-mainnet has no answer: Your app has exceeded its compute units per second capacity")
	assert.Equal(t, blockBatchAttempts, f.calls)
}

// TestThroughputRefusalIsRetried: a 429 is Alchemy saying this second is
// full; the call goes again, and only a refusal past the attempts fails it.
func TestThroughputRefusalIsRetried(t *testing.T) {
	for _, tc := range []struct {
		refusals, wantCalls int
		wantErr             bool
	}{
		{refusals: 1, wantCalls: 2},
		{refusals: 10, wantCalls: rateLimitAttempts, wantErr: true},
	} {
		var calls int
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			calls++
			if calls <= tc.refusals {
				w.WriteHeader(http.StatusTooManyRequests)
				return
			}
			writeJSON(w, `{"jsonrpc":"2.0","id":1,"result":"0x10"}`)
		}))
		client := NewClient(Config{APIKey: "test-key"})
		client.rpcURL = func(string) string { return srv.URL }
		head, err := client.BlockNumber(context.Background(), "eth-mainnet")
		srv.Close()
		assert.Equal(t, tc.wantCalls, calls)
		if tc.wantErr {
			require.ErrorContains(t, err, "API status 429")
			continue
		}
		require.NoError(t, err)
		assert.Equal(t, uint64(16), head)
	}
}
