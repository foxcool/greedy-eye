package postgres

import (
	"context"
	"fmt"
	"strings"

	"github.com/foxcool/greedy-eye/internal/entity"
	"github.com/foxcool/greedy-eye/internal/service/marketdata"
	"github.com/foxcool/greedy-eye/internal/store"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// MergeAssets folds a duplicate asset into the one it duplicates. What the
// caller gets is stated on the RPC in marketdata.proto; why each step and
// refusal is shaped the way it is lives here, next to it.
//
// A dry run executes the same statements and rolls them back, so the plan is
// the commit's own count rather than a second query that could drift from it.
//
// shortcut: one transaction across catalogue, portfolio (holdings, ledger legs)
// and automation (rules) tables, valid while every domain lives in one
// database; it has to become a cross-service protocol when per-domain schemas
// land (Debt 7 in docs/architecture.md).
func (s *MarketDataStore) MergeAssets(ctx context.Context, opts marketdata.MergeAssetsOpts) (*marketdata.MergeAssetsResult, error) {
	src, errSrc := uuid.Parse(opts.SourceID)
	tgt, errTgt := uuid.Parse(opts.TargetID)
	if errSrc != nil || errTgt != nil {
		return nil, fmt.Errorf("%w: source and target must be asset IDs", store.ErrInvalidArgument)
	}
	if src == tgt {
		return nil, fmt.Errorf("%w: source and target are the same asset", store.ErrInvalidArgument)
	}
	m := mergePair{source: src.String(), target: tgt.String()}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }() // no-op once committed

	// Rules have no foreign key to an asset, so no row lock can keep a rule
	// write from naming source while the merge runs; the whole table is held
	// against writes instead. Taken before the asset rows: a rule execution
	// that holds a rule row and then writes a ledger leg on an asset waits here
	// rather than deadlocking with the merge.
	if _, err := tx.Exec(ctx, "LOCK TABLE rules IN SHARE ROW EXCLUSIVE MODE"); err != nil {
		return nil, fmt.Errorf("lock rules: %w", err)
	}
	if err := lockMergePair(ctx, tx, m, opts); err != nil {
		return nil, err
	}
	for _, refuse := range []func(context.Context, pgx.Tx, mergePair) error{
		refuseRefConflict, refuseHoldingOverlap, refuseRuleNamingBoth,
	} {
		if err := refuse(ctx, tx, m); err != nil {
			return nil, err
		}
	}

	var res marketdata.MergeAssetsResult
	if err := runMergeSteps(ctx, tx, m, mergeSteps(&res)); err != nil {
		return nil, err
	}
	if res.RulesRewritten, err = rewriteRules(ctx, tx, m); err != nil {
		return nil, err
	}

	// Attempt log and unasked deferrals go with the source (ON DELETE CASCADE):
	// they schedule the sweep, they are not a record of money.
	if _, err := tx.Exec(ctx, "DELETE FROM assets WHERE id = $1", m.source); err != nil {
		return nil, mergeError("delete merged asset", err)
	}

	if !opts.DryRun {
		if err := tx.Commit(ctx); err != nil {
			return nil, fmt.Errorf("commit: %w", err)
		}
	}
	return &res, nil
}

// mergePair holds both ids in canonical form, the form Postgres prints a uuid
// in, so they compare equal to what the database returns.
type mergePair struct{ source, target string }

// lockMergePair locks both rows for the transaction and checks they are the
// ones the caller saw. FOR UPDATE also blocks any concurrent insert that
// references either row (a sync writing a holding takes KEY SHARE on the
// asset), so nothing can attach to the source between the checks and its
// deletion. Locking in id order keeps two crossing merges from deadlocking.
func lockMergePair(ctx context.Context, tx pgx.Tx, m mergePair, opts marketdata.MergeAssetsOpts) error {
	rows, err := tx.Query(ctx,
		"SELECT id::text, symbol FROM assets WHERE id IN ($1, $2) ORDER BY id FOR UPDATE",
		m.source, m.target)
	if err != nil {
		return fmt.Errorf("lock assets: %w", err)
	}
	symbols := map[string]string{}
	for rows.Next() {
		var id, symbol string
		if err := rows.Scan(&id, &symbol); err != nil {
			rows.Close()
			return fmt.Errorf("scan asset: %w", err)
		}
		symbols[id] = symbol
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return fmt.Errorf("lock assets: %w", err)
	}

	for _, side := range []struct{ role, id, expected string }{
		{"source", m.source, opts.ExpectedSourceSymbol},
		{"target", m.target, opts.ExpectedTargetSymbol},
	} {
		symbol, ok := symbols[side.id]
		if !ok {
			return fmt.Errorf("%w: %s asset %s", store.ErrNotFound, side.role, side.id)
		}
		if symbol != side.expected {
			return fmt.Errorf("%w: %s asset is %q, not the expected %q",
				store.ErrConstraint, side.role, symbol, side.expected)
		}
	}
	return nil
}

