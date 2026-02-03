package postgres

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/foxcool/greedy-eye/internal/entity"
	"github.com/foxcool/greedy-eye/internal/service/portfolio"
	"github.com/foxcool/greedy-eye/internal/store"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// PortfolioStore implements portfolio.Store using PostgreSQL.
type PortfolioStore struct {
	pool *pgxpool.Pool
}

// Compile-time interface implementation check.
var _ portfolio.Store = (*PortfolioStore)(nil)

func NewPortfolioStore(pool *pgxpool.Pool) *PortfolioStore {
	return &PortfolioStore{pool: pool}
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

	// Get user internal ID
	userInternalID, err := s.getUserInternalID(ctx, p.UserID)
	if err != nil {
		return nil, err
	}

	p.ID = uuid.New().String()
	dataJSON, err := json.Marshal(p.Data)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal data: %w", err)
	}

	query := `
		INSERT INTO portfolios (uuid, user_id, name, description, data, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, NOW(), NOW())
		RETURNING created_at, updated_at`

	err = s.pool.QueryRow(ctx, query,
		p.ID,
		userInternalID,
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
		SELECT p.uuid, u.uuid, p.name, p.description, p.data, p.created_at, p.updated_at
		FROM portfolios p
		JOIN users u ON p.user_id = u.id
		WHERE p.uuid = $1`

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
		WHERE uuid = $1
		RETURNING uuid, name, description, data, created_at, updated_at`,
		strings.Join(setClauses, ", "))

	var result entity.Portfolio
	var description *string
	var dataJSON []byte

	// We need the user_id separately
	err := s.pool.QueryRow(ctx, query, args...).Scan(
		&result.ID,
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

	// Get user UUID for result
	full, err := s.GetPortfolio(ctx, result.ID)
	if err != nil {
		return nil, err
	}
	result.UserID = full.UserID

	return &result, nil
}

