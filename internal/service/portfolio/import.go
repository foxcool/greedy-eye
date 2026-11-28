package portfolio

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"connectrpc.com/connect"
	apiv1 "github.com/foxcool/greedy-eye/api/v1"
	"github.com/foxcool/greedy-eye/internal/entity"
	"github.com/foxcool/greedy-eye/internal/store"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

// maxImportBatch caps one import request; keeps a runaway LLM batch from
// flooding the store (see the public-instance anti-abuse notes).
const maxImportBatch = 500

// defaultImportDecimals is the storage scale used when an item does not
// specify one; matches the FE holding form default.
const defaultImportDecimals = 8

// importAccount loads the target account and enforces the import contract:
// caller owns it and it is a manual account.
func (h *Handler) importAccount(ctx context.Context, accountID string) (*entity.Account, error) {
	if accountID == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("account_id is required"))
	}
	account, err := h.ownedAccount(ctx, accountID)
	if err != nil {
		return nil, err
	}
	if account.Type != entity.AccountTypeManual {
		return nil, connect.NewError(connect.CodeFailedPrecondition, errors.New("batch import requires a manual account"))
	}
	return account, nil
}

// syncWrittenIncomeKinds are the income kinds an account's own sync records.
// Importing one of them by hand as well would count one payment twice under
// two sources, so the import refuses it for that account.
//
// The broker sync owns coupons, dividends, broker interest and the taxes that
// name no income, the only operations it is meant to read. Other account types sync no income, so
// every income kind may be imported into them.
func syncWrittenIncomeKinds(account *entity.Account) []entity.IncomeKind {
	if account.Type == entity.AccountTypeBroker {
		return []entity.IncomeKind{entity.IncomeCoupon, entity.IncomeDividend, entity.IncomeInterest, entity.IncomeTax}
	}
	return nil
}

// refuseLedgerImport says why an account cannot take this event, or nil.
//
// A manual account takes every kind: a person is its only author. Any other
// account is synced, and the ledger never writes holdings, so income — which
// no sync of that account records — is the only thing a person can add to it
// without contradicting the sync. Trades and transfers there are the sync's.
func refuseLedgerImport(account *entity.Account, e *entity.LedgerEvent) error {
	if account.Type == entity.AccountTypeManual {
		return nil
	}
	if e.Kind != entity.LedgerKindIncome {
		return fmt.Errorf("a %s account takes only income by import; %s events come from its sync", account.Type, e.Kind)
	}
	if slices.Contains(syncWrittenIncomeKinds(account), e.IncomeKind) {
		return fmt.Errorf("%s income on a %s account is recorded by its sync; importing it too would count it twice", e.IncomeKind, account.Type)
	}
	return nil
}

// resolveImportID validates a caller-provided batch id or generates one.
func resolveImportID(raw *string) (string, error) {
	if raw != nil && *raw != "" {
		if _, err := uuid.Parse(*raw); err != nil {
			return "", connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("import_id must be a UUID: %w", err))
		}
		return *raw, nil
	}
	id, err := uuid.NewV7()
	if err != nil {
		return "", connect.NewError(connect.CodeInternal, fmt.Errorf("generate import_id: %w", err))
	}
	return id.String(), nil
}

