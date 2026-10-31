package portfolio

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/fieldmaskpb"

	apiv1 "github.com/foxcool/greedy-eye/api/v1"
	"github.com/foxcool/greedy-eye/internal/entity"
)

func disableRequest(disabled bool, paths ...string) *connect.Request[apiv1.UpdateAccountRequest] {
	return connect.NewRequest(&apiv1.UpdateAccountRequest{
		Account:    &apiv1.Account{Id: testAccountID, Disabled: disabled},
		UpdateMask: &fieldmaskpb.FieldMask{Paths: append([]string{"disabled"}, paths...)},
	})
}

// TestUpdateAccount_DisableWritesOnlyTheFlagAndForgetsTheDeferral: standing an
// account down reaches the store as the disabled field alone, and the sweep's
// held-back schedule for it is cleared — it will not be swept, so a deferral
// left behind would read as one with nothing behind it.
func TestUpdateAccount_DisableWritesOnlyTheFlagAndForgetsTheDeferral(t *testing.T) {
	since := time.Now().UTC().Truncate(time.Second)
	stored := testAccount(testAccountID)
	stored.DisabledAt = &since

	s := &mockStore{}
	s.On("GetAccount", mock.Anything, testAccountID).Return(testAccount(testAccountID), nil)
	s.On("ClearSyncDeferrals", mock.Anything, testUserID, []string{testAccountID}).Return(1, nil).Once()
	s.On("UpdateAccount", mock.Anything, mock.MatchedBy(func(a *entity.Account) bool {
		return a.Disabled()
	}), []string{"disabled"}).Return(stored, nil)

	resp, err := newHandler(s).UpdateAccount(ctxWithUser(testUserID), disableRequest(true))
	require.NoError(t, err)
	assert.True(t, resp.Msg.Disabled)
	assert.Equal(t, since, resp.Msg.DisabledAt.AsTime())
	s.AssertExpectations(t)
}

// TestUpdateAccount_EnableClearsTheDeferral: re-enabling is what an owner does
// after changing something, often the key, so the account is tried at once
// rather than after the misses its old state earned.
func TestUpdateAccount_EnableClearsTheDeferral(t *testing.T) {
	since := time.Now()
	existing := testAccount(testAccountID)
	existing.DisabledAt = &since

	s := &mockStore{}
	s.On("GetAccount", mock.Anything, testAccountID).Return(existing, nil)
	s.On("ClearSyncDeferrals", mock.Anything, testUserID, []string{testAccountID}).Return(1, nil).Once()
	s.On("UpdateAccount", mock.Anything, mock.MatchedBy(func(a *entity.Account) bool {
		return !a.Disabled()
	}), []string{"disabled"}).Return(testAccount(testAccountID), nil)

	resp, err := newHandler(s).UpdateAccount(ctxWithUser(testUserID), disableRequest(false))
	require.NoError(t, err)
	assert.False(t, resp.Msg.Disabled)
	assert.Nil(t, resp.Msg.DisabledAt)
	s.AssertExpectations(t)
}

// TestUpdateAccount_RepeatedDisableKeepsTheSchedule: saving the same state is
// not a transition, so nothing about the sweep changes.
func TestUpdateAccount_RepeatedDisableKeepsTheSchedule(t *testing.T) {
	since := time.Now()
	existing := testAccount(testAccountID)
	existing.DisabledAt = &since

	s := &mockStore{}
	s.On("GetAccount", mock.Anything, testAccountID).Return(existing, nil)
	s.On("UpdateAccount", mock.Anything, mock.Anything, mock.Anything).Return(existing, nil)

	_, err := newHandler(s).UpdateAccount(ctxWithUser(testUserID), disableRequest(true))
	require.NoError(t, err)
	s.AssertNotCalled(t, "ClearSyncDeferrals", mock.Anything, mock.Anything, mock.Anything)
}

