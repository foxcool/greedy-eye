package tinvest

import (
	"context"
	"strings"

	"github.com/foxcool/greedy-eye/internal/entity"
)

// AccountLister reports the accounts one token reaches.
//
// Separate from BrokerSyncer on purpose: a syncer is built for ONE broker
// account and cannot exist before its id is known, which is exactly the
// chicken-and-egg this type breaks. It needs no id, only the token.
type AccountLister struct {
	client *Client
}

// NewAccountLister binds a lister to a client.
func NewAccountLister(c *Client) *AccountLister {
	return &AccountLister{client: c}
}

// ListBrokerAccounts implements entity.BrokerAccountLister.
//
// Every account is returned, including the ones that must not be synced, each
// carrying why. Filtering here would hand the caller a shorter list with no way
// to say what is missing from it — and an operator who sees two accounts where
// the app shows three has no reason to trust either number.
func (l *AccountLister) ListBrokerAccounts(ctx context.Context) ([]entity.BrokerAccountRef, error) {
	accounts, err := l.client.Accounts(ctx)
	if err != nil {
		return nil, err
	}

	out := make([]entity.BrokerAccountRef, 0, len(accounts))
	for _, a := range accounts {
		ref := entity.BrokerAccountRef{
			ID:       strings.TrimSpace(a.ID),
			Name:     strings.TrimSpace(a.Name),
			ReadOnly: a.AccessLevel == accountAccessReadOnly,
		}
		if ref.ID == "" {
			// Nothing can be built from it and nothing can name it. Dropping it
			// silently would be the one filter this function refuses, so it is
			// returned as unsyncable instead.
			ref.NotSyncableReason = "the broker returned an account with no id"
			out = append(out, ref)
			continue
		}
		if ref.Name == "" {
			ref.Name = ref.ID
		}
		switch {
		case a.Status != AccountStatusOpen:
			ref.NotSyncableReason = "the account is not open (" + a.Status + ")"
		case a.Type == AccountTypeInvestBox:
			// The round-up savings pot. It held 2.67 roubles at the 2026-08-27
			// measurement, and it is not a brokerage account: syncing it would
			// add a row that is noise in every sum it enters.
			ref.NotSyncableReason = "a round-up savings pot, not a brokerage account"
		default:
			ref.Syncable = true
		}
		out = append(out, ref)
	}
	return out, nil
}
