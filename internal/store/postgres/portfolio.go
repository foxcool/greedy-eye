package postgres

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/foxcool/greedy-eye/internal/entity"
	"github.com/foxcool/greedy-eye/internal/service/portfolio"
	"github.com/foxcool/greedy-eye/internal/store"
	storecrypto "github.com/foxcool/greedy-eye/internal/store/crypto"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// PortfolioStore implements portfolio.Store using PostgreSQL.
type PortfolioStore struct {
	pool *pgxpool.Pool
	// encryptor seals accounts.data at rest (ADR-005); nil = plaintext mode.
	encryptor *storecrypto.Encryptor
}

// Compile-time interface implementation check.
var _ portfolio.Store = (*PortfolioStore)(nil)

// PortfolioStoreOption configures a PortfolioStore.
type PortfolioStoreOption func(*PortfolioStore)

// WithEncryptor enables encryption at rest for accounts.data.
func WithEncryptor(e *storecrypto.Encryptor) PortfolioStoreOption {
	return func(s *PortfolioStore) { s.encryptor = e }
}

func NewPortfolioStore(pool *pgxpool.Pool, opts ...PortfolioStoreOption) *PortfolioStore {
	s := &PortfolioStore{pool: pool}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

// --- Portfolio methods ---

func (s *PortfolioStore) CreatePortfolio(ctx context.Context, p *entity.Portfolio) (*entity.Portfolio, error) {
	if p == nil {
		return nil, fmt.Errorf("%w: portfolio is required", store.ErrInvalidArgument)
	}
	if p.Name == "" {
		return nil, fmt.Errorf("%w: portfolio name is required", store.ErrInvalidArgument)
	}
	if p.UserID == "" {
		return nil, fmt.Errorf("%w: user_id is required", store.ErrInvalidArgument)
	}

	id, err := uuid.NewV7()
	if err != nil {
		return nil, fmt.Errorf("failed to generate ID: %w", err)
	}
	p.ID = id.String()

	dataJSON, err := json.Marshal(p.Data)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal data: %w", err)
	}

	query := `
		INSERT INTO portfolios (id, user_id, name, description, data, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, NOW(), NOW())
		RETURNING created_at, updated_at`

	err = s.pool.QueryRow(ctx, query,
		p.ID,
		p.UserID,
		p.Name,
		nullableString(p.Description),
		dataJSON,
	).Scan(&p.CreatedAt, &p.UpdatedAt)
	if err != nil {
		if isConstraintError(err) {
			return nil, fmt.Errorf("%w: %v", store.ErrConstraint, err)
		}
		return nil, fmt.Errorf("failed to create portfolio: %w", err)
	}

	return p, nil
}

func (s *PortfolioStore) GetPortfolio(ctx context.Context, id string) (*entity.Portfolio, error) {
	if id == "" {
		return nil, fmt.Errorf("%w: portfolio ID is required", store.ErrInvalidArgument)
	}
	if !isValidUUID(id) {
		return nil, fmt.Errorf("%w: invalid portfolio ID format", store.ErrInvalidArgument)
	}

	query := `
		SELECT id, user_id, name, description, data, created_at, updated_at
		FROM portfolios
		WHERE id = $1`

	var p entity.Portfolio
	var description *string
	var dataJSON []byte

	err := s.pool.QueryRow(ctx, query, id).Scan(
		&p.ID,
		&p.UserID,
		&p.Name,
		&description,
		&dataJSON,
		&p.CreatedAt,
		&p.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("%w: portfolio with ID %s", store.ErrNotFound, id)
		}
		return nil, fmt.Errorf("failed to get portfolio: %w", err)
	}

	if description != nil {
		p.Description = *description
	}
	if err := json.Unmarshal(dataJSON, &p.Data); err != nil {
		return nil, fmt.Errorf("failed to unmarshal data: %w", err)
	}

	return &p, nil
}

func (s *PortfolioStore) UpdatePortfolio(ctx context.Context, p *entity.Portfolio, fields []string) (*entity.Portfolio, error) {
	if p == nil || p.ID == "" {
		return nil, fmt.Errorf("%w: portfolio with ID is required", store.ErrInvalidArgument)
	}
	if !isValidUUID(p.ID) {
		return nil, fmt.Errorf("%w: invalid portfolio ID format", store.ErrInvalidArgument)
	}

	setClauses := []string{"updated_at = NOW()"}
	args := []any{p.ID}
	argIdx := 2

	for _, field := range fields {
		switch field {
		case "name":
			setClauses = append(setClauses, fmt.Sprintf("name = $%d", argIdx))
			args = append(args, p.Name)
			argIdx++
		case "description":
			setClauses = append(setClauses, fmt.Sprintf("description = $%d", argIdx))
			args = append(args, nullableString(p.Description))
			argIdx++
		case "data":
			dataJSON, err := json.Marshal(p.Data)
			if err != nil {
				return nil, fmt.Errorf("failed to marshal data: %w", err)
			}
			setClauses = append(setClauses, fmt.Sprintf("data = $%d", argIdx))
			args = append(args, dataJSON)
			argIdx++
		}
	}

	query := fmt.Sprintf(`
		UPDATE portfolios
		SET %s
		WHERE id = $1
		RETURNING id, user_id, name, description, data, created_at, updated_at`,
		strings.Join(setClauses, ", "))

	var result entity.Portfolio
	var description *string
	var dataJSON []byte

	err := s.pool.QueryRow(ctx, query, args...).Scan(
		&result.ID,
		&result.UserID,
		&result.Name,
		&description,
		&dataJSON,
		&result.CreatedAt,
		&result.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("%w: portfolio with ID %s", store.ErrNotFound, p.ID)
		}
		return nil, fmt.Errorf("failed to update portfolio: %w", err)
	}

	if description != nil {
		result.Description = *description
	}
	if err := json.Unmarshal(dataJSON, &result.Data); err != nil {
		return nil, fmt.Errorf("failed to unmarshal data: %w", err)
	}

	return &result, nil
}

func (s *PortfolioStore) DeletePortfolio(ctx context.Context, id string) error {
	if id == "" {
		return fmt.Errorf("%w: portfolio ID is required", store.ErrInvalidArgument)
	}
	if !isValidUUID(id) {
		return fmt.Errorf("%w: invalid portfolio ID format", store.ErrInvalidArgument)
	}

	result, err := s.pool.Exec(ctx, "DELETE FROM portfolios WHERE id = $1", id)
	if err != nil {
		if isConstraintError(err) {
			return fmt.Errorf("%w: cannot delete portfolio due to existing dependencies", store.ErrConstraint)
		}
		return fmt.Errorf("failed to delete portfolio: %w", err)
	}

	if result.RowsAffected() == 0 {
		return fmt.Errorf("%w: portfolio with ID %s", store.ErrNotFound, id)
	}

	return nil
}

func (s *PortfolioStore) ListPortfolios(ctx context.Context, opts portfolio.ListPortfoliosOpts) ([]*entity.Portfolio, string, error) {
	limit := opts.PageSize
	if limit <= 0 {
		limit = defaultPageSize
	}

	args := []any{}
	argIdx := 1
	whereClauses := []string{}

	if opts.UserID != "" {
		whereClauses = append(whereClauses, fmt.Sprintf("user_id = $%d", argIdx))
		args = append(args, opts.UserID)
		argIdx++
	}

	if opts.PageToken != "" {
		decoded, err := base64.StdEncoding.DecodeString(opts.PageToken)
		if err == nil && isValidUUID(string(decoded)) {
			whereClauses = append(whereClauses, fmt.Sprintf("id > $%d", argIdx))
			args = append(args, string(decoded))
			argIdx++
		}
	}

	whereClause := ""
	if len(whereClauses) > 0 {
		whereClause = "WHERE " + strings.Join(whereClauses, " AND ")
	}

	query := fmt.Sprintf(`
		SELECT id, user_id, name, description, data, created_at, updated_at
		FROM portfolios
		%s
		ORDER BY id
		LIMIT $%d`,
		whereClause, argIdx)
	args = append(args, limit+1)

	rows, err := s.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, "", fmt.Errorf("failed to list portfolios: %w", err)
	}
	defer rows.Close()

	portfolios := make([]*entity.Portfolio, 0, limit)
	for rows.Next() {
		var p entity.Portfolio
		var description *string
		var dataJSON []byte

		if err := rows.Scan(
			&p.ID,
			&p.UserID,
			&p.Name,
			&description,
			&dataJSON,
			&p.CreatedAt,
			&p.UpdatedAt,
		); err != nil {
			return nil, "", fmt.Errorf("failed to scan portfolio: %w", err)
		}

		if description != nil {
			p.Description = *description
		}
		if err := json.Unmarshal(dataJSON, &p.Data); err != nil {
			return nil, "", fmt.Errorf("failed to unmarshal data: %w", err)
		}

		portfolios = append(portfolios, &p)
	}

	var nextPageToken string
	if len(portfolios) > limit {
		lastItem := portfolios[limit-1]
		portfolios = portfolios[:limit]
		nextPageToken = base64.StdEncoding.EncodeToString([]byte(lastItem.ID))
	}

	return portfolios, nextPageToken, nil
}

