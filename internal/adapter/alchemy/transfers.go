package alchemy

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/foxcool/greedy-eye/internal/entity"
)

// maxTransferPages bounds one direction's walk in one read. A longer history
// is finished by the next read from where this one stopped.
const maxTransferPages = 10

// maxOneBlockPages is how many more pages a walk may take to finish the block
// it started in (see walk).
const maxOneBlockPages = 40

// Transfer categories of alchemy_getAssetTransfers this reader asks for.
const (
	categoryExternal = "external"
	categoryInternal = "internal"
	categoryERC20    = "erc20"
)

// incompleteInternal is what a network without internal transfers misses.
const incompleteInternal = "coin moved by contract calls (internal transfers) is not indexed on this network"

// ReadTransfers reads the address's transfers on chain from block `from` up to
// `confirmations` blocks behind the head.
//
// The covered end comes from this source's own head, not from the caller: a
// node behind the requested range answers an empty list, and an empty list is
// a statement only up to the block the source has. A walk cut short by the
// page bound ends before the last block it saw, because that block may
// continue on the next page.
func (a *WalletSyncerAdapter) ReadTransfers(ctx context.Context, address, chain string, from, confirmations uint64) (entity.TransferBatch, error) {
	n, ok := network[chain]
	if !ok {
		return entity.TransferBatch{}, &entity.ChainError{Chain: chain, Err: entity.ErrNoChainReader}
	}
	batch := entity.TransferBatch{Next: from}
	if !n.internalTransfers {
		batch.Incomplete = incompleteInternal
	}

	head, err := a.client.BlockNumber(ctx, n.slug)
	if err != nil {
		return batch, err
	}
	if head < confirmations || head-confirmations < from {
		return batch, nil // nothing confirmed past the cursor yet
	}
	end := head - confirmations

	categories := []string{categoryExternal, categoryERC20}
	if n.internalTransfers {
		categories = append(categories, categoryInternal)
	}

	next := end + 1
	byID := map[string]entity.AssetTransfer{}
	for _, dir := range []TransferQuery{{FromAddress: address}, {ToAddress: address}} {
		dir.FromBlock, dir.ToBlock, dir.Categories, dir.WithMetadata = from, end, categories, n.transferTimes
		got, dirNext, err := a.walk(ctx, chain, n.slug, dir)
		if err != nil {
			return entity.TransferBatch{Next: from, Incomplete: batch.Incomplete}, err
		}
		next = min(next, dirNext)
		for _, t := range got {
			byID[t.TxHash+"|"+t.ID] = t // a transfer to oneself comes back in both directions
		}
	}

	var transfers []entity.AssetTransfer
	for _, t := range byID {
		if t.Block < next {
			transfers = append(transfers, t)
		}
	}
	// Where the call serves times a row may still come without one; any row
	// missing it is read from its block rather than lost.
	if err := a.stampTimes(ctx, n.slug, transfers); err != nil {
		return entity.TransferBatch{Next: from, Incomplete: batch.Incomplete}, err
	}
	slices.SortFunc(transfers, func(x, y entity.AssetTransfer) int {
		if c := cmp.Compare(x.Block, y.Block); c != 0 {
			return c
		}
		if c := strings.Compare(x.TxHash, y.TxHash); c != 0 {
			return c
		}
		return strings.Compare(x.ID, y.ID)
	})
	batch.Transfers, batch.Next = transfers, next
	return batch, nil
}

// walk pages one direction and returns its transfers with the first block it
// did not finish.
//
// Results come oldest block first, so while the last transfer seen is still in
// the first block the walk goes on to a second, higher bound: stopping there
// would cover nothing, and a block holding more than one walk's worth of
// transfers (a spammer can make one for cents) would stop the cursor for
// good. Past the higher bound the read is an error naming the block.
func (a *WalletSyncerAdapter) walk(ctx context.Context, chain, slug string, q TransferQuery) ([]entity.AssetTransfer, uint64, error) {
	var out []entity.AssetTransfer
	for pages := 0; ; pages++ {
		if pages >= maxTransferPages && len(out) > 0 && out[len(out)-1].Block > q.FromBlock {
			// Cut short: the last block seen may go on, so it is left for the next read.
			return out, out[len(out)-1].Block, nil
		}
		if pages >= maxTransferPages+maxOneBlockPages {
			if len(out) == 0 {
				return nil, 0, fmt.Errorf("alchemy: %d pages from block %d on %s carried no transfer and no end", pages, q.FromBlock, chain)
			}
			return nil, 0, fmt.Errorf("alchemy: block %d on %s holds more than %d transfers of this address; history is not read past it",
				q.FromBlock, chain, len(out))
		}
		page, err := a.client.AssetTransfers(ctx, slug, q)
		if err != nil {
			return nil, 0, err
		}
		for i, rt := range page.Transfers {
			t, err := convertTransfer(chain, rt)
			if err != nil {
				return nil, 0, err
			}
			t.Raw = page.Raw[i]
			out = append(out, t)
		}
		if page.PageKey == "" {
			return out, q.ToBlock + 1, nil
		}
		q.PageKey = page.PageKey
	}
}