// ImportPositions batch-upserts holdings on a manual account. With dry_run it
// returns the per-item plan without writing; the commit call repeats the same
// batch with dry_run=false. One holding per (account, asset): an existing
// holding gets its amount refreshed, a new one is created with
// source=llm_import and the batch import_id.
//
// A commit writes the batch's holdings all or none (see applyPositionWrites):
// the plan reports failures per item, the commit does not land part of it. A
// full snapshot's deletions are part of that same set.
//
// Catalogue assets are not. An asset the batch has to create is created by
// the market-data service while the batch is planned, before the holdings
// transaction opens; that service owns the catalogue and may run as another
// process with its own store, so its write cannot join this transaction. A
// batch that rolls back therefore keeps the assets it created: AssetsCreated
// still counts them and each such item's error says the asset stayed. An
// unheld catalogue row is reused by the next import of the same identity.
func (h *Handler) ImportPositions(ctx context.Context, req *connect.Request[apiv1.ImportPositionsRequest]) (*connect.Response[apiv1.ImportPositionsResponse], error) {
	if h.mdClient == nil {
		return nil, connect.NewError(connect.CodeUnimplemented, errors.New("asset resolution not configured"))
	}
	account, err := h.importAccount(ctx, req.Msg.AccountId)
	if err != nil {
		return nil, err
	}
	if len(req.Msg.Positions) == 0 {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("positions are required"))
	}
	if len(req.Msg.Positions) > maxImportBatch {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("batch too large: %d positions, max %d", len(req.Msg.Positions), maxImportBatch))
	}
	importID, err := resolveImportID(req.Msg.ImportId)
	if err != nil {
		return nil, err
	}

	holdingByAsset, err := h.accountHoldingsByAsset(ctx, account.ID)
	if err != nil {
		return nil, err
	}

	resp := &apiv1.ImportPositionsResponse{ImportId: importID, DryRun: req.Msg.DryRun}
	// Guards against the same asset appearing twice in one batch: resolved
	// assets by id, plus would-be-created assets by identity in a dry run.
	seenAssets := map[string]bool{}

	var writes []positionWrite
	for _, item := range req.Msg.Positions {
		result, write := h.importOnePosition(ctx, req.Msg.DryRun, account, item, importID, holdingByAsset, seenAssets)
		resp.Items = append(resp.Items, result)
		if write != nil {
			writes = append(writes, *write)
		}
	}
	if req.Msg.FullSnapshot {
		// A batch with failures is not a trustworthy snapshot: a position that
		// merely failed to parse must not be closed as "absent".
		if slices.ContainsFunc(resp.Items, func(r *apiv1.ImportPositionResult) bool { return r.Error != nil }) {
			resp.DeletionsSuppressed = true
		} else {
			writes = append(writes, h.planAbsentHoldings(ctx, resp, holdingByAsset, seenAssets)...)
		}
	}
	if !req.Msg.DryRun {
		h.applyPositionWrites(ctx, account.ID, resp.Items, writes)
	}

	for _, result := range resp.Items {
		switch {
		case result.Error != nil:
			resp.Failed++
		case result.Action == apiv1.ImportAction_IMPORT_ACTION_CREATE:
			resp.Created++
		case result.Action == apiv1.ImportAction_IMPORT_ACTION_UPDATE:
			resp.Updated++
		case result.Action == apiv1.ImportAction_IMPORT_ACTION_DELETE:
			resp.Deleted++
		default:
			resp.Skipped++
		}
		if result.AssetCreated {
			resp.AssetsCreated++
		}
	}

	return connect.NewResponse(resp), nil
}

// planAbsentHoldings plans closing the account holdings that a full-snapshot
// batch did not mention and returns the deletes a commit lands together with
// the batch's upserts. Excluded holdings are deliberate user curation and are
// never touched.
func (h *Handler) planAbsentHoldings(
	ctx context.Context,
	resp *apiv1.ImportPositionsResponse,
	holdingByAsset map[string]*entity.Holding,
	seenAssets map[string]bool,
) []positionWrite {
	// Stable order: map iteration would shuffle the plan between calls.
	assetIDs := make([]string, 0, len(holdingByAsset))
	for assetID := range holdingByAsset {
		if !seenAssets[assetID] && !holdingByAsset[assetID].Excluded() {
			assetIDs = append(assetIDs, assetID)
		}
	}
	slices.Sort(assetIDs)

	deletes := make([]positionWrite, 0, len(assetIDs))
	for _, assetID := range assetIDs {
		hld := holdingByAsset[assetID]
		prev := hld.Amount.String()
		result := &apiv1.ImportPositionResult{
			AssetId:        assetID,
			Action:         apiv1.ImportAction_IMPORT_ACTION_DELETE,
			Amount:         "0",
			Decimals:       hld.Decimals,
			PreviousAmount: &prev,
			Symbol:         h.assetSymbol(ctx, assetID),
		}
		resp.Items = append(resp.Items, result)
		deletes = append(deletes, positionWrite{result: result, holding: hld, delete: true})
	}
	return deletes
}

