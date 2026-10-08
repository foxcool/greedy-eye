package portfolio

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/types/known/timestamppb"

	apiv1 "github.com/foxcool/greedy-eye/api/v1"
	"github.com/foxcool/greedy-eye/internal/entity"
	"github.com/foxcool/greedy-eye/internal/middleware"
)

// healthAccountsPageSize bounds one page of the account listing behind a
// health report.
const healthAccountsPageSize = 100

// PriceSourceHealthSource reports the price registry a user's work resolves,
// with what each source can do now and which accounts were passed over.
type PriceSourceHealthSource interface {
	PriceSourceHealth(ctx context.Context, userID string) (*entity.PriceSourceReport, error)
}

// WithPriceSourceHealth returns a new Handler that reports price source health.
// Without it GetAccountHealth answers UNKNOWN for sources rather than OK: the
// rate limiter that knows a source is paused lives in the process that prices,
// and a handler that cannot ask it has nothing to say.
func (h *Handler) WithPriceSourceHealth(src PriceSourceHealthSource) *Handler {
	copied := h.clone()
	copied.sourceHealth = src
	return copied
}

// GetAccountHealth reports per account, and per price source the caller's
// prices depend on, whether it is producing anything — and if not, why.
//
// Upstream error text goes to admins only; everyone else gets the server's
// phrase. A shared source is named by provider slug and never by the account
// that serves it, which the caller may not own.
func (h *Handler) GetAccountHealth(
	ctx context.Context,
	req *connect.Request[apiv1.GetAccountHealthRequest],
) (*connect.Response[apiv1.GetAccountHealthResponse], error) {
	user, ok := middleware.UserFromContext(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("user not found in context"))
	}

	ownerID := user.ID
	var accounts []*entity.Account
	if id := req.Msg.GetAccountId(); id != "" {
		a, err := h.ownedAccount(ctx, id)
		if err != nil {
			return nil, err
		}
		ownerID = a.UserID
		accounts = []*entity.Account{a}
	} else {
		var err error
		if accounts, err = h.allAccountsOf(ctx, ownerID); err != nil {
			return nil, toConnectError(err)
		}
	}

	deferrals, err := h.store.ListSyncDeferrals(ctx, ownerID, req.Msg.GetAccountId())
	if err != nil {
		return nil, toConnectError(err)
	}
	deferred := make(map[string]*entity.SyncDeferral, len(deferrals))
	for _, d := range deferrals {
		deferred[d.AccountID] = d
	}

	var report *entity.PriceSourceReport
	if h.sourceHealth != nil {
		if report, err = h.sourceHealth.PriceSourceHealth(ctx, ownerID); err != nil {
			return nil, toConnectError(err)
		}
	}

	admin := user.IsAdmin()
	now := time.Now()
	resp := &apiv1.GetAccountHealthResponse{SourcesState: apiv1.HealthState_HEALTH_STATE_UNKNOWN}
	for _, a := range accounts {
		ah, err := h.accountHealth(ctx, a, report, deferred[a.ID], admin, now)
		if err != nil {
			return nil, toConnectError(err)
		}
		resp.Accounts = append(resp.Accounts, ah)
	}

	if report != nil {
		resp.Sources = sourceHealth(report, admin)
		resp.SourcesState = apiv1.HealthState_HEALTH_STATE_OK
		for _, s := range resp.Sources {
			resp.SourcesState = worse(resp.SourcesState, s.State)
		}
	}
	return connect.NewResponse(resp), nil
}

// accountHealth gathers one account's reasons: its price role as the registry
// sees it, then what its syncs have left behind. report and deferral may be nil.
func (h *Handler) accountHealth(
	ctx context.Context,
	a *entity.Account,
	report *entity.PriceSourceReport,
	deferral *entity.SyncDeferral,
	admin bool,
	now time.Time,
) (*apiv1.AccountHealth, error) {
	if a.Disabled() {
		// Diagnosing an account nobody is asking would report the absence of
		// work its owner chose, as faults.
		return &apiv1.AccountHealth{
			AccountId:   a.ID,
			AccountName: a.Name,
			State:       apiv1.HealthState_HEALTH_STATE_DISABLED,
			Reasons: []*apiv1.HealthReason{{
				Kind:    apiv1.HealthReasonKind_HEALTH_REASON_KIND_DISABLED,
				Message: "disabled by its owner: nothing syncs this account or takes it as a provider until it is enabled",
				Since:   timestamppb.New(*a.DisabledAt),
			}},
		}, nil
	}

	var reasons []*apiv1.HealthReason
	state := apiv1.HealthState_HEALTH_STATE_OK
	if report != nil {
		priced := resolverReasons(report, a.ID, admin)
		reasons = append(reasons, priced...)
		state = priceRoleState(a, worstState(priced))
	}

	var sync []*apiv1.HealthReason
	if a.Type == entity.AccountTypeWallet {
		runs, err := h.store.ListChainFailures(ctx, a.ID)
		if err != nil {
			return nil, err
		}
		for _, f := range runs {
			sync = append(sync, chainReason(f, admin))
		}
	}
	if deferral != nil {
		sync = append(sync, deferralReason(deferral, now))
	}

	return &apiv1.AccountHealth{
		AccountId:   a.ID,
		AccountName: a.Name,
		State:       worse(state, worstState(sync)),
		Reasons:     append(reasons, sync...),
	}, nil
}

