package marketdata

import (
	"context"
	"testing"

	"connectrpc.com/connect"
	apiv1 "github.com/foxcool/greedy-eye/api/v1"
	"github.com/foxcool/greedy-eye/internal/entity"
	"github.com/foxcool/greedy-eye/internal/middleware"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBackfillCbrRates_IsAdminOnly(t *testing.T) {
	h := newHandler(&mockStore{})
	req := connect.NewRequest(&apiv1.BackfillCbrRatesRequest{FromDate: "2024-02-16", ToDate: "2024-02-19"})

	plain := middleware.ContextWithUser(context.Background(), &entity.User{ID: "user-1"})
	_, err := h.BackfillCbrRates(plain, req)
	require.Error(t, err)
	assert.Equal(t, connect.CodePermissionDenied, connect.CodeOf(err))

	// The sync may fetch prices, but a backfill writes history for everyone
	// and is an operator's call, like any other price write.
	_, err = h.BackfillCbrRates(syncContext(), req)
	require.Error(t, err)
	assert.Equal(t, connect.CodeUnauthenticated, connect.CodeOf(err))
}

func TestBackfillCbrRates_RejectsABadRange(t *testing.T) {
	h := newHandler(&mockStore{})

	for name, msg := range map[string]*apiv1.BackfillCbrRatesRequest{
		"missing dates":      {},
		"not a date":         {FromDate: "16.02.2024", ToDate: "2024-02-19"},
		"reversed":           {FromDate: "2024-02-19", ToDate: "2024-02-16"},
		"longer than a year": {FromDate: "2023-01-01", ToDate: "2024-02-19"},
		"to date missing":    {FromDate: "2024-02-16"},
		"from date missing":  {ToDate: "2024-02-16"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := h.BackfillCbrRates(adminContext(), connect.NewRequest(msg))
			require.Error(t, err)
			assert.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))
		})
	}
}

func TestBackfillCbrRates_WithoutTheProviderIsNotAQuietZero(t *testing.T) {
	h := newHandler(&mockStore{}).WithProvider("binance", &fakePriceProvider{})

	_, err := h.BackfillCbrRates(adminContext(), connect.NewRequest(
		&apiv1.BackfillCbrRatesRequest{FromDate: "2024-02-16", ToDate: "2024-02-19"}))
	require.Error(t, err)
	assert.Equal(t, connect.CodeFailedPrecondition, connect.CodeOf(err))
}
