package portfolio

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"connectrpc.com/connect"
	apiv1 "github.com/foxcool/greedy-eye/api/v1"
	"github.com/foxcool/greedy-eye/internal/entity"
	"github.com/foxcool/greedy-eye/internal/middleware"
	"github.com/foxcool/greedy-eye/internal/pricefresh"
	"github.com/foxcool/greedy-eye/internal/quoting"
	"github.com/shopspring/decimal"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const (
	// incomeValueDecimals scales report values, as resultDecimals scales a
	// portfolio total: cents of the display currency.
	incomeValueDecimals = 2

	// incomePriceWindow is how far from an event a price may lie and still
	// stand for the event's date. Daily prices miss weekends and holidays; a
	// price further off than this describes another market.
	incomePriceWindow = 3 * 24 * time.Hour

	// incomeRateLookback is how old the last rate before an event may be and
	// still be taken as the rate in force. The central bank's longest pause is
	// the New Year holiday, about eleven days. A longer gap is a gap in our
	// data, not in its rates: dev once held no rate for 507 days, and without a
	// bound every payment in them would be valued at the rate from before it.
	incomeRateLookback = 15 * 24 * time.Hour

	// cbrSource is the price source of central bank rates.
	cbrSource = "cbr"

	historyPageSize = 1000
)

// GetIncomeReport sums the income events of one portfolio or account over a
// period, valued in the display currency at each event's date. The sums are
// buildIncomeReport's; this gathers its inputs and serves its output.
func (h *Handler) GetIncomeReport(ctx context.Context, req *connect.Request[apiv1.GetIncomeReportRequest]) (*connect.Response[apiv1.GetIncomeReportResponse], error) {
	if h.mdClient == nil {
		return nil, connect.NewError(connect.CodeUnimplemented, errors.New("market data client not configured"))
	}
	if _, ok := middleware.UserFromContext(ctx); !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("user not found in context"))
	}

	// Scoped by the ownership check of the named portfolio or account below,
	// not by the caller's id: that check lets an admin read one they do not
	// own, as ListHoldings does, and a user filter on top would answer empty.
	opts := ListLedgerOpts{Kind: entity.LedgerKindIncome, PageSize: maxImportBatch}
	portfolioID, accountID := req.Msg.GetPortfolioId(), req.Msg.GetAccountId()
	switch {
	case (portfolioID == "") == (accountID == ""):
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("exactly one of portfolio_id and account_id is required"))
	case portfolioID != "":
		if _, err := h.ownedPortfolio(ctx, portfolioID); err != nil {
			return nil, err
		}
		opts.PortfolioID = portfolioID
	default:
		if _, err := h.ownedAccount(ctx, accountID); err != nil {
			return nil, err
		}
		opts.AccountID = accountID
	}

	if req.Msg.From == nil || req.Msg.To == nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("from and to are required"))
	}
	opts.ExecutedFrom, opts.ExecutedTo = req.Msg.From.AsTime(), req.Msg.To.AsTime()
	if !opts.ExecutedFrom.Before(opts.ExecutedTo) {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("from must be before to"))
	}

	quoteAssetID := pricefresh.PolicyFrom(ctx, h.setClient, h.log).QuoteAsset()
	if req.Msg.GetQuoteAssetId() != "" {
		quoteAssetID = req.Msg.GetQuoteAssetId()
	}
	quoteAssetID, err := quoting.ResolveQuote(ctx, h.mdClient, quoteAssetID)
	if err != nil {
		return nil, err
	}

	var events []*entity.LedgerEvent
	for {
		page, next, err := h.store.ListLedgerEvents(ctx, opts)
		if err != nil {
			return nil, toConnectError(err)
		}
		events = append(events, page...)
		if next == "" {
			break
		}
		opts.PageToken = next
	}

	assets, err := h.incomeAssets(ctx, events)
	if err != nil {
		return nil, err
	}
	price, err := h.incomePrices(ctx, events, assets, quoteAssetID)
	if err != nil {
		return nil, err
	}
	report := buildIncomeReport(events, price)
	return connect.NewResponse(incomeReportToProto(report, assets, quoteAssetID)), nil
}

