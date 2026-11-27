package portfolio

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"math"
	"slices"
	"strings"
	"time"

	"connectrpc.com/connect"
	"github.com/shopspring/decimal"

	"github.com/foxcool/greedy-eye/internal/entity"
	"github.com/foxcool/greedy-eye/internal/store"
)

// defaultConfirmations is how far behind a chain's head the ledger reads, so
// a reorg does not leave an event for a transaction that no longer exists.
// Polygon has reorged deeper than the rest.
const defaultConfirmations = 64

var chainConfirmations = map[string]uint64{"polygon": 128}

func (h *Handler) confirmationsFor(chain string) uint64 {
	if n, ok := h.confirmations[chain]; ok {
		return n
	}
	if n, ok := chainConfirmations[chain]; ok {
		return n
	}
	return defaultConfirmations
}

// ledgerOutcome is what one run of a ledger reader did: events written, and
// what it could not read, one line per failure.
type ledgerOutcome struct {
	eventsCreated int32
	errors        []string
}

// readTransfers continues the account's transfer history into the ledger on
// every chain that has a cursor. With backfill, or once the account has any
// cursor, chains holding a position and having no cursor get one at block 0:
// the full history is read on request, and from then on after each sync.
//
// A chain whose read fails, or whose source confirmed nothing past the cursor,
// keeps its cursor and is reported. Events already written stay: their refs
// make the retry write nothing twice.
func (h *Handler) readTransfers(ctx context.Context, account *entity.Account, backfill bool) ledgerOutcome {
	var out ledgerOutcome
	if h.transferSource == nil {
		if backfill {
			out.errors = append(out.errors, "transfer history is not configured on this instance")
		}
		return out
	}
	addresses := walletAddresses(account)
	if len(addresses) == 0 {
		return out
	}

	cursors, err := h.store.ListLedgerCursors(ctx, account.ID)
	if err != nil {
		return ledgerOutcome{errors: []string{fmt.Sprintf("transfer history: %v", err)}}
	}
	byChain := make(map[string]*entity.LedgerCursor, len(cursors))
	for _, c := range cursors {
		byChain[c.Chain] = c
	}
	// An account with any cursor has asked for its history, so a chain it
	// holds without one (new since, or one whose first read found no source)
	// is opened on every sync, not only on the request.
	if backfill || len(cursors) > 0 {
		holdings, err := h.allHoldings(ctx, ListHoldingsOpts{AccountID: account.ID})
		if err != nil {
			return ledgerOutcome{errors: []string{fmt.Sprintf("transfer history: %v", err)}}
		}
		for _, hld := range holdings {
			if hld.Chain != "" && byChain[hld.Chain] == nil {
				byChain[hld.Chain] = &entity.LedgerCursor{AccountID: account.ID, Chain: hld.Chain}
			}
		}
	}

	for _, chain := range slices.Sorted(maps.Keys(byChain)) {
		cursor := byChain[chain]
		created, err := h.readChainTransfers(ctx, account, addresses, cursor)
		out.eventsCreated += created
		cursor.CheckedAt = time.Now()
		if err != nil {
			cursor.LastError = err.Error()
			out.errors = append(out.errors, fmt.Sprintf("chain %s: %s", chain, err.Error()))
			h.log.Warn("transfer history not reconciled",
				"account_id", account.ID, "chain", chain, "next_block", cursor.NextBlock, "error", err)
		} else {
			cursor.LastError = ""
		}
		// A cursor is saved even when nothing moved: checked_at and the
		// failure are what says how old this chain's history is.
		if cursor.Source == "" {
			continue // no source ever answered; nothing to continue from
		}
		if serr := h.store.SaveLedgerCursor(ctx, cursor); serr != nil {
			out.errors = append(out.errors, fmt.Sprintf("chain %s: cursor not saved: %v", chain, serr))
		}
	}
	return out
}