// --- Account methods ---

const accountColumns = "id, user_id, name, description, type, data, capabilities, system_scopes, portfolio_id, created_at, updated_at"

// encDataKey marks the encrypted form of accounts.data: {"enc": "v1:..."} (ADR-005).
const encDataKey = "enc"

// marshalAccountData serializes the data map, sealing it when encryption is enabled.
func (s *PortfolioStore) marshalAccountData(accountID string, data map[string]string) ([]byte, error) {
	plain, err := json.Marshal(data)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal data: %w", err)
	}
	if s.encryptor == nil {
		return plain, nil
	}
	sealed, err := s.encryptor.Encrypt(accountID, plain)
	if err != nil {
		return nil, fmt.Errorf("failed to encrypt data: %w", err)
	}
	return json.Marshal(map[string]string{encDataKey: sealed})
}

// unmarshalAccountData reads a stored data value, transparently opening the
// {"enc": ...} wrapper; legacy plaintext rows pass through unchanged.
func (s *PortfolioStore) unmarshalAccountData(accountID string, raw []byte) (map[string]string, error) {
	var m map[string]string
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, fmt.Errorf("failed to unmarshal data: %w", err)
	}
	sealed, ok := m[encDataKey]
	if !ok || len(m) != 1 {
		return m, nil // legacy plaintext row
	}
	if s.encryptor == nil {
		return nil, fmt.Errorf("account %s data is encrypted but no master key is configured", accountID)
	}
	plain, err := s.encryptor.Decrypt(accountID, sealed)
	if err != nil {
		return nil, fmt.Errorf("failed to decrypt data for account %s: %w", accountID, err)
	}
	var data map[string]string
	if err := json.Unmarshal(plain, &data); err != nil {
		return nil, fmt.Errorf("failed to unmarshal decrypted data: %w", err)
	}
	return data, nil
}

// RewrapResult reports what a rewrap pass did.
type RewrapResult struct {
	// Scanned is every account row read.
	Scanned int
	// Rewritten is the rows re-sealed under the current key. After a successful
	// pass this equals Scanned: every row is re-sealed, including the ones whose
	// data is empty. An earlier version skipped those as "nothing to seal" and
	// left them sealed under the RETIRED key while reporting success — the dev
	// stand's gate.io account, an encrypted empty map, was unreadable the moment
	// the previous key was dropped. Emptiness of the plaintext says nothing
	// about which key the ciphertext is under.
	Rewritten int
}

// VerifyAccountDataReadable reads every accounts.data row and reports the first
// one it cannot open, without writing anything.
//
// Run with a current-key-only encryptor, it answers the question a rewrap's
// counters cannot: is every row now readable WITHOUT the stale keys, so they
// can be dropped from the configuration? That question needs its own pass
// because a rewrap can report success and still leave a row behind — an earlier
// version skipped rows with empty data as "nothing to seal", and the dev
// stand's gate.io account, an encrypted empty map, stayed under the retired key.
func (s *PortfolioStore) VerifyAccountDataReadable(ctx context.Context) (int, error) {
	rows, err := s.pool.Query(ctx, `SELECT id, data FROM accounts ORDER BY id`)
	if err != nil {
		return 0, fmt.Errorf("failed to list accounts for verification: %w", err)
	}
	defer rows.Close()

	var checked int
	for rows.Next() {
		var id string
		var raw []byte
		if err := rows.Scan(&id, &raw); err != nil {
			return checked, fmt.Errorf("failed to scan account for verification: %w", err)
		}
		if _, err := s.unmarshalAccountData(id, raw); err != nil {
			return checked, fmt.Errorf("account %s is not readable: %w", id, err)
		}
		checked++
	}
	if err := rows.Err(); err != nil {
		return checked, fmt.Errorf("failed to read accounts for verification: %w", err)
	}
	return checked, nil
}

// RewrapAccountData re-encrypts every accounts.data row under the CURRENT master
// key, reading each one through whatever configured key still opens it.
//
// This is what makes a master key rotation survivable. The encryptor writes with
// the first key and reads with any of them, so a rotation leaves the instance
// working but half its rows sealed under a key the operator intends to retire;
// this pass finishes the job. It also converges the legacy plaintext rows
// ADR-005 left readable, which until now were only re-sealed if something
// happened to update them.
//
// Idempotent by construction: re-sealing a row already under the current key
// just gives it a fresh nonce, so the pass can be repeated or interrupted.
//
// Refuses to run in plaintext mode: without an encryptor there is nothing to
// rewrap to, and silently doing nothing would look like success.
func (s *PortfolioStore) RewrapAccountData(ctx context.Context) (RewrapResult, error) {
	var res RewrapResult
	if s.encryptor == nil {
		return res, fmt.Errorf("%w: rewrap needs a master key; the store is in plaintext mode", store.ErrInvalidArgument)
	}

	rows, err := s.pool.Query(ctx, `SELECT id, data FROM accounts ORDER BY id`)
	if err != nil {
		return res, fmt.Errorf("failed to list accounts for rewrap: %w", err)
	}

	type pending struct {
		id   string
		data map[string]string
	}
	var todo []pending

	for rows.Next() {
		var id string
		var raw []byte
		if err := rows.Scan(&id, &raw); err != nil {
			rows.Close()
			return res, fmt.Errorf("failed to scan account for rewrap: %w", err)
		}
		res.Scanned++

		data, err := s.unmarshalAccountData(id, raw)
		if err != nil {
			// Stop rather than skip. A row no configured key can open is the
			// one thing an operator has to know about before the old key is
			// discarded, and a pass that reports success having silently left
			// it behind is worse than no pass at all.
			rows.Close()
			return res, fmt.Errorf("account %s cannot be read with the configured keys: %w", id, err)
		}
		todo = append(todo, pending{id: id, data: data})
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return res, fmt.Errorf("failed to read accounts for rewrap: %w", err)
	}
	rows.Close()

	for _, p := range todo {
		sealed, err := s.marshalAccountData(p.id, p.data)
		if err != nil {
			return res, fmt.Errorf("failed to seal account %s: %w", p.id, err)
		}
		if _, err := s.pool.Exec(ctx, `UPDATE accounts SET data = $2 WHERE id = $1`, p.id, sealed); err != nil {
			return res, fmt.Errorf("failed to write account %s: %w", p.id, err)
		}
		res.Rewritten++
	}

	return res, nil
}

// scanAccount reads one account row in accountColumns order.
func (s *PortfolioStore) scanAccount(row interface{ Scan(dest ...any) error }) (*entity.Account, error) {
	var a entity.Account
	var description *string
	var typeStr string
	var dataJSON, capabilitiesJSON, systemScopesJSON []byte
	var portfolioID *string

	err := row.Scan(
		&a.ID,
		&a.UserID,
		&a.Name,
		&description,
		&typeStr,
		&dataJSON,
		&capabilitiesJSON,
		&systemScopesJSON,
		&portfolioID,
		&a.CreatedAt,
		&a.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}

	if description != nil {
		a.Description = *description
	}
	if portfolioID != nil {
		a.PortfolioID = *portfolioID
	}
	a.Type = stringToAccountType(typeStr)
	if a.Data, err = s.unmarshalAccountData(a.ID, dataJSON); err != nil {
		return nil, err
	}
	if err := json.Unmarshal(capabilitiesJSON, &a.Capabilities); err != nil {
		return nil, fmt.Errorf("failed to unmarshal capabilities: %w", err)
	}
	if err := json.Unmarshal(systemScopesJSON, &a.SystemScopes); err != nil {
		return nil, fmt.Errorf("failed to unmarshal system_scopes: %w", err)
	}

	return &a, nil
}

