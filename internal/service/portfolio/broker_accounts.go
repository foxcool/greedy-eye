package portfolio

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"strings"

	"connectrpc.com/connect"
	"github.com/foxcool/greedy-eye/internal/entity"
)

// maxDiscoveredAccounts caps how many accounts one listing may create.
//
// Not a tuning knob — a blast radius. The listing arrives over the network from
// a host the ACCOUNT names (data["base_url"], personal-5sgx), so a person can
// point this at something that answers with fifty thousand accounts and have
// the server write fifty thousand rows into the shared accounts table, each
// carrying an encrypted copy of a credential, and fire that many requests back
// at the same host. Nobody has thirty-two brokerage accounts; a listing longer
// than this is not a person's broker, so it is refused whole rather than
// truncated — half of an unbelievable answer is still unbelievable.
const maxDiscoveredAccounts = 32

// brokerAccountsPageSize is how many accounts one store round trip asks for.
// Nobody has three hundred brokerage accounts, but an unpaged read is how a
// missed row becomes a DUPLICATE account rather than a missing one — the
// discovery below decides what to create by what it failed to find.
const brokerAccountsPageSize = 500

// syncBrokerAccounts syncs every account the token on this one reaches.
//
// It is the answer to a shape that has no analogue among wallets or exchanges:
// one credential, several accounts, and the ids known only to the broker. The
// account holding the token holds no positions of its own — it is the key, and
// syncing it means "sync everything it opens".
//
// What it does NOT do is merge them. Each broker account becomes an account
// here, with its own holdings, because collapsing them destroys the only record
// of where paper sits: two accounts holding the same share would be one row,
// and moving it between them would change no sum at all.
func (h *Handler) syncBrokerAccounts(ctx context.Context, parent *entity.Account) (syncOutcome, error) {
	var out syncOutcome

	if h.brokerListerSource == nil {
		return out, connect.NewError(connect.CodeFailedPrecondition, fmt.Errorf(
			"account names no broker account (data[%q]) and this build cannot ask the broker which accounts its token reaches",
			entity.BrokerAccountDataKey))
	}
	lister, err := h.brokerListerSource.BrokerAccountListerForAccount(parent)
	if err != nil {
		// Configuration, like every other way this factory fails: a malformed
		// host, a missing trust anchor. Internal would tell the caller to retry
		// what retrying cannot fix.
		return out, connect.NewError(connect.CodeInvalidArgument, err)
	}
	if lister == nil {
		return out, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf(
			"this provider cannot list its accounts; name one in data[%q]", entity.BrokerAccountDataKey))
	}

	refs, err := lister.ListBrokerAccounts(ctx)
	if err != nil {
		// A hard error, unlike a single account failing mid-fan-out: without the
		// listing there is nothing to sync and nothing to report, and a silent
		// zero-account success would read as "the token reaches nothing".
		return out, toConnectError(err)
	}

	accounts, created, errs := h.ensureBrokerAccounts(ctx, parent, refs)
	out.accountsCreated = created
	out.errors = append(out.errors, errs...)
	if len(accounts) == 0 {
		// Not an error: a token that opens only a savings pot is a real answer.
		// It is said out loud because an empty success is indistinguishable from
		// a sync that did nothing.
		out.errors = append(out.errors, "this token reaches no syncable account")
		return out, nil
	}

	for _, account := range accounts {
		one, err := h.syncOneAccount(ctx, account)
		if err != nil {
			// Each account is its own call to the broker, so one failing says
			// nothing about the others — the edge the design named: "падение
			// одного не трогает остальные". Aborting here would let a single
			// revoked permission hide two healthy portfolios.
			out.errors = append(out.errors, fmt.Sprintf("%s: %s", account.Name, err.Error()))
			continue
		}
		out.merge(one, account.Name)
	}
	return out, nil
}

