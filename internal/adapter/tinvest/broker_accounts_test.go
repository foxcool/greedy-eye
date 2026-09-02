package tinvest

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/foxcool/greedy-eye/internal/entity"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var _ entity.BrokerAccountLister = (*AccountLister)(nil)

func newAccountListerWith(t *testing.T, body string) *AccountLister {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)

	c, err := NewClient(Config{Token: "t", BaseURL: srv.URL, Transport: srv.Client().Transport})
	require.NoError(t, err)
	return NewAccountLister(c)
}

// TestAccountListerReadsTheFixture: the accounts fixture carries both shapes
// the capture showed — an open brokerage account and the round-up savings pot —
// and the adapter, not the caller, decides which of them holds a portfolio.
func TestAccountListerReadsTheFixture(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("testdata", "accounts.json"))
	require.NoError(t, err)

	refs, err := newAccountListerWith(t, string(raw)).ListBrokerAccounts(context.Background())
	require.NoError(t, err)
	require.Len(t, refs, 2, "both accounts come back; only one of them is syncable")

	byID := map[string]entity.BrokerAccountRef{}
	for _, ref := range refs {
		byID[ref.ID] = ref
	}

	brokerage := byID["acc-1"]
	assert.True(t, brokerage.Syncable)
	assert.Empty(t, brokerage.NotSyncableReason)
	assert.True(t, brokerage.ReadOnly, "the read-only token is the assumption the live-API decision rests on")

	pot := byID["acc-2"]
	assert.False(t, pot.Syncable, "the round-up pot held 2.67 roubles and is not a portfolio")
	assert.NotEmpty(t, pot.NotSyncableReason, "an account that is passed over says why")
}

// TestAccountListerNamesWhatItWillNotSync is the rule this type is built on: a
// shorter list with nothing said about what is missing from it is worse than a
// long one. An operator seeing two accounts where the app shows three cannot
// trust either number.
func TestAccountListerNamesWhatItWillNotSync(t *testing.T) {
	refs, err := newAccountListerWith(t, `{"accounts":[
		{"id":"a-closed","name":"old","status":"ACCOUNT_STATUS_CLOSED","type":"ACCOUNT_TYPE_TINKOFF","accessLevel":"ACCOUNT_ACCESS_LEVEL_READ_ONLY"},
		{"id":"a-new","name":"pending","status":"ACCOUNT_STATUS_NEW","type":"ACCOUNT_TYPE_TINKOFF","accessLevel":"ACCOUNT_ACCESS_LEVEL_READ_ONLY"},
		{"id":"","name":"nameless","status":"ACCOUNT_STATUS_OPEN","type":"ACCOUNT_TYPE_TINKOFF","accessLevel":"ACCOUNT_ACCESS_LEVEL_READ_ONLY"},
		{"id":"a-full","name":"","status":"ACCOUNT_STATUS_OPEN","type":"ACCOUNT_TYPE_TINKOFF","accessLevel":"ACCOUNT_ACCESS_LEVEL_FULL_ACCESS"}
	]}`).ListBrokerAccounts(context.Background())
	require.NoError(t, err)
	require.Len(t, refs, 4, "nothing is filtered away, including the account with no id")

	for _, ref := range refs[:3] {
		assert.False(t, ref.Syncable, "%q", ref.ID)
		assert.NotEmpty(t, ref.NotSyncableReason, "%q is passed over without saying why", ref.ID)
	}

	// An account the token can trade on is still syncable — this system only
	// ever reads — but the access level travels out so the caller can say so.
	full := refs[3]
	assert.True(t, full.Syncable)
	assert.False(t, full.ReadOnly)
	assert.Equal(t, "a-full", full.Name, "an unnamed account is called by its id rather than left blank")
}

// TestAccountListerPropagatesFailure: no listing means nothing to sync, and the
// caller has to be able to tell that from "the token reaches nothing".
func TestAccountListerPropagatesFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"message":"token is not valid"}`))
	}))
	t.Cleanup(srv.Close)

	c, err := NewClient(Config{Token: "t", BaseURL: srv.URL, Transport: srv.Client().Transport})
	require.NoError(t, err)

	_, err = NewAccountLister(c).ListBrokerAccounts(context.Background())
	require.Error(t, err)
}
