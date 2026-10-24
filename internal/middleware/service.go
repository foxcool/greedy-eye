package middleware

import (
	"context"
	"crypto/subtle"
	"errors"
	"strings"

	"connectrpc.com/connect"
)

// Headers one service sends another. The edge must strip HeaderServiceToken
// from outside requests, the same way it overwrites the X-User-* headers.
const (
	HeaderServiceToken  = "X-Service-Token"
	HeaderSyncAuthority = "X-Sync-Authority"

	syncAuthorityGranted = "1"
)

type serviceCallerKey struct{}

// ServiceCaller reports whether ctx belongs to a call another service made
// with a valid service token.
func ServiceCaller(ctx context.Context) bool {
	v, _ := ctx.Value(serviceCallerKey{}).(bool)
	return v
}

// ServiceIdentityInterceptor admits calls from sibling services. A valid
// token marks the call as a service call, which may come without a user,
// and only such a call may carry SyncAuthority over the wire. An empty token
// disables it, so a monolith accepts no service calls at all.
// It must run before UserProvisioningInterceptor.
func ServiceIdentityInterceptor(token string) connect.UnaryInterceptorFunc {
	return func(next connect.UnaryFunc) connect.UnaryFunc {
		if token == "" {
			return next
		}
		return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
			got := req.Header().Get(HeaderServiceToken)
			if got == "" {
				return next(ctx, req)
			}
			if subtle.ConstantTimeCompare([]byte(got), []byte(token)) != 1 {
				return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("invalid credentials"))
			}
			ctx = context.WithValue(ctx, serviceCallerKey{}, true)
			if req.Header().Get(HeaderSyncAuthority) == syncAuthorityGranted {
				ctx = WithSyncAuthority(ctx)
			}
			return next(ctx, req)
		}
	}
}

// ForwardIdentityInterceptor is the client half: it carries the caller's user
// and SyncAuthority from ctx to the next hop. The token goes on every call,
// so a background call without a user is still admitted.
func ForwardIdentityInterceptor(token string) connect.UnaryInterceptorFunc {
	return func(next connect.UnaryFunc) connect.UnaryFunc {
		return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
			// Headers already on the request are dropped: only ctx decides what
			// the next hop sees, so a reused request cannot carry stale rights.
			h := req.Header()
			for _, k := range []string{HeaderUserID, HeaderUserEmail, HeaderUserRoles, HeaderSyncAuthority} {
				h.Del(k)
			}
			if user, ok := UserFromContext(ctx); ok {
				h.Set(HeaderUserID, user.ID)
				h.Set(HeaderUserEmail, user.Email)
				h.Set(HeaderUserRoles, strings.Join(user.Roles, ","))
			}
			h.Set(HeaderServiceToken, token)
			if SyncAuthority(ctx) {
				h.Set(HeaderSyncAuthority, syncAuthorityGranted)
			}
			return next(ctx, req)
		}
	}
}