// ensureBrokerAccounts maps every syncable broker account onto an account here,
// creating the ones that are missing. It returns the accounts to sync, how many
// it created, and what it could not do.
//
// Matching is by (provider, broker account id) among the owner's own broker
// accounts, which is what makes a second run create nothing: the trigger is a
// side effect of syncing, so it has to be safe to repeat.
func (h *Handler) ensureBrokerAccounts(ctx context.Context, parent *entity.Account, refs []entity.BrokerAccountRef) ([]*entity.Account, int32, []string) {
	var errs []string

	// Checked before the store is read, not after. With an empty user id the
	// account query drops its user_id predicate entirely and returns EVERY
	// user's broker accounts, which would then be matched and synced under this
	// owner. Unreachable today — the column is NOT NULL with a foreign key —
	// but the guard belongs on this side of the read, not after it.
	if parent.UserID == "" {
		return nil, 0, []string{"the discovering account has no owner"}
	}
	if len(refs) > maxDiscoveredAccounts {
		return nil, 0, []string{fmt.Sprintf(
			"the source listed %d accounts, more than the %d this can be asked to create; nothing was created",
			len(refs), maxDiscoveredAccounts)}
	}

	existing, err := h.brokerAccountsOf(ctx, parent.UserID, parent.Data[providerDataKey])
	if err != nil {
		return nil, 0, []string{fmt.Sprintf("read existing broker accounts: %v", err)}
	}

	var (
		accounts []*entity.Account
		created  int32
	)
	// What this call has already handled. The map read from the store is a
	// SNAPSHOT taken before the loop, so without this a listing that names the
	// same account twice passes the "does it exist" check twice and mints two
	// accounts for one broker account — and then syncs both, doubling that
	// position in every total. Lying in the plus, from an input nobody here
	// controls.
	//
	// It closes the duplicate WITHIN one call. Two syncs of the same account
	// running at once can still both find nothing and both create; the durable
	// guard for that is a unique index on the pair, which is a schema change
	// (personal-gskb).
	seen := make(map[string]bool, len(refs))
	for _, ref := range refs {
		if !ref.Syncable {
			// Named, not dropped. An operator who sees two accounts where the
			// broker's app shows three has no reason to trust either number.
			h.log.Info("broker account is not synced",
				"account_id", parent.ID, "broker_account_id", ref.ID,
				"name", ref.Name, "reason", ref.NotSyncableReason)
			continue
		}
		if !ref.ReadOnly {
			// Reported, not refused: whether a token may trade is the owner's
			// decision, and this system only ever reads. But "the token is
			// read-only" is the assumption the whole arrangement rests on, so
			// the moment it stops holding, it stops holding out loud.
			h.log.Warn("broker token can trade on this account, not only read it",
				"account_id", parent.ID, "broker_account_id", ref.ID, "name", ref.Name)
		}
		ref.ID = strings.TrimSpace(ref.ID)
		if ref.ID == "" {
			// The adapter marks these unsyncable, but the contract does not
			// oblige every adapter to. An empty id would be stored, matched
			// against nothing next time, and create another account on every
			// sync — the account growth this matching exists to prevent.
			errs = append(errs, "the source listed a syncable account with no id")
			continue
		}
		if seen[ref.ID] {
			// Syncing the first row twice would count it twice; creating a
			// second row would double it. Neither, and out loud — a broker
			// repeating an account is a fact about the broker.
			h.log.Warn("the broker listed the same account twice",
				"account_id", parent.ID, "broker_account_id", ref.ID, "name", ref.Name)
			continue
		}
		seen[ref.ID] = true

		if account, ok := existing[ref.ID]; ok {
			// The same broker account can be seen by two tokens — a second key
			// with the same rights is an ordinary thing to hold. Syncing the
			// row that belongs to another portfolio would rewrite positions the
			// caller did not name, in a portfolio they did not name, with
			// credentials that are not the ones they just used. Creating a
			// second row instead would put the same money in two portfolios.
			// Neither: say who already owns it.
			if account.PortfolioID != parent.PortfolioID {
				errs = append(errs, fmt.Sprintf(
					"broker account %s already belongs to account %s in another portfolio; it was left alone",
					ref.ID, account.ID))
				continue
			}
			accounts = append(accounts, account)
			continue
		}
		account, err := h.createBrokerAccount(ctx, parent, ref)
		if err != nil {
			errs = append(errs, fmt.Sprintf("create account for broker account %s: %v", ref.ID, err))
			continue
		}
		existing[ref.ID] = account
		created++
		accounts = append(accounts, account)
	}
	return accounts, created, errs
}

