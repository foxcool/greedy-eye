package analytics

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"time"

	"connectrpc.com/connect"
	apiv1 "github.com/foxcool/greedy-eye/api/v1"
	"github.com/foxcool/greedy-eye/api/v1/apiv1connect"
	"github.com/foxcool/greedy-eye/internal/middleware"
	"github.com/foxcool/greedy-eye/internal/service/portfolio"
	"github.com/foxcool/greedy-eye/internal/store"
	"github.com/shopspring/decimal"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// defaultQuoteAsset is the symbol used when the caller omits quote_asset_id.
// The marketdata handler resolves it to a UUID via GetAssetBySymbol.
const defaultQuoteAsset = "USD"

// maxHoldings caps how many holdings a single heatmap reads.
const maxHoldings = 1000

// Handler implements apiv1connect.AnalyticsServiceHandler.
type Handler struct {
	apiv1connect.UnimplementedAnalyticsServiceHandler
	store    Store
	mdClient MarketDataClient // optional; nil if not configured
	log      *slog.Logger
}

func NewHandler(store Store, log *slog.Logger) *Handler {
	return &Handler{store: store, log: log}
}

// WithMarketDataClient returns a new Handler with the MarketData client injected.
func (h *Handler) WithMarketDataClient(mc MarketDataClient) *Handler {
	copied := *h
	copied.mdClient = mc
	return &copied
}

func (h *Handler) GetHeatmap(ctx context.Context, req *connect.Request[apiv1.GetHeatmapRequest]) (*connect.Response[apiv1.GetHeatmapResponse], error) {
	if h.mdClient == nil {
		return nil, connect.NewError(connect.CodeUnimplemented, errors.New("market data client not configured"))
	}

	switch req.Msg.Scope {
	case apiv1.HeatmapScope_HEATMAP_SCOPE_PORTFOLIO:
		return h.portfolioHeatmap(ctx, req.Msg)
	case apiv1.HeatmapScope_HEATMAP_SCOPE_UNSPECIFIED:
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("scope is required"))
	default:
		return nil, connect.NewError(connect.CodeUnimplemented,
			fmt.Errorf("scope %s is not implemented yet", req.Msg.Scope))
	}
}

func (h *Handler) portfolioHeatmap(ctx context.Context, msg *apiv1.GetHeatmapRequest) (*connect.Response[apiv1.GetHeatmapResponse], error) {
	if msg.ScopeId == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("scope_id (portfolio id) is required"))
	}

	switch msg.SizeMetric {
	case apiv1.HeatmapSizeMetric_HEATMAP_SIZE_METRIC_UNSPECIFIED, apiv1.HeatmapSizeMetric_HEATMAP_SIZE_METRIC_VALUE:
	default:
		return nil, connect.NewError(connect.CodeInvalidArgument,
			fmt.Errorf("size metric %s does not apply to portfolio scope", msg.SizeMetric))
	}

	switch msg.ColorMetric {
	case apiv1.HeatmapColorMetric_HEATMAP_COLOR_METRIC_UNSPECIFIED, apiv1.HeatmapColorMetric_HEATMAP_COLOR_METRIC_CHANGE_PCT:
	default:
		return nil, connect.NewError(connect.CodeUnimplemented,
			fmt.Errorf("color metric %s is not implemented yet", msg.ColorMetric))
	}

	switch msg.GroupBy {
	case apiv1.HeatmapGroupBy_HEATMAP_GROUP_BY_UNSPECIFIED, apiv1.HeatmapGroupBy_HEATMAP_GROUP_BY_ACCOUNT:
	default:
		return nil, connect.NewError(connect.CodeUnimplemented,
			fmt.Errorf("grouping by %s is not implemented yet", msg.GroupBy))
	}

	p, err := h.store.GetPortfolio(ctx, msg.ScopeId)
	if err != nil {
		return nil, toConnectError(err)
	}
	if err := middleware.EnsureOwner(ctx, p.UserID); err != nil {
		return nil, err
	}

	quoteAssetID := msg.QuoteAssetId
	if quoteAssetID == "" {
		quoteAssetID = defaultQuoteAsset
	}
	from := time.Now().Add(-windowDuration(msg.Window))

	holdings, _, err := h.store.ListHoldings(ctx, portfolio.ListHoldingsOpts{
		PortfolioID:  msg.ScopeId,
		PageSize:     maxHoldings,
		HideExcluded: true,
	})
	if err != nil {
		return nil, toConnectError(err)
	}

	// Aggregate holding values per leaf. Leaves are per asset when flat,
	// per (account, asset) when grouped by account.
	grouped := msg.GroupBy == apiv1.HeatmapGroupBy_HEATMAP_GROUP_BY_ACCOUNT
	type leafKey struct{ accountID, assetID string }
	values := map[leafKey]decimal.Decimal{}
	assets := map[string]*assetPricing{} // assetID → resolved price/change/label

	for _, hld := range holdings {
		ap, err := h.assetPricing(ctx, assets, hld.AssetID, quoteAssetID, from)
		if err != nil {
			return nil, err
		}
		if !ap.priced {
			continue // no price path for this asset → skip, same as CalculatePortfolioValue
		}
		key := leafKey{assetID: hld.AssetID}
		if grouped {
			key.accountID = hld.AccountID
		}
		holdingValue := hld.Amount.Shift(-decI32(hld.Decimals)).Mul(ap.unit)
		values[key] = values[key].Add(holdingValue)
	}

	var nodes []*apiv1.HeatmapNode
	for key, value := range values {
		ap := assets[key.assetID]
		id, parent := key.assetID, ""
		if grouped {
			id = key.accountID + ":" + key.assetID
			parent = key.accountID
		}
		price := ap.unit.InexactFloat64()
		nodes = append(nodes, &apiv1.HeatmapNode{
			Id:         id,
			Label:      ap.label,
			ParentId:   parent,
			Size:       value.InexactFloat64(),
			ColorValue: ap.changePct,
			Price:      &price,
			AssetId:    key.assetID,
		})
	}
	sort.Slice(nodes, func(i, j int) bool { return nodes[i].Size > nodes[j].Size })

	if grouped {
		groups, err := h.accountGroupNodes(ctx, nodes)
		if err != nil {
			return nil, err
		}
		nodes = append(groups, nodes...)
	}

	return connect.NewResponse(&apiv1.GetHeatmapResponse{
		Nodes:        nodes,
		QuoteAssetId: quoteAssetID,
		CalculatedAt: timestamppb.New(time.Now()),
	}), nil
}

