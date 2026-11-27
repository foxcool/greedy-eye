package portfolio

import (
	"context"
	"errors"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	apiv1 "github.com/foxcool/greedy-eye/api/v1"
	"github.com/foxcool/greedy-eye/internal/entity"
	"github.com/foxcool/greedy-eye/internal/store"
)

// ledgerStore keeps cursors and events in memory, refusing an event whose ref
// it already has the way the unique index does.
type ledgerStore struct {
	*mockStore
	holdings []*entity.Holding
	cursors  map[string]*entity.LedgerCursor
	events   map[string]*entity.LedgerEvent
	writeErr error
}

func newLedgerStore(holdings ...*entity.Holding) *ledgerStore {
	return &ledgerStore{mockStore: &mockStore{}, holdings: holdings,
		cursors: map[string]*entity.LedgerCursor{}, events: map[string]*entity.LedgerEvent{}}
}

func (s *ledgerStore) ListHoldings(context.Context, ListHoldingsOpts) ([]*entity.Holding, string, error) {
	return s.holdings, "", nil
}

func (s *ledgerStore) ListLedgerCursors(context.Context, string) ([]*entity.LedgerCursor, error) {
	var out []*entity.LedgerCursor
	for _, c := range s.cursors {
		copied := *c
		out = append(out, &copied)
	}
	return out, nil
}

func (s *ledgerStore) SaveLedgerCursor(_ context.Context, c *entity.LedgerCursor) error {
	copied := *c
	s.cursors[c.Chain] = &copied
	return nil
}

func (s *ledgerStore) CreateLedgerEvent(_ context.Context, e *entity.LedgerEvent) (*entity.LedgerEvent, error) {
	if s.writeErr != nil {
		return nil, s.writeErr
	}
	if err := e.Validate(); err != nil {
		return nil, err
	}
	key := e.Source + "|" + e.ExternalRef
	if _, ok := s.events[key]; ok {
		return nil, store.ErrAlreadyExists
	}
	s.events[key] = e
	return e, nil
}

// fakeReader answers ReadTransfers with whatever the test puts in read.
type fakeReader struct {
	read  func(address string, from uint64) (entity.TransferBatch, error)
	froms []uint64
}

func (r *fakeReader) ReadTransfers(_ context.Context, address, _ string, from, _ uint64) (entity.TransferBatch, error) {
	r.froms = append(r.froms, from)
	return r.read(address, from)
}

type fakeTransferSource struct {
	reader entity.TransferReader
	slug   string
}

func (s fakeTransferSource) TransferReaderFor(context.Context, string, string) (entity.TransferReader, string, error) {
	return s.reader, s.slug, nil
}

const (
	walletA  = "0xaaaa000000000000000000000000000000000001"
	walletB  = "0xbbbb000000000000000000000000000000000002"
	stranger = "0xcccc000000000000000000000000000000000003"
)

func move(tx, id string, block uint64, from, to, contract, symbol, amount string) entity.AssetTransfer {
	return entity.AssetTransfer{
		Chain: "eth", TxHash: tx, ID: id, Block: block, At: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC).Add(time.Duration(block) * time.Second),
		From: from, To: to, ContractAddress: contract, Symbol: symbol, Decimals: new(18), Amount: amount,
		Raw: []byte(`{}`),
	}
}

func ledgerHandler(s Store, r entity.TransferReader) *Handler {
	return newHandler(s).
		WithMarketDataClient(&mockMDClient{autoAsset: true}).
		WithTransferReaderSource(fakeTransferSource{reader: r, slug: "alchemy"})
}

func walletAccount(addresses string) *entity.Account {
	return &entity.Account{ID: "acct-1", UserID: "user-1", Type: entity.AccountTypeWallet,
		Data: map[string]string{"addresses": addresses}}
}