// marshalCapabilities encodes a capability list as a JSON array, mapping nil to [].
func marshalCapabilities(caps []entity.AccountCapability) ([]byte, error) {
	if caps == nil {
		caps = []entity.AccountCapability{}
	}
	return json.Marshal(caps)
}

func (s *PortfolioStore) CreateAccount(ctx context.Context, a *entity.Account) (*entity.Account, error) {
	if a == nil {
		return nil, fmt.Errorf("%w: account is required", store.ErrInvalidArgument)
	}
	if a.Name == "" {
		return nil, fmt.Errorf("%w: account name is required", store.ErrInvalidArgument)
	}
	if a.UserID == "" {
		return nil, fmt.Errorf("%w: user_id is required", store.ErrInvalidArgument)
	}
	if a.Type == entity.AccountTypeUnspecified {
		return nil, fmt.Errorf("%w: account type is required", store.ErrInvalidArgument)
	}
	if err := a.ValidateCapabilities(); err != nil {
		return nil, fmt.Errorf("%w: %v", store.ErrInvalidArgument, err)
	}

	id, err := uuid.NewV7()
	if err != nil {
		return nil, fmt.Errorf("failed to generate ID: %w", err)
	}
	a.ID = id.String()

	dataJSON, err := s.marshalAccountData(a.ID, a.Data)
	if err != nil {
		return nil, err
	}
	capabilitiesJSON, err := marshalCapabilities(a.Capabilities)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal capabilities: %w", err)
	}
	systemScopesJSON, err := marshalCapabilities(a.SystemScopes)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal system_scopes: %w", err)
	}

	query := `
		INSERT INTO accounts (id, user_id, name, description, type, data, capabilities, system_scopes, portfolio_id, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, NOW(), NOW())
		RETURNING created_at, updated_at`

	err = s.pool.QueryRow(ctx, query,
		a.ID,
		a.UserID,
		a.Name,
		nullableString(a.Description),
		accountTypeToString(a.Type),
		dataJSON,
		capabilitiesJSON,
		systemScopesJSON,
		nullableString(a.PortfolioID),
	).Scan(&a.CreatedAt, &a.UpdatedAt)
	if err != nil {
		if isConstraintError(err) {
			return nil, fmt.Errorf("%w: %v", store.ErrConstraint, err)
		}
		return nil, fmt.Errorf("failed to create account: %w", err)
	}

	return a, nil
}

func (s *PortfolioStore) GetAccount(ctx context.Context, id string) (*entity.Account, error) {
	if id == "" {
		return nil, fmt.Errorf("%w: account ID is required", store.ErrInvalidArgument)
	}
	if !isValidUUID(id) {
		return nil, fmt.Errorf("%w: invalid account ID format", store.ErrInvalidArgument)
	}

	query := fmt.Sprintf(`
		SELECT %s
		FROM accounts
		WHERE id = $1`, accountColumns)

	a, err := s.scanAccount(s.pool.QueryRow(ctx, query, id))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("%w: account with ID %s", store.ErrNotFound, id)
		}
		return nil, fmt.Errorf("failed to get account: %w", err)
	}

	return a, nil
}

func (s *PortfolioStore) UpdateAccount(ctx context.Context, a *entity.Account, fields []string) (*entity.Account, error) {
	if a == nil || a.ID == "" {
		return nil, fmt.Errorf("%w: account with ID is required", store.ErrInvalidArgument)
	}
	if !isValidUUID(a.ID) {
		return nil, fmt.Errorf("%w: invalid account ID format", store.ErrInvalidArgument)
	}

	// Capability invariants span type, capabilities, and system_scopes; on a
	// partial update they must hold for the merged state, not just the patch.
	if slices.ContainsFunc(fields, func(f string) bool {
		return f == "type" || f == "capabilities" || f == "system_scopes"
	}) {
		merged, err := s.GetAccount(ctx, a.ID)
		if err != nil {
			return nil, err
		}
		for _, field := range fields {
			switch field {
			case "type":
				merged.Type = a.Type
			case "capabilities":
				merged.Capabilities = a.Capabilities
			case "system_scopes":
				merged.SystemScopes = a.SystemScopes
			}
		}
		if err := merged.ValidateCapabilities(); err != nil {
			return nil, fmt.Errorf("%w: %v", store.ErrInvalidArgument, err)
		}
	}

	setClauses := []string{"updated_at = NOW()"}
	args := []any{a.ID}
	argIdx := 2

	for _, field := range fields {
		switch field {
		case "name":
			setClauses = append(setClauses, fmt.Sprintf("name = $%d", argIdx))
			args = append(args, a.Name)
			argIdx++
		case "description":
			setClauses = append(setClauses, fmt.Sprintf("description = $%d", argIdx))
			args = append(args, nullableString(a.Description))
			argIdx++
		case "type":
			setClauses = append(setClauses, fmt.Sprintf("type = $%d", argIdx))
			args = append(args, accountTypeToString(a.Type))
			argIdx++
		case "data":
			if a.Data == nil {
				continue // Not provided — preserve existing credentials
			}
			dataJSON, err := s.marshalAccountData(a.ID, a.Data)
			if err != nil {
				return nil, err
			}
			setClauses = append(setClauses, fmt.Sprintf("data = $%d", argIdx))
			args = append(args, dataJSON)
			argIdx++
		case "capabilities":
			capabilitiesJSON, err := marshalCapabilities(a.Capabilities)
			if err != nil {
				return nil, fmt.Errorf("failed to marshal capabilities: %w", err)
			}
			setClauses = append(setClauses, fmt.Sprintf("capabilities = $%d", argIdx))
			args = append(args, capabilitiesJSON)
			argIdx++
		case "system_scopes":
			systemScopesJSON, err := marshalCapabilities(a.SystemScopes)
			if err != nil {
				return nil, fmt.Errorf("failed to marshal system_scopes: %w", err)
			}
			setClauses = append(setClauses, fmt.Sprintf("system_scopes = $%d", argIdx))
			args = append(args, systemScopesJSON)
			argIdx++
		case "portfolio_id":
			setClauses = append(setClauses, fmt.Sprintf("portfolio_id = $%d", argIdx))
			args = append(args, nullableString(a.PortfolioID))
			argIdx++
		}
	}

	query := fmt.Sprintf(`
		UPDATE accounts
		SET %s
		WHERE id = $1`,
		strings.Join(setClauses, ", "))

	result, err := s.pool.Exec(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to update account: %w", err)
	}

	if result.RowsAffected() == 0 {
		return nil, fmt.Errorf("%w: account with ID %s", store.ErrNotFound, a.ID)
	}

	return s.GetAccount(ctx, a.ID)
}

func (s *PortfolioStore) DeleteAccount(ctx context.Context, id string) error {
	if id == "" {
		return fmt.Errorf("%w: account ID is required", store.ErrInvalidArgument)
	}
	if !isValidUUID(id) {
		return fmt.Errorf("%w: invalid account ID format", store.ErrInvalidArgument)
	}

	result, err := s.pool.Exec(ctx, "DELETE FROM accounts WHERE id = $1", id)
	if err != nil {
		if isConstraintError(err) {
			return fmt.Errorf("%w: cannot delete account due to existing dependencies", store.ErrConstraint)
		}
		return fmt.Errorf("failed to delete account: %w", err)
	}

	if result.RowsAffected() == 0 {
		return fmt.Errorf("%w: account with ID %s", store.ErrNotFound, id)
	}

	return nil
}