// assetSymbol resolves an asset id to its symbol for plan readability;
// best-effort, empty on lookup failure.
func (h *Handler) assetSymbol(ctx context.Context, assetID string) string {
	resp, err := h.mdClient.GetAsset(ctx, connect.NewRequest(&apiv1.GetAssetRequest{Id: assetID}))
	if err != nil {
		return ""
	}
	return resp.Msg.GetSymbol()
}

// positionWrite is one holding write of a committed batch: the holding to
// close when delete is set, a holding to create when holding.ID is empty,
// otherwise the refreshed copy to update.
type positionWrite struct {
	result  *apiv1.ImportPositionResult
	holding *entity.Holding
	delete  bool
}

// importOnePosition validates, resolves and plans a single batch item, and
// returns the holding write a commit would make (nil when there is none).
// Failures are per-item: they land in the result, not as an error.
func (h *Handler) importOnePosition(
	ctx context.Context,
	dryRun bool,
	account *entity.Account,
	item *apiv1.ImportPositionItem,
	importID string,
	holdingByAsset map[string]*entity.Holding,
	seenAssets map[string]bool,
) (*apiv1.ImportPositionResult, *positionWrite) {
	result := &apiv1.ImportPositionResult{Symbol: entity.NormalizeSymbol(item.Symbol)}
	fail := func(err error) (*apiv1.ImportPositionResult, *positionWrite) {
		setImportError(result, err)
		return result, nil
	}

	amount, err := decimal.NewFromString(item.Amount)
	if err != nil {
		return fail(fmt.Errorf("invalid amount %q: %w", item.Amount, err))
	}
	if amount.IsNegative() {
		return fail(errors.New("amount must not be negative"))
	}
	decimals := uint32(defaultImportDecimals)
	if item.Decimals != nil {
		decimals = *item.Decimals
	}
	raw := amount.Shift(int32(decimals)) // #nosec G115 -- decimals is a small storage scale
	if !raw.IsInteger() {
		return fail(fmt.Errorf("amount %s has more fractional digits than decimals=%d", item.Amount, decimals))
	}
	result.Amount = raw.String()
	result.Decimals = decimals

	assetID, assetCreated, err := h.resolveImportAsset(ctx, dryRun, item, seenAssets)
	if err != nil {
		return fail(err)
	}
	result.AssetId = assetID
	result.AssetCreated = assetCreated

	existing := holdingByAsset[assetID]
	switch {
	case assetID == "": // dry run, asset would be created — so the holding would be too
		result.Action = apiv1.ImportAction_IMPORT_ACTION_CREATE
	case existing == nil:
		result.Action = apiv1.ImportAction_IMPORT_ACTION_CREATE
	case existing.Amount.Equal(raw) && existing.Decimals == decimals:
		result.Action = apiv1.ImportAction_IMPORT_ACTION_SKIP
	default:
		prev := existing.Amount.String()
		result.PreviousAmount = &prev
		result.Action = apiv1.ImportAction_IMPORT_ACTION_UPDATE
	}

	if dryRun || result.Action == apiv1.ImportAction_IMPORT_ACTION_SKIP {
		return result, nil
	}

	if existing == nil {
		created := &entity.Holding{
			AssetID:   assetID,
			AccountID: account.ID,
			Amount:    raw,
			Decimals:  decimals,
			ImportID:  importID,
		}
		if err := created.StampSource(entity.SourceLLMImport); err != nil {
			return fail(err)
		}
		return result, &positionWrite{result: result, holding: created}
	}
	// A copy: the loaded holding stays as stored until the batch commits.
	// Only the balance is refreshed; source stays creation provenance.
	updated := *existing
	updated.Amount = raw
	updated.Decimals = decimals
	return result, &positionWrite{result: result, holding: &updated}
}

