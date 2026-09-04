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
type SweepReport struct {
	// Picked is how many accounts this run took. Stale is how many were
	// waiting. They were one number called Due, which reported the LIMIT and
	// read like the queue: "due 2" with twelve stale is what let four broken
	// accounts hold every slot for two days without the line ever looking wrong.
	Picked           int
	Stale            int
	Synced           int // accounts whose sync returned
	Failed           int // accounts whose sync returned an error
	HoldingsUpserted int32
	HoldingsZeroed   int32
	// Failures names the accounts that did not sync and why, capped for log size.
	Failures []SweepFailure
	// PartialAccounts names accounts that synced with per-item errors: the
	// snapshot landed but could not speak for every balance in it.
	PartialAccounts []SweepFailure
}

// SweepFailure is one account the sweep could not finish, and the reason.
type SweepFailure struct {
	AccountID string
	Name      string
	Reason    string
}

// maxSweepFailuresLogged caps the named failures in a report. The counts stay
// exact; the list is a sample, because a provider outage fails every account at
// once and the log line is not the place to enumerate them.
const maxSweepFailuresLogged = 20

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

	for _, acct := range accounts {
		if ctx.Err() != nil {
			// The sweep's own deadline expired. Say which accounts never got
			// their turn instead of reporting a short run as a complete one.
			report.Failures = append(report.Failures, SweepFailure{
				AccountID: acct.ID, Name: acct.Name, Reason: ctx.Err().Error(),
			})
			report.Failed++
			continue
		}
		h.sweepAccount(ctx, acct, &report, opts.Now)
	}

	return report, nil
}

// sweepAccount syncs one account and folds the outcome into the report.
func (h *Handler) sweepAccount(ctx context.Context, acct *entity.Account, report *SweepReport, now time.Time) {
	owned := middleware.ContextWithUser(ctx, &entity.User{ID: acct.UserID})
	resp, err := h.SyncAccount(owned, connect.NewRequest(&apiv1.SyncAccountRequest{AccountId: acct.ID}))
	if err != nil {
		report.Failed++
		if len(report.Failures) < maxSweepFailuresLogged {
			report.Failures = append(report.Failures, SweepFailure{
				AccountID: acct.ID, Name: acct.Name, Reason: err.Error(),
			})
		}
		h.log.WarnContext(ctx, "balance sweep: account sync failed",
			"account_id", acct.ID, "account", acct.Name, "error", err)
		h.deferAccount(ctx, acct, now)
		return
	}

	if leftNoFresher(resp.Msg) {
		h.deferAccount(ctx, acct, now)
	}

	report.Synced++
	report.HoldingsUpserted += resp.Msg.GetHoldingsUpserted()
	report.HoldingsZeroed += resp.Msg.GetHoldingsZeroed()

	// A sync that returned with per-item errors wrote a snapshot it could not
	// fully vouch for — and, by the removal gate, one that was not allowed to
	// drop vanished positions. Interactively the caller reads those strings;
	// on a schedule nobody does unless the sweep repeats them.
	if errs := resp.Msg.GetErrors(); len(errs) > 0 {
		if len(report.PartialAccounts) < maxSweepFailuresLogged {
			report.PartialAccounts = append(report.PartialAccounts, SweepFailure{
				AccountID: acct.ID, Name: acct.Name, Reason: errs[0],
			})
		}
		h.log.WarnContext(ctx, "balance sweep: account synced with errors",
			"account_id", acct.ID, "account", acct.Name,
			"error_count", len(errs), "errors", errs)
	}
}

// LogSweepReport emits one line per sweep plus a line per account that did not
// finish. It lives here rather than in the caller so every scheduler, test or
// operator command reports a sweep the same way.
func (h *Handler) LogSweepReport(report SweepReport, elapsed time.Duration) {
	h.log.Info("balance sweep complete",
		slog.Int("stale", report.Stale),
		slog.Int("picked", report.Picked),
		slog.Int("synced", report.Synced),
		slog.Int("failed", report.Failed),
		slog.Int("partial", len(report.PartialAccounts)),
		slog.Int("holdings_upserted", int(report.HoldingsUpserted)),
		slog.Int("holdings_zeroed", int(report.HoldingsZeroed)),
		slog.Duration("duration", elapsed))

	for _, f := range report.Failures {
		h.log.Warn("balance sweep: account not synced",
			slog.String("account_id", f.AccountID),
			slog.String("account", f.Name),
			slog.String("reason", f.Reason))
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
// A failure to record is logged and swallowed. The sweep's job is to refresh
// balances, and refusing to run because the bookkeeping is unavailable would
// trade a scheduling problem for an outage.
func (h *Handler) deferAccount(ctx context.Context, acct *entity.Account, now time.Time) {
	misses, next, err := h.store.RecordSyncMiss(ctx, acct.ID, now, missBackoffBase, missBackoffCap)
	if err != nil {
		h.log.WarnContext(ctx, "balance sweep: cannot defer the account",
			"account_id", acct.ID, "account", acct.Name, "error", err)
		return
	}

	h.log.WarnContext(ctx, "balance sweep: account left no fresher, standing it down",
		"account_id", acct.ID, "account", acct.Name,
		"misses", misses, "next_attempt_at", next)
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
