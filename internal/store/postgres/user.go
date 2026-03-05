package postgres

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/foxcool/greedy-eye/internal/entity"
	userservice "github.com/foxcool/greedy-eye/internal/service/user"
	"github.com/foxcool/greedy-eye/internal/store"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// UserStore implements user.Store using PostgreSQL.
type UserStore struct {
	pool *pgxpool.Pool
}

// Compile-time interface implementation check.
var _ userservice.Store = (*UserStore)(nil)

func NewUserStore(pool *pgxpool.Pool) *UserStore {
	return &UserStore{pool: pool}
}

// GetOrCreate returns an existing user by id or creates a new one using email as the name default.
func (s *UserStore) GetOrCreate(ctx context.Context, id string, email string) (*entity.User, error) {
	if !isValidUUID(id) {
		return nil, fmt.Errorf("%w: invalid user ID format", store.ErrInvalidArgument)
	}

	_, err := s.pool.Exec(ctx, `
		INSERT INTO users (id, email, name, preferences, created_at, updated_at)
		VALUES ($1, $2, $2, '{}', NOW(), NOW())
		ON CONFLICT (id) DO NOTHING`,
		id, email,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to upsert user: %w", err)
	}

	var u entity.User
	err = s.pool.QueryRow(ctx, `
		SELECT id, email, name, preferences, created_at, updated_at
		FROM users WHERE id = $1`, id,
	).Scan(&u.ID, &u.Email, &u.Name, &u.Preferences, &u.CreatedAt, &u.UpdatedAt)
	if err != nil {
		return nil, fmt.Errorf("failed to get user after upsert: %w", err)
	}

	return &u, nil
}

func (s *UserStore) CreateUser(ctx context.Context, u *entity.User) (*entity.User, error) {
	if u == nil {
		return nil, fmt.Errorf("%w: user is required", store.ErrInvalidArgument)
	}
	if u.Email == "" {
		return nil, fmt.Errorf("%w: email is required", store.ErrInvalidArgument)
	}
	if u.Name == "" {
		return nil, fmt.Errorf("%w: name is required", store.ErrInvalidArgument)
	}

	id, err := uuid.NewV7()
	if err != nil {
		return nil, fmt.Errorf("failed to generate ID: %w", err)
	}
	u.ID = id.String()

	prefsJSON := u.Preferences
	if prefsJSON == nil {
		prefsJSON = json.RawMessage("{}")
	}

	query := `
		INSERT INTO users (id, email, name, preferences, created_at, updated_at)
		VALUES ($1, $2, $3, $4, NOW(), NOW())
		RETURNING created_at, updated_at`

	err = s.pool.QueryRow(ctx, query,
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

func (s *UserStore) GetUser(ctx context.Context, id string) (*entity.User, error) {
	if id == "" {
		return nil, fmt.Errorf("%w: user ID is required", store.ErrInvalidArgument)
	}
	if !isValidUUID(id) {
		return nil, fmt.Errorf("%w: invalid user ID format", store.ErrInvalidArgument)
	}

	var u entity.User
	err := s.pool.QueryRow(ctx, `
		SELECT id, email, name, preferences, created_at, updated_at
		FROM users WHERE id = $1`, id,
	).Scan(&u.ID, &u.Email, &u.Name, &u.Preferences, &u.CreatedAt, &u.UpdatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("%w: user with ID %s", store.ErrNotFound, id)
		}
		return nil, fmt.Errorf("failed to get user: %w", err)
	}

	return &u, nil
}

func (s *UserStore) GetUserByEmail(ctx context.Context, email string) (*entity.User, error) {
	if email == "" {
		return nil, fmt.Errorf("%w: email is required", store.ErrInvalidArgument)
	}

	var u entity.User
	err := s.pool.QueryRow(ctx, `
		SELECT id, email, name, preferences, created_at, updated_at
		FROM users WHERE email = $1`, email,
	).Scan(&u.ID, &u.Email, &u.Name, &u.Preferences, &u.CreatedAt, &u.UpdatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("%w: user with email %s", store.ErrNotFound, email)
		}
		return nil, fmt.Errorf("failed to get user: %w", err)
	}

	return &u, nil
}

func (s *UserStore) UpdateUser(ctx context.Context, u *entity.User, fields []string) (*entity.User, error) {
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
		UPDATE users SET %s
		WHERE id = $1
		RETURNING id, email, name, preferences, created_at, updated_at`,
		strings.Join(setClauses, ", "))

	var result entity.User
	err := s.pool.QueryRow(ctx, query, args...).Scan(
		&result.ID, &result.Email, &result.Name, &result.Preferences, &result.CreatedAt, &result.UpdatedAt,
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

func (s *UserStore) DeleteUser(ctx context.Context, id string) error {
	if id == "" {
		return fmt.Errorf("%w: user ID is required", store.ErrInvalidArgument)
	}
	if !isValidUUID(id) {
		return fmt.Errorf("%w: invalid user ID format", store.ErrInvalidArgument)
	}

	result, err := s.pool.Exec(ctx, "DELETE FROM users WHERE id = $1", id)
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

func (s *UserStore) ListUsers(ctx context.Context, opts userservice.ListUsersOpts) ([]*entity.User, string, error) {
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
		SELECT id, email, name, preferences, created_at, updated_at
		FROM users %s ORDER BY id LIMIT $%d`,
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
		if err := rows.Scan(&u.ID, &u.Email, &u.Name, &u.Preferences, &u.CreatedAt, &u.UpdatedAt); err != nil {
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