// refuseRefConflict refuses when a provider namespace gives the two assets
// different ids: a provider lists one id per instrument, so that is a provider
// saying they are two things. On-chain namespaces are exempt — one coin lives
// at many contracts, and collecting them on the survivor is the point.
func refuseRefConflict(ctx context.Context, tx pgx.Tx, m mergePair) error {
	conflicts, err := collectStrings(ctx, tx, `
		SELECT s.source || ': ' || s.ref || ' vs ' || t.ref
		FROM asset_external_refs s
		JOIN asset_external_refs t ON t.source = s.source AND t.ref <> s.ref
		WHERE s.asset_id = $1 AND t.asset_id = $2 AND s.source NOT LIKE $3
		ORDER BY 1`,
		m.source, m.target, entity.OnchainPrefix+"%")
	if err != nil {
		return fmt.Errorf("check ref conflicts: %w", err)
	}
	if len(conflicts) > 0 {
		return fmt.Errorf("%w: providers name these as different instruments (%s)",
			store.ErrConstraint, namedList(conflicts))
	}
	return nil
}

// refuseHoldingOverlap refuses when both assets hold a position under the same
// sync key. Moving the source's row would leave two positions the sync reads
// as one; which amount is right is the admin's call, made by deleting one.
// The holdings of both are locked first, so a concurrent change of chain or
// liquidity cannot create an overlap after this check.
func refuseHoldingOverlap(ctx context.Context, tx pgx.Tx, m mergePair) error {
	if _, err := tx.Exec(ctx,
		"SELECT 1 FROM holdings WHERE asset_id IN ($1, $2) ORDER BY id FOR NO KEY UPDATE",
		m.source, m.target); err != nil {
		return fmt.Errorf("lock holdings: %w", err)
	}
	pairs, err := collectStrings(ctx, tx, `
		SELECT s.id::text || ' vs ' || t.id::text
		FROM holdings s
		JOIN holdings t ON t.account_id = s.account_id AND t.chain = s.chain AND t.liquidity = s.liquidity
		WHERE s.asset_id = $1 AND t.asset_id = $2
		ORDER BY 1`,
		m.source, m.target)
	if err != nil {
		return fmt.Errorf("check holding overlap: %w", err)
	}
	if len(pairs) > 0 {
		return fmt.Errorf("%w: both assets hold a position under the same account, chain and liquidity; delete one of each pair first (source vs target holding: %s)",
			store.ErrConstraint, namedList(pairs))
	}
	return nil
}

// configIDPattern matches an id inside a rule configuration in any spelling
// uuid.Parse accepts (any case, braced, urn:uuid:, with or without hyphens):
// configurations are free-form JSON stored as the owner sent them, with no
// foreign key, so the id can only be found as text. Every such spelling
// carries the 32 hex digits in order; a UUID's digits do not turn up inside an
// unrelated value by accident. Used with the case-insensitive operators.
func configIDPattern(canonical string) string {
	return strings.ReplaceAll(canonical, "-", "-?")
}

// refuseRuleNamingBoth refuses when one rule names both assets: repointing it
// would make a pair of one asset with itself (a DCA buying target with target)
// or list target twice (a rebalancing allocation), and which the owner meant
// is theirs to decide.
func refuseRuleNamingBoth(ctx context.Context, tx pgx.Tx, m mergePair) error {
	src, tgt := configIDPattern(m.source), configIDPattern(m.target)
	ids, err := collectStrings(ctx, tx,
		"SELECT id::text FROM rules WHERE configuration::text ~* $1 AND configuration::text ~* $2 ORDER BY 1",
		src, tgt)
	if err != nil {
		return fmt.Errorf("check rules: %w", err)
	}
	if len(ids) > 0 {
		return fmt.Errorf("%w: rules name both assets; edit them first (%s)", store.ErrConstraint, namedList(ids))
	}
	return nil
}

