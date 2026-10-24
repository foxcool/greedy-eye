package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"net/http"
	"net/url"
	"slices"
	"time"

	"connectrpc.com/connect"
	"github.com/foxcool/greedy-eye/internal/middleware"
)

// remoteCallCeiling bounds a call whose ctx carries no deadline, so a hung
// sibling cannot hold a background job forever.
// shortcut: fixed for every method; make it configurable when a method needs more (personal-wih8).
const remoteCallCeiling = time.Minute

// validateRemotes refuses a config this process could not run: a domain
// served here and called over the network at once, a domain nobody knows
// (a typo would silently fall back to the local handler), or remotes without
// the token the other side admits service calls by.
func validateRemotes(config *Config) error {
	if len(config.Remotes) == 0 {
		return nil
	}
	for _, domain := range slices.Sorted(maps.Keys(config.Remotes)) {
		if !slices.Contains(serviceTypes, domain) {
			return fmt.Errorf("remotes.%s: unknown domain, expected one of %v", domain, serviceTypes)
		}
		// Only cleartext h2c is spoken; anything else would pass start and
		// fail every call.
		if u, err := url.Parse(config.Remotes[domain]); err != nil || u.Scheme != "http" || u.Host == "" {
			return fmt.Errorf("remotes.%s: want http://host:port, got %q", domain, config.Remotes[domain])
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

func newRemotes(urls map[string]string, token string, log *slog.Logger) *remotes {
	// gRPC needs HTTP/2; siblings speak cleartext h2c, same as the server.
	protocols := new(http.Protocols)
	protocols.SetUnencryptedHTTP2(true)
	return &remotes{
		urls: urls,
		http: &http.Client{
			Transport: &http.Transport{Protocols: protocols},
			// A redirect would carry the token and identity headers to
			// whatever host it names; a sibling never redirects.
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		},
		opts: []connect.ClientOption{
			connect.WithGRPC(),
			connect.WithInterceptors(
				transportErrorInterceptor(log),
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
	for _, domain := range serviceTypes {
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

// transportErrorInterceptor keeps sibling addresses out of what a user sees:
// an error that never reached the sibling (dial, reset, timeout) names the
// host, so it is logged here and returned with its code only. Errors the
// sibling answered with pass through untouched.
func transportErrorInterceptor(log *slog.Logger) connect.UnaryInterceptorFunc {
	return func(next connect.UnaryFunc) connect.UnaryFunc {
		return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
			resp, err := next(ctx, req)
			if err == nil || connect.IsWireError(err) {
				return resp, err
			}
			log.Error("remote call failed",
				slog.String("procedure", req.Spec().Procedure),
				slog.Any("error", err))
			return nil, connect.NewError(connect.CodeOf(err), errors.New("dependent service unavailable"))
		}
	}
}
