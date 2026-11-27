package entity

import (
	"context"
	"encoding/json"
	"time"
)

// AssetTransfer is one movement of a fungible asset to or from an address, as
// the chain recorded it.
type AssetTransfer struct {
	Chain  string
	TxHash string
	// ID tells the movement apart from the others of its transaction, in the
	// source's own spelling (a log index for a token, a sequence for the coin).
	ID    string
	Block uint64
	At    time.Time
	From  string
	To    string
	// ContractAddress is empty for the chain's own coin.
	ContractAddress string
	Symbol          string
	// Decimals is nil when the source does not know the token's scale: the
	// raw amount is then exact but cannot be read as a quantity.
	Decimals *int
	// Amount is a raw integer string scaled by Decimals.
	Amount string
	// Raw is the source's record of the movement as received.
	Raw json.RawMessage
}

// TransferBatch is what a reader could say about an address on one chain from
// a starting block.
//
// Next is the reader's own statement of coverage: every transfer in blocks
// [from, Next) is in Transfers. An empty list says "nothing moved" only up to
// Next. Next equal to from says nothing was covered, which is not the same as
// nothing having happened.
type TransferBatch struct {
	Transfers []AssetTransfer
	Next      uint64
	// Incomplete names what this source cannot read on this chain at all (the
	// coin's internal transfers on some networks), empty when nothing is
	// missing. Movements it hides never appear, whatever the range.
	Incomplete string
}

// TransferReader reads the transfer history of an address on one chain.
type TransferReader interface {
	// ReadTransfers returns the transfers from block `from` up to a block at
	// least `confirmations` behind the source's head, or less when the walk
	// is cut short; Next says where the answer ends.
	ReadTransfers(ctx context.Context, address, chain string, from, confirmations uint64) (TransferBatch, error)
}

// LedgerCursor is how far an account's history on one chain has been read
// into the ledger, and by which source. A history that is not per chain (a
// broker's income) has an empty Chain and is read by time: AdvancedAt is the
// end of the last complete read.
type LedgerCursor struct {
	AccountID string
	// Chain is empty for a history that is the whole account's.
	Chain string
	// Source is the provider whose history this is. Another source's refs do
	// not deduplicate against it, so it may not continue this cursor.
	Source string
	// NextBlock is the first block not yet read.
	NextBlock uint64
	// Addresses are the account's addresses, lower case and sorted, whose
	// history this cursor speaks for.
	Addresses []string
	// Incomplete is what the source cannot read on this chain; see TransferBatch.
	Incomplete string
	// LastError is the last run's failure; empty after a run that read.
	LastError  string
	CheckedAt  time.Time
	AdvancedAt time.Time
}
