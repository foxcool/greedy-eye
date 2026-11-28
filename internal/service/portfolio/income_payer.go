package portfolio

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"

	"connectrpc.com/connect"
	apiv1 "github.com/foxcool/greedy-eye/api/v1"
	"github.com/foxcool/greedy-eye/internal/entity"
	"github.com/foxcool/greedy-eye/internal/middleware"
)

// IncomePayerRefFunc reads, from an income operation as the provider sent it,
// the id the paper that paid is bound by in the catalogue; "" when it names
// none.
type IncomePayerRefFunc = func(operation json.RawMessage) (string, error)

// WithIncomePayerRefs returns a new Handler able to read the paying paper back
// from the income events each provider's sync recorded, keyed by provider
// slug. Events of a provider without one are never backfilled or named as
// unresolved.
func (h *Handler) WithIncomePayerRefs(byProvider map[string]IncomePayerRefFunc) *Handler {
	copied := h.clone()
	copied.incomePayerRefs = byProvider
	return copied
}

// payerRef is a paper as the catalogue binds it: an id in the namespace of
// the provider that issued it.
type payerRef struct {
	source, ref string
}

// recordedPayerRef reads the paper an income event names from its raw, the
// way the provider's sync kept it (incomeRaw). Zero when the event names none
// or its provider cannot say.
func (h *Handler) recordedPayerRef(e *entity.LedgerEvent) (payerRef, error) {
	provider, ok := strings.CutPrefix(e.Source, entity.LedgerSyncSource(""))
	if !ok || !entity.CanHavePayer(e.Kind, e.IncomeKind) {
		return payerRef{}, nil
	}
	decode := h.incomePayerRefs[provider]
	if decode == nil {
		return payerRef{}, nil
	}
	var raw incomeRaw
	if err := json.Unmarshal(e.Raw, &raw); err != nil {
		return payerRef{}, fmt.Errorf("event %s: decode raw: %w", e.ID, err)
	}
	if len(raw.Operation) == 0 {
		return payerRef{}, nil
	}
	ref, err := decode(raw.Operation)
	if err != nil || ref == "" {
		return payerRef{}, err
	}
	return payerRef{source: provider, ref: ref}, nil
}

// payerResolver finds the catalogue asset a paper is bound to, once per paper.
type payerResolver struct {
	h        *Handler
	resolved map[payerRef]string
}

func (h *Handler) newPayerResolver() *payerResolver {
	return &payerResolver{h: h, resolved: map[payerRef]string{}}
}

// asset is the id of the asset bound to the paper, "" when the catalogue
// holds none. It never creates one: an income operation says which paper
// paid, not on which market it trades, and a guessed listing is the thing
// asset identity refuses (ADR-006). A paper the account holds is in the
// catalogue already, because the balance sync wrote it first.
//
// shortcut: one ListAssets plus a GetAsset per candidate for each paper, as
// the catalogue has no read-only lookup by bound ref; upgrade when it gets
// one (personal-khyz).
func (r *payerResolver) asset(ctx context.Context, p payerRef) (string, error) {
	if id, ok := r.resolved[p]; ok {
		return id, nil
	}
	candidates, err := r.h.assetsMatching(ctx, p.ref)
	if err != nil {
		return "", fmt.Errorf("look up %s %s: %w", p.source, p.ref, err)
	}
	// The query also matches symbols and names; only a binding in this
	// namespace names the paper. GetAsset is what carries the bindings.
	var id string
	for _, a := range candidates {
		full, err := r.h.mdClient.GetAsset(ctx, connect.NewRequest(&apiv1.GetAssetRequest{Id: a.GetId()}))
		if err != nil {
			return "", fmt.Errorf("look up %s %s: %w", p.source, p.ref, err)
		}
		for _, b := range full.Msg.GetExternalRefs() {
			if b.GetSource() == p.source && b.GetRef() == p.ref {
				id = a.GetId()
			}
		}
		if id != "" {
			break
		}
	}
	r.resolved[p] = id
	return id, nil
}

// unresolvedPayers counts income events per paper the catalogue does not hold.
type unresolvedPayers map[payerRef]int32

// refs names the papers, sorted, for a log line.
func (u unresolvedPayers) refs() []string {
	out := make([]string, 0, len(u))
	for p := range u {
		out = append(out, p.source+":"+p.ref)
	}
	slices.Sort(out)
	return out
}

func (u unresolvedPayers) toProto() []*apiv1.UnresolvedPayer {
	out := make([]*apiv1.UnresolvedPayer, 0, len(u))
	for p, n := range u {
		out = append(out, &apiv1.UnresolvedPayer{RefSource: p.source, Ref: p.ref, Events: n})
	}
	slices.SortFunc(out, func(a, b *apiv1.UnresolvedPayer) int {
		return cmp.Or(cmp.Compare(a.RefSource, b.RefSource), cmp.Compare(a.Ref, b.Ref))
	})
	return out
}

// BackfillIncomePayers names the payer of every income event that could name
// one and does not, from the operation it kept. What the catalogue still does
// not hold is returned, not failed: a later sync may bring it in, and a rerun
// picks it up.
func (h *Handler) BackfillIncomePayers(ctx context.Context, _ *connect.Request[apiv1.BackfillIncomePayersRequest]) (*connect.Response[apiv1.BackfillIncomePayersResponse], error) {
	user, ok := middleware.UserFromContext(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("user not found in context"))
	}
	if !user.IsAdmin() {
		return nil, connect.NewError(connect.CodePermissionDenied, errors.New("backfilling income payers is admin-only"))
	}
	if h.mdClient == nil {
		return nil, connect.NewError(connect.CodeUnimplemented, errors.New("market data client not configured"))
	}

	resolver := h.newPayerResolver()
	unresolved := unresolvedPayers{}
	out := &apiv1.BackfillIncomePayersResponse{}
	opts := ListLedgerOpts{Kind: entity.LedgerKindIncome, PayerMissing: true, PageSize: maxImportBatch}
	for {
		page, next, err := h.store.ListLedgerEvents(ctx, opts)
		if err != nil {
			return nil, toConnectError(err)
		}
		for _, e := range page {
			out.EventsRead++
			ref, err := h.recordedPayerRef(e)
			if err != nil {
				// One unreadable raw does not stop the rest; it stays without a
				// payer and the next run meets it again.
				h.log.Warn("income event payer not readable", "event_id", e.ID, "error", err)
				continue
			}
			if ref == (payerRef{}) {
				continue // no paper paid it
			}
			assetID, err := resolver.asset(ctx, ref)
			if err != nil {
				return nil, connect.NewError(connect.CodeInternal, err)
			}
			if assetID == "" {
				unresolved[ref]++
				continue
			}
			set, err := h.store.SetLedgerEventPayer(ctx, e.ID, assetID)
			if err != nil {
				return nil, toConnectError(err)
			}
			if set {
				out.PayersSet++
			}
		}
		if next == "" || len(page) == 0 {
			break
		}
		opts.PageToken = next
	}
	out.UnresolvedPayers = unresolved.toProto()
	h.log.Info("income payers backfilled",
		"events_read", out.EventsRead, "payers_set", out.PayersSet, "unresolved", unresolved.refs())
	return connect.NewResponse(out), nil
}
