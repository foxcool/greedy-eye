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
// Every lookup goes through the single-pair handler it stands for —
// GetLatestPrice, or ListPriceHistory from `from` with one row — so a batch
// cannot answer differently from the calls it replaces. A pair with no price
// (NotFound, an unknown ticker included) comes back without one, which is how
// callers of the single-pair RPC already read NotFound; any other failure
// fails the batch, as it would have failed the caller.
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

	results := make([]*apiv1.PriceLookupResult, 0, len(lookups))
	for _, l := range lookups {
		p, err := h.lookupPrice(ctx, l)
		if err != nil && connect.CodeOf(err) != connect.CodeNotFound {
			return nil, err
		}
		results = append(results, &apiv1.PriceLookupResult{Lookup: l, Price: p})
	}
	return connect.NewResponse(&apiv1.GetPricesResponse{Results: results}), nil
}

// lookupPrice answers one lookup; a nil price with a nil error means none.
func (h *Handler) lookupPrice(ctx context.Context, l *apiv1.PriceLookup) (*apiv1.Price, error) {
	if l.From == nil {
		resp, err := h.GetLatestPrice(ctx, connect.NewRequest(&apiv1.GetLatestPriceRequest{
			AssetId: l.GetAssetId(), BaseAssetId: l.GetBaseAssetId(),
		}))
		if err != nil {
			return nil, err
		}
		return resp.Msg, nil
	}
	pageSize := int32(1)
	resp, err := h.ListPriceHistory(ctx, connect.NewRequest(&apiv1.ListPriceHistoryRequest{
		AssetId: l.GetAssetId(), BaseAssetId: l.GetBaseAssetId(), From: l.From, PageSize: &pageSize,
	}))
	if err != nil {
		return nil, err
	}
	if len(resp.Msg.GetPrices()) == 0 {
		return nil, nil
	}
	return resp.Msg.GetPrices()[0], nil
}
