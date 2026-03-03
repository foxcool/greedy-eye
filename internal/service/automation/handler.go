package automation

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"

	"connectrpc.com/connect"
	apiv1 "github.com/foxcool/greedy-eye/internal/api/v1"
	"github.com/foxcool/greedy-eye/internal/api/v1/apiv1connect"
	"github.com/foxcool/greedy-eye/internal/entity"
	"github.com/foxcool/greedy-eye/internal/store"
	"google.golang.org/protobuf/types/known/emptypb"
	"google.golang.org/protobuf/types/known/structpb"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// Handler implements apiv1connect.AutomationServiceHandler.
type Handler struct {
	apiv1connect.UnimplementedAutomationServiceHandler
	store Store
	log   *slog.Logger
}

func NewHandler(store Store, log *slog.Logger) *Handler {
	return &Handler{store: store, log: log}
}

// --- Rule CRUD ---

func (h *Handler) CreateRule(ctx context.Context, req *connect.Request[apiv1.CreateRuleRequest]) (*connect.Response[apiv1.Rule], error) {
	if req.Msg.Rule == nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("rule is required"))
	}

	r, err := ruleFromProto(req.Msg.Rule)
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}

	created, err := h.store.CreateRule(ctx, r)
	if err != nil {
		return nil, toConnectError(err)
	}

	proto, err := ruleToProto(created)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	return connect.NewResponse(proto), nil
}

func (h *Handler) GetRule(ctx context.Context, req *connect.Request[apiv1.GetRuleRequest]) (*connect.Response[apiv1.Rule], error) {
	if req.Msg.Id == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("rule ID is required"))
	}

	r, err := h.store.GetRule(ctx, req.Msg.Id)
	if err != nil {
		return nil, toConnectError(err)
	}

	proto, err := ruleToProto(r)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	return connect.NewResponse(proto), nil
}

func (h *Handler) UpdateRule(ctx context.Context, req *connect.Request[apiv1.UpdateRuleRequest]) (*connect.Response[apiv1.Rule], error) {
	if req.Msg.Rule == nil || req.Msg.Rule.Id == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("rule with ID is required"))
	}

	var fields []string
	if req.Msg.UpdateMask != nil {
		fields = req.Msg.UpdateMask.Paths
	}

	r, err := ruleFromProto(req.Msg.Rule)
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}

	updated, err := h.store.UpdateRule(ctx, r, fields)
	if err != nil {
		return nil, toConnectError(err)
	}

	proto, err := ruleToProto(updated)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	return connect.NewResponse(proto), nil
}

func (h *Handler) DeleteRule(ctx context.Context, req *connect.Request[apiv1.DeleteRuleRequest]) (*connect.Response[emptypb.Empty], error) {
	if req.Msg.Id == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("rule ID is required"))
	}

	if err := h.store.DeleteRule(ctx, req.Msg.Id); err != nil {
		return nil, toConnectError(err)
	}

	return connect.NewResponse(&emptypb.Empty{}), nil
}

func (h *Handler) ListRules(ctx context.Context, req *connect.Request[apiv1.ListRulesRequest]) (*connect.Response[apiv1.ListRulesResponse], error) {
	opts := ListRulesOpts{}
	if req.Msg.UserId != nil {
		opts.UserID = *req.Msg.UserId
	}
	if req.Msg.PortfolioId != nil {
		opts.PortfolioID = *req.Msg.PortfolioId
	}
	if req.Msg.RuleType != nil {
		opts.RuleType = *req.Msg.RuleType
	}
	if req.Msg.Status != nil {
		opts.Status = entity.RuleStatus(*req.Msg.Status)
	}
	if req.Msg.PageSize != nil {
		opts.PageSize = int(*req.Msg.PageSize)
	}
	if req.Msg.PageToken != nil {
		opts.PageToken = *req.Msg.PageToken
	}

	rules, nextPageToken, err := h.store.ListRules(ctx, opts)
	if err != nil {
		return nil, toConnectError(err)
	}

	protoRules := make([]*apiv1.Rule, 0, len(rules))
	for _, r := range rules {
		p, err := ruleToProto(r)
		if err != nil {
			return nil, connect.NewError(connect.CodeInternal, err)
		}
		protoRules = append(protoRules, p)
	}

	return connect.NewResponse(&apiv1.ListRulesResponse{
		Rules:         protoRules,
		NextPageToken: nextPageToken,
	}), nil
}

