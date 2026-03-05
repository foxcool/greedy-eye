package middleware

import (
	"context"
	"net/http"
	"testing"
	"log/slog"
	"os"

	"connectrpc.com/connect"
	"github.com/foxcool/greedy-eye/internal/entity"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// --- Mock UserProvisioner ---

type mockProvisioner struct {
	mock.Mock
}

func (m *mockProvisioner) GetOrCreate(ctx context.Context, id string, email string) (*entity.User, error) {
	args := m.Called(ctx, id, email)
	if v := args.Get(0); v != nil {
		return v.(*entity.User), args.Error(1)
	}
	return nil, args.Error(1)
}

// --- Helpers ---

func newLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
}

type mockRequest struct {
	connect.AnyRequest
	headers http.Header
}

func (r *mockRequest) Header() http.Header {
	return r.headers
}

func (r *mockRequest) Spec() connect.Spec {
	return connect.Spec{Procedure: "/test.Service/Method"}
}

func captureNext(captured *context.Context) connect.UnaryFunc {
	return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
		if captured != nil {
			*captured = ctx
		}
		return nil, nil
	}
}

// --- Tests ---

func TestUserProvisioningInterceptor_MissingHeader(t *testing.T) {
	prov := &mockProvisioner{}
	interceptor := UserProvisioningInterceptor(prov, newLogger())

	req := &mockRequest{headers: http.Header{}}
	fn := interceptor(captureNext(nil))
	_, err := fn(context.Background(), req)

	require.Error(t, err)
	var connectErr *connect.Error
	require.ErrorAs(t, err, &connectErr)
	assert.Equal(t, connect.CodeUnauthenticated, connectErr.Code())
	prov.AssertNotCalled(t, "GetOrCreate")
}

func TestUserProvisioningInterceptor_HappyPath(t *testing.T) {
	const userID = "01932d35-6a1e-7000-8000-000000000001"
	const email = "user@example.com"

	expectedUser := &entity.User{ID: userID, Email: email, Name: email}

	prov := &mockProvisioner{}
	prov.On("GetOrCreate", mock.Anything, userID, email).Return(expectedUser, nil)

	interceptor := UserProvisioningInterceptor(prov, newLogger())

	req := &mockRequest{headers: http.Header{
		"X-User-Id":    []string{userID},
		"X-User-Email": []string{email},
	}}

	var capturedCtx context.Context
	fn := interceptor(captureNext(&capturedCtx))
	_, err := fn(context.Background(), req)

	require.NoError(t, err)
	prov.AssertExpectations(t)

	u, ok := UserFromContext(capturedCtx)
	require.True(t, ok)
	assert.Equal(t, userID, u.ID)
	assert.Equal(t, email, u.Email)
}

func TestUserProvisioningInterceptor_StoreError(t *testing.T) {
	const userID = "01932d35-6a1e-7000-8000-000000000002"

	prov := &mockProvisioner{}
	prov.On("GetOrCreate", mock.Anything, userID, "").Return(nil, assert.AnError)

	interceptor := UserProvisioningInterceptor(prov, newLogger())

	req := &mockRequest{headers: http.Header{
		"X-User-Id": []string{userID},
	}}

	fn := interceptor(captureNext(nil))
	_, err := fn(context.Background(), req)

	require.Error(t, err)
	var connectErr *connect.Error
	require.ErrorAs(t, err, &connectErr)
	assert.Equal(t, connect.CodeInternal, connectErr.Code())
	prov.AssertExpectations(t)
}