func (s *PortfolioStore) DeletePortfolio(ctx context.Context, id string) error {
	if id == "" {
		return fmt.Errorf("%w: portfolio ID is required", store.ErrInvalidArgument)
	}
	if !isValidUUID(id) {
		return fmt.Errorf("%w: invalid portfolio ID format", store.ErrInvalidArgument)
	}

	result, err := s.pool.Exec(ctx, "DELETE FROM portfolios WHERE uuid = $1", id)
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
		userInternalID, err := s.getUserInternalID(ctx, opts.UserID)
		if err != nil {
			return nil, "", err
		}
		whereClauses = append(whereClauses, fmt.Sprintf("p.user_id = $%d", argIdx))
		args = append(args, userInternalID)
		argIdx++
	}

	if opts.PageToken != "" {
		decoded, err := base64.StdEncoding.DecodeString(opts.PageToken)
		if err == nil && isValidUUID(string(decoded)) {
			whereClauses = append(whereClauses, fmt.Sprintf("p.uuid > $%d", argIdx))
			args = append(args, string(decoded))
			argIdx++
		}
	}

	whereClause := ""
	if len(whereClauses) > 0 {
		whereClause = "WHERE " + strings.Join(whereClauses, " AND ")
	}

	query := fmt.Sprintf(`
		SELECT p.uuid, u.uuid, p.name, p.description, p.data, p.created_at, p.updated_at
		FROM portfolios p
		JOIN users u ON p.user_id = u.id
		%s
		ORDER BY p.uuid
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

	userInternalID, err := s.getUserInternalID(ctx, a.UserID)
	if err != nil {
		return nil, err
	}

	a.ID = uuid.New().String()
	dataJSON, err := json.Marshal(a.Data)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal data: %w", err)
	}

	query := `
		INSERT INTO accounts (uuid, user_id, name, description, type, data, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, NOW(), NOW())
		RETURNING created_at, updated_at`

	err = s.pool.QueryRow(ctx, query,
		a.ID,
		userInternalID,
		a.Name,
		nullableString(a.Description),
		accountTypeToString(a.Type),
		dataJSON,
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

	query := `
		SELECT a.uuid, u.uuid, a.name, a.description, a.type, a.data, a.created_at, a.updated_at
		FROM accounts a
		JOIN users u ON a.user_id = u.id
		WHERE a.uuid = $1`

	var a entity.Account
	var description *string
	var typeStr string
	var dataJSON []byte

	err := s.pool.QueryRow(ctx, query, id).Scan(
		&a.ID,
		&a.UserID,
		&a.Name,
		&description,
		&typeStr,
		&dataJSON,
		&a.CreatedAt,
		&a.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("%w: account with ID %s", store.ErrNotFound, id)
		}
		return nil, fmt.Errorf("failed to get account: %w", err)
	}

	if description != nil {
		a.Description = *description
	}
	a.Type = stringToAccountType(typeStr)
	if err := json.Unmarshal(dataJSON, &a.Data); err != nil {
		return nil, fmt.Errorf("failed to unmarshal data: %w", err)
	}

	return &a, nil
}

func (s *PortfolioStore) UpdateAccount(ctx context.Context, a *entity.Account, fields []string) (*entity.Account, error) {
	if a == nil || a.ID == "" {
		return nil, fmt.Errorf("%w: account with ID is required", store.ErrInvalidArgument)
	}
	if !isValidUUID(a.ID) {
		return nil, fmt.Errorf("%w: invalid account ID format", store.ErrInvalidArgument)
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
			dataJSON, err := json.Marshal(a.Data)
			if err != nil {
				return nil, fmt.Errorf("failed to marshal data: %w", err)
			}
			setClauses = append(setClauses, fmt.Sprintf("data = $%d", argIdx))
			args = append(args, dataJSON)
			argIdx++
		}
	}

	query := fmt.Sprintf(`
		UPDATE accounts
		SET %s
		WHERE uuid = $1`,
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

	result, err := s.pool.Exec(ctx, "DELETE FROM accounts WHERE uuid = $1", id)
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

func (s *PortfolioStore) ListAccounts(ctx context.Context, opts portfolio.ListAccountsOpts) ([]*entity.Account, string, error) {
	limit := opts.PageSize
	if limit <= 0 {
		limit = defaultPageSize
	}

	args := []any{}
	argIdx := 1
	whereClauses := []string{}

	if opts.UserID != "" {
		userInternalID, err := s.getUserInternalID(ctx, opts.UserID)
		if err != nil {
			return nil, "", err
		}
		whereClauses = append(whereClauses, fmt.Sprintf("a.user_id = $%d", argIdx))
		args = append(args, userInternalID)
		argIdx++
	}

	if opts.Type != entity.AccountTypeUnspecified {
		whereClauses = append(whereClauses, fmt.Sprintf("a.type = $%d", argIdx))
		args = append(args, accountTypeToString(opts.Type))
		argIdx++
	}

	if opts.PageToken != "" {
		decoded, err := base64.StdEncoding.DecodeString(opts.PageToken)
		if err == nil && isValidUUID(string(decoded)) {
			whereClauses = append(whereClauses, fmt.Sprintf("a.uuid > $%d", argIdx))
			args = append(args, string(decoded))
			argIdx++
		}
	}

	whereClause := ""
	if len(whereClauses) > 0 {
		whereClause = "WHERE " + strings.Join(whereClauses, " AND ")
	}

	query := fmt.Sprintf(`
		SELECT a.uuid, u.uuid, a.name, a.description, a.type, a.data, a.created_at, a.updated_at
		FROM accounts a
		JOIN users u ON a.user_id = u.id
		%s
		ORDER BY a.uuid
		LIMIT $%d`,
		whereClause, argIdx)
	args = append(args, limit+1)

	rows, err := s.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, "", fmt.Errorf("failed to list accounts: %w", err)
	}
	defer rows.Close()

	accounts := make([]*entity.Account, 0, limit)
	for rows.Next() {
		var a entity.Account
		var description *string
		var typeStr string
		var dataJSON []byte

		if err := rows.Scan(
			&a.ID,
			&a.UserID,
			&a.Name,
			&description,
			&typeStr,
			&dataJSON,
			&a.CreatedAt,
			&a.UpdatedAt,
		); err != nil {
			return nil, "", fmt.Errorf("failed to scan account: %w", err)
		}

		if description != nil {
			a.Description = *description
		}
		a.Type = stringToAccountType(typeStr)
		if err := json.Unmarshal(dataJSON, &a.Data); err != nil {
			return nil, "", fmt.Errorf("failed to unmarshal data: %w", err)
		}

		accounts = append(accounts, &a)
	}

	var nextPageToken string
	if len(accounts) > limit {
		lastItem := accounts[limit-1]
		accounts = accounts[:limit]
		nextPageToken = base64.StdEncoding.EncodeToString([]byte(lastItem.ID))
	}

	return accounts, nextPageToken, nil
}

// --- Holding methods ---

func (s *PortfolioStore) CreateHolding(ctx context.Context, h *entity.Holding) (*entity.Holding, error) {
	if h == nil {
		return nil, fmt.Errorf("%w: holding is required", store.ErrInvalidArgument)
	}
	if h.AssetID == "" {
		return nil, fmt.Errorf("%w: asset_id is required", store.ErrInvalidArgument)
	}
	if h.AccountID == "" {
		return nil, fmt.Errorf("%w: account_id is required", store.ErrInvalidArgument)
	}

	assetInternalID, err := s.getAssetInternalID(ctx, h.AssetID)
	if err != nil {
		return nil, err
	}

	accountInternalID, err := s.getAccountInternalID(ctx, h.AccountID)
	if err != nil {
		return nil, err
	}

	var portfolioInternalID *int64
	if h.PortfolioID != "" {
		id, err := s.getPortfolioInternalID(ctx, h.PortfolioID)
		if err != nil {
			return nil, err
		}
		portfolioInternalID = &id
	}

	h.ID = uuid.New().String()

	query := `
		INSERT INTO holdings (uuid, amount, decimals, asset_id, account_id, portfolio_id, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, NOW(), NOW())
		RETURNING created_at, updated_at`

	err = s.pool.QueryRow(ctx, query,
		h.ID,
		h.Amount,
		h.Decimals,
		assetInternalID,
		accountInternalID,
		portfolioInternalID,
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
		SELECT h.uuid, h.amount, h.decimals, a.uuid, acc.uuid, p.uuid, h.created_at, h.updated_at
		FROM holdings h
		JOIN assets a ON h.asset_id = a.id
		JOIN accounts acc ON h.account_id = acc.id
		LEFT JOIN portfolios p ON h.portfolio_id = p.id
		WHERE h.uuid = $1`

	var h entity.Holding
	var portfolioID *string

	err := s.pool.QueryRow(ctx, query, id).Scan(
		&h.ID,
		&h.Amount,
		&h.Decimals,
		&h.AssetID,
		&h.AccountID,
		&portfolioID,
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

	return &h, nil
}

func (s *PortfolioStore) UpdateHolding(ctx context.Context, h *entity.Holding, fields []string) (*entity.Holding, error) {
	if h == nil || h.ID == "" {
		return nil, fmt.Errorf("%w: holding with ID is required", store.ErrInvalidArgument)
	}
	if !isValidUUID(h.ID) {
		return nil, fmt.Errorf("%w: invalid holding ID format", store.ErrInvalidArgument)
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
			if h.PortfolioID == "" {
				setClauses = append(setClauses, fmt.Sprintf("portfolio_id = $%d", argIdx))
				args = append(args, nil)
			} else {
				portfolioInternalID, err := s.getPortfolioInternalID(ctx, h.PortfolioID)
				if err != nil {
					return nil, err
				}
				setClauses = append(setClauses, fmt.Sprintf("portfolio_id = $%d", argIdx))
				args = append(args, portfolioInternalID)
			}
			argIdx++
		}
	}

	query := fmt.Sprintf(`
		UPDATE holdings
		SET %s
		WHERE uuid = $1`,
		strings.Join(setClauses, ", "))

	result, err := s.pool.Exec(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to update holding: %w", err)
	}

	if result.RowsAffected() == 0 {
		return nil, fmt.Errorf("%w: holding with ID %s", store.ErrNotFound, h.ID)
	}

	return s.GetHolding(ctx, h.ID)
}

