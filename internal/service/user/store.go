package user

import (
	"context"

	"github.com/foxcool/greedy-eye/internal/entity"
)

// Store defines the data access contract for user management.
type Store interface {
	GetOrCreate(ctx context.Context, id string, email string) (*entity.User, error)
	CreateUser(ctx context.Context, u *entity.User) (*entity.User, error)
	GetUser(ctx context.Context, id string) (*entity.User, error)
	GetUserByEmail(ctx context.Context, email string) (*entity.User, error)
	UpdateUser(ctx context.Context, u *entity.User, fields []string) (*entity.User, error)
	DeleteUser(ctx context.Context, id string) error
	ListUsers(ctx context.Context, opts ListUsersOpts) ([]*entity.User, string, error)
}

// ListUsersOpts contains options for listing users.
type ListUsersOpts struct {
	PageSize  int
	PageToken string
}
