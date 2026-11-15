package marketdata

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"connectrpc.com/connect"
	apiv1 "github.com/foxcool/greedy-eye/api/v1"
	"github.com/foxcool/greedy-eye/internal/entity"
	"github.com/foxcool/greedy-eye/internal/store"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func TestGetPrices_AnswersEachLookupInOrder(t *testing.T) {
	const (
		eth  = "00000000-0000-0000-0000-0000000000e1"
		usd  = "00000000-0000-0000-0000-0000000000d1"
		btc  = "00000000-0000-0000-0000-0000000000b1"
		none = "00000000-0000-0000-0000-0000000000ff"
	)
	from := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	s := &mockStore{}
	s.On("LatestPrices", mock.Anything, []PricePair{{AssetID: eth}, {AssetID: eth, BaseAssetID: usd}, {AssetID: none}}).
		Return(func([]PricePair) []*entity.StoredPrice {
			return []*entity.StoredPrice{
				{ID: "any", AssetID: eth, BaseAssetID: btc},
				{ID: "direct", AssetID: eth, BaseAssetID: usd},
				nil, // no price for this pair
			}
		}, nil)
	s.On("ListPriceHistory", mock.Anything, ListPriceHistoryOpts{AssetID: eth, BaseAssetID: btc, From: &from, PageSize: 1}).
		Return([]*entity.StoredPrice{{ID: "then", AssetID: eth, BaseAssetID: btc}}, "", nil)
	s.On("ListPriceHistory", mock.Anything, ListPriceHistoryOpts{AssetID: none, BaseAssetID: btc, From: &from, PageSize: 1}).
		Return([]*entity.StoredPrice{}, "", nil)
	h := newHandler(s)

	lookups := []*apiv1.PriceLookup{
		{AssetId: eth},
		{AssetId: eth, BaseAssetId: usd},
		{AssetId: none},
		{AssetId: eth, BaseAssetId: btc, From: timestamppb.New(from)},
		{AssetId: none, BaseAssetId: btc, From: timestamppb.New(from)},
	}
	resp, err := h.GetPrices(context.Background(), connect.NewRequest(&apiv1.GetPricesRequest{Lookups: lookups}))
	require.NoError(t, err)
	require.Len(t, resp.Msg.Results, len(lookups))

	for i, r := range resp.Msg.Results {
		assert.Same(t, lookups[i], r.Lookup, "result %d echoes its lookup", i)
	}
	assert.Equal(t, "any", resp.Msg.Results[0].GetPrice().GetId())
	assert.Equal(t, "direct", resp.Msg.Results[1].GetPrice().GetId())
	assert.Nil(t, resp.Msg.Results[2].Price, "no price is an answer, not a failure")
	assert.Equal(t, "then", resp.Msg.Results[3].GetPrice().GetId())
	assert.Nil(t, resp.Msg.Results[4].Price)
}

func TestGetPrices_StoreFailureFailsTheBatch(t *testing.T) {
	const eth = "00000000-0000-0000-0000-0000000000e1"
	s := &mockStore{}
	s.On("LatestPrices", mock.Anything, []PricePair{{AssetID: eth}}).Return(nil, errors.New("connection reset"))
	h := newHandler(s)

	_, err := h.GetPrices(context.Background(), connect.NewRequest(&apiv1.GetPricesRequest{
		Lookups: []*apiv1.PriceLookup{{AssetId: eth}},
	}))
	require.Error(t, err)
	assert.Equal(t, connect.CodeInternal, connect.CodeOf(err))
}

func TestGetPrices_RejectsInvalidRequests(t *testing.T) {
	tooMany := make([]*apiv1.PriceLookup, maxPriceLookups+1)
	for i := range tooMany {
		tooMany[i] = &apiv1.PriceLookup{AssetId: "x"}
	}
	tests := []struct {
		name    string
		lookups []*apiv1.PriceLookup
	}{
		{"too many lookups", tooMany},
		{"missing asset_id", []*apiv1.PriceLookup{{BaseAssetId: "usd"}}},
		{"from without base", []*apiv1.PriceLookup{{AssetId: "eth", From: timestamppb.Now()}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// The empty store panics on any read: a rejected request reads nothing.
			h := newHandler(&mockStore{})
			_, err := h.GetPrices(context.Background(), connect.NewRequest(&apiv1.GetPricesRequest{Lookups: tt.lookups}))
			require.Error(t, err)
			assert.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))
		})
	}
}

// A ticker is accepted in a windowed lookup too, as the latest one accepts it.
func TestGetPrices_WindowedLookupResolvesTicker(t *testing.T) {
	const (
		btc = "00000000-0000-0000-0000-0000000000b1"
		usd = "00000000-0000-0000-0000-0000000000d1"
	)
	from := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	s := &mockStore{}
	s.On("GetAssetBySymbol", mock.Anything, "BTC").Return(&entity.Asset{ID: btc}, nil)
	s.On("ListPriceHistory", mock.Anything, ListPriceHistoryOpts{AssetID: btc, BaseAssetID: usd, From: &from, PageSize: 1}).
		Return([]*entity.StoredPrice{{ID: "then", AssetID: btc, BaseAssetID: usd}}, "", nil)
	h := newHandler(s)

	resp, err := h.GetPrices(context.Background(), connect.NewRequest(&apiv1.GetPricesRequest{
		Lookups: []*apiv1.PriceLookup{{AssetId: "BTC", BaseAssetId: usd, From: timestamppb.New(from)}},
	}))
	require.NoError(t, err)
	assert.Equal(t, "then", resp.Msg.Results[0].GetPrice().GetId())
}

