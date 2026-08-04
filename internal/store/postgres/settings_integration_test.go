//go:build integration

package postgres

import (
	"context"
	"testing"

	"github.com/foxcool/greedy-eye/internal/entity"
	"github.com/foxcool/greedy-eye/internal/store"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestUserSettingRoundtrip(t *testing.T) {
	pool := getTestPool(t)
	users := NewUserStore(pool)
	s := NewSettingsStore(pool)
	ctx := context.Background()

	user := createTestUser(t, users)
	const layout = `{"version": 1, "widgets": [{"id": "a", "order": 2}]}`

	written, err := s.SetUserSetting(ctx, &entity.UserSetting{
		UserID: user.ID, Key: "dashboard.v1", Value: layout,
	})
	require.NoError(t, err)
	assert.NotZero(t, written.UpdatedAt)

	read, err := s.GetUserSetting(ctx, user.ID, "dashboard.v1")
	require.NoError(t, err)

	// jsonb normalizes whitespace, so the bytes are not compared — the document
	// is. What must survive is the value's structure and its number types: an
	// order of 2 must not come back as 2.0.
	assert.JSONEq(t, layout, read.Value)
	assert.Contains(t, read.Value, `"order": 2`, "jsonb must not retype an integer")
	assert.Equal(t, user.ID, read.UserID)
	assert.Equal(t, written.UpdatedAt.UTC(), read.UpdatedAt.UTC())
}

func TestUserSettingSecondWriteReplacesAndRestamps(t *testing.T) {
	pool := getTestPool(t)
	s := NewSettingsStore(pool)
	ctx := context.Background()

	user := createTestUser(t, NewUserStore(pool))

	first, err := s.SetUserSetting(ctx, &entity.UserSetting{
		UserID: user.ID, Key: "dashboard.v1", Value: `{"widgets": []}`,
	})
	require.NoError(t, err)

	second, err := s.SetUserSetting(ctx, &entity.UserSetting{
		UserID: user.ID, Key: "dashboard.v1", Value: `{"widgets": ["x"]}`,
	})
	require.NoError(t, err)

	// One row per (user, key): the upsert replaces rather than appending, and
	// the timestamp moves so a client can tell a stored write from a stale one.
	assert.JSONEq(t, `{"widgets": ["x"]}`, second.Value)
	assert.False(t, second.UpdatedAt.Before(first.UpdatedAt))

	var rows int
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT count(*) FROM user_settings WHERE user_id = $1`, user.ID).Scan(&rows))
	assert.Equal(t, 1, rows)
}

func TestUserSettingIsScopedToItsOwner(t *testing.T) {
	pool := getTestPool(t)
	users := NewUserStore(pool)
	s := NewSettingsStore(pool)
	ctx := context.Background()

	alice := createTestUser(t, users)
	bob := createTestUser(t, users)

	_, err := s.SetUserSetting(ctx, &entity.UserSetting{
		UserID: alice.ID, Key: "dashboard.v1", Value: `{"owner": "alice"}`,
	})
	require.NoError(t, err)

	// Same key, different owner: two rows that must never be confused for one.
	_, err = s.GetUserSetting(ctx, bob.ID, "dashboard.v1")
	require.ErrorIs(t, err, store.ErrNotFound)

	_, err = s.SetUserSetting(ctx, &entity.UserSetting{
		UserID: bob.ID, Key: "dashboard.v1", Value: `{"owner": "bob"}`,
	})
	require.NoError(t, err)

	hers, err := s.GetUserSetting(ctx, alice.ID, "dashboard.v1")
	require.NoError(t, err)
	assert.JSONEq(t, `{"owner": "alice"}`, hers.Value)
}

func TestUserSettingUnsetKeyIsNotFound(t *testing.T) {
	pool := getTestPool(t)
	s := NewSettingsStore(pool)
	ctx := context.Background()

	user := createTestUser(t, NewUserStore(pool))

	_, err := s.GetUserSetting(ctx, user.ID, "dashboard.v1")
	require.ErrorIs(t, err, store.ErrNotFound)
}

func TestUserSettingRejectsBadInput(t *testing.T) {
	pool := getTestPool(t)
	s := NewSettingsStore(pool)
	ctx := context.Background()

	user := createTestUser(t, NewUserStore(pool))

	_, err := s.GetUserSetting(ctx, "not-a-uuid", "dashboard.v1")
	require.ErrorIs(t, err, store.ErrInvalidArgument)

	_, err = s.GetUserSetting(ctx, user.ID, "")
	require.ErrorIs(t, err, store.ErrInvalidArgument)

	_, err = s.SetUserSetting(ctx, nil)
	require.ErrorIs(t, err, store.ErrInvalidArgument)

	// The column type is the last line of defence: a value that is not JSON
	// must fail at the write rather than poison every later read.
	_, err = s.SetUserSetting(ctx, &entity.UserSetting{
		UserID: user.ID, Key: "dashboard.v1", Value: "not json",
	})
	require.Error(t, err)
}

func TestUserSettingsDieWithTheirUser(t *testing.T) {
	pool := getTestPool(t)
	s := NewSettingsStore(pool)
	ctx := context.Background()

	user := createTestUser(t, NewUserStore(pool))
	_, err := s.SetUserSetting(ctx, &entity.UserSetting{
		UserID: user.ID, Key: "dashboard.v1", Value: `{"v": 1}`,
	})
	require.NoError(t, err)

	_, err = pool.Exec(ctx, `DELETE FROM users WHERE id = $1`, user.ID)
	require.NoError(t, err, "a preference must not keep a deleted user alive")

	var rows int
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT count(*) FROM user_settings WHERE user_id = $1`, user.ID).Scan(&rows))
	assert.Zero(t, rows)
}
