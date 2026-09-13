package portfolio

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"connectrpc.com/connect"

	"google.golang.org/protobuf/types/known/timestamppb"

	apiv1 "github.com/foxcool/greedy-eye/api/v1"
	"github.com/foxcool/greedy-eye/internal/entity"
	"github.com/foxcool/greedy-eye/internal/middleware"
)

// Defaults for the balance sweep. They are product decisions rather than
// operator knobs, for the same reason the pricing freshness policy is:
//
//   - MaxAge is what "current" means for an amount. Twelve hours is deliberately
//     coarser than the hourly price sweep, because an amount changes when the
//     owner moves money, not when a market ticks.
//
//   - AccountsPerSweep is the provider budget. One sync of a heavy EVM account
//     fans out to a provider per chain and costs far more than one price call,
//     so the sweep takes the two stalest accounts per fire rather than everything
//     due. With an hourly cron that is up to 48 account syncs a day, and an
//     account that stays stale is picked again next hour instead of all of them
//     competing for the same monthly allowance at once (see personal-a3v).
//
//   - MissBackoff is the pair to the ordering above. A sync that leaves the
//     account no fresher does not move holdings.updated_at, so without a stand
//     -down the same account is the stalest again next hour and takes a slot
//     forever. The first miss costs one fire, and the wait doubles per
//     consecutive miss up to a day.
//
//     A day, not the week the price path caps at: an amount cannot be
//     recalculated afterwards the way a price can, so a broken account has to
//     be re-offered often enough that a repair is noticed without anyone
//     watching. At the cap a permanently dead account costs one of 48 daily
//     slots — 2% — which is the price of finding out it came back.
const (
	defaultSweepMaxAge          = 12 * time.Hour
	defaultSweepAccountsPerFire = 2

	missBackoffBase = time.Hour
	missBackoffCap  = 24 * time.Hour
)

// SweepOpts bounds one balance sweep. A zero value means the defaults above.
type SweepOpts struct {
	// MaxAge is how old an account's newest holding may be before the account
	// is due for a re-read.
	MaxAge time.Duration
	// Limit is how many accounts one sweep may sync.
	Limit int
	// Now is the clock the staleness cutoff is measured from; zero means
	// time.Now.
	Now time.Time
}

func (o SweepOpts) withDefaults() SweepOpts {
	if o.MaxAge <= 0 {
		o.MaxAge = defaultSweepMaxAge
	}
	if o.Limit <= 0 {
		o.Limit = defaultSweepAccountsPerFire
	}
	if o.Now.IsZero() {
		o.Now = time.Now()
	}
	return o
}

// SweepReport is what one balance sweep did, for the caller to log. A scheduled
// sync has no user watching its return value, so the run has to describe itself:
// an account that failed silently is indistinguishable from one that was never
// due.
//
// THE UNIT OF THE LINE IS THE ACCOUNT, and that is a decision rather than an
// accident. The sweep queues accounts, budgets accounts and stands accounts
// down, so an account is the only thing the line can promise anything about. A
// chain is not: nothing here can schedule one, skip one or wait for one.
//
// But the thing that goes dark alone IS a chain. Hydration answered 404 for
// sixteen days inside an account whose other four chains kept it looking fresh,
// and the account was never late (personal-isy9). So the chain is not made a
// second unit — it is named in the REASON, and a partial note carries EVERY
// complaint rather than the first, because one of five chains failing and two
// of five failing must not read the same.
//
// The result is one vocabulary — state, reasons, until — for everything a run
// cannot vouch for, whether the unit that failed is the whole account or a part
// of it.
type SweepReport struct {
	// Picked is how many accounts this run took, Stale how many were waiting,
	// Deferred how many the sweep is holding back. The third is in neither of
	// the first two — the selection and the count both exclude a standing-down
	// account — so without it three permanently broken accounts render as
	// "stale 0, picked 0", which reads as a run with nothing to do
	// (personal-g1zt).
	//
	// Picked was once the only one of the three, called Due, and reported the
	// LIMIT: "due 2" with twelve stale is what let four broken accounts hold
	// every slot for two days without the line ever looking wrong.
	Picked   int
	Stale    int
	Deferred int

	Synced           int // accounts whose sync returned
	Failed           int // accounts whose sync errored, or that never got a turn
	Partial          int // accounts that synced without speaking for every balance
	HoldingsUpserted int32
	HoldingsZeroed   int32

	// Notes name the accounts this run has something to say about. The counts
	// above stay exact; this is a sample, because a provider outage fails every
	// account at once and the log line is not the place to enumerate them.
	Notes []SweepNote
}

