package marketdata

import (
	"context"
	"fmt"

	"connectrpc.com/connect"
	apiv1 "github.com/foxcool/greedy-eye/api/v1"
)

// maxPriceLookups bounds one GetPrices request; each lookup is a store read.
const maxPriceLookups = 1000

// GetPrices answers a set of price lookups in one call, so a consumer in
// another domain collects its prices before deciding instead of hopping once
// per holding.
//
// Latest lookups are answered together by Store.LatestPrices, one query for
// the whole set, which runs the single-pair GetLatestPrice query per pair so
// the batch cannot answer differently from the calls it replaces. Windowed
// lookups still go one by one through ListPriceHistory from `from` with one
// row: they are the minority of a batch (one per priced asset, at most), and
// "first row at or after a time" per pair has no set form as plain as the
// latest one.
//
// A pair with no price (NotFound, an unknown ticker included) comes back
// without one, which is how callers of the single-pair RPC already read
// NotFound; any other failure fails the batch, as it would have failed the
// caller.
func (h *Handler) GetPrices(ctx context.Context, req *connect.Request[apiv1.GetPricesRequest]) (*connect.Response[apiv1.GetPricesResponse], error) {
	lookups := req.Msg.GetLookups()
	if len(lookups) > maxPriceLookups {
		return nil, connect.NewError(connect.CodeInvalidArgument,
			fmt.Errorf("at most %d lookups per request", maxPriceLookups))
	}
	for i, l := range lookups {
		if l.GetAssetId() == "" {
			return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("lookup %d: asset_id is required", i))
		}
		if l.From != nil && l.GetBaseAssetId() == "" {
			return nil, connect.NewError(connect.CodeInvalidArgument,
				fmt.Errorf("lookup %d: base_asset_id is required with from", i))
		}
	}

	results := make([]*apiv1.PriceLookupResult, len(lookups))
	for i, l := range lookups {
		results[i] = &apiv1.PriceLookupResult{Lookup: l}
	}
	if err := h.latestPrices(ctx, lookups, results); err != nil {
		return nil, err
	}
	for i, l := range lookups {
		if l.From == nil {
			continue
		}
		p, err := h.lookupPrice(ctx, l)
		if err != nil && connect.CodeOf(err) != connect.CodeNotFound {
			return nil, err
		}
		results[i].Price = p
	}
	return connect.NewResponse(&apiv1.GetPricesResponse{Results: results}), nil
}

// latestPrices fills the result of every latest (unwindowed) lookup from one
// store read. Tickers are resolved first, as GetLatestPrice resolves them; a
// ticker the catalogue does not know leaves its lookup without a price.
func (h *Handler) latestPrices(ctx context.Context, lookups []*apiv1.PriceLookup, results []*apiv1.PriceLookupResult) error {
	resolved := map[string]string{} // ticker or id -> id; "" when unknown
	resolve := func(id string) (string, error) {
		if got, ok := resolved[id]; ok {
			return got, nil
		}
		got, err := h.resolveAssetID(ctx, id)
		if err != nil {
			if cerr := toConnectError(err); connect.CodeOf(cerr) != connect.CodeNotFound {
				return "", cerr
			}
			got = ""
		}
		resolved[id] = got
		return got, nil
	}

	var (
		pairs []PricePair
		at    []int // at[j] is the result pairs[j] answers
	)
	for i, l := range lookups {
		if l.From != nil {
			continue
		}
		assetID, err := resolve(l.GetAssetId())
		if err != nil {
			return err
		}
		baseID := ""
		if l.GetBaseAssetId() != "" {
			if baseID, err = resolve(l.GetBaseAssetId()); err != nil {
				return err
			}
		}
		if assetID == "" || (l.GetBaseAssetId() != "" && baseID == "") {
			continue // unknown ticker: no price, as GetLatestPrice's NotFound
		}
		pairs = append(pairs, PricePair{AssetID: assetID, BaseAssetID: baseID})
		at = append(at, i)
	}
	if len(pairs) == 0 {
		return nil
	}

	prices, err := h.store.LatestPrices(ctx, pairs)
	if err != nil {
		return toConnectError(err)
	}
	if len(prices) != len(pairs) {
		return connect.NewError(connect.CodeInternal,
			fmt.Errorf("store answered %d of %d latest prices", len(prices), len(pairs)))
	}
	for j, p := range prices {
		if p != nil {
			results[at[j]].Price = priceToProto(p)
		}
	}
	return nil
}

// lookupPrice answers one windowed lookup; a nil price with a nil error means
// none.
func (h *Handler) lookupPrice(ctx context.Context, l *apiv1.PriceLookup) (*apiv1.Price, error) {
	// ListPriceHistory resolves only the base, so the asset is resolved here:
	// the contract accepts a ticker in every lookup, windowed or not.
	assetID, err := h.resolveAssetID(ctx, l.GetAssetId())
	if err != nil {
		return nil, toConnectError(err)
	}
	pageSize := int32(1)
	resp, err := h.ListPriceHistory(ctx, connect.NewRequest(&apiv1.ListPriceHistoryRequest{
		AssetId: assetID, BaseAssetId: l.GetBaseAssetId(), From: l.From, PageSize: &pageSize,
	}))
	if err != nil {
		return nil, err
	}
	if len(resp.Msg.GetPrices()) == 0 {
		return nil, nil
	}
	return resp.Msg.GetPrices()[0], nil
}
