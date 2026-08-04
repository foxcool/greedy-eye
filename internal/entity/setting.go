package entity

import "time"

// UserSetting is one stored preference of one user. Value carries JSON text,
// and the shape of a setting stays the concern of whoever wrote it. The
// document survives a round trip, the byte string does not: jsonb normalizes
// whitespace and object key order.
type UserSetting struct {
	UserID    string
	Key       string
	Value     string
	UpdatedAt time.Time
}
