package quoting

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"connectrpc.com/connect"
	apiv1 "github.com/foxcool/greedy-eye/api/v1"
)

// BatchReader is the set form of PriceReader: many lookups in one call.
// Satisfied by *marketdata.Handler (monolith) and
// apiv1connect.MarketDataServiceClient (microservice), like PriceReader.
type BatchReader interface {
	GetPrices(context.Context, *connect.Request[apiv1.GetPricesRequest]) (*connect.Response[apiv1.GetPricesResponse], error)
}

// MaxLookups is the most lookups one GetPrices request carries, the bound
// market data enforces. A larger set costs one more call per batch, not one
// per asset.
const MaxLookups = 1000

// Prefetch collects, in a constant number of market-data calls, every price
// Candidates will ask about for these assets in quoteAssetID, so the pricing
// rule runs over a set without a hop per pair. The reads, each depending on
// the one before:
//
//  1. each asset's latest price in any base, and in the quote asset;
//  2. the conversion rate, both directions, for every base step 1 found.
//
// Feed the result to Candidates as its PriceReader. It answers exactly what
// the per-pair reads would have, because it is their answers, collected first.
func Prefetch(ctx context.Context, r BatchReader, assetIDs []string, quoteAssetID string) (Prefetched, error) {
	prices := Prefetched{}
	var lookups []*apiv1.PriceLookup
	var ids []string
	seen := make(map[string]struct{}, len(assetIDs))
	for _, id := range assetIDs {
		if _, dup := seen[id]; dup || id == quoteAssetID {
			continue // identity: Candidates asks nothing
		}
		seen[id] = struct{}{}
		ids = append(ids, id)
		lookups = append(lookups, &apiv1.PriceLookup{AssetId: id}, &apiv1.PriceLookup{AssetId: id, BaseAssetId: quoteAssetID})
	}
	if err := prices.Fetch(ctx, r, lookups); err != nil {
		return nil, err
	}
	lookups = nil
	for _, id := range ids {
		latest := prices[PriceKey{Asset: id}]
		if latest == nil || latest.BaseAssetId == "" || latest.BaseAssetId == quoteAssetID {
			continue
		}
		lookups = append(lookups,
			&apiv1.PriceLookup{AssetId: latest.BaseAssetId, BaseAssetId: quoteAssetID},
			&apiv1.PriceLookup{AssetId: quoteAssetID, BaseAssetId: latest.BaseAssetId})
	}
	if err := prices.Fetch(ctx, r, lookups); err != nil {
		return nil, err
	}
	return prices, nil
}

// PriceKey identifies one price lookup: a pair, latest unless windowed.
type PriceKey struct {
	Asset, Base string
	Windowed    bool
}

// KeyOf is the key a lookup's answer is recorded under.
func KeyOf(l *apiv1.PriceLookup) PriceKey {
	return PriceKey{Asset: l.GetAssetId(), Base: l.GetBaseAssetId(), Windowed: l.From != nil}
}

// Prefetched serves PriceReader from answers already collected. A pair asked
// and found priceless answers NotFound, as the RPC would. A pair never asked is
// a bug in the caller's lookup set, and fails loudly rather than reading as "no
// price" and silently dropping the asset from the valuation.
type Prefetched map[PriceKey]*apiv1.Price

// Fetch answers lookups through GetPrices and records every answer —
// including "no price" — in p.
func (p Prefetched) Fetch(ctx context.Context, r BatchReader, lookups []*apiv1.PriceLookup) error {
	for chunk := range slices.Chunk(lookups, MaxLookups) {
		resp, err := r.GetPrices(ctx, connect.NewRequest(&apiv1.GetPricesRequest{Lookups: chunk}))
		if err != nil {
			return err
		}
		if len(resp.Msg.GetResults()) != len(chunk) {
			return connect.NewError(connect.CodeInternal,
				fmt.Errorf("market data answered %d of %d price lookups", len(resp.Msg.GetResults()), len(chunk)))
		}
		for i, res := range resp.Msg.GetResults() {
			p[KeyOf(chunk[i])] = res.GetPrice()
		}
	}
	return nil
}

// GetLatestPrice makes Prefetched a PriceReader.
func (p Prefetched) GetLatestPrice(_ context.Context, req *connect.Request[apiv1.GetLatestPriceRequest]) (*connect.Response[apiv1.Price], error) {
	row, asked := p[PriceKey{Asset: req.Msg.GetAssetId(), Base: req.Msg.GetBaseAssetId()}]
	switch {
	case !asked:
		return nil, connect.NewError(connect.CodeInternal,
			fmt.Errorf("price %s/%s was not collected before pricing", req.Msg.GetAssetId(), req.Msg.GetBaseAssetId()))
	case row == nil:
		return nil, connect.NewError(connect.CodeNotFound, errors.New("no price"))
	default:
		return connect.NewResponse(row), nil
	}
}
