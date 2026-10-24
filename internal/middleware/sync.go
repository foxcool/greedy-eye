package middleware

import "context"

type syncAuthorityKey struct{}

// WithSyncAuthority marks ctx as an in-process caller, trusted for writes no RPC
// client may make. It carries two grants: a sync resolving the positions a
// provider reported may bind an asset's external ref (see marketdata
// mayBindRef), and in-process pricing — the price sweep, a sync pricing what it
// touched — may fetch prices (see marketdata mayFetchPrices).
// Over RPC only a sibling service carries it, as X-Sync-Authority next to a
// valid service token (see ServiceIdentityInterceptor); a user's call cannot.
func WithSyncAuthority(ctx context.Context) context.Context {
	return context.WithValue(ctx, syncAuthorityKey{}, true)
}

// SyncAuthority reports whether ctx carries WithSyncAuthority.
func SyncAuthority(ctx context.Context) bool {
	v, _ := ctx.Value(syncAuthorityKey{}).(bool)
	return v
}
