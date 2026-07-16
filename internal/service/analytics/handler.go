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
	"github.com/foxcool/greedy-eye/internal/entity"
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

// unassignedGroupID labels holdings whose account belongs to no portfolio
// when grouping a balance heatmap by portfolio.
const unassignedGroupID = "unassigned"

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
	case apiv1.HeatmapScope_HEATMAP_SCOPE_PORTFOLIO, apiv1.HeatmapScope_HEATMAP_SCOPE_BALANCE:
		return h.heatmap(ctx, req.Msg)
	case apiv1.HeatmapScope_HEATMAP_SCOPE_UNSPECIFIED:
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("scope is required"))
	default:
		return nil, connect.NewError(connect.CodeUnimplemented,
			fmt.Errorf("scope %s is not implemented yet", req.Msg.Scope))
	}
}

// heatmap serves both holding-backed scopes: PORTFOLIO (one portfolio) and
// BALANCE (all caller's holdings across portfolios).
func (h *Handler) heatmap(ctx context.Context, msg *apiv1.GetHeatmapRequest) (*connect.Response[apiv1.GetHeatmapResponse], error) {
	switch msg.SizeMetric {
	case apiv1.HeatmapSizeMetric_HEATMAP_SIZE_METRIC_UNSPECIFIED, apiv1.HeatmapSizeMetric_HEATMAP_SIZE_METRIC_VALUE:
	default:
		return nil, connect.NewError(connect.CodeInvalidArgument,
			fmt.Errorf("size metric %s does not apply to scope %s", msg.SizeMetric, msg.Scope))
	}

	switch msg.ColorMetric {
	case apiv1.HeatmapColorMetric_HEATMAP_COLOR_METRIC_UNSPECIFIED, apiv1.HeatmapColorMetric_HEATMAP_COLOR_METRIC_CHANGE_PCT:
	default:
		return nil, connect.NewError(connect.CodeUnimplemented,
			fmt.Errorf("color metric %s is not implemented yet", msg.ColorMetric))
	}

	isBalance := msg.Scope == apiv1.HeatmapScope_HEATMAP_SCOPE_BALANCE

	switch msg.GroupBy {
	case apiv1.HeatmapGroupBy_HEATMAP_GROUP_BY_UNSPECIFIED, apiv1.HeatmapGroupBy_HEATMAP_GROUP_BY_ACCOUNT:
	case apiv1.HeatmapGroupBy_HEATMAP_GROUP_BY_PORTFOLIO:
		if !isBalance {
			return nil, connect.NewError(connect.CodeInvalidArgument,
				errors.New("grouping by portfolio only applies to scope BALANCE"))
		}
	default:
		return nil, connect.NewError(connect.CodeUnimplemented,
			fmt.Errorf("grouping by %s is not implemented yet", msg.GroupBy))
	}

	// Scope resolution: which holdings, and who may see them.
	var opts portfolio.ListHoldingsOpts
	if isBalance {
		if msg.ScopeId != "" {
			return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("scope_id must be empty for scope BALANCE"))
		}
		user, ok := middleware.UserFromContext(ctx)
		if !ok {
			return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("user not found in context"))
		}
		opts.UserID = user.ID
	} else {
		if msg.ScopeId == "" {
			return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("scope_id (portfolio id) is required"))
		}
		p, err := h.store.GetPortfolio(ctx, msg.ScopeId)
		if err != nil {
			return nil, toConnectError(err)
		}
		if err := middleware.EnsureOwner(ctx, p.UserID); err != nil {
			return nil, err
		}
		opts.PortfolioID = msg.ScopeId
	}
	opts.PageSize = maxHoldings
	opts.HideExcluded = true

	quoteAssetID := msg.QuoteAssetId
	if quoteAssetID == "" {
		quoteAssetID = defaultQuoteAsset
	}
	from := time.Now().Add(-windowDuration(msg.Window))

	holdings, _, err := h.store.ListHoldings(ctx, opts)
	if err != nil {
		return nil, toConnectError(err)
	}

	// Aggregate holding values per leaf. Leaves are per asset when flat,
	// per (group, asset) when grouped.
	grouped := msg.GroupBy != apiv1.HeatmapGroupBy_HEATMAP_GROUP_BY_UNSPECIFIED
	type leafKey struct{ groupID, assetID string }
	values := map[leafKey]decimal.Decimal{}
	assets := map[string]*assetPricing{}       // assetID → resolved price/change/label
	accountPortfolios := map[string]string{}   // accountID → its portfolio id (for group_by=portfolio)

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
			key.groupID, err = h.groupID(ctx, msg.GroupBy, hld, accountPortfolios)
			if err != nil {
				return nil, err
			}
		}
		holdingValue := hld.Amount.Shift(-decI32(hld.Decimals)).Mul(ap.unit)
		values[key] = values[key].Add(holdingValue)
	}

	var nodes []*apiv1.HeatmapNode
	for key, value := range values {
		ap := assets[key.assetID]
		id, parent := key.assetID, ""
		if grouped {
			id = key.groupID + ":" + key.assetID
			parent = key.groupID
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
		groups, err := h.groupNodes(ctx, msg.GroupBy, nodes)
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

// groupID resolves which group a holding belongs to for the given axis.
// For portfolio grouping a holding without its own portfolio inherits the
// account's; accounts outside any portfolio land in the "unassigned" group.
func (h *Handler) groupID(ctx context.Context, groupBy apiv1.HeatmapGroupBy, hld *entity.Holding, accountPortfolios map[string]string) (string, error) {
	if groupBy == apiv1.HeatmapGroupBy_HEATMAP_GROUP_BY_ACCOUNT {
		return hld.AccountID, nil
	}

	// HEATMAP_GROUP_BY_PORTFOLIO
	if hld.PortfolioID != "" {
		return hld.PortfolioID, nil
	}
	if pid, ok := accountPortfolios[hld.AccountID]; ok {
		return pid, nil
	}
	pid := ""
	acc, err := h.store.GetAccount(ctx, hld.AccountID)
	switch {
	case err == nil:
		pid = acc.PortfolioID
	case errors.Is(err, store.ErrNotFound):
		// Keep the holding under "unassigned"; it outlived its account record.
	default:
		return "", toConnectError(err)
	}
	if pid == "" {
		pid = unassignedGroupID
	}
	accountPortfolios[hld.AccountID] = pid
	return pid, nil
}

// groupNodes builds one parent node per group referenced by leaves.
// Group size is the sum of child sizes; group color is the size-weighted
// average of child colors.
func (h *Handler) groupNodes(ctx context.Context, groupBy apiv1.HeatmapGroupBy, leaves []*apiv1.HeatmapNode) ([]*apiv1.HeatmapNode, error) {
	type agg struct {
		size, weightedColor float64
	}
	byGroup := map[string]*agg{}
	for _, n := range leaves {
		a := byGroup[n.ParentId]
		if a == nil {
			a = &agg{}
			byGroup[n.ParentId] = a
		}
		a.size += n.Size
		a.weightedColor += n.Size * n.ColorValue
	}

	groups := make([]*apiv1.HeatmapNode, 0, len(byGroup))
	for groupID, a := range byGroup {
		label, err := h.groupLabel(ctx, groupBy, groupID)
		if err != nil {
			return nil, err
		}
		color := 0.0
		if a.size != 0 {
			color = a.weightedColor / a.size
		}
		groups = append(groups, &apiv1.HeatmapNode{
			Id:         groupID,
			Label:      label,
			Size:       a.size,
			ColorValue: color,
		})
	}
	sort.Slice(groups, func(i, j int) bool { return groups[i].Size > groups[j].Size })
	return groups, nil
}

// groupLabel resolves a human label for a group node; a group whose entity
// record is gone keeps its ID as the label.
func (h *Handler) groupLabel(ctx context.Context, groupBy apiv1.HeatmapGroupBy, groupID string) (string, error) {
	if groupBy == apiv1.HeatmapGroupBy_HEATMAP_GROUP_BY_PORTFOLIO && groupID == unassignedGroupID {
		return "Unassigned", nil
	}

	var name string
	var err error
	switch groupBy {
	case apiv1.HeatmapGroupBy_HEATMAP_GROUP_BY_ACCOUNT:
		var acc *entity.Account
		if acc, err = h.store.GetAccount(ctx, groupID); err == nil {
			name = acc.Name
		}
	case apiv1.HeatmapGroupBy_HEATMAP_GROUP_BY_PORTFOLIO:
		var p *entity.Portfolio
		if p, err = h.store.GetPortfolio(ctx, groupID); err == nil {
			name = p.Name
		}
	default:
		return groupID, nil
	}

	switch {
	case err == nil:
		return name, nil
	case errors.Is(err, store.ErrNotFound):
		return groupID, nil
	default:
		return "", toConnectError(err)
	}
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
