// Package settings stores per-user preferences that must follow the user
// across devices. It is a key-value service on purpose: a table per preference
// means a migration per preference.
package settings

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"

	"connectrpc.com/connect"
	apiv1 "github.com/foxcool/greedy-eye/api/v1"
	"github.com/foxcool/greedy-eye/api/v1/apiv1connect"
	"github.com/foxcool/greedy-eye/internal/entity"
	"github.com/foxcool/greedy-eye/internal/middleware"
	"github.com/foxcool/greedy-eye/internal/store"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// KeyDashboard holds the dashboard layout: widget instances, their parameters
// and their order. The version is part of the key so an incompatible layout
// becomes a row the old client never reads, rather than one it misparses.
const KeyDashboard = "dashboard.v1"

// knownKeys is the set a caller may write. Without it this is an unbounded
// per-user blob store reachable by anyone with a session: the quota would be
// whatever the client felt like sending, under names nothing ever reads.
var knownKeys = []string{KeyDashboard}

// maxValueBytes caps one setting. A dashboard layout is a few kilobytes of
// widget instances; the cap is what stops the same endpoint from being used as
// file storage.
const maxValueBytes = 64 * 1024

// Handler implements apiv1connect.SettingsServiceHandler.
type Handler struct {
	apiv1connect.UnimplementedSettingsServiceHandler
	store Store
	log   *slog.Logger
}

func NewHandler(store Store, log *slog.Logger) *Handler {
	return &Handler{store: store, log: log}
}

// GetSetting returns the caller's own setting. NotFound means never written,
// which is the caller's cue to use its own default rather than an empty one.
func (h *Handler) GetSetting(ctx context.Context, req *connect.Request[apiv1.GetSettingRequest]) (*connect.Response[apiv1.GetSettingResponse], error) {
	userID, err := callerID(ctx)
	if err != nil {
		return nil, err
	}
	key, err := validKey(req.Msg.Key)
	if err != nil {
		return nil, err
	}

	setting, err := h.store.GetUserSetting(ctx, userID, key)
	if err != nil {
		return nil, toConnectError(err)
	}
	return connect.NewResponse(&apiv1.GetSettingResponse{Setting: toProto(setting)}), nil
}

// SetSetting writes the caller's own setting, replacing what was there.
func (h *Handler) SetSetting(ctx context.Context, req *connect.Request[apiv1.SetSettingRequest]) (*connect.Response[apiv1.SetSettingResponse], error) {
	userID, err := callerID(ctx)
	if err != nil {
		return nil, err
	}
	key, err := validKey(req.Msg.Key)
	if err != nil {
		return nil, err
	}
	if err := validValue(req.Msg.Value); err != nil {
		return nil, err
	}

	stored, err := h.store.SetUserSetting(ctx, &entity.UserSetting{
		UserID: userID,
		Key:    key,
		Value:  req.Msg.Value,
	})
	if err != nil {
		return nil, toConnectError(err)
	}
	return connect.NewResponse(&apiv1.SetSettingResponse{Setting: toProto(stored)}), nil
}

// callerID reads the owner from the request context. The owner is never taken
// from the request: a user_id field on the wire is a field to forge.
func callerID(ctx context.Context) (string, error) {
	user, ok := middleware.UserFromContext(ctx)
	if !ok || user.ID == "" {
		return "", connect.NewError(connect.CodeUnauthenticated, errors.New("no user in context"))
	}
	return user.ID, nil
}

// validKey normalizes and checks the key against the known set.
func validKey(key string) (string, error) {
	key = strings.TrimSpace(key)
	if key == "" {
		return "", connect.NewError(connect.CodeInvalidArgument, errors.New("key is required"))
	}
	if !slices.Contains(knownKeys, key) {
		return "", connect.NewError(connect.CodeInvalidArgument,
			fmt.Errorf("unknown setting key %q; known keys: %s", key, strings.Join(knownKeys, ", ")))
	}
	return key, nil
}

// validValue checks the two things this service claims about a value: that it
// is JSON, and that it is small. Anything about its shape belongs to whoever
// wrote it.
//
// The size is measured on the text as sent, not as stored. jsonb reformats,
// so the stored size differs slightly — but a limit the caller cannot compute
// before sending is a limit it cannot respect.
func validValue(value string) error {
	if value == "" {
		return connect.NewError(connect.CodeInvalidArgument, errors.New("value is required"))
	}
	if len(value) > maxValueBytes {
		return connect.NewError(connect.CodeInvalidArgument,
			fmt.Errorf("value is %d bytes, limit is %d", len(value), maxValueBytes))
	}
	if !json.Valid([]byte(value)) {
		return connect.NewError(connect.CodeInvalidArgument, errors.New("value must be JSON"))
	}
	return nil
}

func toProto(s *entity.UserSetting) *apiv1.Setting {
	if s == nil {
		return nil
	}
	return &apiv1.Setting{
		Key:       s.Key,
		Value:     s.Value,
		UpdatedAt: timestamppb.New(s.UpdatedAt),
	}
}

func toConnectError(err error) error {
	if errors.Is(err, store.ErrNotFound) {
		return connect.NewError(connect.CodeNotFound, err)
	}
	if errors.Is(err, store.ErrInvalidArgument) {
		return connect.NewError(connect.CodeInvalidArgument, err)
	}
	if errors.Is(err, store.ErrConstraint) {
		return connect.NewError(connect.CodeFailedPrecondition, err)
	}
	return connect.NewError(connect.CodeInternal, err)
}