// --- Rule status management ---
// These are thin wrappers: fetch rule → update status → persist.

func (h *Handler) EnableRule(ctx context.Context, req *connect.Request[apiv1.EnableRuleRequest]) (*connect.Response[apiv1.Rule], error) {
	return h.setRuleStatus(ctx, req.Msg.RuleId, entity.RuleStatusActive)
}

func (h *Handler) DisableRule(ctx context.Context, req *connect.Request[apiv1.DisableRuleRequest]) (*connect.Response[apiv1.Rule], error) {
	return h.setRuleStatus(ctx, req.Msg.RuleId, entity.RuleStatusDisabled)
}

func (h *Handler) PauseRule(ctx context.Context, req *connect.Request[apiv1.PauseRuleRequest]) (*connect.Response[apiv1.Rule], error) {
	return h.setRuleStatus(ctx, req.Msg.RuleId, entity.RuleStatusPaused)
}

func (h *Handler) ResumeRule(ctx context.Context, req *connect.Request[apiv1.ResumeRuleRequest]) (*connect.Response[apiv1.Rule], error) {
	return h.setRuleStatus(ctx, req.Msg.RuleId, entity.RuleStatusActive)
}

func (h *Handler) setRuleStatus(ctx context.Context, ruleID string, status entity.RuleStatus) (*connect.Response[apiv1.Rule], error) {
	if ruleID == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("rule ID is required"))
	}

	r, err := h.store.GetRule(ctx, ruleID)
	if err != nil {
		return nil, toConnectError(err)
	}

	r.Status = status
	updated, err := h.store.UpdateRule(ctx, r, []string{"status"})
	if err != nil {
		return nil, toConnectError(err)
	}

	proto, err := ruleToProto(updated)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	return connect.NewResponse(proto), nil
}

// --- Rule validation ---

func (h *Handler) ValidateRule(ctx context.Context, req *connect.Request[apiv1.ValidateRuleRequest]) (*connect.Response[apiv1.ValidateRuleResponse], error) {
	if req.Msg.Rule == nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("rule is required"))
	}

	var errs []string
	r := req.Msg.Rule
	if r.Name == "" {
		errs = append(errs, "rule name is required")
	}
	if r.RuleType == "" {
		errs = append(errs, "rule_type is required")
	}
	if r.PortfolioId == "" {
		errs = append(errs, "portfolio_id is required")
	}

	return connect.NewResponse(&apiv1.ValidateRuleResponse{
		Valid:            len(errs) == 0,
		ValidationErrors: errs,
	}), nil
}

// --- RuleExecution CRUD ---

func (h *Handler) CreateRuleExecution(ctx context.Context, req *connect.Request[apiv1.CreateRuleExecutionRequest]) (*connect.Response[apiv1.RuleExecution], error) {
	if req.Msg.RuleExecution == nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("rule_execution is required"))
	}

	e, err := ruleExecutionFromProto(req.Msg.RuleExecution)
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}

	created, err := h.store.CreateRuleExecution(ctx, e)
	if err != nil {
		return nil, toConnectError(err)
	}

	proto, err := ruleExecutionToProto(created)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	return connect.NewResponse(proto), nil
}

func (h *Handler) GetRuleExecution(ctx context.Context, req *connect.Request[apiv1.GetRuleExecutionRequest]) (*connect.Response[apiv1.RuleExecution], error) {
	if req.Msg.Id == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("execution ID is required"))
	}

	e, err := h.store.GetRuleExecution(ctx, req.Msg.Id)
	if err != nil {
		return nil, toConnectError(err)
	}

	proto, err := ruleExecutionToProto(e)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	return connect.NewResponse(proto), nil
}

