//go:build smoke

package smoke_test

import (
	"fmt"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/foxcool/greedy-eye/internal/middleware"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// hops is set only in the loopback run, where the stand's remotes.* point at
// this proxy: every cross-domain call leaves the stand, passes through here
// and returns to it. Without it a stand answers the same either way, so a
// call site reverted to the local handler is visible only here.
var hops *hopRecorder

// hop is one cross-domain call as the wire carried it.
type hop struct {
	procedure string
	userID    string
	token     string
}

type hopRecorder struct {
	token  string
	server *http.Server

	mu   sync.Mutex
	seen []hop
}

// startHopRecorder serves h2c on SMOKE_HOP_PROXY and forwards to target. It
// returns nil when the variable is unset: the monolith run.
func startHopRecorder(target string) (*hopRecorder, error) {
	addr := os.Getenv("SMOKE_HOP_PROXY")
	if addr == "" {
		return nil, nil
	}
	token := os.Getenv("SMOKE_SERVICE_TOKEN")
	if token == "" {
		return nil, fmt.Errorf("SMOKE_HOP_PROXY is set but SMOKE_SERVICE_TOKEN is empty")
	}
	u, err := url.Parse(target)
	if err != nil {
		return nil, fmt.Errorf("parse target %q: %w", target, err)
	}
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("listen %s: %w", addr, err)
	}

	// gRPC needs HTTP/2 both ways, cleartext like the stand.
	protocols := new(http.Protocols)
	protocols.SetUnencryptedHTTP2(true)
	proxy := &httputil.ReverseProxy{
		Rewrite:   func(pr *httputil.ProxyRequest) { pr.SetURL(u) },
		Transport: &http.Transport{Protocols: protocols},
	}
	r := &hopRecorder{token: token}
	r.server = &http.Server{
		Protocols: protocols,
		Handler: http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			r.mu.Lock()
			r.seen = append(r.seen, hop{
				procedure: req.URL.Path,
				userID:    req.Header.Get(middleware.HeaderUserID),
				token:     req.Header.Get(middleware.HeaderServiceToken),
			})
			r.mu.Unlock()
			proxy.ServeHTTP(w, req)
		}),
	}
	go func() { _ = r.server.Serve(ln) }()
	return r, nil
}

func (r *hopRecorder) close() {
	if r != nil {
		_ = r.server.Close()
	}
}

// markHops returns where the hops of the next outer call start.
func markHops() int {
	if hops == nil {
		return 0
	}
	hops.mu.Lock()
	defer hops.mu.Unlock()
	return len(hops.seen)
}

// requireHops asserts that the outer call made since mark by userID reached
// each target service over the wire, and that every hop it made carried the
// service token. Hops are attributed by X-User-Id, so a hop that lost the
// caller counts as missing. Skipped in the monolith run.
func requireHops(t *testing.T, mark int, userID, caller string, targets ...string) {
	t.Helper()
	if hops == nil {
		return
	}
	hops.mu.Lock()
	all := slices.Clone(hops.seen[mark:])
	hops.mu.Unlock()
	var mine []hop
	for _, h := range all {
		if h.userID == userID {
			mine = append(mine, h)
		}
	}

	for _, target := range targets {
		prefix := "/" + target + "/"
		found := false
		for _, h := range mine {
			found = found || strings.HasPrefix(h.procedure, prefix)
		}
		require.True(t, found, "edge %s -> %s: no hop carried this caller, hops since the call began: %v",
			caller, target, all)
	}
	for _, h := range mine {
		assert.Equal(t, hops.token, h.token, "hop %s must carry the service token", h.procedure)
	}
}