// TestReadTransfers_BackfillWritesOneEventPerTransaction: a backfill opens a
// cursor on the chain the wallet holds a position on and reads it from block
// 0. Each transaction is one event; one that moved assets both ways is a trade
// whose incoming leg is the principal. The cursor lands where the reader said
// its answer ends.
func TestReadTransfers_BackfillWritesOneEventPerTransaction(t *testing.T) {
	s := newLedgerStore(&entity.Holding{Chain: "eth"})
	r := &fakeReader{read: func(string, uint64) (entity.TransferBatch, error) {
		return entity.TransferBatch{Next: 101, Transfers: []entity.AssetTransfer{
			move("0x01", "0x01:external", 10, walletA, stranger, "", "ETH", "5"),
			move("0x02", "0x02:log:0x0", 20, stranger, walletA, "0xusdc", "USDC", "7"),
			move("0x03", "0x03:log:0x1", 30, walletA, stranger, "0xusdc", "USDC", "7"),
			move("0x03", "0x03:log:0x2", 30, stranger, walletA, "0xweth", "WETH", "3"),
		}}, nil
	}}
	out := ledgerHandler(s, r).readTransfers(context.Background(), walletAccount(walletA), true)

	require.Empty(t, out.errors)
	assert.Equal(t, int32(3), out.eventsCreated)
	assert.Equal(t, []uint64{0}, r.froms, "a backfill starts at the first block")

	send := s.events["sync:alchemy|eth:0x01"]
	require.NotNil(t, send)
	assert.Equal(t, entity.LedgerKindTransferOut, send.Kind)
	require.Len(t, send.Legs, 1)
	assert.Equal(t, entity.LedgerOut, send.Legs[0].Direction)
	assert.Equal(t, "asset-ETH", send.Legs[0].AssetID)
	assert.Equal(t, "eth", send.Legs[0].Chain)

	assert.Equal(t, entity.LedgerKindTransferIn, s.events["sync:alchemy|eth:0x02"].Kind)

	swap := s.events["sync:alchemy|eth:0x03"]
	require.NotNil(t, swap)
	assert.Equal(t, entity.LedgerKindTrade, swap.Kind)
	require.Len(t, swap.Legs, 2)
	for _, l := range swap.Legs {
		if l.Direction == entity.LedgerIn {
			assert.Equal(t, entity.LedgerRolePrincipal, l.Role)
			assert.Equal(t, "asset-WETH", l.AssetID)
		} else {
			assert.Equal(t, entity.LedgerRoleCounter, l.Role)
		}
	}

	c := s.cursors["eth"]
	require.NotNil(t, c)
	assert.Equal(t, uint64(101), c.NextBlock)
	assert.Equal(t, "sync:alchemy", c.Source)
	assert.Empty(t, c.LastError)
}

// TestReadTransfers_SecondRunWritesNothingTwice: a run that reads the same
// range again (the cursor was not saved, or a retry) creates no event.
func TestReadTransfers_SecondRunWritesNothingTwice(t *testing.T) {
	s := newLedgerStore(&entity.Holding{Chain: "eth"})
	r := &fakeReader{read: func(string, uint64) (entity.TransferBatch, error) {
		return entity.TransferBatch{Next: 50, Transfers: []entity.AssetTransfer{
			move("0x01", "0x01:external", 10, walletA, stranger, "", "ETH", "5"),
		}}, nil
	}}
	h := ledgerHandler(s, r)
	first := h.readTransfers(context.Background(), walletAccount(walletA), true)
	require.Equal(t, int32(1), first.eventsCreated)

	s.cursors["eth"].NextBlock = 0 // as if the cursor had not been saved
	second := h.readTransfers(context.Background(), walletAccount(walletA), false)
	assert.Equal(t, int32(0), second.eventsCreated)
	assert.Empty(t, second.errors)
	assert.Len(t, s.events, 1)
}

// TestReadTransfers_EmptyAnswerBelowRequestedEndHoldsTheCursor: the source's
// head is still below the range asked for, so it answers an empty list and
// covers nothing. That is not "nothing moved": the cursor stays and the chain
// is reported not reconciled. When the source catches up, the next run reads
// the range with no duplicates.
func TestReadTransfers_EmptyAnswerBelowRequestedEndHoldsTheCursor(t *testing.T) {
	s := newLedgerStore()
	s.cursors["eth"] = &entity.LedgerCursor{AccountID: "acct-1", Chain: "eth", Source: "sync:alchemy", NextBlock: 100, Addresses: []string{walletA}}
	caughtUp := false
	r := &fakeReader{read: func(_ string, from uint64) (entity.TransferBatch, error) {
		if !caughtUp {
			return entity.TransferBatch{Next: from}, nil
		}
		return entity.TransferBatch{Next: 200, Transfers: []entity.AssetTransfer{
			move("0x09", "0x09:external", 150, stranger, walletA, "", "ETH", "1"),
		}}, nil
	}}
	h := ledgerHandler(s, r)

	lagging := h.readTransfers(context.Background(), walletAccount(walletA), false)
	require.Len(t, lagging.errors, 1)
	assert.Contains(t, lagging.errors[0], "not reconciled")
	assert.Equal(t, uint64(100), s.cursors["eth"].NextBlock)
	assert.Contains(t, s.cursors["eth"].LastError, "not reconciled")
	assert.Empty(t, s.events)

	caughtUp = true
	recovered := h.readTransfers(context.Background(), walletAccount(walletA), false)
	assert.Empty(t, recovered.errors)
	assert.Equal(t, int32(1), recovered.eventsCreated)
	assert.Equal(t, uint64(200), s.cursors["eth"].NextBlock)
	assert.Empty(t, s.cursors["eth"].LastError)
	assert.Equal(t, []uint64{100, 100}, r.froms)
}

