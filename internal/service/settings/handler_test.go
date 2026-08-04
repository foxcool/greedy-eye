package settings

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"
	apiv1 "github.com/foxcool/greedy-eye/api/v1"
	"github.com/foxcool/greedy-eye/internal/entity"
	"github.com/foxcool/greedy-eye/internal/middleware"
	"github.com/foxcool/greedy-eye/internal/store"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	alice = "019cf264-a506-78f8-9a9e-b658966d529e"
	bob   = "019cf264-a506-78f8-9a9e-b658966d52ff"
)

// fakeStore keys rows the way the table does, by (user_id, key), so a handler
// that dropped the owner from a lookup would read somebody else's row here too.
type fakeStore struct {
	rows map[string]*entity.UserSetting
}

func newFakeStore() *fakeStore { return &fakeStore{rows: map[string]*entity.UserSetting{}} }

func (f *fakeStore) GetUserSetting(_ context.Context, userID, key string) (*entity.UserSetting, error) {
	if s, ok := f.rows[userID+"\x00"+key]; ok {
		return s, nil
	}
	return nil, fmt.Errorf("%w: setting %s", store.ErrNotFound, key)
}

func (f *fakeStore) SetUserSetting(_ context.Context, in *entity.UserSetting) (*entity.UserSetting, error) {
	stored := *in
	stored.UpdatedAt = time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC)
	f.rows[in.UserID+"\x00"+in.Key] = &stored
	return &stored, nil
}

func newHandler(t *testing.T) (*Handler, *fakeStore) {
	t.Helper()
	fake := newFakeStore()
	return NewHandler(fake, slog.New(slog.NewTextHandler(io.Discard, nil))), fake
}

// asUser builds the context the interceptor would have produced.
func asUser(id string) context.Context {
	return middleware.ContextWithUser(context.Background(), &entity.User{ID: id})
}

func set(ctx context.Context, h *Handler, key, value string) (*connect.Response[apiv1.SetSettingResponse], error) {
	return h.SetSetting(ctx, connect.NewRequest(&apiv1.SetSettingRequest{Key: key, Value: value}))
}

func get(ctx context.Context, h *Handler, key string) (*connect.Response[apiv1.GetSettingResponse], error) {
	return h.GetSetting(ctx, connect.NewRequest(&apiv1.GetSettingRequest{Key: key}))
}

func TestSetThenGetReturnsTheValueUnchanged(t *testing.T) {
	h, _ := newHandler(t)
	ctx := asUser(alice)

	// Integers matter here: a Struct-typed value would come back as 1.0 and 2.0,
	// which is exactly why the field is JSON text.
	const layout = `{"version":1,"widgets":[{"id":"a","type":"heatmap","order":2,"size":"l"}]}`

	written, err := set(ctx, h, KeyDashboard, layout)
	require.NoError(t, err)
	assert.Equal(t, layout, written.Msg.Setting.Value)

	read, err := get(ctx, h, KeyDashboard)
	require.NoError(t, err)
	assert.Equal(t, layout, read.Msg.Setting.Value)
	assert.Equal(t, KeyDashboard, read.Msg.Setting.Key)
	assert.NotNil(t, read.Msg.Setting.UpdatedAt)
}

func TestSetReplacesThePreviousValue(t *testing.T) {
	h, _ := newHandler(t)
	ctx := asUser(alice)

	_, err := set(ctx, h, KeyDashboard, `{"version":1,"widgets":[]}`)
	require.NoError(t, err)
	_, err = set(ctx, h, KeyDashboard, `{"version":1,"widgets":["x"]}`)
	require.NoError(t, err)

	read, err := get(ctx, h, KeyDashboard)
	require.NoError(t, err)
	assert.Equal(t, `{"version":1,"widgets":["x"]}`, read.Msg.Setting.Value)
}