// TestUpdateAccount_ManualCannotBeDisabled: nothing external reads a manual
// account, so the flag would change nothing while looking like an action —
// whether the account is manual already or becomes manual in the same write.
func TestUpdateAccount_ManualCannotBeDisabled(t *testing.T) {
	manual := testAccount(testAccountID)
	manual.Type = entity.AccountTypeManual

	cases := map[string]struct {
		existing *entity.Account
		req      *connect.Request[apiv1.UpdateAccountRequest]
	}{
		"already manual": {existing: manual, req: disableRequest(true)},
		"becoming manual": {existing: testAccount(testAccountID), req: func() *connect.Request[apiv1.UpdateAccountRequest] {
			r := disableRequest(true, "type")
			r.Msg.Account.Type = apiv1.AccountType_ACCOUNT_TYPE_MANUAL
			return r
		}()},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			s := &mockStore{}
			s.On("GetAccount", mock.Anything, testAccountID).Return(tc.existing, nil)

			_, err := newHandler(s).UpdateAccount(ctxWithUser(testUserID), tc.req)
			require.Error(t, err)
			assert.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))
			assert.Contains(t, err.Error(), "manual")
			s.AssertNotCalled(t, "UpdateAccount", mock.Anything, mock.Anything, mock.Anything)
			s.AssertNotCalled(t, "ClearSyncDeferrals", mock.Anything, mock.Anything, mock.Anything)
		})
	}
}

// TestUpdateAccount_RequestRefusedForItsFieldsKeepsTheDeferral: a disable that
// the same request's other fields get refused changes nothing — the sweep's
// schedule for the account included.
func TestUpdateAccount_RequestRefusedForItsFieldsKeepsTheDeferral(t *testing.T) {
	otherPortfolio := "01926d35-6a1e-7005-8005-0000000000aa"
	cases := map[string]struct {
		account *apiv1.Account
		paths   []string
		code    connect.Code
	}{
		"operator data key by a non-admin": {
			account: &apiv1.Account{Data: map[string]string{"base_url": "http://10.0.0.1"}},
			paths:   []string{"data"}, code: connect.CodePermissionDenied,
		},
		"misspelled data key": {
			account: &apiv1.Account{Data: map[string]string{"chains": "eth"}},
			paths:   []string{"data"}, code: connect.CodeInvalidArgument,
		},
		"masked value with nothing stored": {
			account: &apiv1.Account{Data: map[string]string{"api_key": maskPrefix + "1234"}},
			paths:   []string{"data"}, code: connect.CodeInvalidArgument,
		},
		"capabilities the type does not allow": {
			account: &apiv1.Account{Capabilities: []string{string(entity.CapabilityOnchainLookup)}, Data: map[string]string{}},
			paths:   []string{"capabilities", "data"}, code: connect.CodeInvalidArgument,
		},
		"capabilities the type does not allow, data not in the mask": {
			account: &apiv1.Account{Capabilities: []string{string(entity.CapabilityOnchainLookup)}},
			paths:   []string{"capabilities"}, code: connect.CodeInvalidArgument,
		},
		"portfolio of another user": {
			account: &apiv1.Account{PortfolioId: &otherPortfolio},
			paths:   []string{"portfolio_id"}, code: connect.CodeNotFound,
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			s := &mockStore{}
			s.On("GetAccount", mock.Anything, testAccountID).Return(testAccount(testAccountID), nil)
			foreign := testPortfolio(otherPortfolio)
			foreign.UserID = "someone-else"
			s.On("GetPortfolio", mock.Anything, otherPortfolio).Return(foreign, nil)
			// Answered so that a call — the order this test guards against —
			// is reported by the assertions below instead of as a panic.
			s.On("ClearSyncDeferrals", mock.Anything, mock.Anything, mock.Anything).Return(1, nil).Maybe()
			s.On("UpdateAccount", mock.Anything, mock.Anything, mock.Anything).
				Return((*entity.Account)(nil), errors.New("the store refuses it too")).Maybe()

			req := disableRequest(true, tc.paths...)
			tc.account.Id, tc.account.Disabled = testAccountID, true
			req.Msg.Account = tc.account

			_, err := newHandler(s).UpdateAccount(ctxWithUser(testUserID), req)
			require.Error(t, err)
			assert.Equal(t, tc.code, connect.CodeOf(err), err.Error())
			s.AssertNotCalled(t, "ClearSyncDeferrals", mock.Anything, mock.Anything, mock.Anything)
			s.AssertNotCalled(t, "UpdateAccount", mock.Anything, mock.Anything, mock.Anything)
		})
	}
}