// Sweep note states. Five ways a run can fail to vouch for an account, kept
// apart because the operator's next move differs: a failed account may be
// misconfigured, a no-fresher one is standing down and will retry itself, a
// partial one holds real numbers with a hole in them, one never reached says
// the budget or the deadline is too small, and one standing down says the
// sweep already knows and is waiting.
const (
	// sweepFailed: the sync returned an error, so nothing landed at all.
	sweepFailed = "failed"
	// sweepNoFresher: the sync returned, complained, and wrote nothing. The
	// account is exactly as stale as the run found it, so it stands down.
	sweepNoFresher = "no_fresher"
	// sweepPartial: the snapshot landed and could not speak for every balance
	// in it. The account IS fresher, so it is not stood down — but "synced"
	// stops meaning "every chain it holds answered".
	sweepPartial = "partial"
	// sweepNotReached: picked and never attempted, because the run's own
	// deadline expired first. Not a verdict on the account.
	sweepNotReached = "not_reached"
	// sweepStandingDown: not picked, because an earlier run stood it down. It
	// appears in no count but Deferred, and used to appear in nothing at all.
	sweepStandingDown = "standing_down"
)

// SweepNote is one account the run could not fully vouch for, in the one
// vocabulary the line uses.
type SweepNote struct {
	AccountID string
	Name      string
	// State is one of the constants above.
	State string
	// Reasons is what the account, or the part of it that refused, actually
	// said. Plural on purpose: the first complaint alone hides how much of the
	// account is dark.
	Reasons []string
	// Until is when the sweep may look at this account again. Zero when it is
	// not standing down — a partial account is fresher than it was and keeps
	// its place in the queue.
	Until time.Time
}

// maxSweepNotesLogged caps the named accounts in a report, and
// maxReasonsPerNote the complaints named for one of them: a wallet can object
// once per token it could not read, and a provider outage objects once per
// account. Both are samples over exact counts.
const (
	maxSweepNotesLogged = 20
	maxReasonsPerNote   = 5
)

// note records what the run has against one account. Reasons past the cap are
// replaced by their own count, so a truncated list never reads as a complete
// one.
func (r *SweepReport) note(acct *entity.Account, state string, reasons []string, until time.Time) {
	n := SweepNote{AccountID: acct.ID, Name: acct.Name, State: state, Reasons: reasons, Until: until}
	if len(reasons) > maxReasonsPerNote {
		n.Reasons = append(append([]string{}, reasons[:maxReasonsPerNote]...),
			fmt.Sprintf("(+%d more)", len(reasons)-maxReasonsPerNote))
	}
	r.Notes = append(r.Notes, n)
}