// applyPositionWrites lands a committed batch in one holdings transaction, so
// a caller writing related positions together (the paper venue debits the
// quote and credits the base in one batch) never sees one without the other,
// and a full snapshot never refreshes balances while leaving open a position
// it closed. A batch with a failed item writes nothing, and a write that fails
// rolls the rest back; every planned item then carries an error saying it was
// not applied.
func (h *Handler) applyPositionWrites(ctx context.Context, accountID string, items []*apiv1.ImportPositionResult, writes []positionWrite) {
	if len(writes) == 0 {
		return
	}
	for _, item := range items {
		if item.Error != nil {
			for _, w := range writes {
				setNotApplied(w, errors.New("not applied: another item in the batch failed"))
			}
			return
		}
	}
	failedAt := -1
	err := h.store.InHoldingsTx(ctx, func(tw HoldingWriter) error {
		// Not account.PortfolioID, read before the batch: see
		// HoldingWriter.LockAccountPortfolio.
		portfolioID, err := tw.LockAccountPortfolio(ctx, accountID)
		if err != nil {
			return fmt.Errorf("lock account portfolio: %w", err)
		}
		for i, w := range writes {
			switch {
			case w.delete:
				if err := tw.DeleteHolding(ctx, w.holding.ID); err != nil {
					failedAt = i
					return fmt.Errorf("delete holding: %w", err)
				}
			case w.holding.ID == "":
				w.holding.PortfolioID = portfolioID
				if _, err := tw.CreateHolding(ctx, w.holding); err != nil {
					failedAt = i
					return fmt.Errorf("create holding: %w", err)
				}
			default:
				if _, err := tw.UpdateHolding(ctx, w.holding, []string{"amount", "decimals"}); err != nil {
					failedAt = i
					return fmt.Errorf("update holding: %w", err)
				}
			}
		}
		return nil
	})
	if err != nil {
		for i, w := range writes {
			if i == failedAt {
				setNotApplied(w, err)
				continue
			}
			setNotApplied(w, fmt.Errorf("not applied, batch rolled back: %w", err))
		}
	}
}

// setNotApplied marks a write of a batch that did not land. An asset created
// for it stays: see ImportPositions.
func setNotApplied(w positionWrite, err error) {
	if w.result.AssetCreated {
		err = fmt.Errorf("%w; the asset created for it stays in the catalogue", err)
	}
	setImportError(w.result, err)
}

func setImportError(result *apiv1.ImportPositionResult, err error) {
	msg := err.Error()
	result.Error = &msg
}

// resolveImportAsset turns a batch item into an asset id via explicit id or
// find-or-create by composite identity. In a dry run nothing is created: a
// missing asset comes back as ("", true, nil) — "would create" — for a caller
// who may add a catalogue row; anyone else gets the PermissionDenied the
// commit would get (personal-a483), so the plan fails that item too.
func (h *Handler) resolveImportAsset(ctx context.Context, dryRun bool, item *apiv1.ImportPositionItem, seenAssets map[string]bool) (string, bool, error) {
	if item.AssetId != nil && *item.AssetId != "" {
		resp, err := h.mdClient.GetAsset(ctx, connect.NewRequest(&apiv1.GetAssetRequest{Id: *item.AssetId}))
		if err != nil {
			return "", false, fmt.Errorf("asset %s: %w", *item.AssetId, err)
		}
		id := resp.Msg.Id
		if seenAssets[id] {
			return "", false, errors.New("duplicate asset in batch")
		}
		seenAssets[id] = true
		return id, false, nil
	}

	if strings.TrimSpace(item.Symbol) == "" {
		return "", false, errors.New("symbol or asset_id is required")
	}

	resp, err := h.mdClient.FindOrCreateAsset(ctx, connect.NewRequest(&apiv1.FindOrCreateAssetRequest{
		Symbol: item.Symbol,
		Market: item.Market,
		Type:   item.AssetType,
		Name:   item.Name,
		DryRun: dryRun,
	}))
	if err != nil {
		return "", false, fmt.Errorf("resolve asset %s: %w", item.Symbol, err)
	}

	if resp.Msg.Asset == nil {
		// Dry run, would create: dedupe by identity instead of id.
		key := "new:" + entity.NormalizeSymbol(item.Symbol) + ":" + entity.NormalizeMarket(item.GetMarket()) + ":" + item.AssetType.String()
		if seenAssets[key] {
			return "", false, errors.New("duplicate asset in batch")
		}
		seenAssets[key] = true
		return "", true, nil
	}

	id := resp.Msg.Asset.Id
	if seenAssets[id] {
		return "", false, errors.New("duplicate asset in batch")
	}
	seenAssets[id] = true
	return id, resp.Msg.Created, nil
}

