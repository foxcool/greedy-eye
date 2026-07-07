package middleware

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"connectrpc.com/connect"
	"github.com/foxcool/greedy-eye/internal/entity"
)

// UserProvisioner creates or retrieves a user by their external ID.
type UserProvisioner interface {
	GetOrCreate(ctx context.Context, id string, email string) (*entity.User, error)
}

type contextKey string

const userContextKey contextKey = "user"

// UserFromContext retrieves the provisioned user from the request context.
func UserFromContext(ctx context.Context) (*entity.User, bool) {
	u, ok := ctx.Value(userContextKey).(*entity.User)
	return u, ok
}

// ContextWithUser injects a user into the context. Intended for use in tests.
func ContextWithUser(ctx context.Context, user *entity.User) context.Context {
	return context.WithValue(ctx, userContextKey, user)
}

// UserProvisioningInterceptor reads X-User-Id, X-User-Email and X-User-Roles
// from request headers, lazily provisions the user in the local DB, and
// injects it into the context. Roles are per-request and never persisted —
// the auth service (psina) owns them.
func UserProvisioningInterceptor(store UserProvisioner, log *slog.Logger) connect.UnaryInterceptorFunc {
	return func(next connect.UnaryFunc) connect.UnaryFunc {
		return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
			userID := req.Header().Get("X-User-Id")
			if userID == "" {
				return nil, connect.NewError(connect.CodeUnauthenticated, fmt.Errorf("missing X-User-Id header"))
			}

			email := req.Header().Get("X-User-Email")

			user, err := store.GetOrCreate(ctx, userID, email)
			if err != nil {
				log.Error("user provisioning failed", slog.String("user_id", userID), slog.Any("error", err))
				return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("user provisioning failed"))
			}

			user.Roles = parseRoles(req.Header().Get("X-User-Roles"))

			ctx = context.WithValue(ctx, userContextKey, user)
			return next(ctx, req)
		}
	}
}

// parseRoles splits the comma-joined X-User-Roles header value.
func parseRoles(header string) []string {
	if header == "" {
		return nil
	}
	var roles []string
	for r := range strings.SplitSeq(header, ",") {
		if r = strings.TrimSpace(r); r != "" {
			roles = append(roles, r)
		}
	}
	return roles
}
