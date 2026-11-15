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
	s.On("GetLatestPrice", mock.Anything, eth, "", "").Return(&entity.StoredPrice{ID: "any", AssetID: eth, BaseAssetID: btc}, nil)
	s.On("GetLatestPrice", mock.Anything, eth, usd, "").Return(&entity.StoredPrice{ID: "direct", AssetID: eth, BaseAssetID: usd}, nil)
	s.On("GetLatestPrice", mock.Anything, none, "", "").Return(nil, fmt.Errorf("price: %w", store.ErrNotFound))
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
	s.On("GetLatestPrice", mock.Anything, eth, "", "").Return(nil, errors.New("connection reset"))
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