// TestUpdateAccount_DeferralNotClearedIsNotSaved: the stand-down is reported as
// failed, not written over a deferral it could not clear.
func TestUpdateAccount_DeferralNotClearedIsNotSaved(t *testing.T) {
	s := &mockStore{}
	s.On("GetAccount", mock.Anything, testAccountID).Return(testAccount(testAccountID), nil)
	s.On("ClearSyncDeferrals", mock.Anything, testUserID, []string{testAccountID}).
		Return(0, errors.New("connection reset")).Once()

	_, err := newHandler(s).UpdateAccount(ctxWithUser(testUserID), disableRequest(true))
	require.Error(t, err)
	s.AssertNotCalled(t, "UpdateAccount", mock.Anything, mock.Anything, mock.Anything)
}

// TestUpdateAccount_OtherFieldsLeaveTheFlagAlone: a save that does not name
// disabled must not move it, whatever the message carries — the flag is
// written only through the mask.
func TestUpdateAccount_OtherFieldsLeaveTheFlagAlone(t *testing.T) {
	s := &mockStore{}
	s.On("GetAccount", mock.Anything, testAccountID).Return(testAccount(testAccountID), nil)
	s.On("UpdateAccount", mock.Anything, mock.Anything, mock.MatchedBy(func(fields []string) bool {
		return !slices.Contains(fields, "disabled")
	})).Return(testAccount(testAccountID), nil)

	_, err := newHandler(s).UpdateAccount(ctxWithUser(testUserID), connect.NewRequest(&apiv1.UpdateAccountRequest{
		Account:    &apiv1.Account{Id: testAccountID, Name: "renamed", Disabled: true},
		UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"name"}},
	}))
	require.NoError(t, err)
	s.AssertExpectations(t)
	s.AssertNotCalled(t, "ClearSyncDeferrals", mock.Anything, mock.Anything, mock.Anything)
}

// TestSyncAccount_DisabledRefuses: an explicit sync is still external work,
// and the flag means the same thing everywhere. The refusal names the cause so
// the person does not go looking at the provider.
func TestSyncAccount_DisabledRefuses(t *testing.T) {
	since := time.Date(2026, 10, 8, 4, 0, 0, 0, time.UTC)
	acct := testAccount(testAccountID)
	acct.DisabledAt = &since

	s := &mockStore{}
	s.On("GetAccount", mock.Anything, testAccountID).Return(acct, nil)
	md := &mockMDClient{}

	_, err := newHandler(s).WithMarketDataClient(md).SyncAccount(ctxWithUser(testUserID),
		connect.NewRequest(&apiv1.SyncAccountRequest{AccountId: testAccountID}))
	require.Error(t, err)
	assert.Equal(t, connect.CodeFailedPrecondition, connect.CodeOf(err))
	assert.Contains(t, err.Error(), "disabled")
	assert.Contains(t, err.Error(), "2026-10-08T04:00:00Z")
	md.AssertExpectations(t)
	s.AssertNotCalled(t, "ListHoldings", mock.Anything, mock.Anything)
}

