package ratelimit

import "context"

type costKey struct{}

// WithCost marks a request as costing n of its provider's units (Alchemy's
// compute units) against the rate budget. A request without a cost costs one,
// which is what a provider metering requests means by it.
func WithCost(ctx context.Context, n int) context.Context {
	return context.WithValue(ctx, costKey{}, n)
}

func costOf(ctx context.Context) int {
	if n, ok := ctx.Value(costKey{}).(int); ok && n > 0 {
		return n
	}
	return 1
}
