package main

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"connectrpc.com/connect"
	apiv1 "github.com/foxcool/greedy-eye/api/v1"
	"github.com/foxcool/greedy-eye/api/v1/apiv1connect"
	"github.com/foxcool/greedy-eye/internal/entity"
	"github.com/foxcool/greedy-eye/internal/middleware"
	"github.com/foxcool/greedy-eye/internal/service/portfolio"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func withToken(remotes map[string]string) Config {
	c := Config{Remotes: remotes}
	c.Security.ServiceToken = "s"
	return c
}

func TestValidateRemotes(t *testing.T) {
	svc := func(types ...string) []ServiceConfig {
		out := make([]ServiceConfig, len(types))
		for i, typ := range types {
			out[i] = ServiceConfig{Type: typ}
		}
		return out
	}
	cases := []struct {
		name    string
		config  Config
		wantErr string
	}{
		{name: "monolith", config: Config{Services: svc("portfolio", "marketdata")}},
		{
			name: "split",
			config: func() Config {
				c := Config{Services: svc("portfolio", "analytics"),
					Remotes: map[string]string{"marketdata": "http://b:8080", "settings": "http://b:8080"}}
				c.Security.ServiceToken = "s"
				return c
			}(),
		},
		{
			name: "served and remote",
			config: func() Config {
				c := Config{Services: svc("portfolio", "marketdata"),
					Remotes: map[string]string{"marketdata": "http://b:8080"}}
				c.Security.ServiceToken = "s"
				return c
			}(),
			wantErr: "remotes.marketdata",
		},
		{
			name: "loopback",
			config: func() Config {
				c := Config{Services: svc("portfolio", "marketdata"), Loopback: true,
					Remotes: map[string]string{"marketdata": "http://proxy:8080"}}
				c.Security.ServiceToken = "s"
				return c
			}(),
		},
		{
			name:    "loopback without remotes",
			config:  Config{Services: svc("portfolio", "marketdata"), Loopback: true},
			wantErr: "loopback is set but remotes is empty",
		},
		{
			name: "loopback without token",
			config: Config{Services: svc("marketdata"), Loopback: true,
				Remotes: map[string]string{"marketdata": "http://proxy:8080"}},
			wantErr: "serviceToken",
		},
		{
			name: "no token",
			config: Config{Services: svc("portfolio"),
				Remotes: map[string]string{"marketdata": "http://b:8080"}},
			wantErr: "serviceToken",
		},
		{
			name: "typo",
			config: func() Config {
				c := Config{Remotes: map[string]string{"marketdat": "http://b:8080"}}
				c.Security.ServiceToken = "s"
				return c
			}(),
			wantErr: "remotes.marketdat: unknown domain",
		},
		{name: "empty url", config: withToken(map[string]string{"settings": ""}), wantErr: "remotes.settings: want http://"},
		{name: "no scheme", config: withToken(map[string]string{"settings": "b:8080"}), wantErr: "remotes.settings: want http://"},
		{name: "tls", config: withToken(map[string]string{"settings": "https://b:8443"}), wantErr: "remotes.settings: want http://"},
		{name: "userinfo", config: withToken(map[string]string{"settings": "https://user:hunter2@b:8443"}), wantErr: "remotes.settings: want http://"},
		{name: "unparseable userinfo", config: withToken(map[string]string{"settings": "http://user:hunter2@b:port"}), wantErr: "remotes.settings: want http://"},
		{name: "userinfo without slashes", config: withToken(map[string]string{"settings": "http:user:hunter2@b:8080"}), wantErr: "remotes.settings: want http://"},
		{name: "userinfo with one slash", config: withToken(map[string]string{"settings": "http:/user:hunter2@b:8080"}), wantErr: "remotes.settings: want http://"},
		{name: "http userinfo", config: withToken(map[string]string{"settings": "http://user:hunter2@b:8080"}), wantErr: "remotes.settings: must not carry credentials"},
		{name: "http username only", config: withToken(map[string]string{"settings": "http://hunter2@b:8080"}), wantErr: "remotes.settings: must not carry credentials"},
		{name: "userinfo without scheme", config: withToken(map[string]string{"settings": "user:hunter2@b:8080"}), wantErr: "remotes.settings: want http://"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := validateRemotes(&tc.config)
			if tc.wantErr == "" {
				assert.NoError(t, err)
				return
			}
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.wantErr)
			assert.NotContains(t, err.Error(), "hunter2")
		})
	}
}

