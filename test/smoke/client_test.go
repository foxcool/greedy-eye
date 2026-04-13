//go:build smoke

package smoke_test

import (
	"context"
	"net/http"

	"connectrpc.com/connect"
	"github.com/foxcool/greedy-eye/internal/api/v1/apiv1connect"
)

// headerInterceptor is a client-side Connect interceptor that adds HTTP headers to each request.
type headerInterceptor map[string]string

func (h headerInterceptor) WrapUnary(next connect.UnaryFunc) connect.UnaryFunc {
	return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
		for k, v := range h {
			req.Header().Set(k, v)
		}
		return next(ctx, req)
	}
}

func (h headerInterceptor) WrapStreamingClient(next connect.StreamingClientFunc) connect.StreamingClientFunc {
	return next
}

func (h headerInterceptor) WrapStreamingHandler(next connect.StreamingHandlerFunc) connect.StreamingHandlerFunc {
	return next
}

func newMDClient(userID string) apiv1connect.MarketDataServiceClient {
	return apiv1connect.NewMarketDataServiceClient(
		http.DefaultClient, serverURL,
		connect.WithInterceptors(userHeaders(userID)),
	)
}

func newPortfolioClient(userID string) apiv1connect.PortfolioServiceClient {
	return apiv1connect.NewPortfolioServiceClient(
		http.DefaultClient, serverURL,
		connect.WithInterceptors(userHeaders(userID)),
	)
}

func newAutomationClient(userID string) apiv1connect.AutomationServiceClient {
	return apiv1connect.NewAutomationServiceClient(
		http.DefaultClient, serverURL,
		connect.WithInterceptors(userHeaders(userID)),
	)
}

// userHeaders returns an interceptor that sets X-User-Id and X-User-Email.
// Email is derived from the user ID to satisfy the unique constraint on users.email.
func userHeaders(userID string) headerInterceptor {
	return headerInterceptor{
		"X-User-Id":    userID,
		"X-User-Email": userID + "@smoke.test",
	}
}