func TestOneUserNeverSeesAnother(t *testing.T) {
	h, _ := newHandler(t)

	_, err := set(asUser(alice), h, KeyDashboard, `{"owner":"alice"}`)
	require.NoError(t, err)

	// Bob has written nothing, so his read is NotFound rather than Alice's row.
	_, err = get(asUser(bob), h, KeyDashboard)
	require.Error(t, err)
	assert.Equal(t, connect.CodeNotFound, connect.CodeOf(err))

	// And Bob writing his own does not disturb hers.
	_, err = set(asUser(bob), h, KeyDashboard, `{"owner":"bob"}`)
	require.NoError(t, err)

	hers, err := get(asUser(alice), h, KeyDashboard)
	require.NoError(t, err)
	assert.Equal(t, `{"owner":"alice"}`, hers.Msg.Setting.Value)
}

func TestUnsetKeyIsNotFoundNotEmpty(t *testing.T) {
	h, _ := newHandler(t)

	// The caller's own default is not the service's to invent: an empty layout
	// and no layout at all are different instructions to the frontend.
	_, err := get(asUser(alice), h, KeyDashboard)
	require.Error(t, err)
	assert.Equal(t, connect.CodeNotFound, connect.CodeOf(err))
}

func TestWithoutAUserNothingIsReachable(t *testing.T) {
	h, _ := newHandler(t)

	_, err := get(context.Background(), h, KeyDashboard)
	require.Error(t, err)
	assert.Equal(t, connect.CodeUnauthenticated, connect.CodeOf(err))

	_, err = set(context.Background(), h, KeyDashboard, `{}`)
	require.Error(t, err)
	assert.Equal(t, connect.CodeUnauthenticated, connect.CodeOf(err))
}

func TestUnknownKeyIsRefused(t *testing.T) {
	h, fake := newHandler(t)
	ctx := asUser(alice)

	for _, key := range []string{"", "dashboard", "dashboard.v2", "../../etc/passwd", "anything"} {
		_, err := set(ctx, h, key, `{}`)
		require.Error(t, err, key)
		assert.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err), key)
	}
	assert.Empty(t, fake.rows, "a refused key must not reach the store")
}

func TestKeyIsTrimmedNotGuessed(t *testing.T) {
	h, _ := newHandler(t)
	ctx := asUser(alice)

	_, err := set(ctx, h, "  "+KeyDashboard+" ", `{"v":1}`)
	require.NoError(t, err)

	read, err := get(ctx, h, KeyDashboard)
	require.NoError(t, err)
	assert.Equal(t, `{"v":1}`, read.Msg.Setting.Value)
}

func TestValueMustBeJSON(t *testing.T) {
	h, fake := newHandler(t)
	ctx := asUser(alice)

	for _, value := range []string{"", "not json", `{"unclosed":`, `{'single':'quotes'}`} {
		_, err := set(ctx, h, KeyDashboard, value)
		require.Error(t, err, value)
		assert.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err), value)
	}
	assert.Empty(t, fake.rows, "a value that is not JSON must not reach a jsonb column")
}

func TestValueIsCapped(t *testing.T) {
	h, fake := newHandler(t)
	ctx := asUser(alice)

	// Valid JSON, just far too much of it: without the cap this endpoint is
	// per-user file storage that anyone with a session can fill.
	huge := `{"pad":"` + strings.Repeat("x", maxValueBytes) + `"}`
	_, err := set(ctx, h, KeyDashboard, huge)
	require.Error(t, err)
	assert.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))
	assert.Empty(t, fake.rows)
}

func TestAnyJSONDocumentIsAcceptable(t *testing.T) {
	h, _ := newHandler(t)
	ctx := asUser(alice)

	// The service does not own the shape of a value. A future setting that is a
	// bare number or an array must not need a code change here.
	for _, value := range []string{`{}`, `[]`, `3`, `"text"`, `null`, `true`} {
		_, err := set(ctx, h, KeyDashboard, value)
		require.NoError(t, err, value)

		read, err := get(ctx, h, KeyDashboard)
		require.NoError(t, err, value)
		assert.Equal(t, value, read.Msg.Setting.Value, value)
	}
}
