package portfolio

import (
	"context"
	"time"

	"github.com/foxcool/greedy-eye/internal/entity"
)

// HoldingWriter is the subset of holdings writes a sync or a batch import
// performs. It exists so the whole set can be handed to one transaction: a sync
// rewrites a snapshot, and half a snapshot is not a smaller truth, it is a
// wrong one.
type HoldingWriter interface {
	// LockAccountPortfolio returns the account's portfolio and holds it until
	// the transaction ends. A new holding stamped with an account's portfolio
	// takes the value from here, never from a read made before the
	// transaction: that one can be a move out of date, and the holding would
	// stay behind in the portfolio the account left.
	//
	// The read is FOR SHARE, which conflicts with the FOR NO KEY UPDATE a move
	// takes (the KEY SHARE of an insert's foreign-key check does not). So
	// either this write commits first and the move's holdings UPDATE, a new
	// statement under READ COMMITTED, carries its rows; or it waits for the
	// move and reads the portfolio the move wrote. Call it first in the
	// transaction, before any holding is locked, so the two cannot deadlock.
	LockAccountPortfolio(ctx context.Context, accountID string) (string, error)
	CreateHolding(ctx context.Context, h *entity.Holding) (*entity.Holding, error)
	UpdateHolding(ctx context.Context, h *entity.Holding, fields []string) (*entity.Holding, error)
	DeleteHolding(ctx context.Context, id string) error
}

// Store defines the data access contract for PortfolioService.
type Store interface {
	// Portfolios
	CreatePortfolio(ctx context.Context, p *entity.Portfolio) (*entity.Portfolio, error)
	GetPortfolio(ctx context.Context, id string) (*entity.Portfolio, error)
	UpdatePortfolio(ctx context.Context, p *entity.Portfolio, fields []string) (*entity.Portfolio, error)
	DeletePortfolio(ctx context.Context, id string) error
	ListPortfolios(ctx context.Context, opts ListPortfoliosOpts) ([]*entity.Portfolio, string, error)

	// Accounts
	CreateAccount(ctx context.Context, a *entity.Account) (*entity.Account, error)
	GetAccount(ctx context.Context, id string) (*entity.Account, error)
	UpdateAccount(ctx context.Context, a *entity.Account, fields []string) (*entity.Account, error)
	DeleteAccount(ctx context.Context, id string) error
	// DeleteAccountWithHoldings removes the account and its holdings
	// atomically. Ledger history is never removed this way.
	DeleteAccountWithHoldings(ctx context.Context, id string) error
	// ListAccounts and every other account list leave out a row that cannot be
	// read; ListUnreadableAccounts is where the owner finds it.
	ListAccounts(ctx context.Context, opts ListAccountsOpts) ([]*entity.Account, string, error)
	// ListUnreadableAccounts returns the user's accounts whose stored data or
	// capabilities cannot be read by this instance.
	ListUnreadableAccounts(ctx context.Context, userID string) ([]*entity.UnreadableAccount, error)
	// ListStaleSyncTargets returns syncable accounts whose balances were last
	// confirmed before `olderThan`, stalest first, capped at `limit`. Freshness
	// comes from the account's own holdings, so it cannot claim a sync that
	// never landed; an account with no holdings sorts first.
	//
	// Which types are eligible is the store's rule, documented there: not every
	// account SyncAccount accepts is one the sweep may take.
	ListStaleSyncTargets(ctx context.Context, olderThan, now time.Time, limit int) ([]*entity.Account, error)

	// RecordSyncMiss stands an account down until nextAttemptAt after a sync
	// that left it no fresher. Without it the stalest-first ordering hands the
	// same unanswering account every slot, because a sync that writes nothing
	// leaves the account exactly as stale as it found it.
	RecordSyncMiss(ctx context.Context, accountID string, attemptedAt time.Time, base, cap time.Duration) (int, time.Time, error)

	// RecordChainFailures writes one sync's verdict on the chains of a wallet
	// account: every chain in failed extends its run of failures (or starts
	// one), and every chain in answered that did not fail has its run ended.
	// A chain in neither was not asked this time and keeps its run. It returns
	// the runs extended by this write.
	RecordChainFailures(ctx context.Context, accountID string, failed map[string]string, answered []string, at time.Time) ([]*entity.ChainFailure, error)

	// ListChainFailures returns the chains of an account currently on a run of
	// whole-chain failures, oldest run first.
	ListChainFailures(ctx context.Context, accountID string) ([]*entity.ChainFailure, error)

	// RecordSyncAnswer notes that the account's source answered, even with
	// nothing, and forgives what the account owes, from a swept sync or a
	// hand-triggered one alike.
	RecordSyncAnswer(ctx context.Context, accountID string, at time.Time) error

	// CountDueSyncTargets counts what the sweep WOULD take if it had no budget,
	// so a run can report how many are waiting behind the ones it picked.
	CountDueSyncTargets(ctx context.Context, olderThan, now time.Time) (int, error)

	// ListSyncDeferrals returns the owner's accounts currently standing down.
	// An empty accountID means all of them.
	ListSyncDeferrals(ctx context.Context, userID, accountID string) ([]*entity.SyncDeferral, error)

	// ListSweepDeferrals returns the accounts standing down at `now` across
	// every owner, soonest first, capped at `limit`, together with the exact
	// total the cap hid. The sweep is a system caller and its run line has to
	// account for a queue it is holding back regardless of whose account it is.
	ListSweepDeferrals(ctx context.Context, now time.Time, limit int) ([]*entity.SyncDeferral, int, error)

	// ClearSyncDeferrals withdraws the deferral of the named accounts and
	// reports how many owed anything.
	ClearSyncDeferrals(ctx context.Context, userID string, accountIDs []string) (int, error)

	// ClaimAccountSync takes the account's sync lease for ttl and returns its
	// token, or "" when a live claim is held by someone else.
	ClaimAccountSync(ctx context.Context, accountID string, ttl time.Duration) (string, error)
	// ReleaseAccountSync ends the claim if token still holds it.
	ReleaseAccountSync(ctx context.Context, accountID, token string) error

	// Holdings
	CreateHolding(ctx context.Context, h *entity.Holding) (*entity.Holding, error)
	GetHolding(ctx context.Context, id string) (*entity.Holding, error)
	UpdateHolding(ctx context.Context, h *entity.Holding, fields []string) (*entity.Holding, error)
	DeleteHolding(ctx context.Context, id string) error
	ListHoldings(ctx context.Context, opts ListHoldingsOpts) ([]*entity.Holding, string, error)
	// InHoldingsTx runs fn against a transactional holdings writer: every write
	// inside lands together or none does. Returning an error from fn rolls the
	// whole set back.
	InHoldingsTx(ctx context.Context, fn func(HoldingWriter) error) error

	// Ledger
	// CreateLedgerEvent writes the event and its legs together or not at all.
	// An external_ref the account already has from the same source is
	// store.ErrAlreadyExists.
	CreateLedgerEvent(ctx context.Context, e *entity.LedgerEvent) (*entity.LedgerEvent, error)
	// ListLedgerEvents pages events with all their legs, oldest executed_at first.
	ListLedgerEvents(ctx context.Context, opts ListLedgerOpts) ([]*entity.LedgerEvent, string, error)
	// SetLedgerEventPayer names the asset that paid an event which names none
	// yet; false when the event already names one (or is gone).
	SetLedgerEventPayer(ctx context.Context, eventID, payerAssetID string) (bool, error)
	// ListLedgerCursors returns how far each chain's transfer history of the
	// account has been read.
	ListLedgerCursors(ctx context.Context, accountID string) ([]*entity.LedgerCursor, error)
	// SaveLedgerCursor writes one (account, chain) cursor, replacing it.
	SaveLedgerCursor(ctx context.Context, c *entity.LedgerCursor) error

	// UnsolicitedAssets maps each asset with a non-zero position to whether
	// every such position came from sync and no ledger leg outside a sync
	// names it. An
	// asset nobody holds is absent.
	UnsolicitedAssets(ctx context.Context, assetIDs []string) (map[string]bool, error)
}

