package portfolio

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"connectrpc.com/connect"

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
//   - AccountsPerSweep is the provider budget. One sync of a heavy EVM account
//     fans out to a provider per chain and costs far more than one price call,
//     so the sweep takes the two stalest accounts per fire rather than everything
//     due. With an hourly cron that is up to 48 account syncs a day, and an
//     account that stays stale is picked again next hour instead of all of them
//     competing for the same monthly allowance at once (see personal-a3v).
const (
	defaultSweepMaxAge          = 12 * time.Hour
	defaultSweepAccountsPerFire = 2
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
	Due              int // accounts selected as stale
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

	accounts, err := h.store.ListStaleSyncTargets(ctx, opts.Now.Add(-opts.MaxAge), opts.Limit)
	if err != nil {
		return report, fmt.Errorf("list stale sync targets: %w", err)
	}
	report.Due = len(accounts)

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
		h.sweepAccount(ctx, acct, &report)
	}

	return report, nil
}

// sweepAccount syncs one account and folds the outcome into the report.
func (h *Handler) sweepAccount(ctx context.Context, acct *entity.Account, report *SweepReport) {
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
		return
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
		slog.Int("due", report.Due),
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