// incomeAssets loads every asset the events moved, for its type and symbol.
func (h *Handler) incomeAssets(ctx context.Context, events []*entity.LedgerEvent) (map[string]*apiv1.Asset, error) {
	assets := map[string]*apiv1.Asset{}
	for _, e := range events {
		for _, l := range e.Legs {
			if _, seen := assets[l.AssetID]; seen {
				continue
			}
			resp, err := h.mdClient.GetAsset(ctx, connect.NewRequest(&apiv1.GetAssetRequest{Id: l.AssetID}))
			if err != nil {
				return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("get asset %s: %w", l.AssetID, err))
			}
			assets[l.AssetID] = resp.Msg
		}
	}
	return assets, nil
}

// datedPrice is one unit price of an asset in the display currency.
type datedPrice struct {
	at   time.Time
	unit decimal.Decimal
}

// incomePrices decides which price stands for an event's date.
//
// A currency is valued at the central bank rate in force on that date: the
// last rate set at or before the event, the way an accountant would, if it
// is no older than incomeRateLookback. Anything
// else takes the price nearest the event within incomePriceWindow, whatever
// source wrote it. The display currency is worth one of itself.
//
// shortcut: only prices quoted directly in the display currency count, plus
// the inverse central bank pair for a currency; a cross through a third asset
// is not tried. Upgrade when an income asset is quoted only against another
// base (personal-go7b).
func (h *Handler) incomePrices(ctx context.Context, events []*entity.LedgerEvent, assets map[string]*apiv1.Asset, quoteAssetID string) (incomePrice, error) {
	if len(events) == 0 {
		return func(string, time.Time) (decimal.Decimal, bool) { return decimal.Zero, false }, nil
	}
	first, last := events[0].ExecutedAt, events[0].ExecutedAt
	for _, e := range events {
		first, last = minTime(first, e.ExecutedAt), maxTime(last, e.ExecutedAt)
	}

	series := map[string][]datedPrice{}
	for assetID, asset := range assets {
		if assetID == quoteAssetID {
			continue
		}
		var (
			prices []datedPrice
			err    error
		)
		if asset.Type == apiv1.AssetType_ASSET_TYPE_FOREX {
			prices, err = h.priceSeries(ctx, assetID, quoteAssetID, cbrSource, first.Add(-incomeRateLookback), last.Add(time.Second))
			if err == nil && len(prices) == 0 {
				prices, err = h.inverseSeries(ctx, assetID, quoteAssetID, first.Add(-incomeRateLookback), last.Add(time.Second))
			}
		} else {
			prices, err = h.priceSeries(ctx, assetID, quoteAssetID, "", first.Add(-incomePriceWindow), last.Add(incomePriceWindow))
		}
		if err != nil {
			return nil, err
		}
		series[assetID] = prices
	}

	return func(assetID string, at time.Time) (decimal.Decimal, bool) {
		if assetID == quoteAssetID {
			return decimal.NewFromInt(1), true
		}
		prices := series[assetID]
		if asset := assets[assetID]; asset != nil && asset.Type == apiv1.AssetType_ASSET_TYPE_FOREX {
			return rateInForce(prices, at)
		}
		return nearestPrice(prices, at, incomePriceWindow)
	}, nil
}

// rateInForce is the last price set at or before at, unless it is older than
// incomeRateLookback (see there). Prices are sorted by time.
func rateInForce(prices []datedPrice, at time.Time) (decimal.Decimal, bool) {
	i, _ := slices.BinarySearchFunc(prices, at, func(p datedPrice, t time.Time) int {
		if p.at.After(t) {
			return 1
		}
		return -1
	})
	if i == 0 {
		return decimal.Zero, false
	}
	p := prices[i-1]
	if at.Sub(p.at) > incomeRateLookback {
		return decimal.Zero, false
	}
	return p.unit, true
}

// nearestPrice is the price closest to at within window. prices are sorted by time.
func nearestPrice(prices []datedPrice, at time.Time, window time.Duration) (decimal.Decimal, bool) {
	best, found := time.Duration(0), false
	var unit decimal.Decimal
	for _, p := range prices {
		d := p.at.Sub(at).Abs()
		if d > window || (found && d >= best) {
			continue
		}
		best, unit, found = d, p.unit, true
	}
	return unit, found
}