// TestReadTransfers_FailedChainKeepsItsCursor: a read that fails moves
// nothing and is reported; other chains go on.
func TestReadTransfers_FailedChainKeepsItsCursor(t *testing.T) {
	s := newLedgerStore()
	s.cursors["eth"] = &entity.LedgerCursor{AccountID: "acct-1", Chain: "eth", Source: "sync:alchemy", NextBlock: 7, Addresses: []string{walletA}}
	r := &fakeReader{read: func(string, uint64) (entity.TransferBatch, error) {
		return entity.TransferBatch{Next: 7}, errors.New("quota exhausted")
	}}
	out := ledgerHandler(s, r).readTransfers(context.Background(), walletAccount(walletA), false)
	require.Len(t, out.errors, 1)
	assert.Contains(t, out.errors[0], "chain eth: quota exhausted")
	assert.Equal(t, uint64(7), s.cursors["eth"].NextBlock)
	assert.Equal(t, "quota exhausted", s.cursors["eth"].LastError)
}

// TestReadTransfers_FailedWriteKeepsTheCursor: an event that could not be
// written leaves the cursor where it was, so the next run writes it.
func TestReadTransfers_FailedWriteKeepsTheCursor(t *testing.T) {
	s := newLedgerStore()
	s.writeErr = errors.New("connection reset")
	s.cursors["eth"] = &entity.LedgerCursor{AccountID: "acct-1", Chain: "eth", Source: "sync:alchemy", NextBlock: 0, Addresses: []string{walletA}}
	r := &fakeReader{read: func(string, uint64) (entity.TransferBatch, error) {
		return entity.TransferBatch{Next: 50, Transfers: []entity.AssetTransfer{
			move("0x01", "0x01:external", 10, walletA, stranger, "", "ETH", "5"),
		}}, nil
	}}
	out := ledgerHandler(s, r).readTransfers(context.Background(), walletAccount(walletA), false)
	require.Len(t, out.errors, 1)
	assert.Equal(t, uint64(0), s.cursors["eth"].NextBlock)
}

// TestReadTransfers_SeveralAddresses: the cursor moves only as far as every
// address was read, a transfer past that end waits for the next run, and a
// transfer between two of the account's addresses is one event with a leg
// each way.
func TestReadTransfers_SeveralAddresses(t *testing.T) {
	s := newLedgerStore(&entity.Holding{Chain: "eth"})
	between := move("0x05", "0x05:log:0x0", 20, walletA, walletB, "0xusdc", "USDC", "9")
	r := &fakeReader{read: func(address string, _ uint64) (entity.TransferBatch, error) {
		if address == walletA {
			return entity.TransferBatch{Next: 100, Transfers: []entity.AssetTransfer{between,
				move("0x08", "0x08:external", 60, walletA, stranger, "", "ETH", "1"),
				move("0x06", "0x06:external", 90, walletA, stranger, "", "ETH", "1")}}, nil
		}
		return entity.TransferBatch{Next: 60, Transfers: []entity.AssetTransfer{between}}, nil
	}}
	out := ledgerHandler(s, r).readTransfers(context.Background(), walletAccount(walletA+","+walletB), true)

	require.Empty(t, out.errors)
	assert.Equal(t, int32(1), out.eventsCreated, "blocks 60 and 90 are not before what walletB confirmed")
	assert.Equal(t, uint64(60), s.cursors["eth"].NextBlock)
	e := s.events["sync:alchemy|eth:0x05"]
	require.NotNil(t, e)
	require.Len(t, e.Legs, 2)
	assert.Equal(t, entity.LedgerKindTrade, e.Kind)
}

