//go:build integration

package postgres

import (
	"context"
	"testing"

	userservice "github.com/foxcool/greedy-eye/internal/service/user"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A client that sets only X-User-Id provisions with an empty email. Two such
// users must coexist: users_email_key is UNIQUE, and only NULL escapes it.
func TestGetOrCreateWithoutEmailStoresNull(t *testing.T) {
	pool := getTestPool(t)
	s := NewUserStore(pool)
	ctx := context.Background()

	first := uuid.Must(uuid.NewV7()).String()
	second := uuid.Must(uuid.NewV7()).String()

	for _, id := range []string{first, second} {
		u, err := s.GetOrCreate(ctx, id, "")
		require.NoError(t, err, "second user without an email must provision")
		assert.Equal(t, id, u.ID)
		assert.Empty(t, u.Email)

		var isNull bool
		require.NoError(t, pool.QueryRow(ctx,
			"SELECT email IS NULL FROM users WHERE id = $1", id).Scan(&isNull))
		assert.True(t, isNull, "an empty email must be stored as NULL, not ''")
	}

	// Every read path tolerates the NULL.
	got, err := s.GetUser(ctx, first)
	require.NoError(t, err)
	assert.Empty(t, got.Email)

	listed, _, err := s.ListUsers(ctx, userservice.ListUsersOpts{})
	require.NoError(t, err)
	assert.Len(t, listed, 2)
}

func TestGetOrCreateWithEmailKeepsIt(t *testing.T) {
	pool := getTestPool(t)
	s := NewUserStore(pool)
	ctx := context.Background()

	id := uuid.Must(uuid.NewV7()).String()
	u, err := s.GetOrCreate(ctx, id, "a@example.com")
	require.NoError(t, err)
	assert.Equal(t, "a@example.com", u.Email)

	byEmail, err := s.GetUserByEmail(ctx, "a@example.com")
	require.NoError(t, err)
	assert.Equal(t, id, byEmail.ID)
}

func TestUpdateUserClearingEmailStoresNull(t *testing.T) {
	pool := getTestPool(t)
	s := NewUserStore(pool)
	ctx := context.Background()

	a := createTestUser(t, s)
	b := createTestUser(t, s)

	for _, u := range []string{a.ID, b.ID} {
		a2 := *a
		a2.ID, a2.Email = u, ""
		got, err := s.UpdateUser(ctx, &a2, []string{"email"})
		require.NoError(t, err, "clearing the email of a second user must not collide")
		assert.Empty(t, got.Email)
	}
}
