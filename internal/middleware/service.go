package middleware

import (
	"context"
	"crypto/subtle"
	"errors"
	"strings"

	"connectrpc.com/connect"
	"github.com/foxcool/greedy-eye/internal/spend"
)

// Headers one service sends another. The edge must strip all of them from
// outside requests, the same way it overwrites the X-User-* headers.
//
// HeaderSpendClass and HeaderSpendCaller carry the spend labels (package
// spend) across a hop. Without them the far side meters a background sweep as
// an interactive rpc:<method> and spends the reserve held for people. They are
// trusted for the same reason SyncAuthority is: a caller who could set them
// could pick whose budget a request is charged to.
const (
	HeaderServiceToken  = "X-Service-Token"
	HeaderSyncAuthority = "X-Sync-Authority"
	HeaderSpendClass    = "X-Spend-Class"
	HeaderSpendCaller   = "X-Spend-Caller"

	syncAuthorityGranted = "1"
	// Only background goes on the wire: an absent header is interactive, the
	// same default spend.ClassFromContext applies to an unmarked ctx.
	spendClassBackground = "background"
)

// trustHeaders are the headers ForwardIdentityInterceptor writes from ctx.
var trustHeaders = []string{
	HeaderUserID, HeaderUserEmail, HeaderUserRoles,
	HeaderSyncAuthority, HeaderSpendClass, HeaderSpendCaller,
}

type serviceCallerKey struct{}

// ServiceCaller reports whether ctx belongs to a call another service made
// with a valid service token.
func ServiceCaller(ctx context.Context) bool {
	v, _ := ctx.Value(serviceCallerKey{}).(bool)
	return v
}

// ServiceIdentityInterceptor admits calls from sibling services. A valid
// token marks the call as a service call, which may come without a user,
// and only such a call may carry SyncAuthority and the spend labels over the
// wire. An empty token
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
			if req.Header().Get(HeaderSpendClass) == spendClassBackground {
				ctx = spend.WithClass(ctx, spend.ClassBackground)
			}
			if caller := req.Header().Get(HeaderSpendCaller); caller != "" {
				ctx = spend.WithCaller(ctx, caller)
			}
			return next(ctx, req)
		}
	}
}

// ForwardIdentityInterceptor is the client half: it carries the caller's user,
// SyncAuthority and spend labels from ctx to the next hop. The token goes on every call,
// so a background call without a user is still admitted.
func ForwardIdentityInterceptor(token string) connect.UnaryInterceptorFunc {
	return func(next connect.UnaryFunc) connect.UnaryFunc {
		return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
			// Headers already on the request are dropped: only ctx decides what
			// the next hop sees, so a reused request cannot carry stale rights.
			h := req.Header()
			for _, k := range trustHeaders {
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
			if spend.ClassFromContext(ctx) == spend.ClassBackground {
				h.Set(HeaderSpendClass, spendClassBackground)
			}
			// Unlabelled stays off the wire so the far side names the call
			// itself rather than recording a hop as unlabelled.
			if caller := spend.CallerFromContext(ctx); caller != spend.Unlabelled {
				h.Set(HeaderSpendCaller, caller)
			}
			return next(ctx, req)
		}
	}
}