// TestCreateAccount_IsBornActive: the INSERT does not write the flag, so a
// create carrying it must not reach the store as disabled — the response
// would claim a state the row does not hold, and a manual account would
// slip past the refusal.
func TestCreateAccount_IsBornActive(t *testing.T) {
	s := &mockStore{}
	s.On("CreateAccount", mock.Anything, mock.MatchedBy(func(a *entity.Account) bool {
		return !a.Disabled()
	})).Return(testAccount(testAccountID), nil)

	resp, err := newHandler(s).CreateAccount(ctxWithUser(testUserID), connect.NewRequest(&apiv1.CreateAccountRequest{
		Account: &apiv1.Account{Name: "x", Type: apiv1.AccountType_ACCOUNT_TYPE_EXCHANGE, Disabled: true},
	}))
	require.NoError(t, err)
	assert.False(t, resp.Msg.Disabled)
	s.AssertExpectations(t)
}

// TestUpdateAccount_DisabledCannotTurnManual: the flag left out of the mask
// does not make the type change safe — the result would still be a disabled
// manual account.
func TestUpdateAccount_DisabledCannotTurnManual(t *testing.T) {
	since := time.Now()
	existing := testAccount(testAccountID)
	existing.DisabledAt = &since

	s := &mockStore{}
	s.On("GetAccount", mock.Anything, testAccountID).Return(existing, nil)

	_, err := newHandler(s).UpdateAccount(ctxWithUser(testUserID), connect.NewRequest(&apiv1.UpdateAccountRequest{
		Account:    &apiv1.Account{Id: testAccountID, Type: apiv1.AccountType_ACCOUNT_TYPE_MANUAL},
		UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"type"}},
	}))
	require.Error(t, err)
	assert.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))
	s.AssertNotCalled(t, "UpdateAccount", mock.Anything, mock.Anything, mock.Anything)
}

// TestUpdateAccount_AdminEnableClearsTheOwnersDeferral: the deferral is filed
// under the owner, so clearing it under the caller's id would match nothing
// when an admin re-enables somebody else's account.
func TestUpdateAccount_AdminEnableClearsTheOwnersDeferral(t *testing.T) {
	since := time.Now()
	existing := testAccount(testAccountID)
	existing.DisabledAt = &since

	s := &mockStore{}
	s.On("GetAccount", mock.Anything, testAccountID).Return(existing, nil)
	s.On("ClearSyncDeferrals", mock.Anything, testUserID, []string{testAccountID}).Return(1, nil).Once()
	s.On("UpdateAccount", mock.Anything, mock.Anything, []string{"disabled"}).Return(testAccount(testAccountID), nil)

	_, err := newHandler(s).UpdateAccount(ctxWithAdmin("admin-user"), disableRequest(false))
	require.NoError(t, err)
	s.AssertExpectations(t)
}

// TestSweep_AccountDisabledAfterSelectionIsNotAFailure: the sweep picked the
// account, then its owner disabled it. The refusal is a decision, not a
// miss: no failure in the report and no deferral filed for an account the
// sweep no longer takes.
func TestSweep_AccountDisabledAfterSelectionIsNotAFailure(t *testing.T) {
	now := time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)
	picked := testAccount(testAccountID)
	picked.Type = entity.AccountTypeWallet
	since := now.Add(-time.Minute)
	stoodDown := testAccount(testAccountID)
	stoodDown.Type = entity.AccountTypeWallet
	stoodDown.DisabledAt = &since

	s := &mockStore{}
	s.On("ListStaleSyncTargets", mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return([]*entity.Account{picked}, nil).Once()
	s.On("GetAccount", mock.Anything, testAccountID).Return(stoodDown, nil)

	report, err := newHandler(s).WithMarketDataClient(&mockMDClient{}).
		SyncDueAccounts(context.Background(), SweepOpts{Now: now})
	require.NoError(t, err)
	assert.Equal(t, 0, report.Failed)
	assert.Equal(t, 0, report.Synced)
	s.AssertNotCalled(t, "RecordSyncMiss", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything)
}