// readChainTransfers reads one chain from the cursor, writes its events and
// moves the cursor to where every address's read was complete.
func (h *Handler) readChainTransfers(ctx context.Context, account *entity.Account, addresses []string, cursor *entity.LedgerCursor) (int32, error) {
	reader, slug, err := h.transferSource.TransferReaderFor(ctx, account.UserID, cursor.Chain)
	if err != nil {
		return 0, err
	}
	if reader == nil {
		return 0, errors.New("no transfer history source reads this chain")
	}
	source := entity.LedgerSyncSource(slug)
	if cursor.Source != "" && cursor.Source != source {
		// Its refs would not deduplicate against the history already read.
		return 0, fmt.Errorf("history here was read from %s, %s would record it twice", cursor.Source, source)
	}
	cursor.Source = source

	// An address the cursor does not speak for has had nothing read before
	// it. The chain is read again from the start for all of them; what was
	// written already is refused by its ref.
	own := normalizedAddresses(addresses)
	from := cursor.NextBlock
	var gained []string
	if from > 0 && !isSubset(own, cursor.Addresses) {
		gained = difference(own, cursor.Addresses)
		h.log.Info("account gained an address; re-reading its transfer history from the start",
			"account_id", account.ID, "chain", cursor.Chain, "had", strings.Join(cursor.Addresses, ","), "has", strings.Join(own, ","))
		from = 0
	}

	next := uint64(math.MaxUint64)
	var transfers []entity.AssetTransfer
	for _, addr := range own {
		batch, err := reader.ReadTransfers(ctx, addr, cursor.Chain, from, h.confirmationsFor(cursor.Chain))
		if err != nil {
			return 0, err
		}
		next = min(next, batch.Next)
		cursor.Incomplete = batch.Incomplete
		transfers = append(transfers, batch.Transfers...)
	}
	if next <= from {
		return 0, fmt.Errorf("not reconciled: the source confirmed nothing past block %d", from)
	}

	created, w, err := h.writeTransferEvents(ctx, account.ID, own, gained, cursor.Chain, source, transfers, next)
	if err != nil {
		return created, err
	}
	cursor.NextBlock, cursor.Addresses, cursor.AdvancedAt = next, own, time.Now()
	// The cursor moves anyway: no retry reads these any better.
	var problems []error
	if w.skips.count > 0 {
		problems = append(problems, fmt.Errorf("%d movement(s) not recorded: %s", w.skips.count, strings.Join(w.skips.named, "; ")))
	}
	if w.stale.count > 0 {
		// Rewriting a recorded event is a reclassification and leaves a trace
		// elsewhere; here it is only named.
		problems = append(problems, fmt.Errorf("%d event(s) recorded before %s joined the account lack its legs and were not rewritten: %s",
			w.stale.count, strings.Join(gained, ","), strings.Join(w.stale.named, "; ")))
	}
	return created, errors.Join(problems...)
}

// difference is what of a is not in b.
func difference(a, b []string) []string {
	var out []string
	for _, x := range a {
		if !slices.Contains(b, x) {
			out = append(out, x)
		}
	}
	return out
}

// normalizedAddresses is the set of addresses lower case, sorted, once each.
func normalizedAddresses(addresses []string) []string {
	out := make([]string, 0, len(addresses))
	for _, a := range addresses {
		out = append(out, strings.ToLower(strings.TrimSpace(a)))
	}
	slices.Sort(out)
	return slices.Compact(out)
}

func isSubset(sub, of []string) bool {
	for _, a := range sub {
		if !slices.Contains(of, a) {
			return false
		}
	}
	return true
}

// maxSkipsNamed bounds the movements named in one error line.
const maxSkipsNamed = 5

// legSkips counts the movements no leg was written for and names the first few.
type legSkips struct {
	count int
	named []string
}

func (s *legSkips) add(t entity.AssetTransfer, why string) {
	s.count++
	if len(s.named) < maxSkipsNamed {
		s.named = append(s.named, fmt.Sprintf("%s %s: %s", t.TxHash, t.ID, why))
	}
}