func (s *PortfolioStore) DeleteHolding(ctx context.Context, id string) error {
	if id == "" {
		return fmt.Errorf("%w: holding ID is required", store.ErrInvalidArgument)
	}
	if !isValidUUID(id) {
		return fmt.Errorf("%w: invalid holding ID format", store.ErrInvalidArgument)
	}

	result, err := s.pool.Exec(ctx, "DELETE FROM holdings WHERE uuid = $1", id)
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

	if opts.PortfolioID != "" {
		portfolioInternalID, err := s.getPortfolioInternalID(ctx, opts.PortfolioID)
		if err != nil {
			return nil, "", err
		}
		whereClauses = append(whereClauses, fmt.Sprintf("h.portfolio_id = $%d", argIdx))
		args = append(args, portfolioInternalID)
		argIdx++
	}

	if opts.AccountID != "" {
		accountInternalID, err := s.getAccountInternalID(ctx, opts.AccountID)
		if err != nil {
			return nil, "", err
		}
		whereClauses = append(whereClauses, fmt.Sprintf("h.account_id = $%d", argIdx))
		args = append(args, accountInternalID)
		argIdx++
	}

	if opts.AssetID != "" {
		assetInternalID, err := s.getAssetInternalID(ctx, opts.AssetID)
		if err != nil {
			return nil, "", err
		}
		whereClauses = append(whereClauses, fmt.Sprintf("h.asset_id = $%d", argIdx))
		args = append(args, assetInternalID)
		argIdx++
	}

	if opts.PageToken != "" {
		decoded, err := base64.StdEncoding.DecodeString(opts.PageToken)
		if err == nil && isValidUUID(string(decoded)) {
			whereClauses = append(whereClauses, fmt.Sprintf("h.uuid > $%d", argIdx))
			args = append(args, string(decoded))
			argIdx++
		}
	}

	whereClause := ""
	if len(whereClauses) > 0 {
		whereClause = "WHERE " + strings.Join(whereClauses, " AND ")
	}

	query := fmt.Sprintf(`
		SELECT h.uuid, h.amount, h.decimals, a.uuid, acc.uuid, p.uuid, h.created_at, h.updated_at
		FROM holdings h
		JOIN assets a ON h.asset_id = a.id
		JOIN accounts acc ON h.account_id = acc.id
		LEFT JOIN portfolios p ON h.portfolio_id = p.id
		%s
		ORDER BY h.uuid
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
		var portfolioID *string

		if err := rows.Scan(
			&h.ID,
			&h.Amount,
			&h.Decimals,
			&h.AssetID,
			&h.AccountID,
			&portfolioID,
			&h.CreatedAt,
			&h.UpdatedAt,
		); err != nil {
			return nil, "", fmt.Errorf("failed to scan holding: %w", err)
		}

		if portfolioID != nil {
			h.PortfolioID = *portfolioID
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

	accountInternalID, err := s.getAccountInternalID(ctx, t.AccountID)
	if err != nil {
		return nil, err
	}

	var assetInternalID *int64
	if t.AssetID != "" {
		id, err := s.getAssetInternalID(ctx, t.AssetID)
		if err != nil {
			return nil, err
		}
		assetInternalID = &id
	}

	t.ID = uuid.New().String()
	if t.Status == entity.TransactionStatusUnspecified {
		t.Status = entity.TransactionStatusPending
	}

	dataJSON, err := json.Marshal(t.Data)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal data: %w", err)
	}

	query := `
		INSERT INTO transactions (uuid, type, status, account_id, asset_transactions, data, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, NOW(), NOW())
		RETURNING created_at, updated_at`

	err = s.pool.QueryRow(ctx, query,
		t.ID,
		transactionTypeToString(t.Type),
		transactionStatusToString(t.Status),
		accountInternalID,
		assetInternalID,
		dataJSON,
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
		SELECT t.uuid, t.type, t.status, acc.uuid, a.uuid, t.data, t.created_at, t.updated_at
		FROM transactions t
		JOIN accounts acc ON t.account_id = acc.id
		LEFT JOIN assets a ON t.asset_transactions = a.id
		WHERE t.uuid = $1`

	var t entity.Transaction
	var typeStr, statusStr string
	var assetID *string
	var dataJSON []byte

	err := s.pool.QueryRow(ctx, query, id).Scan(
		&t.ID,
		&typeStr,
		&statusStr,
		&t.AccountID,
		&assetID,
		&dataJSON,
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
		WHERE uuid = $1`,
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

	if opts.AccountID != "" {
		accountInternalID, err := s.getAccountInternalID(ctx, opts.AccountID)
		if err != nil {
			return nil, "", err
		}
		whereClauses = append(whereClauses, fmt.Sprintf("t.account_id = $%d", argIdx))
		args = append(args, accountInternalID)
		argIdx++
	}

	if opts.AssetID != "" {
		assetInternalID, err := s.getAssetInternalID(ctx, opts.AssetID)
		if err != nil {
			return nil, "", err
		}
		whereClauses = append(whereClauses, fmt.Sprintf("t.asset_transactions = $%d", argIdx))
		args = append(args, assetInternalID)
		argIdx++
	}

	if opts.Type != entity.TransactionTypeUnspecified {
		whereClauses = append(whereClauses, fmt.Sprintf("t.type = $%d", argIdx))
		args = append(args, transactionTypeToString(opts.Type))
		argIdx++
	}

	if opts.Status != entity.TransactionStatusUnspecified {
		whereClauses = append(whereClauses, fmt.Sprintf("t.status = $%d", argIdx))
		args = append(args, transactionStatusToString(opts.Status))
		argIdx++
	}

	if opts.PageToken != "" {
		decoded, err := base64.StdEncoding.DecodeString(opts.PageToken)
		if err == nil && isValidUUID(string(decoded)) {
			whereClauses = append(whereClauses, fmt.Sprintf("t.uuid > $%d", argIdx))
			args = append(args, string(decoded))
			argIdx++
		}
	}

	whereClause := ""
	if len(whereClauses) > 0 {
		whereClause = "WHERE " + strings.Join(whereClauses, " AND ")
	}

	query := fmt.Sprintf(`
		SELECT t.uuid, t.type, t.status, acc.uuid, a.uuid, t.data, t.created_at, t.updated_at
		FROM transactions t
		JOIN accounts acc ON t.account_id = acc.id
		LEFT JOIN assets a ON t.asset_transactions = a.id
		%s
		ORDER BY t.uuid
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
		var assetID *string
		var dataJSON []byte

		if err := rows.Scan(
			&t.ID,
			&typeStr,
			&statusStr,
			&t.AccountID,
			&assetID,
			&dataJSON,
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

// --- Helper methods ---

func (s *PortfolioStore) getUserInternalID(ctx context.Context, uuid string) (int64, error) {
	if !isValidUUID(uuid) {
		return 0, fmt.Errorf("%w: invalid user ID format", store.ErrInvalidArgument)
	}

	var id int64
	err := s.pool.QueryRow(ctx, "SELECT id FROM users WHERE uuid = $1", uuid).Scan(&id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return 0, fmt.Errorf("%w: user not found", store.ErrNotFound)
		}
		return 0, fmt.Errorf("failed to get user: %w", err)
	}
	return id, nil
}

func (s *PortfolioStore) getAccountInternalID(ctx context.Context, uuid string) (int64, error) {
	if !isValidUUID(uuid) {
		return 0, fmt.Errorf("%w: invalid account ID format", store.ErrInvalidArgument)
	}

	var id int64
	err := s.pool.QueryRow(ctx, "SELECT id FROM accounts WHERE uuid = $1", uuid).Scan(&id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return 0, fmt.Errorf("%w: account not found", store.ErrNotFound)
		}
		return 0, fmt.Errorf("failed to get account: %w", err)
	}
	return id, nil
}

func (s *PortfolioStore) getPortfolioInternalID(ctx context.Context, uuid string) (int64, error) {
	if !isValidUUID(uuid) {
		return 0, fmt.Errorf("%w: invalid portfolio ID format", store.ErrInvalidArgument)
	}

	var id int64
	err := s.pool.QueryRow(ctx, "SELECT id FROM portfolios WHERE uuid = $1", uuid).Scan(&id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return 0, fmt.Errorf("%w: portfolio not found", store.ErrNotFound)
		}
		return 0, fmt.Errorf("failed to get portfolio: %w", err)
	}
	return id, nil
}

func (s *PortfolioStore) getAssetInternalID(ctx context.Context, uuid string) (int64, error) {
	if !isValidUUID(uuid) {
		return 0, fmt.Errorf("%w: invalid asset ID format", store.ErrInvalidArgument)
	}

	var id int64
	err := s.pool.QueryRow(ctx, "SELECT id FROM assets WHERE uuid = $1", uuid).Scan(&id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return 0, fmt.Errorf("%w: asset not found", store.ErrNotFound)
		}
		return 0, fmt.Errorf("failed to get asset: %w", err)
	}
	return id, nil
}

func nullableString(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func accountTypeToString(t entity.AccountType) string {
	switch t {
	case entity.AccountTypeWallet:
		return "wallet"
	case entity.AccountTypeExchange:
		return "exchange"
	case entity.AccountTypeBank:
		return "bank"
	case entity.AccountTypeBroker:
		return "broker"
	default:
		return "unspecified"
	}
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