// accountHoldingsByAsset loads every holding of the account keyed by asset id.
func (h *Handler) accountHoldingsByAsset(ctx context.Context, accountID string) (map[string]*entity.Holding, error) {
	byAsset := map[string]*entity.Holding{}
	pageToken := ""
	for {
		holdings, next, err := h.store.ListHoldings(ctx, ListHoldingsOpts{AccountID: accountID, PageSize: maxImportBatch, PageToken: pageToken})
		if err != nil {
			return nil, toConnectError(err)
		}
		for _, hld := range holdings {
			byAsset[hld.AssetID] = hld
		}
		if next == "" {
			return byAsset, nil
		}
		pageToken = next
	}
}

// ImportTransactions batch-creates ledger events on an account the caller
// owns: each item is one event with its legs, written together. A manual
// account takes every kind; any other account takes income only, minus the
// income kinds its own sync writes (refuseLedgerImport), refused per item so
// the rest of the batch still lands. Dedup: an item with an
// external_ref is skipped when the account already has that ref from an
// import; an item without one is skipped when an imported event has the same
// kind, executed_at and legs. Re-importing the same export therefore writes
// nothing. Ledger import never creates assets: unknown symbols are per-item
// errors.
func (h *Handler) ImportTransactions(ctx context.Context, req *connect.Request[apiv1.ImportTransactionsRequest]) (*connect.Response[apiv1.ImportTransactionsResponse], error) {
	if h.mdClient == nil {
		return nil, connect.NewError(connect.CodeUnimplemented, errors.New("asset resolution not configured"))
	}
	if req.Msg.AccountId == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("account_id is required"))
	}
	account, err := h.ownedAccount(ctx, req.Msg.AccountId)
	if err != nil {
		return nil, err
	}
	if len(req.Msg.Transactions) == 0 {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("transactions are required"))
	}
	if len(req.Msg.Transactions) > maxImportBatch {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("batch too large: %d transactions, max %d", len(req.Msg.Transactions), maxImportBatch))
	}
	importID, err := resolveImportID(req.Msg.ImportId)
	if err != nil {
		return nil, err
	}

	externalRefs, heuristicKeys, err := h.importedLedgerKeys(ctx, account.ID)
	if err != nil {
		return nil, err
	}

	resp := &apiv1.ImportTransactionsResponse{ImportId: importID, DryRun: req.Msg.DryRun}

	for i, item := range req.Msg.Transactions {
		result := h.importOneLedgerEvent(ctx, req.Msg.DryRun, account, item, importID, externalRefs, heuristicKeys)
		result.Index = int32(i) // #nosec G115 -- bounded by maxImportBatch
		resp.Items = append(resp.Items, result)
		switch {
		case result.Error != nil:
			resp.Failed++
		case result.Action == apiv1.ImportAction_IMPORT_ACTION_CREATE:
			resp.Created++
		default:
			resp.Skipped++
		}
	}

	return connect.NewResponse(resp), nil
}

