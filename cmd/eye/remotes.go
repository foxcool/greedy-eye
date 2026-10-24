package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"net/http"
	"slices"
	"time"

	"connectrpc.com/connect"
	"github.com/foxcool/greedy-eye/internal/middleware"
)

// remoteCallCeiling bounds a call whose ctx carries no deadline, so a hung
// sibling cannot hold a background job forever.
// shortcut: fixed for every method; make it configurable when a method needs more.
const remoteCallCeiling = time.Minute

// knownDomains are the domains remotes: may name: the service types.
var knownDomains = []string{
	ServiceConfigTypeMarketData,
	ServiceConfigTypePortfolio,
	ServiceConfigTypeAutomation,
	ServiceConfigTypeAnalytics,
	ServiceConfigTypeSettings,
}

// validateRemotes refuses a config this process could not run: a domain
// served here and called over the network at once, a domain nobody knows
// (a typo would silently fall back to the local handler), or remotes without
// the token the other side admits service calls by.
func validateRemotes(config *Config) error {
	if len(config.Remotes) == 0 {
		return nil
	}
	for _, domain := range slices.Sorted(maps.Keys(config.Remotes)) {
		if !slices.Contains(knownDomains, domain) {
			return fmt.Errorf("remotes.%s: unknown domain, expected one of %v", domain, knownDomains)
		}
		if config.Remotes[domain] == "" {
			return fmt.Errorf("remotes.%s: empty url", domain)
		}
		for _, svc := range config.Services {
			if svc.Type == domain {
				return fmt.Errorf("remotes.%s: domain is also in services, serve it here or call it remotely, not both", domain)
			}
		}
	}
	if config.Security.ServiceToken == "" {
		return errors.New("remotes is set but security.serviceToken is empty: the remote side would refuse every call")
	}
	return nil
}

// remotes builds Connect clients for the domains this process calls over the
// network. Only cross-domain reads made by handlers go through it. Edges still
// in-process whatever remotes says:
//   - credResolver reads accounts.data through the portfolio store (personal-dwa.9)
//   - the scheduler calls marketdata, automation and portfolio handlers (personal-dwa.11)
//   - UserProvisioningInterceptor writes users from every service (personal-dwa.10)
//   - marketdata reads holding provenance through the portfolio store (personal-dwa.3.3)
type remotes struct {
	urls map[string]string
	http connect.HTTPClient
	opts []connect.ClientOption
}

func newRemotes(urls map[string]string, token string) *remotes {
	// gRPC needs HTTP/2; siblings speak cleartext h2c, same as the server.
	protocols := new(http.Protocols)
	protocols.SetUnencryptedHTTP2(true)
	return &remotes{
		urls: urls,
		http: &http.Client{Transport: &http.Transport{Protocols: protocols}},
		opts: []connect.ClientOption{
			connect.WithGRPC(),
			connect.WithInterceptors(
				callCeilingInterceptor(remoteCallCeiling),
				middleware.ForwardIdentityInterceptor(token),
			),
		},
	}
}

// pick returns a client for domain when it is remote, the local handler
// otherwise. T is the generated client interface: a local handler satisfies
// it too, since unary handler and client methods share one signature.
func pick[T any](r *remotes, domain string, local T,
	newClient func(connect.HTTPClient, string, ...connect.ClientOption) T,
) T {
	url, ok := r.urls[domain]
	if !ok {
		return local
	}
	return newClient(r.http, url, r.opts...)
}

// logRemotes says once at startup which domains this process reaches over
// the network, so a split deployment answering from the wrong side is
// visible before its numbers are.
func logRemotes(urls map[string]string, log *slog.Logger) {
	for _, domain := range knownDomains {
		if url, ok := urls[domain]; ok {
			log.Info("domain is remote", slog.String("domain", domain), slog.String("url", url))
		} else {
			log.Info("domain is local", slog.String("domain", domain))
		}
	}
}

// callCeilingInterceptor gives a call without a deadline the ceiling; a
// deadline already in ctx wins.
func callCeilingInterceptor(ceiling time.Duration) connect.UnaryInterceptorFunc {
	return func(next connect.UnaryFunc) connect.UnaryFunc {
		return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
			if _, ok := ctx.Deadline(); ok {
				return next(ctx, req)
			}
			ctx, cancel := context.WithTimeout(ctx, ceiling)
			defer cancel()
			return next(ctx, req)
		}
	}
}