// priceSeries reads the asset's price history in the base over [from, to),
// sorted by time; source empty means any.
func (h *Handler) priceSeries(ctx context.Context, assetID, baseID, source string, from, to time.Time) ([]datedPrice, error) {
	req := &apiv1.ListPriceHistoryRequest{
		AssetId: assetID, BaseAssetId: baseID,
		To: timestamppb.New(to),
	}
	if !from.IsZero() {
		req.From = timestamppb.New(from)
	}
	pageSize := int32(historyPageSize)
	req.PageSize = &pageSize
	if source != "" {
		req.SourceId = &source
	}
	var out []datedPrice
	for {
		resp, err := h.mdClient.ListPriceHistory(ctx, connect.NewRequest(req))
		if err != nil {
			return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("price history of %s: %w", assetID, err))
		}
		for _, p := range resp.Msg.Prices {
			last, err := decimal.NewFromString(p.Last)
			if err != nil || !last.IsPositive() {
				continue // a malformed or zero price is no price
			}
			out = append(out, datedPrice{at: p.Timestamp.AsTime(), unit: last.Shift(-int32(p.Decimals))}) // #nosec G115 -- decimals are small
		}
		if resp.Msg.NextPageToken == "" {
			break
		}
		req.PageToken = &resp.Msg.NextPageToken
	}
	slices.SortFunc(out, func(a, b datedPrice) int { return a.at.Compare(b.at) })
	return out, nil
}

// inverseSeries prices a currency through the central bank's rate of the
// display currency in it: RUB in USD is one over USD in RUB.
func (h *Handler) inverseSeries(ctx context.Context, assetID, quoteAssetID string, from, to time.Time) ([]datedPrice, error) {
	inverse, err := h.priceSeries(ctx, quoteAssetID, assetID, cbrSource, from, to)
	if err != nil {
		return nil, err
	}
	for i := range inverse {
		inverse[i].unit = decimal.NewFromInt(1).DivRound(inverse[i].unit, 16)
	}
	return inverse, nil
}

func incomeReportToProto(r incomeReport, assets map[string]*apiv1.Asset, quoteAssetID string) *apiv1.GetIncomeReportResponse {
	symbol := func(id string) string {
		if a := assets[id]; a != nil {
			return a.GetSymbol()
		}
		return ""
	}
	// Values are rounded to cents once, per row, and everything above a row is
	// added up from those cents: a net that is not gross minus tax, or a total
	// that is not the sum of its rows, is a number disagreeing with its parts.
	cents := func(d decimal.Decimal) decimal.Decimal { return d.Shift(incomeValueDecimals).Round(0) }
	var grossTotal, taxTotal decimal.Decimal

	out := &apiv1.GetIncomeReportResponse{QuoteAssetId: quoteAssetID, ValueDecimals: incomeValueDecimals}
	for _, row := range r.rows {
		amount := func(d decimal.Decimal) string { return d.Shift(entity.DecI32(row.decimals)).Round(0).String() }
		gross, tax := cents(row.grossValue), cents(row.taxValue)
		grossTotal, taxTotal = grossTotal.Add(gross), taxTotal.Add(tax)
		out.Rows = append(out.Rows, &apiv1.IncomeReportRow{
			Month:          timestamppb.New(row.month),
			IncomeKind:     incomeKindToProto[row.kind],
			AssetId:        row.assetID,
			Symbol:         symbol(row.assetID),
			GrossAmount:    amount(row.gross),
			TaxAmount:      amount(row.tax),
			NetAmount:      amount(row.net()),
			Decimals:       row.decimals,
			GrossValue:     gross.String(),
			TaxValue:       tax.String(),
			NetValue:       gross.Sub(tax).String(),
			UnvaluedEvents: int32(row.unvaluedEvents), // #nosec G115 -- bounded by the event count
		})
	}
	out.GrossValue, out.TaxValue, out.NetValue = grossTotal.String(), taxTotal.String(), grossTotal.Sub(taxTotal).String()
	for _, u := range r.unvalued {
		out.Unvalued = append(out.Unvalued, &apiv1.UnvaluedIncome{
			EventId: u.eventID, ExecutedAt: timestamppb.New(u.executedAt),
			AssetId: u.assetID, Symbol: symbol(u.assetID),
		})
	}
	return out
}

func minTime(a, b time.Time) time.Time {
	if b.Before(a) {
		return b
	}
	return a
}

func maxTime(a, b time.Time) time.Time {
	if b.After(a) {
		return b
	}
	return a
}