// ListPortfoliosOpts contains options for listing portfolios.
type ListPortfoliosOpts struct {
	UserID    string
	PageSize  int
	PageToken string
}

// ListAccountsOpts contains options for listing accounts.
type ListAccountsOpts struct {
	UserID    string
	Type      entity.AccountType
	PageSize  int
	PageToken string
}

// ListHoldingsOpts contains options for listing holdings.
type ListHoldingsOpts struct {
	// UserID scopes results to holdings whose owning portfolio (own or
	// inherited from the account) belongs to this user. Holdings outside
	// any portfolio are scoped by account owner instead.
	UserID      string
	PortfolioID string
	AccountID   string
	AssetID     string
	PageSize    int
	PageToken   string
	// HideExcluded filters out holdings where excluded=true when set to true.
	HideExcluded bool
	// OmitExcludedFromTotals drops holdings whose owning portfolio (own or
	// inherited from the account) is flagged excluded_from_totals. Holdings
	// outside any portfolio are kept.
	OmitExcludedFromTotals bool
}

// ListLedgerOpts contains options for listing ledger events.
type ListLedgerOpts struct {
	// UserID scopes results to events of accounts owned by this user.
	UserID    string
	AccountID string
	// PortfolioID keeps events of the accounts in this portfolio.
	PortfolioID string
	// Kind keeps events of one kind; empty keeps every kind.
	Kind entity.LedgerEventKind
	// AssetID keeps events with at least one leg in the asset.
	AssetID string
	// PayerAssetID keeps events this asset paid.
	PayerAssetID string
	// PayerMissing keeps events that could name a payer and name none.
	PayerMissing bool
	// ExecutedFrom (inclusive) and ExecutedTo (exclusive) bound executed_at; zero is open.
	ExecutedFrom time.Time
	ExecutedTo   time.Time
	PageSize     int
	PageToken    string
}