func (h *Handler) importOneLedgerEvent(
	ctx context.Context,
	dryRun bool,
	account *entity.Account,
	item *apiv1.ImportTransactionItem,
	importID string,
	externalRefs map[string]bool,
	heuristicKeys map[string]bool,
) *apiv1.ImportTransactionResult {
	result := &apiv1.ImportTransactionResult{}
	fail := func(err error) *apiv1.ImportTransactionResult {
		msg := err.Error()
		result.Error = &msg
		return result
	}

	event, err := h.ledgerEventFromImport(ctx, account.ID, importID, item)
	if err != nil {
		return fail(err)
	}
	if err := refuseLedgerImport(account, event); err != nil {
		return fail(err)
	}

	// The heuristic is only for rows the export gave no id: two genuinely
	// separate events can share kind, time and amounts, and an id says so.
	hKey := ""
	if event.ExternalRef == "" {
		hKey = ledgerHeuristicKey(event)
	}
	skip := func() *apiv1.ImportTransactionResult {
		result.Action = apiv1.ImportAction_IMPORT_ACTION_SKIP
		return result
	}
	if (event.ExternalRef != "" && externalRefs[event.ExternalRef]) || (hKey != "" && heuristicKeys[hKey]) {
		return skip()
	}

	result.Action = apiv1.ImportAction_IMPORT_ACTION_CREATE
	if !dryRun {
		created, err := h.store.CreateLedgerEvent(ctx, event)
		switch {
		case errors.Is(err, store.ErrAlreadyExists):
			// A concurrent import of the same ref landed first.
			return skip()
		case err != nil:
			return fail(fmt.Errorf("create ledger event: %w", err))
		}
		result.EventId = &created.ID
	}

	// Register keys either way so duplicates within the batch are caught in
	// dry runs too.
	if event.ExternalRef != "" {
		externalRefs[event.ExternalRef] = true
	}
	if hKey != "" {
		heuristicKeys[hKey] = true
	}
	return result
}

// ledgerEventFromImport turns an import item into a validated event: assets
// resolved (never created), amounts scaled to raw integers.
func (h *Handler) ledgerEventFromImport(ctx context.Context, accountID, importID string, item *apiv1.ImportTransactionItem) (*entity.LedgerEvent, error) {
	if item.ExecutedAt == nil {
		return nil, errors.New("executed_at is required")
	}
	event := &entity.LedgerEvent{
		AccountID:  accountID,
		Kind:       protoLedgerKind(item.Kind),
		IncomeKind: protoIncomeKind(item.IncomeKind),
		// The column keeps microseconds; truncating here makes a re-import of
		// the same export compare equal to what was stored.
		ExecutedAt:  item.ExecutedAt.AsTime().UTC().Truncate(time.Microsecond),
		ExternalRef: strings.TrimSpace(item.GetExternalRef()),
		Source:      string(entity.SourceLLMImport),
		ImportID:    importID,
	}
	if len(item.Raw) > 0 {
		raw, err := json.Marshal(item.Raw)
		if err != nil {
			return nil, fmt.Errorf("raw: %w", err)
		}
		event.Raw = raw
	}
	for i, l := range item.Legs {
		leg, err := h.ledgerLegFromImport(ctx, l)
		if err != nil {
			return nil, fmt.Errorf("leg %d: %w", i, err)
		}
		event.Legs = append(event.Legs, leg)
	}
	if err := event.Validate(); err != nil {
		return nil, err
	}
	return event, nil
}

func (h *Handler) ledgerLegFromImport(ctx context.Context, l *apiv1.ImportLedgerLeg) (entity.LedgerLeg, error) {
	amount, err := decimal.NewFromString(l.Amount)
	if err != nil {
		return entity.LedgerLeg{}, fmt.Errorf("invalid amount %q: %w", l.Amount, err)
	}
	if !amount.IsPositive() {
		return entity.LedgerLeg{}, errors.New("amount must be positive; direction carries the sign")
	}
	decimals := uint32(defaultImportDecimals)
	if l.Decimals != nil {
		decimals = *l.Decimals
	}
	raw := amount.Shift(entity.DecI32(decimals))
	if !raw.IsInteger() {
		return entity.LedgerLeg{}, fmt.Errorf("amount %s has more fractional digits than decimals=%d", l.Amount, decimals)
	}
	assetID, err := h.resolveLedgerAsset(ctx, l)
	if err != nil {
		return entity.LedgerLeg{}, err
	}
	return entity.LedgerLeg{
		AssetID:   assetID,
		Direction: protoLedgerDirection(l.Direction),
		Role:      protoLedgerRole(l.Role),
		Amount:    raw,
		Decimals:  decimals,
		Chain:     strings.TrimSpace(l.GetChain()),
	}, nil
}