// SyncDueAccounts re-reads the balances of accounts nobody has synced lately.
//
// Nothing else does: the scheduler prices assets hourly, so a portfolio whose
// amounts were last confirmed a week ago keeps producing a total that moves
// every hour and stays wrong. A number that moves is read as a number that is
// current — the same failure as an unpriced holding, on the amount axis instead
// of the price one.
//
// Selection is staleness-driven rather than a flat pass over every account, so
// the sweep spends the provider allowance on the accounts that actually went
// stale, and an account that stays stale is picked again on the next fire.
//
// Each account syncs under its own owner's identity. The sweep is an in-process
// trusted caller, and the alternative — a user-agnostic sync — cannot work:
// SyncAccount resolves wallet syncers and exchange credentials per user, so a
// sweep without an owner would reach only what an admin shared system-wide
// (personal-cpw). Ownership is not bypassed, it is attributed.
func (h *Handler) SyncDueAccounts(ctx context.Context, opts SweepOpts) (SweepReport, error) {
	opts = opts.withDefaults()
	report := SweepReport{}

	if h.mdClient == nil {
		return report, fmt.Errorf("balance sweep needs a market-data client")
	}

	accounts, err := h.store.ListStaleSyncTargets(ctx, opts.Now.Add(-opts.MaxAge), opts.Now, opts.Limit)
	if err != nil {
		return report, fmt.Errorf("list stale sync targets: %w", err)
	}
	report.Picked = len(accounts)

	// Counted separately and deliberately after the selection: the selection is
	// capped, this is not, and the gap between them is the thing worth seeing.
	// A failure to count is not a failure to sweep — the run goes on and the
	// line says the number is unknown rather than pretending it is zero.
	stale, err := h.store.CountDueSyncTargets(ctx, opts.Now.Add(-opts.MaxAge), opts.Now)
	if err != nil {
		h.log.WarnContext(ctx, "balance sweep: cannot count what is waiting", "error", err)
		report.Stale = -1
	} else {
		report.Stale = stale
	}

	// The rest of the queue: accounts nobody picked because the sweep is
	// standing them down. They are in neither number above — ListStaleSyncTargets
	// and CountDueSyncTargets skip them by the same clause — so a queue made
	// entirely of broken accounts reported itself as empty.
	//
	// Read BEFORE the loop on purpose: this is the queue the run found, not the
	// one it leaves behind. Accounts stood down BY this run are named on their
	// own, under the state that says why.
	deferrals, deferred, err := h.store.ListSweepDeferrals(ctx, opts.Now, maxSweepNotesLogged)
	if err != nil {
		h.log.WarnContext(ctx, "balance sweep: cannot see who is standing down", "error", err)
		report.Deferred = -1
	} else {
		report.Deferred = deferred
	}

	for _, acct := range accounts {
		if ctx.Err() != nil {
			// The sweep's own deadline expired. Say which accounts never got
			// their turn instead of reporting a short run as a complete one.
			report.note(acct, sweepNotReached, []string{ctx.Err().Error()}, time.Time{})
			report.Failed++
			continue
		}
		h.sweepAccount(ctx, acct, &report, opts.Now)
	}

	// Appended after the loop so the accounts this run actually touched can
	// never be crowded out of the sample by a long-standing queue.
	for _, d := range deferrals {
		if len(report.Notes) >= maxSweepNotesLogged {
			break
		}
		report.note(
			&entity.Account{ID: d.AccountID, Name: d.AccountName},
			sweepStandingDown,
			// The miss count, not a verdict: the store keeps how often an
			// account came back with nothing, not what it said the last time.
			[]string{fmt.Sprintf("%d consecutive misses", d.Misses)},
			d.NextAttemptAt,
		)
	}

	return report, nil
}

