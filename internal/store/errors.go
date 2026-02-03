package store

import "errors"

// Store error definitions.
var (
	ErrNotFound        = errors.New("not found")
	ErrInvalidArgument = errors.New("invalid argument")
	ErrConstraint      = errors.New("constraint violation")
)