// A remote URL may carry userinfo; the startup log must not.
func TestLogRemotesRedactsUserinfo(t *testing.T) {
	var buf bytes.Buffer
	log := slog.New(slog.NewTextHandler(&buf, nil))

	logRemotes(&Config{Remotes: map[string]string{
		"settings":   "http://user:hunter2@b:8080",
		"marketdata": "http://user:hunter2@b:port",
	}}, log)
	assert.Contains(t, buf.String(), "b:8080")
	assert.NotContains(t, buf.String(), "hunter2")
}

// Loopback is announced at startup as a test-stand mode; without it nothing warns.
func TestLogRemotesWarnsOnLoopback(t *testing.T) {
	var buf bytes.Buffer
	log := slog.New(slog.NewTextHandler(&buf, nil))

	logRemotes(&Config{Remotes: map[string]string{"settings": "http://b:8080"}}, log)
	assert.NotContains(t, buf.String(), "level=WARN")

	buf.Reset()
	logRemotes(&Config{Loopback: true, Remotes: map[string]string{"settings": "http://b:8080"}}, log)
	assert.Contains(t, buf.String(), "level=WARN")
	assert.Contains(t, buf.String(), "test stands only")
}

type settingsSpy struct {
	apiv1connect.UnimplementedSettingsServiceHandler
	header      http.Header
	hasDeadline bool
}

func (s *settingsSpy) GetSetting(ctx context.Context, req *connect.Request[apiv1.GetSettingRequest]) (*connect.Response[apiv1.GetSettingResponse], error) {
	s.header = req.Header().Clone()
	_, s.hasDeadline = ctx.Deadline()
	return connect.NewResponse(&apiv1.GetSettingResponse{Setting: &apiv1.Setting{Key: req.Msg.Key}}), nil
}

// A domain absent from remotes is answered by the local handler itself.
func TestPickLocalWhenNotRemote(t *testing.T) {
	local := &settingsSpy{}
	got := pick[apiv1connect.SettingsServiceClient](newRemotes(nil, "", slog.Default()), ServiceConfigTypeSettings,
		local, apiv1connect.NewSettingsServiceClient)
	assert.Same(t, local, got)
}

// A remote domain is reached over gRPC on h2c, the way the server listens, and
// the call carries the caller and the token.
func TestPickRemoteCallsOverH2C(t *testing.T) {
	spy := &settingsSpy{}
	mux := http.NewServeMux()
	mux.Handle(apiv1connect.NewSettingsServiceHandler(spy))
	srv := h2cServer(t, mux)
	client := remoteSettings(t, srv.URL)

	ctx := middleware.ContextWithUser(context.Background(), &entity.User{ID: "u1", Email: "u1@x", Roles: []string{"user"}})
	resp, err := client.GetSetting(ctx, connect.NewRequest(&apiv1.GetSettingRequest{Key: "k"}))
	require.NoError(t, err)
	assert.Equal(t, "k", resp.Msg.Setting.Key)
	assert.Equal(t, "u1", spy.header.Get(middleware.HeaderUserID))
	assert.Equal(t, "tok", spy.header.Get(middleware.HeaderServiceToken))
	assert.True(t, spy.hasDeadline, "a call without a deadline gets the ceiling")
}

func TestCallCeilingKeepsAnExistingDeadline(t *testing.T) {
	var got time.Time
	next := connect.UnaryFunc(func(ctx context.Context, _ connect.AnyRequest) (connect.AnyResponse, error) {
		got, _ = ctx.Deadline()
		return nil, nil
	})
	call := callCeilingInterceptor(time.Hour)(next)

	want := time.Now().Add(time.Second)
	ctx, cancel := context.WithDeadline(context.Background(), want)
	defer cancel()
	_, _ = call(ctx, connect.NewRequest(&apiv1.GetSettingRequest{}))
	assert.Equal(t, want, got)

	_, _ = call(context.Background(), connect.NewRequest(&apiv1.GetSettingRequest{}))
	assert.WithinDuration(t, time.Now().Add(time.Hour), got, time.Minute)
}

func h2cServer(t *testing.T, h http.Handler) *httptest.Server {
	t.Helper()
	srv := httptest.NewUnstartedServer(h)
	srv.Config.Protocols = new(http.Protocols)
	srv.Config.Protocols.SetUnencryptedHTTP2(true)
	srv.Start()
	t.Cleanup(srv.Close)
	return srv
}