// TestReadTransfers_UnscaledMovementIsNamedNotGuessed: a token whose scale the
// source does not know gets no leg (a guessed scale is a wrong number), the
// rest of the transaction is written, and the run says what it left out.
func TestReadTransfers_UnscaledMovementIsNamedNotGuessed(t *testing.T) {
	s := newLedgerStore(&entity.Holding{Chain: "eth"})
	spam := move("0x07", "0x07:log:0x3", 10, stranger, walletA, "0xspam", "", "5")
	spam.Decimals = nil
	r := &fakeReader{read: func(string, uint64) (entity.TransferBatch, error) {
		return entity.TransferBatch{Next: 50, Transfers: []entity.AssetTransfer{
			spam, move("0x07", "0x07:external", 10, walletA, stranger, "", "ETH", "1"),
		}}, nil
	}}
	out := ledgerHandler(s, r).readTransfers(context.Background(), walletAccount(walletA), true)

	require.Len(t, out.errors, 1)
	assert.Contains(t, out.errors[0], "1 movement(s) not recorded")
	assert.Contains(t, out.errors[0], "0x07:log:0x3")
	e := s.events["sync:alchemy|eth:0x07"]
	require.NotNil(t, e)
	assert.Len(t, e.Legs, 1)
	assert.Equal(t, uint64(50), s.cursors["eth"].NextBlock, "no retry reads it better")
}

// unreachableMD fails every asset lookup as a transport would.
type unreachableMD struct{ mockMDClient }

func (m *unreachableMD) FindOrCreateAsset(context.Context, *connect.Request[apiv1.FindOrCreateAssetRequest]) (*connect.Response[apiv1.FindOrCreateAssetResponse], error) {
	return nil, connect.NewError(connect.CodeUnavailable, errors.New("marketdata down"))
}

// TestReadTransfers_UnreachableCatalogueKeepsTheCursor: an asset that could
// not be looked up because the catalogue was down is not a movement to drop.
func TestReadTransfers_UnreachableCatalogueKeepsTheCursor(t *testing.T) {
	s := newLedgerStore(&entity.Holding{Chain: "eth"})
	r := &fakeReader{read: func(string, uint64) (entity.TransferBatch, error) {
		return entity.TransferBatch{Next: 50, Transfers: []entity.AssetTransfer{
			move("0x01", "0x01:external", 10, walletA, stranger, "", "ETH", "5"),
		}}, nil
	}}
	h := ledgerHandler(s, r).WithMarketDataClient(&unreachableMD{})
	out := h.readTransfers(context.Background(), walletAccount(walletA), true)

	require.Len(t, out.errors, 1)
	assert.Contains(t, out.errors[0], "marketdata down")
	assert.Empty(t, s.events)
	assert.Equal(t, uint64(0), s.cursors["eth"].NextBlock)
}

// TestReadTransfers_AnotherSourceMayNotContinue: history read from one
// provider is not continued by another, whose refs would not deduplicate.
func TestReadTransfers_AnotherSourceMayNotContinue(t *testing.T) {
	s := newLedgerStore()
	s.cursors["eth"] = &entity.LedgerCursor{AccountID: "acct-1", Chain: "eth", Source: "sync:moralis", NextBlock: 5}
	r := &fakeReader{read: func(string, uint64) (entity.TransferBatch, error) {
		t.Fatal("must not read")
		return entity.TransferBatch{}, nil
	}}
	out := ledgerHandler(s, r).readTransfers(context.Background(), walletAccount(walletA), false)
	require.Len(t, out.errors, 1)
	assert.Contains(t, out.errors[0], "sync:moralis")
	assert.Equal(t, "sync:moralis", s.cursors["eth"].Source)
}

// TestReadTransfers_NoCursorNoBackfillReadsNothing: the history is read only
// once someone asked for it.
func TestReadTransfers_NoCursorNoBackfillReadsNothing(t *testing.T) {
	s := newLedgerStore(&entity.Holding{Chain: "eth"})
	r := &fakeReader{read: func(string, uint64) (entity.TransferBatch, error) {
		t.Fatal("must not read")
		return entity.TransferBatch{}, nil
	}}
	out := ledgerHandler(s, r).readTransfers(context.Background(), walletAccount(walletA), false)
	assert.Empty(t, out.errors)
	assert.Empty(t, s.cursors)
}