// accountGroupNodes builds one parent node per account referenced by leaves.
// Group size is the sum of child sizes; group color is the size-weighted
// average of child colors.
func (h *Handler) accountGroupNodes(ctx context.Context, leaves []*apiv1.HeatmapNode) ([]*apiv1.HeatmapNode, error) {
	type agg struct {
		size, weightedColor float64
	}
	byAccount := map[string]*agg{}
	for _, n := range leaves {
		a := byAccount[n.ParentId]
		if a == nil {
			a = &agg{}
			byAccount[n.ParentId] = a
		}
		a.size += n.Size
		a.weightedColor += n.Size * n.ColorValue
	}

	groups := make([]*apiv1.HeatmapNode, 0, len(byAccount))
	for accountID, a := range byAccount {
		label := accountID
		acc, err := h.store.GetAccount(ctx, accountID)
		switch {
		case err == nil:
			label = acc.Name
		case errors.Is(err, store.ErrNotFound):
			// Keep the ID as label; the holding outlived its account record.
		default:
			return nil, toConnectError(err)
		}
		color := 0.0
		if a.size != 0 {
			color = a.weightedColor / a.size
		}
		groups = append(groups, &apiv1.HeatmapNode{
			Id:         accountID,
			Label:      label,
			Size:       a.size,
			ColorValue: color,
		})
	}
	sort.Slice(groups, func(i, j int) bool { return groups[i].Size > groups[j].Size })
	return groups, nil
}

func windowDuration(w apiv1.HeatmapWindow) time.Duration {
	switch w {
	case apiv1.HeatmapWindow_HEATMAP_WINDOW_7D:
		return 7 * 24 * time.Hour
	case apiv1.HeatmapWindow_HEATMAP_WINDOW_30D:
		return 30 * 24 * time.Hour
	default:
		return 24 * time.Hour
	}
}

func toConnectError(err error) error {
	if errors.Is(err, store.ErrNotFound) {
		return connect.NewError(connect.CodeNotFound, err)
	}
	if errors.Is(err, store.ErrInvalidArgument) {
		return connect.NewError(connect.CodeInvalidArgument, err)
	}
	if errors.Is(err, store.ErrConstraint) {
		return connect.NewError(connect.CodeFailedPrecondition, err)
	}
	return connect.NewError(connect.CodeInternal, err)
}