// sweepAccount syncs one account and folds the outcome into the report.
func (h *Handler) sweepAccount(ctx context.Context, acct *entity.Account, report *SweepReport, now time.Time) {
	owned := middleware.ContextWithUser(ctx, &entity.User{ID: acct.UserID})
	resp, err := h.SyncAccount(owned, connect.NewRequest(&apiv1.SyncAccountRequest{AccountId: acct.ID}))
	if err != nil {
		report.Failed++
		report.note(acct, sweepFailed, []string{err.Error()}, h.deferAccount(ctx, acct, now))
		return
	}

	report.Synced++
	report.HoldingsUpserted += resp.Msg.GetHoldingsUpserted()
	report.HoldingsZeroed += resp.Msg.GetHoldingsZeroed()

	// One note per account, and no-fresher outranks partial: an account that
	// complained and wrote nothing IS an account that synced with errors, and
	// naming it twice in two vocabularies is the confusion this line was rebuilt
	// to end.
	errs := resp.Msg.GetErrors()
	switch {
	case leftNoFresher(resp.Msg):
		report.note(acct, sweepNoFresher, errs, h.deferAccount(ctx, acct, now))

	case len(errs) > 0:
		// A sync that returned with per-item errors wrote a snapshot it could
		// not fully vouch for — and, by the removal gate, one that was not
		// allowed to drop vanished positions. Interactively the caller reads
		// those strings; on a schedule nobody does unless the run line repeats
		// them.
		report.Partial++
		report.note(acct, sweepPartial, errs, time.Time{})
	}
}

// LogSweepReport emits one line per sweep plus a line per account the run could
// not vouch for. It lives here rather than in the caller so every scheduler,
// test or operator command reports a sweep the same way — and it is the ONLY
// place that names an account, so the two halves of a run cannot describe
// themselves in two vocabularies.
func (h *Handler) LogSweepReport(report SweepReport, elapsed time.Duration) {
	h.log.Info("balance sweep complete",
		slog.Int("stale", report.Stale),
		slog.Int("picked", report.Picked),
		slog.Int("deferred", report.Deferred),
		slog.Int("synced", report.Synced),
		slog.Int("failed", report.Failed),
		slog.Int("partial", report.Partial),
		slog.Int("holdings_upserted", int(report.HoldingsUpserted)),
		slog.Int("holdings_zeroed", int(report.HoldingsZeroed)),
		slog.Duration("duration", elapsed))

	for _, n := range report.Notes {
		attrs := []any{
			slog.String("account_id", n.AccountID),
			slog.String("account", n.Name),
			slog.String("state", n.State),
			slog.Any("reasons", n.Reasons),
		}
		if !n.Until.IsZero() {
			attrs = append(attrs, slog.Time("until", n.Until))
		}
		h.log.Warn("balance sweep: account not fully confirmed", attrs...)
	}
}

// leftNoFresher reports whether a sync that returned without error changed
// nothing about the account while complaining that it could not.
//
// THE TEST IS "NO FRESHER", NOT "FAILED", and the difference is the whole bug.
// The Moralis outage of 31.08-03.09 returned 200 with the 401 inside the
// response body: the sweep counted those runs as synced, logged "account synced
// with errors", and wrote no holdings. A failure-based rule would have watched
// four accounts monopolise the queue for two days without ever calling them
// broken.
//
// An EMPTY WALLET IS NOT A MISS. It writes nothing either, and it is a true
// answer about an account that holds nothing — so silence only counts against
// an account that also complained. A partial sync is not a miss either: some
// chains answered, the account IS fresher, and the errors are disclosure rather
// than failure.
func leftNoFresher(resp *apiv1.SyncAccountResponse) bool {
	if resp == nil {
		return true
	}
	if len(resp.GetErrors()) == 0 {
		return false
	}
	return resp.GetHoldingsUpserted() == 0 && resp.GetHoldingsZeroed() == 0
}

// deferAccount stands an account down. The doubling is done by the store in one
// statement — counting the miss and choosing the wait cannot be two reads — but
// the numbers it doubles are declared here, because the schedule is the sweep's
// policy and not the table's.
//
// A failure to record is logged and swallowed, and reported as a zero deadline.
// The sweep's job is to refresh balances, and refusing to run because the
// bookkeeping is unavailable would trade a scheduling problem for an outage —
// but a note that claimed a stand-down which was never written would send an
// operator looking for a deferral the next run has forgotten.
func (h *Handler) deferAccount(ctx context.Context, acct *entity.Account, now time.Time) time.Time {
	_, next, err := h.store.RecordSyncMiss(ctx, acct.ID, now, missBackoffBase, missBackoffCap)
	if err != nil {
		h.log.WarnContext(ctx, "balance sweep: cannot defer the account",
			"account_id", acct.ID, "account", acct.Name, "error", err)
		return time.Time{}
	}
	return next
}