// written is what writeTransferEvents could not record as read: movements
// with no leg, and events already recorded with fewer legs than they have now.
type written struct {
	skips legSkips
	stale legSkips
}

// writeTransferEvents groups the transfers before `next` by transaction and
// writes one event per transaction. A failure a retry could get past is an
// error. An event that exists already is left as it is; when it touches an
// address in gained it was recorded without that address's legs, and is named.
func (h *Handler) writeTransferEvents(ctx context.Context, accountID string, own, gained []string, chain, source string,
	transfers []entity.AssetTransfer, next uint64) (int32, written, error) {
	byTx := map[string][]entity.AssetTransfer{}
	var order []string
	seen := map[string]bool{}
	for _, t := range transfers {
		// Several addresses read the same transfer between them once each.
		if t.Block >= next || seen[t.TxHash+"|"+t.ID] {
			continue
		}
		seen[t.TxHash+"|"+t.ID] = true
		if _, ok := byTx[t.TxHash]; !ok {
			order = append(order, t.TxHash)
		}
		byTx[t.TxHash] = append(byTx[t.TxHash], t)
	}

	b := eventBuilder{h: h, accountID: accountID, chain: chain, source: source, assets: map[string]string{}, refused: map[string]string{}, own: map[string]bool{}}
	for _, a := range own {
		b.own[a] = true
	}
	var (
		created int32
		stale   legSkips
	)
	for _, tx := range order {
		event, err := b.build(ctx, tx, byTx[tx])
		if err != nil {
			return created, written{skips: b.skips, stale: stale}, err
		}
		if event == nil {
			continue
		}
		if _, err := h.store.CreateLedgerEvent(ctx, event); err != nil {
			if errors.Is(err, store.ErrAlreadyExists) {
				if touchesAny(byTx[tx], gained) {
					stale.add(byTx[tx][0], "recorded before the address was the account's")
				}
				continue
			}
			return created, written{skips: b.skips, stale: stale}, fmt.Errorf("write event %s: %w", tx, err)
		}
		created++
	}
	return created, written{skips: b.skips, stale: stale}, nil
}

// touchesAny reports whether any of the moves is from or to one of addresses.
func touchesAny(moves []entity.AssetTransfer, addresses []string) bool {
	for _, t := range moves {
		if slices.Contains(addresses, t.From) || slices.Contains(addresses, t.To) {
			return true
		}
	}
	return false
}

// eventBuilder turns one transaction's transfers into a ledger event, keeping
// the assets it resolved for the rest of the run.
type eventBuilder struct {
	h                        *Handler
	accountID, chain, source string
	own                      map[string]bool
	assets                   map[string]string // contract ("" for the coin) → asset id
	refused                  map[string]string // contract → why the catalogue refused it
	skips                    legSkips
}

// build returns nil when no movement of the transaction could be recorded.
func (b *eventBuilder) build(ctx context.Context, tx string, moves []entity.AssetTransfer) (*entity.LedgerEvent, error) {
	event := &entity.LedgerEvent{
		AccountID:   b.accountID,
		ExecutedAt:  moves[0].At,
		ExternalRef: b.chain + ":" + tx,
		Source:      b.source,
	}
	raws := make([]json.RawMessage, 0, len(moves))
	var ins, outs int
	for _, t := range moves {
		raws = append(raws, t.Raw)
		amount, err := decimal.NewFromString(t.Amount)
		if t.Decimals == nil || err != nil || !amount.IsPositive() {
			b.skips.add(t, "amount or scale unknown")
			continue
		}
		assetID, ok, err := b.asset(ctx, t)
		if err != nil {
			return nil, err
		}
		if !ok {
			continue
		}
		for _, dir := range directions(t, b.own) {
			event.Legs = append(event.Legs, entity.LedgerLeg{
				AssetID:   assetID,
				Direction: dir,
				Amount:    amount,
				Decimals:  uint32(*t.Decimals), // #nosec G115 -- the reader caps decimals at 255
				Chain:     b.chain,
			})
			if dir == entity.LedgerIn {
				ins++
			} else {
				outs++
			}
		}
	}
	if len(event.Legs) == 0 {
		return nil, nil
	}
	if event.ExecutedAt.IsZero() {
		b.skips.add(moves[0], "no block time")
		return nil, nil
	}
	classify(event, ins, outs)
	raw, err := json.Marshal(map[string]any{"chain": b.chain, "block": moves[0].Block, "transfers": raws})
	if err != nil {
		return nil, fmt.Errorf("encode raw of %s: %w", tx, err)
	}
	event.Raw = raw
	return event, nil
}

