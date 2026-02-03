package entity

import (
	"encoding/json"
	"time"
)

// User represents a system user.
type User struct {
	ID          string
	Email       string
	Name        string
	Preferences json.RawMessage // Flexible user preferences
	CreatedAt   time.Time
	UpdatedAt   time.Time
}