// GetAccountSweepSchedule reports which of the caller's accounts the balance
// sweep is holding back, and how many are waiting behind them.
//
// A deferral nobody can see is the degradation this whole change was made to
// stop, one layer up: the sweep used to hand every slot to the same broken
// accounts and the run line called that a healthy sweep. Standing them down
// fixes the queue and hides the reason, unless the reason is readable.
func (h *Handler) GetAccountSweepSchedule(
	ctx context.Context,
	req *connect.Request[apiv1.GetAccountSweepScheduleRequest],
) (*connect.Response[apiv1.GetAccountSweepScheduleResponse], error) {
	user, ok := middleware.UserFromContext(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("user not found in context"))
	}

	deferrals, err := h.store.ListSyncDeferrals(ctx, user.ID, req.Msg.GetAccountId())
	if err != nil {
		return nil, toConnectError(err)
	}

	entries := make([]*apiv1.AccountSweepEntry, 0, len(deferrals))
	for _, d := range deferrals {
		entry := &apiv1.AccountSweepEntry{
			AccountId:     d.AccountID,
			AccountName:   d.AccountName,
			Misses:        intToU32(d.Misses),
			NextAttemptAt: timestamppb.New(d.NextAttemptAt),
		}
		if d.LastSyncedAt != nil {
			entry.LastSyncedAt = timestamppb.New(*d.LastSyncedAt)
		}
		entries = append(entries, entry)
	}

	// The queue behind the deferrals, counted with the same cutoff the sweep
	// uses. Reported even when nothing is deferred: "nobody is held back and
	// twelve are waiting" is a different instance from "nobody is held back and
	// none are waiting", and only one of them needs a bigger budget.
	opts := SweepOpts{}.withDefaults()
	due, err := h.store.CountDueSyncTargets(ctx, opts.Now.Add(-opts.MaxAge), opts.Now)
	if err != nil {
		return nil, toConnectError(err)
	}

	return connect.NewResponse(&apiv1.GetAccountSweepScheduleResponse{
		Accounts: entries,
		DueNow:   intToU32(due),
	}), nil
}

// ResetAccountSweepSchedule withdraws the deferral of the named accounts.
//
// It asserts nothing about whether they will answer — the next sweep finds that
// out. What it withdraws is the CONCLUSION drawn from earlier attempts, which
// is the only part an operator has grounds to overrule: they know the key was
// replaced, and the instance cannot.
func (h *Handler) ResetAccountSweepSchedule(
	ctx context.Context,
	req *connect.Request[apiv1.ResetAccountSweepScheduleRequest],
) (*connect.Response[apiv1.ResetAccountSweepScheduleResponse], error) {
	user, ok := middleware.UserFromContext(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("user not found in context"))
	}

	ids := req.Msg.GetAccountIds()
	if len(ids) == 0 {
		// Rejected rather than read as "all", exactly as the price-path twin
		// does: forgiving everything is a bigger statement than forgiving one,
		// and nobody should get it by leaving a field out.
		return nil, connect.NewError(connect.CodeInvalidArgument,
			fmt.Errorf("name the accounts to forgive: an empty list is not read as all"))
	}

	freed, err := h.store.ClearSyncDeferrals(ctx, user.ID, ids)
	if err != nil {
		return nil, toConnectError(err)
	}

	h.log.InfoContext(ctx, "balance sweep schedule reset",
		"user_id", user.ID, "accounts_named", len(ids), "accounts_freed", freed)

	return connect.NewResponse(&apiv1.ResetAccountSweepScheduleResponse{
		AccountsFreed: intToU32(freed),
	}), nil
}