func (h *Handler) UpdateRuleExecution(ctx context.Context, req *connect.Request[apiv1.UpdateRuleExecutionRequest]) (*connect.Response[apiv1.RuleExecution], error) {
	if req.Msg.RuleExecution == nil || req.Msg.RuleExecution.Id == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("rule_execution with ID is required"))
	}

	var fields []string
	if req.Msg.UpdateMask != nil {
		fields = req.Msg.UpdateMask.Paths
	}

	e, err := ruleExecutionFromProto(req.Msg.RuleExecution)
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}

	updated, err := h.store.UpdateRuleExecution(ctx, e, fields)
	if err != nil {
		return nil, toConnectError(err)
	}

	proto, err := ruleExecutionToProto(updated)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	return connect.NewResponse(proto), nil
}

func (h *Handler) ListRuleExecutions(ctx context.Context, req *connect.Request[apiv1.ListRuleExecutionsRequest]) (*connect.Response[apiv1.ListRuleExecutionsResponse], error) {
	opts := ListRuleExecutionsOpts{}
	if req.Msg.RuleId != nil {
		opts.RuleID = *req.Msg.RuleId
	}
	if req.Msg.PortfolioId != nil {
		opts.PortfolioID = *req.Msg.PortfolioId
	}
	if req.Msg.UserId != nil {
		opts.UserID = *req.Msg.UserId
	}
	if req.Msg.Status != nil {
		opts.Status = entity.ExecutionStatus(*req.Msg.Status)
	}
	if req.Msg.From != nil {
		t := req.Msg.From.AsTime()
		opts.From = &t
	}
	if req.Msg.To != nil {
		t := req.Msg.To.AsTime()
		opts.To = &t
	}
	if req.Msg.PageSize != nil {
		opts.PageSize = int(*req.Msg.PageSize)
	}
	if req.Msg.PageToken != nil {
		opts.PageToken = *req.Msg.PageToken
	}

	executions, nextPageToken, err := h.store.ListRuleExecutions(ctx, opts)
	if err != nil {
		return nil, toConnectError(err)
	}

	protoExecs := make([]*apiv1.RuleExecution, 0, len(executions))
	for _, e := range executions {
		p, err := ruleExecutionToProto(e)
		if err != nil {
			return nil, connect.NewError(connect.CodeInternal, err)
		}
		protoExecs = append(protoExecs, p)
	}

	return connect.NewResponse(&apiv1.ListRuleExecutionsResponse{
		RuleExecutions: protoExecs,
		NextPageToken:  nextPageToken,
	}), nil
}

// --- Stubs: execution engine (not yet implemented) ---

func (h *Handler) ExecuteRule(ctx context.Context, req *connect.Request[apiv1.ExecuteRuleRequest]) (*connect.Response[apiv1.ExecuteRuleResponse], error) {
	// TODO: implement rule execution engine
	return nil, connect.NewError(connect.CodeUnimplemented, errors.New("ExecuteRule not implemented"))
}

func (h *Handler) ExecuteRuleAsync(ctx context.Context, req *connect.Request[apiv1.ExecuteRuleAsyncRequest]) (*connect.Response[apiv1.ExecuteRuleAsyncResponse], error) {
	// TODO: implement async rule execution
	return nil, connect.NewError(connect.CodeUnimplemented, errors.New("ExecuteRuleAsync not implemented"))
}

func (h *Handler) CancelRuleExecution(ctx context.Context, req *connect.Request[apiv1.CancelRuleExecutionRequest]) (*connect.Response[emptypb.Empty], error) {
	// TODO: implement execution cancellation
	return nil, connect.NewError(connect.CodeUnimplemented, errors.New("CancelRuleExecution not implemented"))
}

func (h *Handler) SimulateRule(ctx context.Context, req *connect.Request[apiv1.SimulateRuleRequest]) (*connect.Response[apiv1.SimulateRuleResponse], error) {
	// TODO: implement rule simulation
	return nil, connect.NewError(connect.CodeUnimplemented, errors.New("SimulateRule not implemented"))
}

// --- Error conversion ---

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

// --- Conversion helpers ---

