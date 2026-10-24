package middleware

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"connectrpc.com/connect"
	apiv1 "github.com/foxcool/greedy-eye/api/v1"
	"github.com/foxcool/greedy-eye/internal/entity"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

const (
	testToken     = "s3cret-service-token"
	testProcedure = "/test.Service/Method"
)

// seen is what the handler on the far side of the hop observed.
type seen struct {
	user      *entity.User
	hasUser   bool
	authority bool
	service   bool
}

// startHop serves one procedure behind the production server chain and
// returns a gRPC client for it that runs the given client interceptors.
func startHop(t *testing.T, prov UserProvisioner, log *slog.Logger, serverToken string,
	clientInterceptors ...connect.Interceptor,
) (*connect.Client[apiv1.GetSettingRequest, apiv1.GetSettingResponse], *seen) {
	t.Helper()
	got := &seen{}
	mux := http.NewServeMux()
	mux.Handle(testProcedure, connect.NewUnaryHandler(testProcedure,
		func(ctx context.Context, _ *connect.Request[apiv1.GetSettingRequest]) (*connect.Response[apiv1.GetSettingResponse], error) {
			got.user, got.hasUser = UserFromContext(ctx)
			got.authority = SyncAuthority(ctx)
			got.service = ServiceCaller(ctx)
			return connect.NewResponse(&apiv1.GetSettingResponse{}), nil
		},
		connect.WithInterceptors(
			ServiceIdentityInterceptor(serverToken),
			UserProvisioningInterceptor(prov, log),
		),
	))
	srv := httptest.NewUnstartedServer(mux)
	srv.EnableHTTP2 = true
	srv.StartTLS()
	t.Cleanup(srv.Close)

	client := connect.NewClient[apiv1.GetSettingRequest, apiv1.GetSettingResponse](
		srv.Client(), srv.URL+testProcedure,
		connect.WithGRPC(), connect.WithInterceptors(clientInterceptors...))
	return client, got
}

func call(ctx context.Context, c *connect.Client[apiv1.GetSettingRequest, apiv1.GetSettingResponse]) error {
	_, err := c.CallUnary(ctx, connect.NewRequest(&apiv1.GetSettingRequest{Key: "k"}))
	return err
}

func requireCode(t *testing.T, err error, code connect.Code) {
	t.Helper()
	require.Error(t, err)
	assert.Equal(t, code, connect.CodeOf(err))
}

// staticHeaders sets raw headers on the outgoing call, standing in for a
// caller that does not run ForwardIdentityInterceptor.
func staticHeaders(kv ...string) connect.UnaryInterceptorFunc {
	return func(next connect.UnaryFunc) connect.UnaryFunc {
		return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
			for i := 0; i < len(kv); i += 2 {
				req.Header().Set(kv[i], kv[i+1])
			}
			return next(ctx, req)
		}
	}
}

func TestForwardIdentity_UserReachesNextHop(t *testing.T) {
	user := &entity.User{ID: "u1", Email: "u1@example.com", Roles: []string{"admin", "beta"}}
	prov := &mockProvisioner{}
	prov.On("GetOrCreate", mock.Anything, "u1", "u1@example.com").
		Return(&entity.User{ID: "u1", Email: "u1@example.com"}, nil)
	client, got := startHop(t, prov, newLogger(), testToken, ForwardIdentityInterceptor(testToken))

	require.NoError(t, call(ContextWithUser(context.Background(), user), client))

	require.True(t, got.hasUser)
	assert.Equal(t, "u1", got.user.ID)
	assert.Equal(t, []string{"admin", "beta"}, got.user.Roles)
	assert.True(t, got.service)
	assert.False(t, got.authority, "a user call does not gain SyncAuthority by crossing a hop")
}

