package store

import (
	"errors"

	"github.com/foxcool/greedy-eye/internal/entity"
)

// Store error definitions.
var (
	ErrNotFound        = errors.New("not found")
	ErrInvalidArgument = errors.New("invalid argument")
	ErrConstraint      = errors.New("constraint violation")
	// ErrAlreadyExists: the row would be a second copy of something that must
	// exist once — the caller can find and use the first.
	ErrAlreadyExists = errors.New("already exists")
	// ErrUnreadable: the row exists, but its stored data cannot be read by
	// this instance. Lists leave such a row out; a read by id fails with this.
	ErrUnreadable = errors.New("account data unreadable")
)

// UnreadableAccountOf returns the row an ErrUnreadable read by id names: its
// open columns and the failure class. That is all of the error that may be
// shown; its text can quote decrypted bytes.
func UnreadableAccountOf(err error) (*entity.UnreadableAccount, bool) {
	var named interface {
		UnreadableAccount() *entity.UnreadableAccount
	}
	if !errors.As(err, &named) {
		return nil, false
	}
	return named.UnreadableAccount(), true
}
