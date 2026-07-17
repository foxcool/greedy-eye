package portfolio

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"connectrpc.com/connect"
	apiv1 "github.com/foxcool/greedy-eye/api/v1"
	"github.com/foxcool/greedy-eye/internal/entity"
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

	for _, item := range req.Msg.Positions {
		result := h.importOnePosition(ctx, req.Msg.DryRun, account, item, importID, holdingByAsset, seenAssets)
		resp.Items = append(resp.Items, result)
		switch {
		case result.Error != nil:
			resp.Failed++
		case result.Action == apiv1.ImportAction_IMPORT_ACTION_CREATE:
			resp.Created++
		case result.Action == apiv1.ImportAction_IMPORT_ACTION_UPDATE:
			resp.Updated++
		default:
			resp.Skipped++
		}
		if result.AssetCreated {
			resp.AssetsCreated++
		}
	}

	return connect.NewResponse(resp), nil
}

// importOnePosition validates, resolves and (unless dry-run) applies a single
// batch item. Failures are per-item: they land in the result, not as an error.
func (h *Handler) importOnePosition(
	ctx context.Context,
	dryRun bool,
	account *entity.Account,
	item *apiv1.ImportPositionItem,
	importID string,
	holdingByAsset map[string]*entity.Holding,
	seenAssets map[string]bool,
) *apiv1.ImportPositionResult {
	result := &apiv1.ImportPositionResult{Symbol: entity.NormalizeSymbol(item.Symbol)}
	fail := func(err error) *apiv1.ImportPositionResult {
		msg := err.Error()
		result.Error = &msg
		return result
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
		return result
	}

	if existing == nil {
		created, err := h.store.CreateHolding(ctx, &entity.Holding{
			AssetID:     assetID,
			AccountID:   account.ID,
			PortfolioID: account.PortfolioID, // inherit the account default, like sync does
			Amount:      raw,
			Decimals:    decimals,
			Source:      entity.SourceLLMImport,
			ImportID:    importID,
		})
		if err != nil {
			return fail(fmt.Errorf("create holding: %w", err))
		}
		holdingByAsset[assetID] = created
		return result
	}

	existing.Amount = raw
	existing.Decimals = decimals
	// Only the balance is refreshed; source stays creation provenance.
	if _, err := h.store.UpdateHolding(ctx, existing, []string{"amount", "decimals"}); err != nil {
		return fail(fmt.Errorf("update holding: %w", err))
	}
	return result
}

// resolveImportAsset turns a batch item into an asset id via explicit id or
// find-or-create by composite identity. In a dry run nothing is created: a
// missing asset comes back as ("", true, nil) — "would create".
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

