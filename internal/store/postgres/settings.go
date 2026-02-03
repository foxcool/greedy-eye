package postgres

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/foxcool/greedy-eye/internal/entity"
	"github.com/foxcool/greedy-eye/internal/service/settings"
	"github.com/foxcool/greedy-eye/internal/store"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// SettingsStore implements settings.Store using PostgreSQL.
type SettingsStore struct {
	pool *pgxpool.Pool
}

// Compile-time interface implementation check.
var _ settings.Store = (*SettingsStore)(nil)

func NewSettingsStore(pool *pgxpool.Pool) *SettingsStore {
	return &SettingsStore{pool: pool}
}

func (s *SettingsStore) CreateUser(ctx context.Context, u *entity.User) (*entity.User, error) {
	if u == nil {
		return nil, fmt.Errorf("%w: user is required", store.ErrInvalidArgument)
	}
	if u.Email == "" {
		return nil, fmt.Errorf("%w: email is required", store.ErrInvalidArgument)
	}
	if u.Name == "" {
		return nil, fmt.Errorf("%w: name is required", store.ErrInvalidArgument)
	}

	u.ID = uuid.New().String()
	prefsJSON := u.Preferences
	if prefsJSON == nil {
		prefsJSON = json.RawMessage("{}")
	}

	query := `
		INSERT INTO users (uuid, email, name, preferences, created_at, updated_at)
		VALUES ($1, $2, $3, $4, NOW(), NOW())
		RETURNING created_at, updated_at`

	err := s.pool.QueryRow(ctx, query,
		u.ID,
		u.Email,
		u.Name,
		prefsJSON,
	).Scan(&u.CreatedAt, &u.UpdatedAt)
	if err != nil {
		if isConstraintError(err) {
			return nil, fmt.Errorf("%w: user with this email already exists", store.ErrConstraint)
		}
		return nil, fmt.Errorf("failed to create user: %w", err)
	}

	return u, nil
}

func (s *SettingsStore) GetUser(ctx context.Context, id string) (*entity.User, error) {
	if id == "" {
		return nil, fmt.Errorf("%w: user ID is required", store.ErrInvalidArgument)
	}
	if !isValidUUID(id) {
		return nil, fmt.Errorf("%w: invalid user ID format", store.ErrInvalidArgument)
	}

	query := `
		SELECT uuid, email, name, preferences, created_at, updated_at
		FROM users
		WHERE uuid = $1`

	var u entity.User
	err := s.pool.QueryRow(ctx, query, id).Scan(
		&u.ID,
		&u.Email,
		&u.Name,
		&u.Preferences,
		&u.CreatedAt,
		&u.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("%w: user with ID %s", store.ErrNotFound, id)
		}
		return nil, fmt.Errorf("failed to get user: %w", err)
	}

	return &u, nil
}

func (s *SettingsStore) GetUserByEmail(ctx context.Context, email string) (*entity.User, error) {
	if email == "" {
		return nil, fmt.Errorf("%w: email is required", store.ErrInvalidArgument)
	}

	query := `
		SELECT uuid, email, name, preferences, created_at, updated_at
		FROM users
		WHERE email = $1`

	var u entity.User
	err := s.pool.QueryRow(ctx, query, email).Scan(
		&u.ID,
		&u.Email,
		&u.Name,
		&u.Preferences,
		&u.CreatedAt,
		&u.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("%w: user with email %s", store.ErrNotFound, email)
		}
		return nil, fmt.Errorf("failed to get user: %w", err)
	}

	return &u, nil
}