// DeleteAccountWithHoldings removes the account together with its holdings, in
// one transaction — a half-deleted account whose positions outlived it would
// leave orphaned value in every total.
//
// Transactions are deliberately not touched: they are the record of what
// happened, not a snapshot that can be rebuilt. An account still carrying them
// hits the foreign key and the whole thing rolls back, which is the intended
// answer rather than a bug.
func (s *PortfolioStore) DeleteAccountWithHoldings(ctx context.Context, id string) error {
	if id == "" {
		return fmt.Errorf("%w: account ID is required", store.ErrInvalidArgument)
	}
	if !isValidUUID(id) {
		return fmt.Errorf("%w: invalid account ID format", store.ErrInvalidArgument)
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }() // no-op once committed

	if _, err := tx.Exec(ctx, "DELETE FROM holdings WHERE account_id = $1", id); err != nil {
		return fmt.Errorf("delete holdings: %w", err)
	}

	result, err := tx.Exec(ctx, "DELETE FROM accounts WHERE id = $1", id)
	if err != nil {
		if isConstraintError(err) {
			return fmt.Errorf("%w: cannot delete account due to existing dependencies", store.ErrConstraint)
		}
		return fmt.Errorf("failed to delete account: %w", err)
	}
	if result.RowsAffected() == 0 {
		return fmt.Errorf("%w: account with ID %s", store.ErrNotFound, id)
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit: %w", err)
	}
	return nil
}

func (s *PortfolioStore) ListAccounts(ctx context.Context, opts portfolio.ListAccountsOpts) ([]*entity.Account, string, error) {
	limit := opts.PageSize
	if limit <= 0 {
		limit = defaultPageSize
	}

	args := []any{}
	argIdx := 1
	whereClauses := []string{}

	if opts.UserID != "" {
		whereClauses = append(whereClauses, fmt.Sprintf("user_id = $%d", argIdx))
		args = append(args, opts.UserID)
		argIdx++
	}

	if opts.Type != entity.AccountTypeUnspecified {
		whereClauses = append(whereClauses, fmt.Sprintf("type = $%d", argIdx))
		args = append(args, accountTypeToString(opts.Type))
		argIdx++
	}

	if opts.PageToken != "" {
		decoded, err := base64.StdEncoding.DecodeString(opts.PageToken)
		if err == nil && isValidUUID(string(decoded)) {
			whereClauses = append(whereClauses, fmt.Sprintf("id > $%d", argIdx))
			args = append(args, string(decoded))
			argIdx++
		}
	}

	whereClause := ""
	if len(whereClauses) > 0 {
		whereClause = "WHERE " + strings.Join(whereClauses, " AND ")
	}

	query := fmt.Sprintf(`
		SELECT %s
		FROM accounts
		%s
		ORDER BY id
		LIMIT $%d`,
		accountColumns, whereClause, argIdx)
	args = append(args, limit+1)

	rows, err := s.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, "", fmt.Errorf("failed to list accounts: %w", err)
	}
	defer rows.Close()

	accounts := make([]*entity.Account, 0, limit)
	for rows.Next() {
		a, err := s.scanAccount(rows)
		if err != nil {
			return nil, "", fmt.Errorf("failed to scan account: %w", err)
		}
		accounts = append(accounts, a)
	}

	var nextPageToken string
	if len(accounts) > limit {
		lastItem := accounts[limit-1]
		accounts = accounts[:limit]
		nextPageToken = base64.StdEncoding.EncodeToString([]byte(lastItem.ID))
	}

	return accounts, nextPageToken, nil
}

// sweepableAccounts is the FROM and WHERE the sweep's selection and its count
// both run. They are one set described twice, and describing it twice is how
// the two can disagree — so the description has one home. The columns and the
// ordering stay with each query, because those differ on purpose.
//
// Placeholders: $1, $2 the always-sweepable types, $3 the broker type, $4 the
// staleness cutoff, $5 now.
//
// WHICH ACCOUNTS BELONG HERE. Wallet and exchange always: a provider can read
// them whole from an address or a key. Manual never: those positions come from
// a human and no provider can refresh them. Broker only once it holds
// positions, and that qualifier is the whole of personal-c1nz.
//
// A broker credential is not one account, it is the key to several: the account
// that carries the token has no positions of its own and fans out to the
// accounts it discovers (see syncBrokerAccounts). Admitting it here would give
// it a permanent seat — it writes no holdings, so it stays NULL-fresh, sorts
// first every hour, and never complains, which is the one shape deferral does
// not catch. That is personal-2sfn's starvation with the failure removed.
//
// So the line drawn is between refreshing and discovering. The sweep refreshes
// positions it can already see; discovering an account at a broker creates rows
// and spends a credential, and stays a deliberate act — the sync button, which
// this same change puts on broker accounts. `h.synced_at IS NOT NULL` is how
// that reads in SQL, because accounts.data is sealed as a whole (ADR-005) and
// no query can ask whether broker_account_id is set.
const sweepableAccounts = `
		FROM accounts a
		LEFT JOIN (
			SELECT account_id, max(updated_at) AS synced_at
			FROM holdings
			GROUP BY account_id
		) h ON h.account_id = a.id
		LEFT JOIN account_sync_attempts s ON s.account_id = a.id
		WHERE (a.type IN ($1, $2) OR (a.type = $3 AND h.synced_at IS NOT NULL))
		  AND (h.synced_at IS NULL OR h.synced_at < $4)
		  AND (s.next_attempt_at IS NULL OR s.next_attempt_at <= $5)`

// sweepableTypes are the type arguments sweepableAccounts expects, in order.
func sweepableTypes() (wallet, exchange, broker string) {
	return accountTypeToString(entity.AccountTypeWallet),
		accountTypeToString(entity.AccountTypeExchange),
		accountTypeToString(entity.AccountTypeBroker)
}

// ListStaleSyncTargets returns syncable accounts whose balances are older than
// `olderThan`, the ones least recently confirmed first, capped at `limit`.
//
// Freshness is read off the account's own rows rather than a column on the
// account: `holdings.updated_at` is written by the sync that produced them, so
// it cannot claim a sync that did not land. An account with no holdings at all
// sorts first — never synced is the stalest state there is, not a fresh one.
//
// Which accounts are eligible at all is sweepableAccounts.
//
// An account standing down is skipped whatever its staleness. Staleness alone
// decided this for a year, and because a sync that writes nothing does not move
// holdings.updated_at, an account whose source answered but returned nothing
// stayed the stalest and was picked again every hour — four of them held both
// slots of every run for two days while every other wallet waited. Deferral is
// the pair to the ordering, not a replacement for it: an account with nothing
// owed still competes on staleness alone.
func (s *PortfolioStore) ListStaleSyncTargets(ctx context.Context, olderThan, now time.Time, limit int) ([]*entity.Account, error) {
	if limit <= 0 {
		return nil, fmt.Errorf("%w: limit must be positive", store.ErrInvalidArgument)
	}

	query := fmt.Sprintf(`
		SELECT %s%s
		ORDER BY h.synced_at ASC NULLS FIRST, a.id
		LIMIT $6`, prefixedAccountColumns("a"), sweepableAccounts)

	wallet, exchange, broker := sweepableTypes()
	rows, err := s.pool.Query(ctx, query,
		wallet, exchange, broker,
		olderThan, now, limit)
	if err != nil {
		return nil, fmt.Errorf("failed to list stale sync targets: %w", err)
	}
	defer rows.Close()

	accounts := make([]*entity.Account, 0, limit)
	for rows.Next() {
		a, err := s.scanAccount(rows)
		if err != nil {
			return nil, fmt.Errorf("failed to scan stale sync target: %w", err)
		}
		accounts = append(accounts, a)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed to iterate stale sync targets: %w", err)
	}
	return accounts, nil
}