func ruleFromProto(p *apiv1.Rule) (*entity.Rule, error) {
	r := &entity.Rule{
		ID:          p.Id,
		Name:        p.Name,
		Description: p.Description,
		RuleType:    p.RuleType,
		PortfolioID: p.PortfolioId,
		UserID:      p.UserId,
		Status:      entity.RuleStatus(p.Status),
	}

	if p.Configuration != nil {
		b, err := p.Configuration.MarshalJSON()
		if err != nil {
			return nil, err
		}
		r.Configuration = json.RawMessage(b)
	}

	if p.Schedule != nil {
		s := &entity.RuleSchedule{
			CronExpression: p.Schedule.CronExpression,
			Timezone:       p.Schedule.Timezone,
			OneTime:        p.Schedule.OneTime,
		}
		if p.Schedule.ExecuteAfter != nil {
			t := p.Schedule.ExecuteAfter.AsTime()
			s.ExecuteAfter = &t
		}
		r.Schedule = s
	}

	return r, nil
}

func ruleToProto(r *entity.Rule) (*apiv1.Rule, error) {
	p := &apiv1.Rule{
		Id:          r.ID,
		Name:        r.Name,
		Description: r.Description,
		RuleType:    r.RuleType,
		PortfolioId: r.PortfolioID,
		UserId:      r.UserID,
		Status:      apiv1.RuleStatus(r.Status),
		CreatedAt:   timestamppb.New(r.CreatedAt),
		UpdatedAt:   timestamppb.New(r.UpdatedAt),
	}

	if len(r.Configuration) > 0 {
		s := &structpb.Struct{}
		if err := s.UnmarshalJSON(r.Configuration); err != nil {
			return nil, err
		}
		p.Configuration = s
	}

	if r.Schedule != nil {
		s := &apiv1.RuleSchedule{
			CronExpression: r.Schedule.CronExpression,
			Timezone:       r.Schedule.Timezone,
			OneTime:        r.Schedule.OneTime,
		}
		if r.Schedule.ExecuteAfter != nil {
			s.ExecuteAfter = timestamppb.New(*r.Schedule.ExecuteAfter)
		}
		p.Schedule = s
	}

	return p, nil
}

func ruleExecutionFromProto(p *apiv1.RuleExecution) (*entity.RuleExecution, error) {
	e := &entity.RuleExecution{
		ID:                    p.Id,
		RuleID:                p.RuleId,
		Status:                entity.ExecutionStatus(p.Status),
		CreatedTransactionIDs: p.CreatedTransactionIds,
		AffectedHoldingIDs:    p.AffectedHoldingIds,
		TransactionsCreated:   p.TransactionsCreated,
	}
	if p.PortfolioId != nil {
		e.PortfolioID = *p.PortfolioId
	}
	if p.UserId != nil {
		e.UserID = *p.UserId
	}
	if p.ErrorMessage != nil {
		e.ErrorMessage = *p.ErrorMessage
	}
	if p.StartedAt != nil {
		e.StartedAt = p.StartedAt.AsTime()
	}
	if p.CompletedAt != nil {
		t := p.CompletedAt.AsTime()
		e.CompletedAt = &t
	}
	if p.ExecutionSummary != nil {
		b, err := p.ExecutionSummary.MarshalJSON()
		if err != nil {
			return nil, err
		}
		e.ExecutionSummary = json.RawMessage(b)
	}
	return e, nil
}

func ruleExecutionToProto(e *entity.RuleExecution) (*apiv1.RuleExecution, error) {
	p := &apiv1.RuleExecution{
		Id:                    e.ID,
		RuleId:                e.RuleID,
		Status:                apiv1.ExecutionStatus(e.Status),
		CreatedTransactionIds: e.CreatedTransactionIDs,
		AffectedHoldingIds:    e.AffectedHoldingIDs,
		TransactionsCreated:   e.TransactionsCreated,
		StartedAt:             timestamppb.New(e.StartedAt),
	}
	if e.PortfolioID != "" {
		p.PortfolioId = &e.PortfolioID
	}
	if e.UserID != "" {
		p.UserId = &e.UserID
	}
	if e.ErrorMessage != "" {
		p.ErrorMessage = &e.ErrorMessage
	}
	if e.CompletedAt != nil {
		p.CompletedAt = timestamppb.New(*e.CompletedAt)
	}
	if len(e.ExecutionSummary) > 0 {
		s := &structpb.Struct{}
		if err := s.UnmarshalJSON(e.ExecutionSummary); err != nil {
			return nil, err
		}
		p.ExecutionSummary = s
	}
	return p, nil
}