// ImportTransactions batch-creates transaction history on a manual account.
// Dedup: an item whose external_id matches an existing transaction — or, when
// both are present, whose (type, asset, data.date, data.amount) tuple does —
// is skipped, so re-importing the same export does not double records.
// Transactions never create assets: unknown symbols are per-item errors.
func (h *Handler) ImportTransactions(ctx context.Context, req *connect.Request[apiv1.ImportTransactionsRequest]) (*connect.Response[apiv1.ImportTransactionsResponse], error) {
	if h.mdClient == nil {
		return nil, connect.NewError(connect.CodeUnimplemented, errors.New("asset resolution not configured"))
	}
	account, err := h.importAccount(ctx, req.Msg.AccountId)
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

	externalIDs, heuristicKeys, err := h.accountTransactionKeys(ctx, account.ID)
	if err != nil {
		return nil, err
	}

	resp := &apiv1.ImportTransactionsResponse{ImportId: importID, DryRun: req.Msg.DryRun}

	for i, item := range req.Msg.Transactions {
		result := h.importOneTransaction(ctx, req.Msg.DryRun, account, item, importID, externalIDs, heuristicKeys)
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

func (h *Handler) importOneTransaction(
	ctx context.Context,
	dryRun bool,
	account *entity.Account,
	item *apiv1.ImportTransactionItem,
	importID string,
	externalIDs map[string]bool,
	heuristicKeys map[string]bool,
) *apiv1.ImportTransactionResult {
	result := &apiv1.ImportTransactionResult{}
	fail := func(err error) *apiv1.ImportTransactionResult {
		msg := err.Error()
		result.Error = &msg
		return result
	}

	txType := entity.TransactionType(item.Type)
	if txType == entity.TransactionTypeUnspecified {
		return fail(errors.New("transaction type is required"))
	}
	status := entity.TransactionStatus(item.Status)
	if status == entity.TransactionStatusUnspecified {
		status = entity.TransactionStatusCompleted
	}

	assetID, err := h.resolveTransactionAsset(ctx, item)
	if err != nil {
		return fail(err)
	}

	data := map[string]string{}
	for k, v := range item.Data {
		data[k] = v
	}
	externalID := strings.TrimSpace(item.GetExternalId())
	if externalID != "" {
		data["external_id"] = externalID
	}

	// Dedup: external_id first, then the (type, asset, date, amount) tuple.
	if externalID != "" && externalIDs[externalID] {
		result.Action = apiv1.ImportAction_IMPORT_ACTION_SKIP
		return result
	}
	hKey := transactionHeuristicKey(txType, assetID, data)
	if hKey != "" && heuristicKeys[hKey] {
		result.Action = apiv1.ImportAction_IMPORT_ACTION_SKIP
		return result
	}

	result.Action = apiv1.ImportAction_IMPORT_ACTION_CREATE
	if !dryRun {
		created, err := h.store.CreateTransaction(ctx, &entity.Transaction{
			Type:      txType,
			Status:    status,
			AccountID: account.ID,
			AssetID:   assetID,
			Data:      data,
			Source:    entity.SourceLLMImport,
			ImportID:  importID,
		})
		if err != nil {
			return fail(fmt.Errorf("create transaction: %w", err))
		}
		result.TransactionId = &created.ID
	}

	// Register keys either way so duplicates within the batch are caught in
	// dry runs too.
	if externalID != "" {
		externalIDs[externalID] = true
	}
	if hKey != "" {
		heuristicKeys[hKey] = true
	}
	return result
}

// resolveTransactionAsset resolves an optional asset reference; never creates.
func (h *Handler) resolveTransactionAsset(ctx context.Context, item *apiv1.ImportTransactionItem) (string, error) {
	if item.AssetId != nil && *item.AssetId != "" {
		resp, err := h.mdClient.GetAsset(ctx, connect.NewRequest(&apiv1.GetAssetRequest{Id: *item.AssetId}))
		if err != nil {
			return "", fmt.Errorf("asset %s: %w", *item.AssetId, err)
		}
		return resp.Msg.Id, nil
	}
	symbol := strings.TrimSpace(item.GetSymbol())
	if symbol == "" {
		return "", nil
	}
	resp, err := h.mdClient.FindOrCreateAsset(ctx, connect.NewRequest(&apiv1.FindOrCreateAssetRequest{
		Symbol: symbol,
		DryRun: true, // lookup only
	}))
	if err != nil {
		return "", fmt.Errorf("resolve asset %s: %w", symbol, err)
	}
	if resp.Msg.Asset == nil {
		return "", fmt.Errorf("asset %s not found; import positions first or create it explicitly", symbol)
	}
	return resp.Msg.Asset.Id, nil
}

// transactionHeuristicKey builds the fallback dedup key. Empty when the item
// lacks the fields that make the heuristic meaningful.
func transactionHeuristicKey(txType entity.TransactionType, assetID string, data map[string]string) string {
	date := strings.TrimSpace(data["date"])
	amount := strings.TrimSpace(data["amount"])
	if date == "" || amount == "" {
		return ""
	}
	return fmt.Sprintf("%d|%s|%s|%s", txType, assetID, date, amount)
}

// accountTransactionKeys loads dedup keys of the account's existing
// transactions: known external ids and heuristic tuples.
func (h *Handler) accountTransactionKeys(ctx context.Context, accountID string) (map[string]bool, map[string]bool, error) {
	externalIDs := map[string]bool{}
	heuristicKeys := map[string]bool{}
	pageToken := ""
	for {
		txs, next, err := h.store.ListTransactions(ctx, ListTransactionsOpts{AccountID: accountID, PageSize: maxImportBatch, PageToken: pageToken})
		if err != nil {
			return nil, nil, toConnectError(err)
		}
		for _, t := range txs {
			if id := strings.TrimSpace(t.Data["external_id"]); id != "" {
				externalIDs[id] = true
			}
			if key := transactionHeuristicKey(t.Type, t.AssetID, t.Data); key != "" {
				heuristicKeys[key] = true
			}
		}
		if next == "" {
			return externalIDs, heuristicKeys, nil
		}
		pageToken = next
	}
}
