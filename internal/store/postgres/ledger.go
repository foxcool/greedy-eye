package postgres

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/foxcool/greedy-eye/internal/entity"
	"github.com/foxcool/greedy-eye/internal/service/portfolio"
	"github.com/foxcool/greedy-eye/internal/store"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
)

// ledgerExternalRefIndex is the unique index on (account_id, source,
// external_ref) — schema.hcl. Hitting it means the event is already recorded.
const ledgerExternalRefIndex = "ledger_events_external_ref"

// CreateLedgerEvent writes the event and its legs in one transaction: an
// event without its legs, or with half of them, would misstate what moved.
func (s *PortfolioStore) CreateLedgerEvent(ctx context.Context, e *entity.LedgerEvent) (*entity.LedgerEvent, error) {
	if e == nil {
		return nil, fmt.Errorf("%w: ledger event is required", store.ErrInvalidArgument)
	}
	if err := e.Validate(); err != nil {
		return nil, fmt.Errorf("%w: %v", store.ErrInvalidArgument, err)
	}
	if !isValidUUID(e.AccountID) {
		return nil, fmt.Errorf("%w: invalid account ID format", store.ErrInvalidArgument)
	}

	id, err := uuid.NewV7()
	if err != nil {
		return nil, fmt.Errorf("failed to generate ID: %w", err)
	}
	e.ID = id.String()
	raw := e.Raw
	if len(raw) == 0 {
		raw = []byte("{}")
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }() // no-op once committed

	err = tx.QueryRow(ctx, `
		INSERT INTO ledger_events (id, account_id, kind, income_kind, executed_at, external_ref, source, import_id, raw, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, NOW(), NOW())
		RETURNING created_at, updated_at`,
		e.ID, e.AccountID, string(e.Kind), nullableString(string(e.IncomeKind)), e.ExecutedAt,
		nullableString(e.ExternalRef), e.Source, nullableString(e.ImportID), raw,
	).Scan(&e.CreatedAt, &e.UpdatedAt)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == uniqueViolation && pgErr.ConstraintName == ledgerExternalRefIndex {
			return nil, fmt.Errorf("%w: ledger event %q from %s", store.ErrAlreadyExists, e.ExternalRef, e.Source)
		}
		if isConstraintError(err) {
			return nil, fmt.Errorf("%w: %v", store.ErrConstraint, err)
		}
		return nil, fmt.Errorf("failed to create ledger event: %w", err)
	}

	for i := range e.Legs {
		leg := &e.Legs[i]
		legID, err := uuid.NewV7()
		if err != nil {
			return nil, fmt.Errorf("failed to generate ID: %w", err)
		}
		leg.ID, leg.EventID = legID.String(), e.ID
		if _, err := tx.Exec(ctx, `
			INSERT INTO ledger_legs (id, event_id, asset_id, direction, role, amount, decimals, chain)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
			leg.ID, leg.EventID, leg.AssetID, string(leg.Direction), string(leg.Role), leg.Amount, leg.Decimals,
			nullableString(leg.Chain),
		); err != nil {
			if isConstraintError(err) {
				return nil, fmt.Errorf("%w: leg %d: %v", store.ErrConstraint, i, err)
			}
			return nil, fmt.Errorf("failed to create ledger leg %d: %w", i, err)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit: %w", err)
	}
	return e, nil
}

// ledgerPageToken is the keyset position after the last event of a page:
// events are ordered by (executed_at, id), and executed_at is not unique.
func ledgerPageToken(e *entity.LedgerEvent) string {
	return base64.StdEncoding.EncodeToString([]byte(e.ExecutedAt.UTC().Format(time.RFC3339Nano) + "|" + e.ID))
}

func parseLedgerPageToken(token string) (time.Time, string, error) {
	decoded, err := base64.StdEncoding.DecodeString(token)
	if err != nil {
		return time.Time{}, "", fmt.Errorf("%w: invalid page token", store.ErrInvalidArgument)
	}
	at, id, ok := strings.Cut(string(decoded), "|")
	if !ok || !isValidUUID(id) {
		return time.Time{}, "", fmt.Errorf("%w: invalid page token", store.ErrInvalidArgument)
	}
	t, err := time.Parse(time.RFC3339Nano, at)
	if err != nil {
		return time.Time{}, "", fmt.Errorf("%w: invalid page token", store.ErrInvalidArgument)
	}
	return t, id, nil
}

// ListLedgerEvents pages events oldest executed_at first, each with all its
// legs — an asset filter selects events, it does not cut legs off them.
func (s *PortfolioStore) ListLedgerEvents(ctx context.Context, opts portfolio.ListLedgerOpts) ([]*entity.LedgerEvent, string, error) {
	limit := pageLimit(opts.PageSize)

	var where []string
	var args []any
	arg := func(v any) string {
		args = append(args, v)
		return fmt.Sprintf("$%d", len(args))
	}

	// Scope by account owner, as holdings are.
	if opts.UserID != "" {
		where = append(where, "e.account_id IN (SELECT id FROM accounts WHERE user_id = "+arg(opts.UserID)+")")
	}
	if opts.AccountID != "" {
		if !isValidUUID(opts.AccountID) {
			return nil, "", fmt.Errorf("%w: invalid account ID format", store.ErrInvalidArgument)
		}
		where = append(where, "e.account_id = "+arg(opts.AccountID))
	}
	if opts.PortfolioID != "" {
		if !isValidUUID(opts.PortfolioID) {
			return nil, "", fmt.Errorf("%w: invalid portfolio ID format", store.ErrInvalidArgument)
		}
		where = append(where, "e.account_id IN (SELECT id FROM accounts WHERE portfolio_id = "+arg(opts.PortfolioID)+")")
	}
	if opts.Kind != "" {
		where = append(where, "e.kind = "+arg(string(opts.Kind)))
	}
	if opts.AssetID != "" {
		if !isValidUUID(opts.AssetID) {
			return nil, "", fmt.Errorf("%w: invalid asset ID format", store.ErrInvalidArgument)
		}
		where = append(where, "EXISTS (SELECT 1 FROM ledger_legs l WHERE l.event_id = e.id AND l.asset_id = "+arg(opts.AssetID)+")")
	}
	if !opts.ExecutedFrom.IsZero() {
		where = append(where, "e.executed_at >= "+arg(opts.ExecutedFrom))
	}
	if !opts.ExecutedTo.IsZero() {
		where = append(where, "e.executed_at < "+arg(opts.ExecutedTo))
	}
	if opts.PageToken != "" {
		at, id, err := parseLedgerPageToken(opts.PageToken)
		if err != nil {
			return nil, "", err
		}
		where = append(where, fmt.Sprintf("(e.executed_at, e.id) > (%s, %s)", arg(at), arg(id)))
	}

	whereClause := ""
	if len(where) > 0 {
		whereClause = "WHERE " + strings.Join(where, " AND ")
	}
	query := fmt.Sprintf(`
		SELECT e.id, e.account_id, e.kind, e.income_kind, e.executed_at, e.external_ref, e.source, e.import_id, e.raw, e.created_at, e.updated_at
		FROM ledger_events e
		%s
		ORDER BY e.executed_at, e.id
		LIMIT %s`, whereClause, arg(limit+1))

	rows, err := s.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, "", fmt.Errorf("failed to list ledger events: %w", err)
	}
	defer rows.Close()

	events := make([]*entity.LedgerEvent, 0, limit)
	byID := map[string]*entity.LedgerEvent{}
	for rows.Next() {
		var e entity.LedgerEvent
		var kind string
		var externalRef, importID, incomeKind *string
		if err := rows.Scan(&e.ID, &e.AccountID, &kind, &incomeKind, &e.ExecutedAt, &externalRef, &e.Source, &importID,
			&e.Raw, &e.CreatedAt, &e.UpdatedAt); err != nil {
			return nil, "", fmt.Errorf("failed to scan ledger event: %w", err)
		}
		e.Kind = entity.LedgerEventKind(kind)
		if incomeKind != nil {
			e.IncomeKind = entity.IncomeKind(*incomeKind)
		}
		if externalRef != nil {
			e.ExternalRef = *externalRef
		}
		if importID != nil {
			e.ImportID = *importID
		}
		events = append(events, &e)
	}
	if err := rows.Err(); err != nil {
		return nil, "", fmt.Errorf("failed to iterate ledger events: %w", err)
	}

	var nextPageToken string
	if len(events) > limit {
		events = events[:limit]
		nextPageToken = ledgerPageToken(events[limit-1])
	}
	if len(events) == 0 {
		return events, "", nil
	}

	ids := make([]string, len(events))
	for i, e := range events {
		ids[i] = e.ID
		byID[e.ID] = e
	}
	legRows, err := s.pool.Query(ctx, `
		SELECT id, event_id, asset_id, direction, role, amount, decimals, chain
		FROM ledger_legs
		WHERE event_id = ANY($1::uuid[])
		ORDER BY event_id, id`, ids)
	if err != nil {
		return nil, "", fmt.Errorf("failed to list ledger legs: %w", err)
	}
	defer legRows.Close()
	for legRows.Next() {
		var l entity.LedgerLeg
		var direction, role string
		var chain *string
		if err := legRows.Scan(&l.ID, &l.EventID, &l.AssetID, &direction, &role, &l.Amount, &l.Decimals, &chain); err != nil {
			return nil, "", fmt.Errorf("failed to scan ledger leg: %w", err)
		}
		l.Direction, l.Role = entity.LedgerDirection(direction), entity.LedgerRole(role)
		if chain != nil {
			l.Chain = *chain
		}
		e := byID[l.EventID]
		e.Legs = append(e.Legs, l)
	}
	if err := legRows.Err(); err != nil {
		return nil, "", fmt.Errorf("failed to iterate ledger legs: %w", err)
	}
	return events, nextPageToken, nil
}