// RecordSyncMiss counts a miss and stands the account down, doubling the wait
// per consecutive miss up to cap. It returns the new miss count and the moment
// the sweep may look at this account again.
//
// The arithmetic is in SQL so counting the miss and deciding the wait are one
// statement: read-then-write would let two sweeps racing on the same account
// each read the same count and both write the shorter wait. The POLICY is still
// the caller's — base and cap are passed in, and the sweep is where they are
// documented.
//
// The exponent is clamped before it is used: an account left broken for months
// would otherwise multiply an interval by 2^200 and overflow, turning a very
// long wait into an error.
func (s *PortfolioStore) RecordSyncMiss(ctx context.Context, accountID string, attemptedAt time.Time, base, cap time.Duration) (int, time.Time, error) {
	const query = `
		INSERT INTO account_sync_attempts (account_id, attempted_at, misses, next_attempt_at)
		VALUES ($1, $2::timestamptz, 1,
		        $2::timestamptz + LEAST(make_interval(secs => $3::float8), make_interval(secs => $4::float8)))
		ON CONFLICT (account_id) DO UPDATE
		SET attempted_at    = EXCLUDED.attempted_at,
		    misses          = account_sync_attempts.misses + 1,
		    next_attempt_at = EXCLUDED.attempted_at + LEAST(
		        make_interval(secs => $3::float8 * pow(2, LEAST(account_sync_attempts.misses, 20))),
		        make_interval(secs => $4::float8))
		RETURNING misses, next_attempt_at`

	var (
		misses int
		next   time.Time
	)
	err := s.pool.QueryRow(ctx, query, accountID, attemptedAt, base.Seconds(), cap.Seconds()).
		Scan(&misses, &next)
	if err != nil {
		return 0, time.Time{}, fmt.Errorf("failed to record sync miss: %w", err)
	}
	return misses, next, nil
}

// CountDueSyncTargets counts the accounts a sweep would take if it had no
// budget: stale past the cutoff and not standing down.
//
// It exists because the run line used to report how many accounts it PICKED as
// though that were how many were due — "due 2" with twelve waiting reads like a
// quiet instance rather than a queue nobody is getting through. The number the
// sweep can act on and the number that need acting on are different facts.
func (s *PortfolioStore) CountDueSyncTargets(ctx context.Context, olderThan, now time.Time) (int, error) {
	const query = `SELECT count(*)` + sweepableAccounts

	wallet, exchange, broker := sweepableTypes()
	var n int
	err := s.pool.QueryRow(ctx, query,
		wallet, exchange, broker,
		olderThan, now).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("failed to count due sync targets: %w", err)
	}
	return n, nil
}

// ListSyncDeferrals returns the accounts currently standing down, soonest first,
// with what the sweep knows about each. Restricted to one owner: a deferral is
// operational detail about somebody's credential, not catalogue.
func (s *PortfolioStore) ListSyncDeferrals(ctx context.Context, userID, accountID string) ([]*entity.SyncDeferral, error) {
	query := `
		SELECT a.id, a.name, h.synced_at, s.misses, s.next_attempt_at
		FROM account_sync_attempts s
		JOIN accounts a ON a.id = s.account_id
		LEFT JOIN (
			SELECT account_id, max(updated_at) AS synced_at
			FROM holdings
			GROUP BY account_id
		) h ON h.account_id = a.id
		WHERE a.user_id = $1
		  AND ($2::text = '' OR a.id::text = $2::text)
		ORDER BY s.next_attempt_at, a.id`

	rows, err := s.pool.Query(ctx, query, userID, accountID)
	if err != nil {
		return nil, fmt.Errorf("failed to list sync deferrals: %w", err)
	}
	defer rows.Close()

	var out []*entity.SyncDeferral
	for rows.Next() {
		var (
			d        entity.SyncDeferral
			syncedAt *time.Time
		)
		if err := rows.Scan(&d.AccountID, &d.AccountName, &syncedAt, &d.Misses, &d.NextAttemptAt); err != nil {
			return nil, fmt.Errorf("failed to scan sync deferral: %w", err)
		}
		d.LastSyncedAt = syncedAt
		out = append(out, &d)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed to read sync deferrals: %w", err)
	}
	return out, nil
}

// ClearSyncDeferrals forgives the named accounts and reports how many owed
// anything. Scoped to the owner so one user cannot withdraw another's schedule.
func (s *PortfolioStore) ClearSyncDeferrals(ctx context.Context, userID string, accountIDs []string) (int, error) {
	if len(accountIDs) == 0 {
		return 0, fmt.Errorf("%w: name the accounts to forgive", store.ErrInvalidArgument)
	}

	const query = `
		DELETE FROM account_sync_attempts s
		USING accounts a
		WHERE a.id = s.account_id
		  AND a.user_id = $1
		  AND s.account_id = ANY($2::uuid[])`

	tag, err := s.pool.Exec(ctx, query, userID, accountIDs)
	if err != nil {
		return 0, fmt.Errorf("failed to clear sync deferrals: %w", err)
	}
	return int(tag.RowsAffected()), nil
}

// ClearSyncDeferral forgives whatever an account owes, because it just answered.
//
// It runs on ANY successful sync, including one a person triggered by hand:
// backoff accrued while a credential was broken must not outlive the repair.
// That is the lesson of the price path's own reset — the corruption a fix stops
// producing is not the corruption it removes.
//
// Deleting the row rather than zeroing it keeps "nothing owed" as the absence
// of a record, so the selection query needs no special case for a healthy
// account and the table stays a list of accounts in trouble.
func (s *PortfolioStore) ClearSyncDeferral(ctx context.Context, accountID string) error {
	if _, err := s.pool.Exec(ctx, `DELETE FROM account_sync_attempts WHERE account_id = $1`, accountID); err != nil {
		return fmt.Errorf("failed to clear sync deferral: %w", err)
	}
	return nil
}

// prefixedAccountColumns qualifies the account column list with a table alias,
// so a join can select it without ambiguity.
func prefixedAccountColumns(alias string) string {
	cols := strings.Split(accountColumns, ", ")
	for i, c := range cols {
		cols[i] = alias + "." + c
	}
	return strings.Join(cols, ", ")
}

// ListSystemAccountsByCapability returns accounts whose admin-granted system
// scopes include the given capability, regardless of owner. Used to resolve
// shared provider credentials for user-agnostic operations.
func (s *PortfolioStore) ListSystemAccountsByCapability(ctx context.Context, capability entity.AccountCapability) ([]*entity.Account, error) {
	if capability == "" {
		return nil, fmt.Errorf("%w: capability is required", store.ErrInvalidArgument)
	}
	return s.listAccountsByCapabilityColumn(ctx, "system_scopes", capability, "")
}

// ListUserAccountsByCapability returns the user's own accounts whose
// capabilities include the given capability.
func (s *PortfolioStore) ListUserAccountsByCapability(ctx context.Context, userID string, capability entity.AccountCapability) ([]*entity.Account, error) {
	if capability == "" {
		return nil, fmt.Errorf("%w: capability is required", store.ErrInvalidArgument)
	}
	if userID == "" {
		return nil, fmt.Errorf("%w: user_id is required", store.ErrInvalidArgument)
	}
	return s.listAccountsByCapabilityColumn(ctx, "capabilities", capability, userID)
}

// ListCapabilityOwners returns the ids of users holding at least one account
// with the capability.
//
// Unattended work asks this to find out whether the instance has one operator
// or several. Counting USERS would answer a different question: an instance
// accumulates test accounts, invitees and smoke-test rows, none of which decide
// whether a sweep may use somebody's key. Counting who actually holds a
// credential does.
func (s *PortfolioStore) ListCapabilityOwners(ctx context.Context, capability entity.AccountCapability) ([]string, error) {
	if capability == "" {
		return nil, fmt.Errorf("%w: capability is required", store.ErrInvalidArgument)
	}
	capJSON, err := json.Marshal([]entity.AccountCapability{capability})
	if err != nil {
		return nil, fmt.Errorf("failed to marshal capability: %w", err)
	}

	rows, err := s.pool.Query(ctx, `
		SELECT DISTINCT user_id
		FROM accounts
		WHERE capabilities @> $1 AND user_id IS NOT NULL
		ORDER BY user_id`, capJSON)
	if err != nil {
		return nil, fmt.Errorf("failed to list capability owners: %w", err)
	}
	defer rows.Close()

	owners := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("failed to scan capability owner: %w", err)
		}
		owners = append(owners, id)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed to iterate capability owners: %w", err)
	}
	return owners, nil
}