// Latest lookups are answered by one set read: the store work for a batch
// must not grow with the number of lookups in it.
func TestGetPrices_LatestLookupsCostOneStoreRead(t *testing.T) {
	storeReads := func(n int) int {
		s := &mockStore{}
		answer := func(pairs []PricePair) []*entity.StoredPrice {
			out := make([]*entity.StoredPrice, len(pairs))
			for i, p := range pairs {
				out[i] = &entity.StoredPrice{ID: "p-" + p.AssetID, AssetID: p.AssetID, BaseAssetID: p.BaseAssetID}
			}
			return out
		}
		s.On("LatestPrices", mock.Anything, mock.Anything).Return(answer, nil)
		s.On("GetLatestPrice", mock.Anything, mock.Anything, mock.Anything, "").
			Return(&entity.StoredPrice{ID: "single"}, nil)
		h := newHandler(s)

		lookups := make([]*apiv1.PriceLookup, 0, 2*n)
		for i := range n {
			id := fmt.Sprintf("00000000-0000-0000-0000-%012d", i)
			lookups = append(lookups, &apiv1.PriceLookup{AssetId: id}, &apiv1.PriceLookup{AssetId: id, BaseAssetId: "00000000-0000-0000-0000-0000000000d1"})
		}
		resp, err := h.GetPrices(context.Background(), connect.NewRequest(&apiv1.GetPricesRequest{Lookups: lookups}))
		require.NoError(t, err)
		require.Len(t, resp.Msg.Results, len(lookups))
		return len(s.Calls)
	}
	assert.Equal(t, storeReads(3), storeReads(300), "store reads for 3 assets vs 300")
}

// A ticker is resolved once per batch however many lookups name it, and one
// the catalogue does not know leaves its lookup without a price, as
// GetLatestPrice's NotFound did.
func TestGetPrices_LatestLookupResolvesTickers(t *testing.T) {
	const (
		eth = "00000000-0000-0000-0000-0000000000e1"
		usd = "00000000-0000-0000-0000-0000000000d1"
	)
	s := &mockStore{}
	s.On("GetAssetBySymbol", mock.Anything, "USD").Return(&entity.Asset{ID: usd}, nil).Once()
	s.On("GetAssetBySymbol", mock.Anything, "NOPE").Return(nil, fmt.Errorf("asset: %w", store.ErrNotFound)).Once()
	s.On("LatestPrices", mock.Anything, []PricePair{{AssetID: eth, BaseAssetID: usd}, {AssetID: usd}}).
		Return(func([]PricePair) []*entity.StoredPrice {
			return []*entity.StoredPrice{{ID: "eth-usd", AssetID: eth, BaseAssetID: usd}, nil}
		}, nil)
	h := newHandler(s)

	resp, err := h.GetPrices(context.Background(), connect.NewRequest(&apiv1.GetPricesRequest{
		Lookups: []*apiv1.PriceLookup{
			{AssetId: eth, BaseAssetId: "USD"},
			{AssetId: "NOPE"},
			{AssetId: "USD"},
			{AssetId: eth, BaseAssetId: "NOPE"},
		},
	}))
	require.NoError(t, err)
	assert.Equal(t, "eth-usd", resp.Msg.Results[0].GetPrice().GetId())
	assert.Nil(t, resp.Msg.Results[1].Price, "unknown ticker: no price")
	assert.Nil(t, resp.Msg.Results[2].Price)
	assert.Nil(t, resp.Msg.Results[3].Price, "unknown base: no price")
	s.AssertExpectations(t)
}

// A resolution failure other than "unknown" fails the batch, as it failed
// GetLatestPrice.
func TestGetPrices_TickerResolutionFailureFailsTheBatch(t *testing.T) {
	s := &mockStore{}
	s.On("GetAssetBySymbol", mock.Anything, "USD").Return(nil, errors.New("connection reset"))
	h := newHandler(s)

	_, err := h.GetPrices(context.Background(), connect.NewRequest(&apiv1.GetPricesRequest{
		Lookups: []*apiv1.PriceLookup{{AssetId: "USD"}},
	}))
	assert.Equal(t, connect.CodeInternal, connect.CodeOf(err))
}

// An unknown asset settles its lookup before the base is looked at, as
// GetLatestPrice does: a base that would fail to resolve must not fail the
// batch for a lookup that already has its answer.
func TestGetPrices_UnknownAssetSkipsBaseResolution(t *testing.T) {
	s := &mockStore{}
	s.On("GetAssetBySymbol", mock.Anything, "NOPE").Return(nil, fmt.Errorf("asset: %w", store.ErrNotFound))
	// BROKEN is not stubbed: resolving it panics the mock.
	h := newHandler(s)

	resp, err := h.GetPrices(context.Background(), connect.NewRequest(&apiv1.GetPricesRequest{
		Lookups: []*apiv1.PriceLookup{{AssetId: "NOPE", BaseAssetId: "BROKEN"}},
	}))
	require.NoError(t, err)
	assert.Nil(t, resp.Msg.Results[0].Price)
}
