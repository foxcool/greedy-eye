package portfolio

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"testing"
	"time"

	"connectrpc.com/connect"
	apiv1 "github.com/foxcool/greedy-eye/api/v1"
	"github.com/foxcool/greedy-eye/internal/entity"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// pagedHoldingsStore serves holdings the way the postgres store does: ordered
// by id, a strict `id > token` cursor, and a page smaller than the handler asks
// for, so one response has to span several store pages.
type pagedHoldingsStore struct {
	*mockStore
	rows     []*entity.Holding
	pageSize int
}

func (s *pagedHoldingsStore) ListHoldings(_ context.Context, opts ListHoldingsOpts) ([]*entity.Holding, string, error) {
	after := ""
	if opts.PageToken != "" {
		raw, err := base64.StdEncoding.DecodeString(opts.PageToken)
		if err != nil {
			return nil, "", err
		}
		after = string(raw)
	}
	var page []*entity.Holding
	for _, r := range s.rows {
		if r.ID > after {
			page = append(page, r)
		}
		if len(page) == s.pageSize {
			break
		}
	}
	next := ""
	if len(page) == s.pageSize && page[len(page)-1].ID != s.rows[len(s.rows)-1].ID {
		next = base64.StdEncoding.EncodeToString([]byte(page[len(page)-1].ID))
	}
	return page, next, nil
}

// TestListUnpricedHoldings_ReasonFilterFillsPages: page_size promises up to N
// rows of the requested reason. Filtering only after the scan had stopped at N
// unpriced rows of any reason handed back short or empty pages with a token,
// and a client reading an empty page as the end lost the rest of the list.
func TestListUnpricedHoldings_ReasonFilterFillsPages(t *testing.T) {
	const (
		neverAsset   = "00000000-0000-0000-0000-0000000000b1"
		noQuoteAsset = "00000000-0000-0000-0000-0000000000b2"
	)
	// Every row is unpriced; only a sparse few were never priced at all.
	never := map[int]bool{2: true, 7: true, 8: true, 12: true}
	var rows []*entity.Holding
	var wantIDs []string
	for i := 1; i <= 12; i++ {
		id := fmt.Sprintf("00000000-0000-0000-0000-0000000001%02d", i)
		asset := noQuoteAsset
		if never[i] {
			asset = neverAsset
			wantIDs = append(wantIDs, id)
		}
		rows = append(rows, &entity.Holding{ID: id, AssetID: asset, Amount: decimal.NewFromInt(1), Decimals: 0})
	}
	s := &pagedHoldingsStore{mockStore: &mockStore{}, rows: rows, pageSize: 3}

	md := &mockMDClient{}
	md.On("GetLatestPrice", mock.Anything, mock.Anything).
		Return((*connect.Response[apiv1.Price])(nil), connect.NewError(connect.CodeNotFound, errors.New("no price")))
	md.On("GetAsset", mock.Anything, assetReq("USD")).
		Return(connect.NewResponse(&apiv1.Asset{Id: "USD", Symbol: strPtr("USD")}), nil).Maybe()
	md.On("GetAsset", mock.Anything, mock.Anything).
		Return(connect.NewResponse(&apiv1.Asset{Symbol: strPtr("X")}), nil)
	md.On("GetPricingStatus", mock.Anything, mock.Anything).
		Return(connect.NewResponse(&apiv1.GetPricingStatusResponse{
			Statuses: []*apiv1.AssetPricingStatus{{
				AssetId:      neverAsset,
				EverPriced:   false,
				FirstAskedAt: timestamppb.New(time.Now().Add(-72 * time.Hour)),
			}},
		}), nil)

	h := newHandler(s).WithMarketDataClient(md)
	reason := apiv1.UnpricedReason_UNPRICED_REASON_NEVER_PRICED
	const pageSize = 2

	var got []string
	seen := map[string]bool{}
	token := ""
	for calls := 0; ; calls++ {
		require.Less(t, calls, len(rows), "the walk must end")
		req := unpricedReq("", pageSize, token)
		req.Msg.Reason = &reason
		resp, err := h.ListUnpricedHoldings(ctxWithUser(testUserID), req)
		require.NoError(t, err)

		token = resp.Msg.NextPageToken
		if token != "" {
			assert.Len(t, resp.Msg.Holdings, pageSize, "a page with a next token must be full")
		}
		for _, u := range resp.Msg.Holdings {
			assert.Equal(t, reason, u.Reason)
			assert.False(t, seen[u.HoldingId], "row %s returned twice", u.HoldingId)
			seen[u.HoldingId] = true
			got = append(got, u.HoldingId)
		}
		if token == "" {
			break
		}
	}
	assert.Equal(t, wantIDs, got, "every matching row exactly once, in cursor order")

	// Symbols are looked up only for rows returned, not for every unpriced row
	// the filter scanned past.
	lookups := 0
	for _, c := range md.Calls {
		if c.Method == "GetAsset" && c.Arguments.Get(1).(*connect.Request[apiv1.GetAssetRequest]).Msg.Id != "USD" {
			lookups++
		}
	}
	assert.Equal(t, len(wantIDs), lookups, "one asset lookup per returned row")
}