// listAccountsByCapabilityColumn queries accounts whose jsonb capability
// column contains the capability, optionally scoped to one user.
func (s *PortfolioStore) listAccountsByCapabilityColumn(ctx context.Context, column string, capability entity.AccountCapability, userID string) ([]*entity.Account, error) {
	capJSON, err := json.Marshal([]entity.AccountCapability{capability})
	if err != nil {
		return nil, fmt.Errorf("failed to marshal capability: %w", err)
	}

	args := []any{capJSON}
	where := fmt.Sprintf("%s @> $1", column)
	if userID != "" {
		where += " AND user_id = $2"
		args = append(args, userID)
	}

	query := fmt.Sprintf(`
		SELECT %s
		FROM accounts
		WHERE %s
		ORDER BY id`, accountColumns, where)

	rows, err := s.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to list accounts by capability: %w", err)
	}
	defer rows.Close()

	accounts := []*entity.Account{}
	for rows.Next() {
		a, err := s.scanAccount(rows)
		if err != nil {
			return nil, fmt.Errorf("failed to scan account: %w", err)
		}
		accounts = append(accounts, a)
	}

	return accounts, nil
}

// --- Holding methods ---

// holdingExecutor is the slice of pgx that both a pool and a transaction
// satisfy, so one holding write works the same standing alone or inside a
// sync's snapshot transaction.
type holdingExecutor interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

// holdingTx is the transactional face of the holdings writes: it runs the same
// statements as the store, against one transaction rather than the pool.
type holdingTx struct{ tx pgx.Tx }

var _ portfolio.HoldingWriter = (*holdingTx)(nil)

func (t *holdingTx) CreateHolding(ctx context.Context, h *entity.Holding) (*entity.Holding, error) {
	return createHolding(ctx, t.tx, h)
}

// UpdateHolding returns the row as written rather than re-read: inside a
// transaction the read would see only the uncommitted state anyway, and the
// sync that uses this path discards the result.
func (t *holdingTx) UpdateHolding(ctx context.Context, h *entity.Holding, fields []string) (*entity.Holding, error) {
	if err := updateHolding(ctx, t.tx, h, fields); err != nil {
		return nil, err
	}
	return h, nil
}

// InHoldingsTx runs fn against a transactional holdings writer. A sync rewrites
// a snapshot of an account's positions, and it is a long enough operation to be
// interrupted in the middle: prod 2026-07-25 logged SyncAccount dying at the
// caller's 10s deadline with assets already created and holdings half written,
// which reads as a complete portfolio that is quietly wrong. Either the whole
// set lands or the account keeps the snapshot it had.
func (s *PortfolioStore) InHoldingsTx(ctx context.Context, fn func(portfolio.HoldingWriter) error) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }() // no-op once committed

	if err := fn(&holdingTx{tx: tx}); err != nil {
		return err
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit: %w", err)
	}
	return nil
}

func (s *PortfolioStore) CreateHolding(ctx context.Context, h *entity.Holding) (*entity.Holding, error) {
	return createHolding(ctx, s.pool, h)
}

func createHolding(ctx context.Context, q holdingExecutor, h *entity.Holding) (*entity.Holding, error) {
	if h == nil {
		return nil, fmt.Errorf("%w: holding is required", store.ErrInvalidArgument)
	}
	if h.AssetID == "" {
		return nil, fmt.Errorf("%w: asset_id is required", store.ErrInvalidArgument)
	}
	if h.AccountID == "" {
		return nil, fmt.Errorf("%w: account_id is required", store.ErrInvalidArgument)
	}
	if !h.Source.Valid() {
		return nil, fmt.Errorf("%w: unknown provenance source %q", store.ErrInvalidArgument, h.Source)
	}
	// Unknown ("") is allowed and is the default; a misspelled state is not.
	// The column is a vocabulary, and a typo in it would read as a fifth
	// liquidity class that nothing aggregates.
	if !h.Liquidity.Valid() {
		return nil, fmt.Errorf("%w: unknown liquidity %q", store.ErrInvalidArgument, h.Liquidity)
	}

	id, err := uuid.NewV7()
	if err != nil {
		return nil, fmt.Errorf("failed to generate ID: %w", err)
	}
	h.ID = id.String()

	var portfolioID *string
	if h.PortfolioID != "" {
		portfolioID = &h.PortfolioID
	}
	var importID *string
	if h.ImportID != "" {
		importID = &h.ImportID
	}

	query := `
		INSERT INTO holdings (id, amount, decimals, asset_id, account_id, portfolio_id, chain, liquidity, excluded, excluded_source, source, import_id, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, NOW(), NOW())
		RETURNING created_at, updated_at`

	err = q.QueryRow(ctx, query,
		h.ID,
		h.Amount,
		h.Decimals,
		h.AssetID,
		h.AccountID,
		portfolioID,
		h.Chain,
		string(h.Liquidity),
		h.Excluded,
		string(h.ExcludedSource),
		string(h.Source),
		importID,
	).Scan(&h.CreatedAt, &h.UpdatedAt)
	if err != nil {
		if isConstraintError(err) {
			return nil, fmt.Errorf("%w: %v", store.ErrConstraint, err)
		}
		return nil, fmt.Errorf("failed to create holding: %w", err)
	}

	return h, nil
}

func (s *PortfolioStore) GetHolding(ctx context.Context, id string) (*entity.Holding, error) {
	if id == "" {
		return nil, fmt.Errorf("%w: holding ID is required", store.ErrInvalidArgument)
	}
	if !isValidUUID(id) {
		return nil, fmt.Errorf("%w: invalid holding ID format", store.ErrInvalidArgument)
	}

	query := `
		SELECT id, amount, decimals, asset_id, account_id, portfolio_id, chain, liquidity, excluded, excluded_source, source, import_id, created_at, updated_at
		FROM holdings
		WHERE id = $1`

	var h entity.Holding
	var portfolioID, importID *string

	err := s.pool.QueryRow(ctx, query, id).Scan(
		&h.ID,
		&h.Amount,
		&h.Decimals,
		&h.AssetID,
		&h.AccountID,
		&portfolioID,
		&h.Chain,
		&h.Liquidity,
		&h.Excluded,
		&h.ExcludedSource,
		&h.Source,
		&importID,
		&h.CreatedAt,
		&h.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("%w: holding with ID %s", store.ErrNotFound, id)
		}
		return nil, fmt.Errorf("failed to get holding: %w", err)
	}

	if portfolioID != nil {
		h.PortfolioID = *portfolioID
	}
	if importID != nil {
		h.ImportID = *importID
	}

	return &h, nil
}

func (s *PortfolioStore) UpdateHolding(ctx context.Context, h *entity.Holding, fields []string) (*entity.Holding, error) {
	if err := updateHolding(ctx, s.pool, h, fields); err != nil {
		return nil, err
	}
	return s.GetHolding(ctx, h.ID)
}

func updateHolding(ctx context.Context, q holdingExecutor, h *entity.Holding, fields []string) error {
	if h == nil || h.ID == "" {
		return fmt.Errorf("%w: holding with ID is required", store.ErrInvalidArgument)
	}
	if !isValidUUID(h.ID) {
		return fmt.Errorf("%w: invalid holding ID format", store.ErrInvalidArgument)
	}

	setClauses := []string{"updated_at = NOW()"}
	args := []any{h.ID}
	argIdx := 2

	for _, field := range fields {
		switch field {
		case "amount":
			setClauses = append(setClauses, fmt.Sprintf("amount = $%d", argIdx))
			args = append(args, h.Amount)
			argIdx++
		case "decimals":
			setClauses = append(setClauses, fmt.Sprintf("decimals = $%d", argIdx))
			args = append(args, h.Decimals)
			argIdx++
		case "portfolio_id":
			var portfolioID *string
			if h.PortfolioID != "" {
				portfolioID = &h.PortfolioID
			}
			setClauses = append(setClauses, fmt.Sprintf("portfolio_id = $%d", argIdx))
			args = append(args, portfolioID)
			argIdx++
		case "excluded":
			setClauses = append(setClauses, fmt.Sprintf("excluded = $%d", argIdx))
			args = append(args, h.Excluded)
			argIdx++
		case "excluded_source":
			setClauses = append(setClauses, fmt.Sprintf("excluded_source = $%d", argIdx))
			args = append(args, string(h.ExcludedSource))
			argIdx++
		case "chain":
			setClauses = append(setClauses, fmt.Sprintf("chain = $%d", argIdx))
			args = append(args, h.Chain)
			argIdx++
		case "liquidity":
			setClauses = append(setClauses, fmt.Sprintf("liquidity = $%d", argIdx))
			args = append(args, string(h.Liquidity))
			argIdx++
		}
	}

	query := fmt.Sprintf(`
		UPDATE holdings
		SET %s
		WHERE id = $1`,
		strings.Join(setClauses, ", "))

	result, err := q.Exec(ctx, query, args...)
	if err != nil {
		return fmt.Errorf("failed to update holding: %w", err)
	}

	if result.RowsAffected() == 0 {
		return fmt.Errorf("%w: holding with ID %s", store.ErrNotFound, h.ID)
	}

	return nil
}