func remoteSettings(t *testing.T, url string) apiv1connect.SettingsServiceClient {
	t.Helper()
	rc := newRemotes(map[string]string{ServiceConfigTypeSettings: url}, "tok", slog.Default())
	return pick[apiv1connect.SettingsServiceClient](rc, ServiceConfigTypeSettings,
		&settingsSpy{}, apiv1connect.NewSettingsServiceClient)
}

// A redirect would hand the token to the host it names, so it is not followed.
func TestRemoteDoesNotFollowRedirects(t *testing.T) {
	var leaked string
	elsewhere := h2cServer(t, http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		leaked = r.Header.Get(middleware.HeaderServiceToken)
	}))
	redirector := h2cServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, elsewhere.URL+r.URL.Path, http.StatusFound)
	}))

	_, err := remoteSettings(t, redirector.URL).GetSetting(context.Background(),
		connect.NewRequest(&apiv1.GetSettingRequest{Key: "k"}))
	require.Error(t, err)
	assert.Empty(t, leaked, "the token must not reach the redirect target")
}

// An unreachable sibling surfaces with its code but without its address.
func TestRemoteTransportErrorHidesAddress(t *testing.T) {
	srv := h2cServer(t, http.NotFoundHandler())
	url := srv.URL
	srv.Close()

	_, err := remoteSettings(t, url).GetSetting(context.Background(),
		connect.NewRequest(&apiv1.GetSettingRequest{Key: "k"}))
	require.Error(t, err)
	assert.Equal(t, connect.CodeUnavailable, connect.CodeOf(err))
	assert.NotContains(t, err.Error(), url[len("http://"):])
}

// What the sibling answered is the sibling's answer and passes through.
func TestRemoteWireErrorPassesThrough(t *testing.T) {
	mux := http.NewServeMux()
	mux.Handle(apiv1connect.NewSettingsServiceHandler(&notFoundSettings{}))
	srv := h2cServer(t, mux)

	_, err := remoteSettings(t, srv.URL).GetSetting(context.Background(),
		connect.NewRequest(&apiv1.GetSettingRequest{Key: "k"}))
	assert.Equal(t, connect.CodeNotFound, connect.CodeOf(err))
	assert.Contains(t, err.Error(), "no such setting")
}

type notFoundSettings struct {
	apiv1connect.UnimplementedSettingsServiceHandler
}

func (notFoundSettings) GetSetting(context.Context, *connect.Request[apiv1.GetSettingRequest]) (*connect.Response[apiv1.GetSettingResponse], error) {
	return nil, connect.NewError(connect.CodeNotFound, errors.New("no such setting"))
}

// provenanceStore answers only the provenance RPC, and only for the ids it was
// asked about, so a request that lost its ids comes back empty.
type provenanceStore struct {
	portfolio.Store
	answer map[string]bool
}

func (s provenanceStore) UnsolicitedAssets(_ context.Context, ids []string) (map[string]bool, error) {
	out := map[string]bool{}
	for _, id := range ids {
		if v, ok := s.answer[id]; ok {
			out[id] = v
		}
	}
	return out, nil
}

// A remote portfolio answers provenance to a call shaped like the nightly
// rescore: no user, no SyncAuthority, only the token. A bought asset arrives
// as false rather than vanishing, and an unheld one stays absent.
func TestHoldingProvenanceOverRemote(t *testing.T) {
	const (
		bought  = "01926d35-6a1e-7005-8005-000000000001"
		airdrop = "01926d35-6a1e-7005-8005-000000000002"
		unheld  = "01926d35-6a1e-7005-8005-000000000003"
	)
	answer := map[string]bool{airdrop: true, bought: false}

	h := portfolio.NewHandler(provenanceStore{answer: answer}, slog.Default())
	mux := http.NewServeMux()
	mux.Handle(apiv1connect.NewPortfolioServiceHandler(h, connect.WithInterceptors(
		middleware.ServiceIdentityInterceptor("tok"),
		middleware.UserProvisioningInterceptor(nil, slog.Default()),
	)))
	srv := h2cServer(t, mux)

	rc := newRemotes(map[string]string{ServiceConfigTypePortfolio: srv.URL}, "tok", slog.Default())
	got, err := holdingProvenance(rc, nil).UnsolicitedAssets(context.Background(), []string{bought, airdrop, unheld})
	require.NoError(t, err)
	assert.Equal(t, map[string]bool{airdrop: true, bought: false}, got)
}

// Without remotes.portfolio provenance stays on the store, never on the RPC.
func TestHoldingProvenanceLocalWhenNotRemote(t *testing.T) {
	local := provenanceStore{}
	assert.Equal(t, local, holdingProvenance(newRemotes(nil, "", slog.Default()), local))
}