// allAccountsOf reads every account the user owns, across pages.
func (h *Handler) allAccountsOf(ctx context.Context, userID string) ([]*entity.Account, error) {
	opts := ListAccountsOpts{UserID: userID, PageSize: healthAccountsPageSize}
	var out []*entity.Account
	for {
		page, next, err := h.store.ListAccounts(ctx, opts)
		if err != nil {
			return nil, err
		}
		out = append(out, page...)
		// A store that keeps handing back the same cursor would spin here.
		if next == "" || len(page) == 0 || next == opts.PageToken {
			return out, nil
		}
		opts.PageToken = next
	}
}

// resolverReasons names what the price registry decided about this account:
// passed over, or serving a source that cannot be asked now.
func resolverReasons(report *entity.PriceSourceReport, accountID string, admin bool) []*apiv1.HealthReason {
	var out []*apiv1.HealthReason
	for _, s := range report.Skipped {
		if s.AccountID == accountID {
			out = append(out, skipReason(s, admin))
		}
	}
	for _, s := range report.Sources {
		if s.AccountID == accountID && s.Unusable {
			out = append(out, pausedReason(s))
		}
	}
	return out
}

// priceRoleState is what a problem with the account's price role does to the
// whole account. An account that also syncs balances keeps doing that, so the
// worst it can be is DEGRADED: calling a working exchange account dead because
// its market-data capability prices nothing would be the same kind of lie this
// surface exists to end.
func priceRoleState(a *entity.Account, state apiv1.HealthState) apiv1.HealthState {
	for _, c := range a.Capabilities {
		if c != entity.CapabilityMarketData && state == apiv1.HealthState_HEALTH_STATE_UNUSABLE {
			return apiv1.HealthState_HEALTH_STATE_DEGRADED
		}
	}
	return state
}

func skipReason(s entity.SkippedAccount, admin bool) *apiv1.HealthReason {
	r := &apiv1.HealthReason{}
	switch s.Kind {
	case entity.SkipNoAdapter:
		r.Kind = apiv1.HealthReasonKind_HEALTH_REASON_KIND_NO_ADAPTER
		r.Message = fmt.Sprintf("declares market data, but this build has no price adapter for %q; the capability prices nothing", s.Provider)
	case entity.SkipDisabled:
		r.Kind = apiv1.HealthReasonKind_HEALTH_REASON_KIND_DISABLED
		r.Message = fmt.Sprintf("a %s account that could serve prices is disabled by its owner", s.Provider)
		return r // nothing upstream to detail
	case entity.SkipShadowed:
		r.Kind = apiv1.HealthReasonKind_HEALTH_REASON_KIND_SHADOWED
		r.Message = fmt.Sprintf("another %s account with a different key prices first; this key is never used for prices", s.Provider)
	default:
		r.Kind = apiv1.HealthReasonKind_HEALTH_REASON_KIND_CANNOT_BUILD
		r.Message = fmt.Sprintf("a %s price client cannot be built from this account's settings", s.Provider)
	}
	if admin {
		r.Detail = &s.Reason
	}
	return r
}

func pausedReason(s entity.PriceSourceState) *apiv1.HealthReason {
	r := &apiv1.HealthReason{
		Kind:    apiv1.HealthReasonKind_HEALTH_REASON_KIND_PROVIDER_PAUSED,
		Message: fmt.Sprintf("%s is not asked by unattended work: %s", s.Provider, s.Reason),
	}
	if !s.Until.IsZero() {
		r.Until = timestamppb.New(s.Until)
	}
	return r
}

func chainReason(f *entity.ChainFailure, admin bool) *apiv1.HealthReason {
	chain := f.Chain
	r := &apiv1.HealthReason{
		Kind:     apiv1.HealthReasonKind_HEALTH_REASON_KIND_CHAIN_FAILING,
		Message:  fmt.Sprintf("%s has failed %d sync(s) in a row; its balances are not refreshed", f.Chain, f.Failures),
		Chain:    &chain,
		Failures: intToU32(f.Failures),
		Since:    timestamppb.New(f.FailingSince),
	}
	if admin && f.LastError != "" {
		detail := f.LastError
		r.Detail = &detail
	}
	return r
}

