package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/foxcool/greedy-eye/internal/entity"
	settingsservice "github.com/foxcool/greedy-eye/internal/service/settings"
	"github.com/foxcool/greedy-eye/internal/store"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// SettingsStore implements settings.Store using PostgreSQL.
type SettingsStore struct {
	pool *pgxpool.Pool
}

// Compile-time interface implementation check.
var _ settingsservice.Store = (*SettingsStore)(nil)

func NewSettingsStore(pool *pgxpool.Pool) *SettingsStore {
	return &SettingsStore{pool: pool}
}

// GetUserSetting returns one setting of one user.
//
// The user id is part of the lookup rather than a filter applied afterwards:
// the primary key is (user_id, key), so a query that forgets the owner cannot
// accidentally match another user's row.
func (s *SettingsStore) GetUserSetting(ctx context.Context, userID, key string) (*entity.UserSetting, error) {
	if !isValidUUID(userID) {
		return nil, fmt.Errorf("%w: invalid user ID format", store.ErrInvalidArgument)
	}
	if key == "" {
		return nil, fmt.Errorf("%w: key is required", store.ErrInvalidArgument)
	}

	setting := entity.UserSetting{UserID: userID, Key: key}
	err := s.pool.QueryRow(ctx, `
		SELECT value::text, updated_at
		FROM user_settings
		WHERE user_id = $1 AND key = $2`, userID, key,
	).Scan(&setting.Value, &setting.UpdatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("%w: setting %s", store.ErrNotFound, key)
		}
		return nil, fmt.Errorf("failed to get user setting: %w", err)
	}
	return &setting, nil
}

// SetUserSetting writes the setting and returns the row as stored.
//
// The value is cast to jsonb by the database rather than trusted as text: the
// column's type is the last place a malformed value can still be caught, and a
// setting that fails to parse must not reach storage where every later read
// inherits it.
func (s *SettingsStore) SetUserSetting(ctx context.Context, in *entity.UserSetting) (*entity.UserSetting, error) {
	if in == nil {
		return nil, fmt.Errorf("%w: setting is required", store.ErrInvalidArgument)
	}
	if !isValidUUID(in.UserID) {
		return nil, fmt.Errorf("%w: invalid user ID format", store.ErrInvalidArgument)
	}
	if in.Key == "" {
		return nil, fmt.Errorf("%w: key is required", store.ErrInvalidArgument)
	}

	out := entity.UserSetting{UserID: in.UserID, Key: in.Key}
	err := s.pool.QueryRow(ctx, `
		INSERT INTO user_settings (user_id, key, value, updated_at)
		VALUES ($1, $2, $3::jsonb, NOW())
		ON CONFLICT (user_id, key) DO UPDATE
			SET value = EXCLUDED.value, updated_at = NOW()
		RETURNING value::text, updated_at`,
		in.UserID, in.Key, in.Value,
	).Scan(&out.Value, &out.UpdatedAt)
	if err != nil {
		return nil, fmt.Errorf("failed to set user setting: %w", err)
	}
	return &out, nil
}