func (s *PortfolioStore) DeleteHolding(ctx context.Context, id string) error {
	if id == "" {
		return fmt.Errorf("%w: holding ID is required", store.ErrInvalidArgument)
	}
	if !isValidUUID(id) {
		return fmt.Errorf("%w: invalid holding ID format", store.ErrInvalidArgument)
	}

	result, err := s.pool.Exec(ctx, "DELETE FROM holdings WHERE id = $1", id)
	if err != nil {
		return fmt.Errorf("failed to delete holding: %w", err)
	}

	if result.RowsAffected() == 0 {
		return fmt.Errorf("%w: holding with ID %s", store.ErrNotFound, id)
	}

	return nil
}

func (s *PortfolioStore) ListHoldings(ctx context.Context, opts portfolio.ListHoldingsOpts) ([]*entity.Holding, string, error) {
	limit := opts.PageSize
	if limit <= 0 {
		limit = defaultPageSize
	}

	args := []any{}
	argIdx := 1
	whereClauses := []string{}

	// Scope by owning portfolio (holding's own or inherited from account).
	// Holdings outside any portfolio fall back to the account owner.
	// Revisit when shared portfolio ownership lands: replace the p.user_id
	// check with a portfolio-visibility predicate.
	if opts.UserID != "" {
		whereClauses = append(whereClauses, fmt.Sprintf("(p.user_id = $%d OR (p.id IS NULL AND a.user_id = $%d))", argIdx, argIdx))
		args = append(args, opts.UserID)
		argIdx++
	}

	// Use COALESCE(h.portfolio_id, a.portfolio_id) so holdings with NULL portfolio_id
	// inherit the account's portfolio_id for filtering.
	if opts.PortfolioID != "" {
		whereClauses = append(whereClauses, fmt.Sprintf("COALESCE(h.portfolio_id, a.portfolio_id) = $%d", argIdx))
		args = append(args, opts.PortfolioID)
		argIdx++
	}

	if opts.AccountID != "" {
		whereClauses = append(whereClauses, fmt.Sprintf("h.account_id = $%d", argIdx))
		args = append(args, opts.AccountID)
		argIdx++
	}

	if opts.AssetID != "" {
		whereClauses = append(whereClauses, fmt.Sprintf("h.asset_id = $%d", argIdx))
		args = append(args, opts.AssetID)
		argIdx++
	}

	if opts.HideExcluded {
		whereClauses = append(whereClauses, "h.excluded = false")
	}

	if opts.PageToken != "" {
		decoded, err := base64.StdEncoding.DecodeString(opts.PageToken)
		if err == nil && isValidUUID(string(decoded)) {
			whereClauses = append(whereClauses, fmt.Sprintf("h.id > $%d", argIdx))
			args = append(args, string(decoded))
			argIdx++
		}
	}

	whereClause := ""
	if len(whereClauses) > 0 {
		whereClause = "WHERE " + strings.Join(whereClauses, " AND ")
	}

	query := fmt.Sprintf(`
		SELECT h.id, h.amount, h.decimals, h.asset_id, h.account_id, h.portfolio_id, h.chain, h.liquidity, h.excluded, h.excluded_source, h.source, h.import_id, h.created_at, h.updated_at
		FROM holdings h
		LEFT JOIN accounts a ON a.id = h.account_id
		LEFT JOIN portfolios p ON p.id = COALESCE(h.portfolio_id, a.portfolio_id)
		%s
		ORDER BY h.id
		LIMIT $%d`,
		whereClause, argIdx)
	args = append(args, limit+1)

	rows, err := s.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, "", fmt.Errorf("failed to list holdings: %w", err)
	}
	defer rows.Close()

	holdings := make([]*entity.Holding, 0, limit)
	for rows.Next() {
		var h entity.Holding
		var portfolioID, importID *string

		if err := rows.Scan(
			&h.ID,
			&h.Amount,
			&h.Decimals,
			&h.AssetID,
			&h.AccountID,
			&portfolioID,
			&h.Chain,
			&h.Liquidity,
			&h.Excluded,
			&h.ExcludedSource,
			&h.Source,
			&importID,
			&h.CreatedAt,
			&h.UpdatedAt,
		); err != nil {
			return nil, "", fmt.Errorf("failed to scan holding: %w", err)
		}

		if portfolioID != nil {
			h.PortfolioID = *portfolioID
		}
		if importID != nil {
			h.ImportID = *importID
		}

		holdings = append(holdings, &h)
	}

	var nextPageToken string
	if len(holdings) > limit {
		lastItem := holdings[limit-1]
		holdings = holdings[:limit]
		nextPageToken = base64.StdEncoding.EncodeToString([]byte(lastItem.ID))
	}

	return holdings, nextPageToken, nil
}

// --- Transaction methods ---

func (s *PortfolioStore) CreateTransaction(ctx context.Context, t *entity.Transaction) (*entity.Transaction, error) {
	if t == nil {
		return nil, fmt.Errorf("%w: transaction is required", store.ErrInvalidArgument)
	}
	if t.AccountID == "" {
		return nil, fmt.Errorf("%w: account_id is required", store.ErrInvalidArgument)
	}
	if t.Type == entity.TransactionTypeUnspecified {
		return nil, fmt.Errorf("%w: transaction type is required", store.ErrInvalidArgument)
	}
	if !t.Source.Valid() {
		return nil, fmt.Errorf("%w: unknown provenance source %q", store.ErrInvalidArgument, t.Source)
	}

	id, err := uuid.NewV7()
	if err != nil {
		return nil, fmt.Errorf("failed to generate ID: %w", err)
	}
	t.ID = id.String()

	if t.Status == entity.TransactionStatusUnspecified {
		t.Status = entity.TransactionStatusPending
	}

	var assetID *string
	if t.AssetID != "" {
		assetID = &t.AssetID
	}
	var importID *string
	if t.ImportID != "" {
		importID = &t.ImportID
	}

	dataJSON, err := json.Marshal(t.Data)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal data: %w", err)
	}

	query := `
		INSERT INTO transactions (id, type, status, account_id, asset_transactions, data, source, import_id, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, NOW(), NOW())
		RETURNING created_at, updated_at`

	err = s.pool.QueryRow(ctx, query,
		t.ID,
		transactionTypeToString(t.Type),
		transactionStatusToString(t.Status),
		t.AccountID,
		assetID,
		dataJSON,
		string(t.Source),
		importID,
	).Scan(&t.CreatedAt, &t.UpdatedAt)
	if err != nil {
		if isConstraintError(err) {
			return nil, fmt.Errorf("%w: %v", store.ErrConstraint, err)
		}
		return nil, fmt.Errorf("failed to create transaction: %w", err)
	}

	return t, nil
}

func (s *PortfolioStore) GetTransaction(ctx context.Context, id string) (*entity.Transaction, error) {
	if id == "" {
		return nil, fmt.Errorf("%w: transaction ID is required", store.ErrInvalidArgument)
	}
	if !isValidUUID(id) {
		return nil, fmt.Errorf("%w: invalid transaction ID format", store.ErrInvalidArgument)
	}

	query := `
		SELECT id, type, status, account_id, asset_transactions, data, source, import_id, created_at, updated_at
		FROM transactions
		WHERE id = $1`

	var t entity.Transaction
	var typeStr, statusStr string
	var assetID, importID *string
	var dataJSON []byte

	err := s.pool.QueryRow(ctx, query, id).Scan(
		&t.ID,
		&typeStr,
		&statusStr,
		&t.AccountID,
		&assetID,
		&dataJSON,
		&t.Source,
		&importID,
		&t.CreatedAt,
		&t.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("%w: transaction with ID %s", store.ErrNotFound, id)
		}
		return nil, fmt.Errorf("failed to get transaction: %w", err)
	}

	t.Type = stringToTransactionType(typeStr)
	t.Status = stringToTransactionStatus(statusStr)
	if assetID != nil {
		t.AssetID = *assetID
	}
	if importID != nil {
		t.ImportID = *importID
	}
	if err := json.Unmarshal(dataJSON, &t.Data); err != nil {
		return nil, fmt.Errorf("failed to unmarshal data: %w", err)
	}

	return &t, nil
}