func TestForwardIdentity_SyncAuthorityCrossesTheHop(t *testing.T) {
	prov := &mockProvisioner{}
	prov.On("GetOrCreate", mock.Anything, "u1", "").Return(&entity.User{ID: "u1"}, nil)
	client, got := startHop(t, prov, newLogger(), testToken, ForwardIdentityInterceptor(testToken))

	ctx := WithSyncAuthority(ContextWithUser(context.Background(), &entity.User{ID: "u1"}))
	require.NoError(t, call(ctx, client))

	assert.True(t, got.authority)
	assert.Equal(t, "u1", got.user.ID)
}

func TestForwardIdentity_BackgroundCallWithoutUser(t *testing.T) {
	prov := &mockProvisioner{}
	client, got := startHop(t, prov, newLogger(), testToken, ForwardIdentityInterceptor(testToken))

	require.NoError(t, call(WithSyncAuthority(context.Background()), client))

	assert.False(t, got.hasUser)
	assert.True(t, got.authority)
	prov.AssertNotCalled(t, "GetOrCreate")
}

func TestForwardIdentity_StaleHeadersDropped(t *testing.T) {
	prov := &mockProvisioner{}
	// Outermost, so it writes before the forwarder runs, as a reused request would.
	stale := staticHeaders(HeaderUserID, "admin-1", HeaderUserRoles, "admin", HeaderSyncAuthority, syncAuthorityGranted)
	client, got := startHop(t, prov, newLogger(), testToken, stale, ForwardIdentityInterceptor(testToken))

	require.NoError(t, call(context.Background(), client))

	assert.False(t, got.hasUser, "a stale X-User-Id must not become the caller")
	assert.False(t, got.authority, "a stale X-Sync-Authority must not cross the hop")
	prov.AssertNotCalled(t, "GetOrCreate")
}

func TestServiceIdentity_WrongTokenRejected(t *testing.T) {
	prov := &mockProvisioner{}
	client, got := startHop(t, prov, newLogger(), testToken, ForwardIdentityInterceptor("not-the-token"))

	ctx := ContextWithUser(WithSyncAuthority(context.Background()), &entity.User{ID: "u1"})
	requireCode(t, call(ctx, client), connect.CodeUnauthenticated)
	assert.False(t, got.authority)
	prov.AssertNotCalled(t, "GetOrCreate")
}

func TestServiceIdentity_AuthorityHeaderWithoutTokenIgnored(t *testing.T) {
	prov := &mockProvisioner{}
	prov.On("GetOrCreate", mock.Anything, "u1", "").Return(&entity.User{ID: "u1"}, nil)
	client, got := startHop(t, prov, newLogger(), testToken,
		staticHeaders(HeaderUserID, "u1", HeaderSyncAuthority, syncAuthorityGranted))

	require.NoError(t, call(context.Background(), client))
	assert.False(t, got.authority)
	assert.False(t, got.service)
}

func TestServiceIdentity_MissingTokenAndUser(t *testing.T) {
	client, _ := startHop(t, &mockProvisioner{}, newLogger(), testToken)
	requireCode(t, call(context.Background(), client), connect.CodeUnauthenticated)
}

func TestServiceIdentity_DisabledWithoutConfiguredToken(t *testing.T) {
	// With no server token a presented one is ignored, not compared.
	prov := &mockProvisioner{}
	client, got := startHop(t, prov, newLogger(), "",
		staticHeaders(HeaderServiceToken, "anything", HeaderSyncAuthority, syncAuthorityGranted))

	requireCode(t, call(context.Background(), client), connect.CodeUnauthenticated)
	assert.False(t, got.authority)
}

func TestServiceIdentity_TokenNotLogged(t *testing.T) {
	var buf bytes.Buffer
	log := slog.New(slog.NewTextHandler(&buf, nil))
	prov := &mockProvisioner{}
	prov.On("GetOrCreate", mock.Anything, "u1", "").Return(nil, errors.New("db down"))
	client, _ := startHop(t, prov, log, testToken, ForwardIdentityInterceptor(testToken))

	requireCode(t, call(ContextWithUser(context.Background(), &entity.User{ID: "u1"}), client), connect.CodeInternal)
	assert.NotEmpty(t, buf.String(), "the failure path must actually log")
	assert.NotContains(t, buf.String(), testToken)
}
