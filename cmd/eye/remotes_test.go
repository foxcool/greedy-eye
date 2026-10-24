package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"connectrpc.com/connect"
	apiv1 "github.com/foxcool/greedy-eye/api/v1"
	"github.com/foxcool/greedy-eye/api/v1/apiv1connect"
	"github.com/foxcool/greedy-eye/internal/entity"
	"github.com/foxcool/greedy-eye/internal/middleware"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

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
		})
	}
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
	got := pick[apiv1connect.SettingsServiceClient](newRemotes(nil, ""), ServiceConfigTypeSettings,
		local, apiv1connect.NewSettingsServiceClient)
	assert.Same(t, local, got)
}

// A remote domain is reached over gRPC on h2c, the way the server listens, and
// the call carries the caller and the token.
func TestPickRemoteCallsOverH2C(t *testing.T) {
	spy := &settingsSpy{}
	mux := http.NewServeMux()
	mux.Handle(apiv1connect.NewSettingsServiceHandler(spy))
	srv := httptest.NewUnstartedServer(mux)
	srv.Config.Protocols = new(http.Protocols)
	srv.Config.Protocols.SetUnencryptedHTTP2(true)
	srv.Start()
	t.Cleanup(srv.Close)

	rc := newRemotes(map[string]string{ServiceConfigTypeSettings: srv.URL}, "tok")
	client := pick[apiv1connect.SettingsServiceClient](rc, ServiceConfigTypeSettings,
		&settingsSpy{}, apiv1connect.NewSettingsServiceClient)

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