// mergeStep is one statement of a merge over ($1 source, $2 target), counted
// into the result field it affects.
type mergeStep struct {
	what string
	dst  *int64
	sql  string
}

// mergeSteps is the merge in the order it must run. The source prices that
// cannot move are dropped first: a price between the two would value target
// in itself, and where target already has the same observation (the unique
// key: asset, source, timestamp, base) target's is kept. Then every row naming
// source is repointed. Holdings keep updated_at: a merge confirms no amount,
// and freshness is read from it.
func mergeSteps(res *marketdata.MergeAssetsResult) []mergeStep {
	return []mergeStep{
		{"self prices", &res.PricesDropped, `
			DELETE FROM prices
			WHERE (asset_id = $1 AND base_asset_id IN ($1, $2)) OR (asset_id = $2 AND base_asset_id = $1)`},
		{"duplicate prices", &res.PricesDropped, `
			DELETE FROM prices p
			WHERE p.asset_id = $1 AND EXISTS (
				SELECT 1 FROM prices q
				WHERE q.asset_id = $2 AND q.source_id = p.source_id
				  AND q.timestamp = p.timestamp AND q.base_asset_id = p.base_asset_id)`},
		{"duplicate base prices", &res.PricesDropped, `
			DELETE FROM prices p
			WHERE p.base_asset_id = $1 AND EXISTS (
				SELECT 1 FROM prices q
				WHERE q.base_asset_id = $2 AND q.asset_id = p.asset_id
				  AND q.source_id = p.source_id AND q.timestamp = p.timestamp)`},
		{"prices", &res.PricesMoved, `UPDATE prices SET asset_id = $2 WHERE asset_id = $1`},
		{"base prices", &res.PricesMoved, `UPDATE prices SET base_asset_id = $2 WHERE base_asset_id = $1`},
		{"holdings", &res.HoldingsMoved, `UPDATE holdings SET asset_id = $2 WHERE asset_id = $1`},
		{"ledger legs", &res.LedgerLegsMoved, `UPDATE ledger_legs SET asset_id = $2 WHERE asset_id = $1`},
		{"external refs", &res.ExternalRefsMoved, `UPDATE asset_external_refs SET asset_id = $2 WHERE asset_id = $1`},
		{"risk flags", &res.RiskFlagsMoved, `UPDATE asset_risk_flags SET asset_id = $2 WHERE asset_id = $1`},
	}
}

func runMergeSteps(ctx context.Context, tx pgx.Tx, m mergePair, steps []mergeStep) error {
	for _, st := range steps {
		tag, err := tx.Exec(ctx, st.sql, m.source, m.target)
		if err != nil {
			return mergeError("merge "+st.what, err)
		}
		*st.dst += tag.RowsAffected()
	}
	return nil
}

// rewriteRules repoints the rules naming source, found as text in any
// spelling (see configIDPattern); the rewritten id is the canonical one.
func rewriteRules(ctx context.Context, tx pgx.Tx, m mergePair) (int64, error) {
	tag, err := tx.Exec(ctx, `
		UPDATE rules
		SET configuration = regexp_replace(configuration::text, $1, $2, 'gi')::jsonb, updated_at = NOW()
		WHERE configuration::text ~* $1`,
		configIDPattern(m.source), m.target)
	if err != nil {
		return 0, mergeError("merge rules", err)
	}
	return tag.RowsAffected(), nil
}

// mergeError reports a constraint the merge ran into as a refusal, since the
// transaction rolls back and nothing is written.
func mergeError(what string, err error) error {
	if isConstraintError(err) {
		return fmt.Errorf("%w: %s: %w", store.ErrConstraint, what, err)
	}
	return fmt.Errorf("%s: %w", what, err)
}

// maxNamedInRefusal bounds what a refusal lists; the count says how many there
// are in all.
const maxNamedInRefusal = 10

func namedList(items []string) string {
	shown := strings.Join(items[:min(len(items), maxNamedInRefusal)], "; ")
	if len(items) > maxNamedInRefusal {
		return fmt.Sprintf("%s; and %d more", shown, len(items)-maxNamedInRefusal)
	}
	return shown
}

func collectStrings(ctx context.Context, tx pgx.Tx, sql string, args ...any) ([]string, error) {
	rows, err := tx.Query(ctx, sql, args...)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowTo[string])
}
