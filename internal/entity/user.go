package entity

import (
	"encoding/json"
	"slices"
	"time"
)

// User represents a system user.
type User struct {
	ID          string
	Email       string
	Name        string
	Preferences json.RawMessage // Flexible user preferences
	Roles       []string        // Transient, from the auth proxy per request; not persisted (psina owns roles)
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// IsAdmin reports whether the user carries the admin role.
func (u *User) IsAdmin() bool {
	return slices.Contains(u.Roles, "admin")
}