func deferralReason(d *entity.SyncDeferral, now time.Time) *apiv1.HealthReason {
	r := &apiv1.HealthReason{
		Kind:     apiv1.HealthReasonKind_HEALTH_REASON_KIND_SWEEP_DEFERRED,
		Failures: intToU32(d.Misses),
		Until:    timestamppb.New(d.NextAttemptAt),
	}
	if d.NextAttemptAt.After(now) {
		r.Message = fmt.Sprintf("the balance sweep holds this account back after %d sync(s) that left it no fresher", d.Misses)
	} else {
		r.Message = fmt.Sprintf("%d sync(s) left this account no fresher; the sweep may try it again", d.Misses)
	}
	return r
}

// sourceHealth reports every source the registry reached, and every provider
// it only passed over. Named by slug alone: the account behind a shared source
// is not the caller's to know.
func sourceHealth(report *entity.PriceSourceReport, admin bool) []*apiv1.SourceHealth {
	var out []*apiv1.SourceHealth
	reached := make(map[string]bool, len(report.Sources))
	for _, s := range report.Sources {
		reached[s.Provider] = true
		sh := &apiv1.SourceHealth{Provider: s.Provider, State: apiv1.HealthState_HEALTH_STATE_OK}
		if s.Unusable {
			sh.Reasons = []*apiv1.HealthReason{pausedReason(s)}
			sh.State = apiv1.HealthState_HEALTH_STATE_UNUSABLE
		}
		out = append(out, sh)
	}

	// A price provider every account of which failed to build, or was
	// disabled, is a source the caller's prices lack entirely. A slug with no
	// price adapter is not a price source at all, and a shadowed account means
	// another one serves. Disabled and broken together read as broken: the
	// owner's choice must not hide an account that would fail if enabled.
	unreached := map[string]*apiv1.SourceHealth{}
	for _, s := range report.Skipped {
		if s.Provider == "" || reached[s.Provider] {
			continue
		}
		if s.Kind != entity.SkipCannotBuild && s.Kind != entity.SkipDisabled {
			continue
		}
		sh, ok := unreached[s.Provider]
		if !ok {
			sh = &apiv1.SourceHealth{Provider: s.Provider, State: apiv1.HealthState_HEALTH_STATE_DISABLED}
			unreached[s.Provider] = sh
			out = append(out, sh)
		}
		r := skipReason(entity.SkippedAccount{Provider: s.Provider, Kind: s.Kind, Reason: s.Reason}, admin)
		sh.State = worse(sh.State, stateOf(r))
		// The disabled phrase is about the provider, not the account: once.
		if r.Kind == apiv1.HealthReasonKind_HEALTH_REASON_KIND_DISABLED &&
			slices.ContainsFunc(sh.Reasons, func(have *apiv1.HealthReason) bool { return have.Kind == r.Kind }) {
			continue
		}
		sh.Reasons = append(sh.Reasons, r)
	}
	return out
}

// stateOf is the state one reason puts its owner in.
func stateOf(r *apiv1.HealthReason) apiv1.HealthState {
	switch r.Kind {
	case apiv1.HealthReasonKind_HEALTH_REASON_KIND_CHAIN_FAILING,
		apiv1.HealthReasonKind_HEALTH_REASON_KIND_SWEEP_DEFERRED:
		return apiv1.HealthState_HEALTH_STATE_DEGRADED
	case apiv1.HealthReasonKind_HEALTH_REASON_KIND_DISABLED:
		return apiv1.HealthState_HEALTH_STATE_DISABLED
	default:
		return apiv1.HealthState_HEALTH_STATE_UNUSABLE
	}
}

func worstState(reasons []*apiv1.HealthReason) apiv1.HealthState {
	state := apiv1.HealthState_HEALTH_STATE_OK
	for _, r := range reasons {
		state = worse(state, stateOf(r))
	}
	return state
}

// severity orders states for worse; UNKNOWN outranks DEGRADED because "cannot
// tell" must not read as "mostly fine". DISABLED ranks below OK: a source its
// owner turned off is not a fault, and must not colour the whole report.
var severity = map[apiv1.HealthState]int{
	apiv1.HealthState_HEALTH_STATE_DISABLED: 0,
	apiv1.HealthState_HEALTH_STATE_OK:       1,
	apiv1.HealthState_HEALTH_STATE_DEGRADED: 2,
	apiv1.HealthState_HEALTH_STATE_UNKNOWN:  3,
	apiv1.HealthState_HEALTH_STATE_UNUSABLE: 4,
}

func worse(a, b apiv1.HealthState) apiv1.HealthState {
	if severity[b] > severity[a] {
		return b
	}
	return a
}
