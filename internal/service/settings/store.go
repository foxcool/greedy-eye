package settings

import (
	"context"

	"github.com/foxcool/greedy-eye/internal/entity"
)

// Store is the data access contract for SettingsService.
type Store interface {
	// GetUserSetting returns one setting of one user. store.ErrNotFound when
	// the user has never written that key — an unset setting and an empty one
	// are different answers, and only the caller knows its own default.
	GetUserSetting(ctx context.Context, userID, key string) (*entity.UserSetting, error)
	// SetUserSetting writes the setting, replacing any previous value, and
	// returns the row as stored.
	SetUserSetting(ctx context.Context, s *entity.UserSetting) (*entity.UserSetting, error)
}