// asset resolves the transfer's asset once per run. The catalogue refusing
// the token is final and skips the leg; it being unreachable is an error, and
// must not cost the leg.
func (b *eventBuilder) asset(ctx context.Context, t entity.AssetTransfer) (string, bool, error) {
	if id, ok := b.assets[t.ContractAddress]; ok {
		return id, true, nil
	}
	// A refusal is final for the run: a spam token moved a thousand times is
	// asked about once.
	if why, ok := b.refused[t.ContractAddress]; ok {
		b.skips.add(t, why)
		return "", false, nil
	}
	id, err := b.h.transferAsset(ctx, t)
	if err != nil {
		if code := connect.CodeOf(err); code != connect.CodeInvalidArgument && code != connect.CodeFailedPrecondition {
			return "", false, fmt.Errorf("resolve asset of %s: %w", t.TxHash, err)
		}
		b.refused[t.ContractAddress] = err.Error()
		b.skips.add(t, err.Error())
		return "", false, nil
	}
	b.assets[t.ContractAddress] = id
	return id, true, nil
}

// directions is how a transfer moved the account: in, out, or both for one
// between two of its own addresses (or to itself).
func directions(t entity.AssetTransfer, own map[string]bool) []entity.LedgerDirection {
	var dirs []entity.LedgerDirection
	if own[t.From] {
		dirs = append(dirs, entity.LedgerOut)
	}
	if own[t.To] {
		dirs = append(dirs, entity.LedgerIn)
	}
	return dirs
}

// classify sets the kind and the leg roles. A transaction that moved assets
// both ways is a trade: what came in is the principal, what went out paid
// for it. Reclassifying swaps more finely is a later change.
func classify(e *entity.LedgerEvent, ins, outs int) {
	switch {
	case ins > 0 && outs > 0:
		e.Kind = entity.LedgerKindTrade
		for i := range e.Legs {
			e.Legs[i].Role = entity.LedgerRolePrincipal
			if e.Legs[i].Direction == entity.LedgerOut {
				e.Legs[i].Role = entity.LedgerRoleCounter
			}
		}
		return
	case ins > 0:
		e.Kind = entity.LedgerKindTransferIn
	default:
		e.Kind = entity.LedgerKindTransferOut
	}
	for i := range e.Legs {
		e.Legs[i].Role = entity.LedgerRolePrincipal
	}
}

// transferAsset finds the asset a movement is of, the way a sync finds the
// asset of a balance: by contract on its chain, or the chain's coin.
func (h *Handler) transferAsset(ctx context.Context, t entity.AssetTransfer) (string, error) {
	b := syncedBalance{
		symbol:    t.Symbol,
		assetType: entity.AssetTypeCryptocurrency,
		decimals:  *t.Decimals,
		chain:     t.Chain,
	}
	if t.ContractAddress != "" {
		b.refSource, b.ref = entity.OnchainSource(t.Chain), t.ContractAddress
		if strings.TrimSpace(b.symbol) == "" {
			b.symbol, _ = contractDisplaySymbol(b.refSource, b.ref)
		}
	}
	if h.mdClient == nil {
		return "", errors.New("asset catalogue not configured")
	}
	id, _, _, err := h.resolveSyncedAsset(ctx, b)
	return id, err
}