// brokerAccountsOf indexes the user's broker accounts of one provider by the
// broker account each names. Accounts naming none are skipped: the credential
// holder is one of them, and it is not a position-bearing account.
func (h *Handler) brokerAccountsOf(ctx context.Context, userID, provider string) (map[string]*entity.Account, error) {
	opts := ListAccountsOpts{UserID: userID, Type: entity.AccountTypeBroker, PageSize: brokerAccountsPageSize}
	byBrokerID := map[string]*entity.Account{}
	for {
		page, next, err := h.store.ListAccounts(ctx, opts)
		if err != nil {
			return nil, err
		}
		for _, a := range page {
			if a.Data[providerDataKey] != provider {
				continue
			}
			if id := strings.TrimSpace(a.Data[entity.BrokerAccountDataKey]); id != "" {
				byBrokerID[id] = a
			}
		}
		// The same guard the holdings reader carries: a store that keeps
		// handing back the same cursor would spin here forever.
		if next == "" || len(page) == 0 {
			return byBrokerID, nil
		}
		opts.PageToken = next
	}
}

// createBrokerAccount creates the account one broker account syncs into.
//
// It inherits owner, portfolio and credentials from the account whose token
// found it: all three are unambiguous — it is the same person, the same
// portfolio the operator already chose, and literally the same token. What it
// does NOT inherit is system_scopes: those are admin-managed, and portfolio
// sync is never system-scoped in the first place.
func (h *Handler) createBrokerAccount(ctx context.Context, parent *entity.Account, ref entity.BrokerAccountRef) (*entity.Account, error) {
	if parent.UserID == "" {
		return nil, errors.New("the discovering account has no owner")
	}

	// The credentials are copied, and that is a real cost rather than an
	// oversight: rotating the token now touches every account it opened instead
	// of one. The alternative — a child pointing at its parent's credentials —
	// is account hierarchy, which the design refused for the price it puts on
	// every question about a total (personal-b7y7 records the trade).
	data := maps.Clone(parent.Data)
	if data == nil {
		data = map[string]string{}
	}
	data[entity.BrokerAccountDataKey] = strings.TrimSpace(ref.ID)

	account := &entity.Account{
		UserID: parent.UserID,
		// The broker's own label plus the id, because all three of one person's
		// accounts are commonly called the same thing and the id is the only
		// part that differs.
		Name:         fmt.Sprintf("%s · %s (%s)", parent.Name, ref.Name, ref.ID),
		Description:  fmt.Sprintf("Discovered through the token on account %s", parent.ID),
		Type:         entity.AccountTypeBroker,
		Data:         data,
		Capabilities: parent.Capabilities,
		PortfolioID:  parent.PortfolioID,
	}
	created, err := h.store.CreateAccount(ctx, account)
	if err != nil {
		return nil, err
	}
	if created == nil {
		// A store that reports success and returns nothing would be
		// dereferenced two lines down, taking the whole sync with it — and the
		// other accounts of a fan-out with it. Say what happened instead.
		return nil, errors.New("the store created the account and returned nothing")
	}
	h.log.Info("created an account for a broker account the token reaches",
		"account_id", created.ID, "discovered_by", parent.ID,
		"broker_account_id", ref.ID, "name", created.Name)
	return created, nil
}
