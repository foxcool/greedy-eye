package middleware

import "context"

type syncAuthorityKey struct{}

// WithSyncAuthority marks ctx as an in-process sync resolving what a provider
// reported, which may bind an asset's external ref (see marketdata mayBindRef).
// The mark has no wire form and no interceptor sets it, so an RPC caller cannot
// carry it; it holds while PortfolioService calls MarketData in-process.
func WithSyncAuthority(ctx context.Context) context.Context {
	return context.WithValue(ctx, syncAuthorityKey{}, true)
}

// SyncAuthority reports whether ctx carries WithSyncAuthority.
func SyncAuthority(ctx context.Context) bool {
	v, _ := ctx.Value(syncAuthorityKey{}).(bool)
	return v
}