// convertTransfer reads one API row. An amount or a scale it cannot read is
// left empty rather than refused: the movement happened, and the caller
// decides what an unscaled leg is worth. A block it cannot read is an error,
// because coverage is counted in blocks.
func convertTransfer(chain string, rt RawTransfer) (entity.AssetTransfer, error) {
	block, ok := hexToUint(rt.BlockNum)
	if !ok {
		return entity.AssetTransfer{}, fmt.Errorf("alchemy: transfer %s on %s has block %q", rt.UniqueID, chain, rt.BlockNum)
	}
	if rt.Hash == "" || rt.UniqueID == "" {
		return entity.AssetTransfer{}, fmt.Errorf("alchemy: transfer in block %d on %s has no hash or id", block, chain)
	}
	t := entity.AssetTransfer{
		Chain:  chain,
		TxHash: strings.ToLower(rt.Hash),
		ID:     rt.UniqueID,
		Block:  block,
		From:   strings.ToLower(rt.From),
	}
	if rt.To != nil {
		t.To = strings.ToLower(*rt.To)
	}
	if rt.Metadata != nil && rt.Metadata.BlockTimestamp != "" {
		at, err := time.Parse(time.RFC3339, rt.Metadata.BlockTimestamp)
		if err != nil {
			return entity.AssetTransfer{}, fmt.Errorf("alchemy: transfer %s on %s has time %q", rt.UniqueID, chain, rt.Metadata.BlockTimestamp)
		}
		t.At = at.UTC()
	}
	if rt.RawContract.Value != nil {
		if amount, ok := hexToDecimal(*rt.RawContract.Value); ok {
			t.Amount = amount
		}
	}

	switch rt.Category {
	case categoryExternal, categoryInternal:
		n := network[chain]
		decimals := nativeDecimals
		t.Symbol, t.Decimals = n.symbol, &decimals
	case categoryERC20:
		if rt.RawContract.Address == nil || *rt.RawContract.Address == "" {
			return entity.AssetTransfer{}, fmt.Errorf("alchemy: token transfer %s on %s names no contract", rt.UniqueID, chain)
		}
		t.ContractAddress = strings.ToLower(*rt.RawContract.Address)
		if rt.Asset != nil {
			t.Symbol = *rt.Asset
		}
		if rt.RawContract.Decimal != nil {
			if d, ok := hexToUint(*rt.RawContract.Decimal); ok && d <= maxDecimals {
				t.Decimals = new(int(d)) // #nosec G115 -- at most maxDecimals
			}
		}
	default:
		return entity.AssetTransfer{}, fmt.Errorf("alchemy: transfer %s on %s has category %q this reader did not ask for", rt.UniqueID, chain, rt.Category)
	}
	return t, nil
}

// maxDecimals is the largest scale read as one; ERC-20 decimals is a uint8.
const maxDecimals = 255

// stampTimes fills the block time of every transfer from the blocks themselves.
func (a *WalletSyncerAdapter) stampTimes(ctx context.Context, slug string, transfers []entity.AssetTransfer) error {
	seen := map[uint64]bool{}
	var blocks []uint64
	for _, t := range transfers {
		if t.At.IsZero() && !seen[t.Block] {
			seen[t.Block] = true
			blocks = append(blocks, t.Block)
		}
	}
	if len(blocks) == 0 {
		return nil
	}
	times, err := a.client.BlockTimes(ctx, slug, blocks)
	if err != nil {
		return err
	}
	for i := range transfers {
		if transfers[i].At.IsZero() {
			transfers[i].At = times[transfers[i].Block]
		}
	}
	return nil
}