// resolveLedgerAsset resolves a leg's asset reference; never creates.
func (h *Handler) resolveLedgerAsset(ctx context.Context, l *apiv1.ImportLedgerLeg) (string, error) {
	if l.AssetId != nil && *l.AssetId != "" {
		resp, err := h.mdClient.GetAsset(ctx, connect.NewRequest(&apiv1.GetAssetRequest{Id: *l.AssetId}))
		if err != nil {
			return "", fmt.Errorf("asset %s: %w", *l.AssetId, err)
		}
		return resp.Msg.Id, nil
	}
	symbol := strings.TrimSpace(l.GetSymbol())
	if symbol == "" {
		return "", errors.New("asset_id or symbol is required")
	}
	resp, err := h.mdClient.FindOrCreateAsset(ctx, connect.NewRequest(&apiv1.FindOrCreateAssetRequest{
		Symbol: symbol,
		DryRun: true, // lookup only
	}))
	switch {
	case connect.CodeOf(err) == connect.CodePermissionDenied:
		// A caller who may not add a catalogue row is refused where a dry run
		// would say "would create" (personal-a483). This lookup sends no ref,
		// so mayBindRef cannot fire and the catalogue gate is the only
		// PermissionDenied FindOrCreateAsset can return on it: the refusal
		// means the asset is not there. A new check on this path must keep
		// that true or come with its own code.
	case err != nil:
		return "", fmt.Errorf("resolve asset %s: %w", symbol, err)
	case resp.Msg.Asset != nil:
		return resp.Msg.Asset.Id, nil
	}
	return "", fmt.Errorf("asset %s not found; import positions first or create it explicitly", symbol)
}

// ledgerHeuristicKey is the dedup key of an event without an external_ref:
// kind, time and every leg, legs in a fixed order and amounts as values so the
// same movement at another storage scale still matches.
func ledgerHeuristicKey(e *entity.LedgerEvent) string {
	legs := make([]string, 0, len(e.Legs))
	for _, l := range e.Legs {
		value := l.Amount.Shift(-entity.DecI32(l.Decimals))
		legs = append(legs, strings.Join([]string{l.AssetID, string(l.Direction), string(l.Role), value.String(), l.Chain}, "|"))
	}
	slices.Sort(legs)
	// The income kind is part of what happened: a coupon and a dividend of the
	// same amount on the same date are two payments, not one written twice.
	// The reported kind, so an income event stored before kinds existed matches
	// its re-import as other.
	kind := string(e.Kind)
	if e.Kind == entity.LedgerKindIncome {
		kind += "/" + string(e.ReportedIncomeKind())
	}
	return kind + "|" + e.ExecutedAt.UTC().Format(time.RFC3339Nano) + "|" + strings.Join(legs, ";")
}

// importedLedgerKeys loads the dedup keys of the account's imported events:
// external refs (the unique index is per source, so only this source's count)
// and heuristic keys of the events that have no ref.
func (h *Handler) importedLedgerKeys(ctx context.Context, accountID string) (map[string]bool, map[string]bool, error) {
	externalRefs := map[string]bool{}
	heuristicKeys := map[string]bool{}
	pageToken := ""
	for {
		events, next, err := h.store.ListLedgerEvents(ctx, ListLedgerOpts{AccountID: accountID, PageSize: maxImportBatch, PageToken: pageToken})
		if err != nil {
			return nil, nil, toConnectError(err)
		}
		for _, e := range events {
			switch {
			case e.Source != string(entity.SourceLLMImport):
			case e.ExternalRef != "":
				externalRefs[e.ExternalRef] = true
			default:
				heuristicKeys[ledgerHeuristicKey(e)] = true
			}
		}
		if next == "" {
			return externalRefs, heuristicKeys, nil
		}
		pageToken = next
	}
}