func (s *PortfolioStore) UpdateTransaction(ctx context.Context, t *entity.Transaction, fields []string) (*entity.Transaction, error) {
	if t == nil || t.ID == "" {
		return nil, fmt.Errorf("%w: transaction with ID is required", store.ErrInvalidArgument)
	}
	if !isValidUUID(t.ID) {
		return nil, fmt.Errorf("%w: invalid transaction ID format", store.ErrInvalidArgument)
	}

	setClauses := []string{"updated_at = NOW()"}
	args := []any{t.ID}
	argIdx := 2

	for _, field := range fields {
		switch field {
		case "status":
			setClauses = append(setClauses, fmt.Sprintf("status = $%d", argIdx))
			args = append(args, transactionStatusToString(t.Status))
			argIdx++
		case "data":
			dataJSON, err := json.Marshal(t.Data)
			if err != nil {
				return nil, fmt.Errorf("failed to marshal data: %w", err)
			}
			setClauses = append(setClauses, fmt.Sprintf("data = $%d", argIdx))
			args = append(args, dataJSON)
			argIdx++
		}
	}

	query := fmt.Sprintf(`
		UPDATE transactions
		SET %s
		WHERE id = $1`,
		strings.Join(setClauses, ", "))

	result, err := s.pool.Exec(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to update transaction: %w", err)
	}

	if result.RowsAffected() == 0 {
		return nil, fmt.Errorf("%w: transaction with ID %s", store.ErrNotFound, t.ID)
	}

	return s.GetTransaction(ctx, t.ID)
}

func (s *PortfolioStore) ListTransactions(ctx context.Context, opts portfolio.ListTransactionsOpts) ([]*entity.Transaction, string, error) {
	limit := opts.PageSize
	if limit <= 0 {
		limit = defaultPageSize
	}

	args := []any{}
	argIdx := 1
	whereClauses := []string{}

	// Scope by account owner. Revisit with the shared-ownership ADR (vnr).
	if opts.UserID != "" {
		whereClauses = append(whereClauses, fmt.Sprintf("account_id IN (SELECT id FROM accounts WHERE user_id = $%d)", argIdx))
		args = append(args, opts.UserID)
		argIdx++
	}

	if opts.AccountID != "" {
		whereClauses = append(whereClauses, fmt.Sprintf("account_id = $%d", argIdx))
		args = append(args, opts.AccountID)
		argIdx++
	}

	if opts.AssetID != "" {
		whereClauses = append(whereClauses, fmt.Sprintf("asset_transactions = $%d", argIdx))
		args = append(args, opts.AssetID)
		argIdx++
	}

	if opts.Type != entity.TransactionTypeUnspecified {
		whereClauses = append(whereClauses, fmt.Sprintf("type = $%d", argIdx))
		args = append(args, transactionTypeToString(opts.Type))
		argIdx++
	}

	if opts.Status != entity.TransactionStatusUnspecified {
		whereClauses = append(whereClauses, fmt.Sprintf("status = $%d", argIdx))
		args = append(args, transactionStatusToString(opts.Status))
		argIdx++
	}

	if opts.PageToken != "" {
		decoded, err := base64.StdEncoding.DecodeString(opts.PageToken)
		if err == nil && isValidUUID(string(decoded)) {
			whereClauses = append(whereClauses, fmt.Sprintf("id > $%d", argIdx))
			args = append(args, string(decoded))
			argIdx++
		}
	}

	whereClause := ""
	if len(whereClauses) > 0 {
		whereClause = "WHERE " + strings.Join(whereClauses, " AND ")
	}

	query := fmt.Sprintf(`
		SELECT id, type, status, account_id, asset_transactions, data, source, import_id, created_at, updated_at
		FROM transactions
		%s
		ORDER BY id
		LIMIT $%d`,
		whereClause, argIdx)
	args = append(args, limit+1)

	rows, err := s.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, "", fmt.Errorf("failed to list transactions: %w", err)
	}
	defer rows.Close()

	transactions := make([]*entity.Transaction, 0, limit)
	for rows.Next() {
		var t entity.Transaction
		var typeStr, statusStr string
		var assetID, importID *string
		var dataJSON []byte

		if err := rows.Scan(
			&t.ID,
			&typeStr,
			&statusStr,
			&t.AccountID,
			&assetID,
			&dataJSON,
			&t.Source,
			&importID,
			&t.CreatedAt,
			&t.UpdatedAt,
		); err != nil {
			return nil, "", fmt.Errorf("failed to scan transaction: %w", err)
		}

		t.Type = stringToTransactionType(typeStr)
		t.Status = stringToTransactionStatus(statusStr)
		if assetID != nil {
			t.AssetID = *assetID
		}
		if importID != nil {
			t.ImportID = *importID
		}
		if err := json.Unmarshal(dataJSON, &t.Data); err != nil {
			return nil, "", fmt.Errorf("failed to unmarshal data: %w", err)
		}

		transactions = append(transactions, &t)
	}

	var nextPageToken string
	if len(transactions) > limit {
		lastItem := transactions[limit-1]
		transactions = transactions[:limit]
		nextPageToken = base64.StdEncoding.EncodeToString([]byte(lastItem.ID))
	}

	return transactions, nextPageToken, nil
}

// --- Helper functions ---

func nullableString(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func accountTypeToString(t entity.AccountType) string {
	return t.String()
}

func stringToAccountType(s string) entity.AccountType {
	switch s {
	case "wallet":
		return entity.AccountTypeWallet
	case "exchange":
		return entity.AccountTypeExchange
	case "bank":
		return entity.AccountTypeBank
	case "broker":
		return entity.AccountTypeBroker
	case "service":
		return entity.AccountTypeService
	case "manual":
		return entity.AccountTypeManual
	default:
		return entity.AccountTypeUnspecified
	}
}

func transactionTypeToString(t entity.TransactionType) string {
	switch t {
	case entity.TransactionTypeExtended:
		return "extended"
	case entity.TransactionTypeTrade:
		return "trade"
	case entity.TransactionTypeTransfer:
		return "transfer"
	case entity.TransactionTypeDeposit:
		return "deposit"
	case entity.TransactionTypeWithdrawal:
		return "withdrawal"
	default:
		return "unspecified"
	}
}

func stringToTransactionType(s string) entity.TransactionType {
	switch s {
	case "extended":
		return entity.TransactionTypeExtended
	case "trade":
		return entity.TransactionTypeTrade
	case "transfer":
		return entity.TransactionTypeTransfer
	case "deposit":
		return entity.TransactionTypeDeposit
	case "withdrawal":
		return entity.TransactionTypeWithdrawal
	default:
		return entity.TransactionTypeUnspecified
	}
}

func transactionStatusToString(s entity.TransactionStatus) string {
	switch s {
	case entity.TransactionStatusPending:
		return "pending"
	case entity.TransactionStatusProcessing:
		return "processing"
	case entity.TransactionStatusCompleted:
		return "completed"
	case entity.TransactionStatusFailed:
		return "failed"
	case entity.TransactionStatusCancelled:
		return "cancelled"
	default:
		return "unspecified"
	}
}

func stringToTransactionStatus(s string) entity.TransactionStatus {
	switch s {
	case "pending":
		return entity.TransactionStatusPending
	case "processing":
		return entity.TransactionStatusProcessing
	case "completed":
		return entity.TransactionStatusCompleted
	case "failed":
		return entity.TransactionStatusFailed
	case "cancelled":
		return entity.TransactionStatusCancelled
	default:
		return entity.TransactionStatusUnspecified
	}
}