// TestSyncAccount_BackfillRefusedForNonWallet: the flag names a wallet's history.
func TestSyncAccount_BackfillRefusedForNonWallet(t *testing.T) {
	s := &mockStore{}
	acct := &entity.Account{ID: "019cf264-a506-78f8-9a9e-b658966d5201", UserID: testUserID, Type: entity.AccountTypeExchange}
	s.On("GetAccount", mock.Anything, acct.ID).Return(acct, nil)
	h := newHandler(s).WithMarketDataClient(&mockMDClient{})
	_, err := h.SyncAccount(ctxWithUser(testUserID), connect.NewRequest(&apiv1.SyncAccountRequest{AccountId: acct.ID, BackfillLedger: true}))
	require.Error(t, err)
	assert.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))
}

// TestReadTransfers_GainedAddressIsReadFromTheStart: an address added to the
// account after its history began had nothing read before the cursor. The
// chain is read again from block 0 for every address; what was written
// already is not written twice.
func TestReadTransfers_GainedAddressIsReadFromTheStart(t *testing.T) {
	s := newLedgerStore()
	s.cursors["eth"] = &entity.LedgerCursor{AccountID: "acct-1", Chain: "eth", Source: "sync:alchemy", NextBlock: 500, Addresses: []string{walletA}}
	s.events["sync:alchemy|eth:0x01"] = &entity.LedgerEvent{}
	s.events["sync:alchemy|eth:0x03"] = &entity.LedgerEvent{} // A -> B, recorded as an outflow
	toB := move("0x03", "0x03:external", 30, walletA, walletB, "", "ETH", "2")
	r := &fakeReader{read: func(address string, _ uint64) (entity.TransferBatch, error) {
		if address == walletA {
			return entity.TransferBatch{Next: 900, Transfers: []entity.AssetTransfer{
				move("0x01", "0x01:external", 10, walletA, stranger, "", "ETH", "5"), toB}}, nil
		}
		return entity.TransferBatch{Next: 900, Transfers: []entity.AssetTransfer{
			move("0x02", "0x02:external", 20, stranger, walletB, "", "ETH", "3"), toB}}, nil
	}}
	out := ledgerHandler(s, r).readTransfers(context.Background(), walletAccount(walletA+","+walletB), false)

	// The transfer A -> B recorded as an outflow is named, not rewritten;
	// the A -> stranger one recorded before is complete and is not.
	require.Len(t, out.errors, 1)
	assert.Contains(t, out.errors[0], "1 event(s) recorded before "+walletB)
	assert.Contains(t, out.errors[0], "0x03")
	assert.NotContains(t, out.errors[0], "0x01")
	assert.Equal(t, []uint64{0, 0}, r.froms, "both addresses from the first block")
	assert.Equal(t, int32(1), out.eventsCreated, "only walletB's event is new")
	assert.Equal(t, uint64(900), s.cursors["eth"].NextBlock)
	assert.Equal(t, []string{walletA, walletB}, s.cursors["eth"].Addresses)
}

// TestReadTransfers_GainedAddressReadFailsKeepsTheCursor: the re-read failing
// leaves the cursor as it was, so the next run re-reads again.
func TestReadTransfers_GainedAddressReadFailsKeepsTheCursor(t *testing.T) {
	s := newLedgerStore()
	s.cursors["eth"] = &entity.LedgerCursor{AccountID: "acct-1", Chain: "eth", Source: "sync:alchemy", NextBlock: 500, Addresses: []string{walletA}}
	r := &fakeReader{read: func(string, uint64) (entity.TransferBatch, error) {
		return entity.TransferBatch{}, errors.New("boom")
	}}
	out := ledgerHandler(s, r).readTransfers(context.Background(), walletAccount(walletA+","+walletB), false)
	require.Len(t, out.errors, 1)
	assert.Equal(t, []uint64{0}, r.froms, "the gained address is re-read from the start")
	assert.Equal(t, uint64(500), s.cursors["eth"].NextBlock)
	assert.Equal(t, []string{walletA}, s.cursors["eth"].Addresses)
}