func (s *SettingsStore) UpdateUser(ctx context.Context, u *entity.User, fields []string) (*entity.User, error) {
	if u == nil || u.ID == "" {
		return nil, fmt.Errorf("%w: user with ID is required", store.ErrInvalidArgument)
	}
	if !isValidUUID(u.ID) {
		return nil, fmt.Errorf("%w: invalid user ID format", store.ErrInvalidArgument)
	}

	setClauses := []string{"updated_at = NOW()"}
	args := []any{u.ID}
	argIdx := 2

	for _, field := range fields {
		switch field {
		case "email":
			setClauses = append(setClauses, fmt.Sprintf("email = $%d", argIdx))
			args = append(args, u.Email)
			argIdx++
		case "name":
			setClauses = append(setClauses, fmt.Sprintf("name = $%d", argIdx))
			args = append(args, u.Name)
			argIdx++
		case "preferences":
			setClauses = append(setClauses, fmt.Sprintf("preferences = $%d", argIdx))
			args = append(args, u.Preferences)
			argIdx++
		}
	}

	query := fmt.Sprintf(`
		UPDATE users
		SET %s
		WHERE uuid = $1
		RETURNING uuid, email, name, preferences, created_at, updated_at`,
		strings.Join(setClauses, ", "))

	var result entity.User
	err := s.pool.QueryRow(ctx, query, args...).Scan(
		&result.ID,
		&result.Email,
		&result.Name,
		&result.Preferences,
		&result.CreatedAt,
		&result.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("%w: user with ID %s", store.ErrNotFound, u.ID)
		}
		if isConstraintError(err) {
			return nil, fmt.Errorf("%w: email already exists", store.ErrConstraint)
		}
		return nil, fmt.Errorf("failed to update user: %w", err)
	}

	return &result, nil
}

func (s *SettingsStore) DeleteUser(ctx context.Context, id string) error {
	if id == "" {
		return fmt.Errorf("%w: user ID is required", store.ErrInvalidArgument)
	}
	if !isValidUUID(id) {
		return fmt.Errorf("%w: invalid user ID format", store.ErrInvalidArgument)
	}

	result, err := s.pool.Exec(ctx, "DELETE FROM users WHERE uuid = $1", id)
	if err != nil {
		if isConstraintError(err) {
			return fmt.Errorf("%w: cannot delete user due to existing dependencies", store.ErrConstraint)
		}
		return fmt.Errorf("failed to delete user: %w", err)
	}

	if result.RowsAffected() == 0 {
		return fmt.Errorf("%w: user with ID %s", store.ErrNotFound, id)
	}

	return nil
}

func (s *SettingsStore) ListUsers(ctx context.Context, opts settings.ListUsersOpts) ([]*entity.User, string, error) {
	limit := opts.PageSize
	if limit <= 0 {
		limit = defaultPageSize
	}

	args := []any{}
	argIdx := 1
	whereClauses := []string{}

	if opts.PageToken != "" {
		decoded, err := base64.StdEncoding.DecodeString(opts.PageToken)
		if err == nil && isValidUUID(string(decoded)) {
			whereClauses = append(whereClauses, fmt.Sprintf("uuid > $%d", argIdx))
			args = append(args, string(decoded))
			argIdx++
		}
	}

	whereClause := ""
	if len(whereClauses) > 0 {
		whereClause = "WHERE " + strings.Join(whereClauses, " AND ")
	}

	query := fmt.Sprintf(`
		SELECT uuid, email, name, preferences, created_at, updated_at
		FROM users
		%s
		ORDER BY uuid
		LIMIT $%d`,
		whereClause, argIdx)
	args = append(args, limit+1)

	rows, err := s.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, "", fmt.Errorf("failed to list users: %w", err)
	}
	defer rows.Close()

	users := make([]*entity.User, 0, limit)
	for rows.Next() {
		var u entity.User
		if err := rows.Scan(
			&u.ID,
			&u.Email,
			&u.Name,
			&u.Preferences,
			&u.CreatedAt,
			&u.UpdatedAt,
		); err != nil {
			return nil, "", fmt.Errorf("failed to scan user: %w", err)
		}
		users = append(users, &u)
	}

	var nextPageToken string
	if len(users) > limit {
		lastItem := users[limit-1]
		users = users[:limit]
		nextPageToken = base64.StdEncoding.EncodeToString([]byte(lastItem.ID))
	}

	return users, nextPageToken, nil
}
