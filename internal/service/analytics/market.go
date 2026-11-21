package analytics

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"connectrpc.com/connect"
	apiv1 "github.com/foxcool/greedy-eye/api/v1"
	"github.com/foxcool/greedy-eye/internal/entity"
	"github.com/foxcool/greedy-eye/internal/middleware"
	"github.com/foxcool/greedy-eye/internal/quoting"
	"github.com/foxcool/greedy-eye/internal/spend"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const (
	// marketTopN is how many coins the market map draws. One source page holds
	// up to 250 at the same cost, so the bound is legibility: past about a
	// hundred tiles the tail is slivers nobody can read or click.
	marketTopN = 100
	// marketTTL is how long one ranking read serves every caller. The colors
	// are 24h/7d/30d changes, which half an hour does not move visibly.
	marketTTL = 30 * time.Minute
	// marketQuote is the only currency the map is quoted in. The source prices
	// one currency per request; a cache per display currency would multiply the
	// spend.
	marketQuote = "USD"
)

// MarketRankingSource resolves the rankers a caller may read, in the order to
// try them. Satisfied by the credentials resolver.
type MarketRankingSource interface {
	MarketRankersFor(ctx context.Context, userID string) ([]entity.MarketRanker, error)
}

// marketCache holds the last successful ranking read. In memory only, like the
// macro series cache: a restart costs one read.
type marketCache struct {
	mu   sync.Mutex
	last *cachedRanking // nil until the first successful read
	now  func() time.Time
}

// cachedRanking is one ranking read and the ranker that made it; held coins are
// matched by that ranker's identity rule.
type cachedRanking struct {
	ranking entity.MarketRanking
	ranker  entity.MarketRanker
}

// WithMarketRankingSource returns a new Handler that serves scope MARKET from
// the rankings src resolves. Without it the scope stays Unimplemented.
func (h *Handler) WithMarketRankingSource(src MarketRankingSource) *Handler {
	copied := *h
	copied.rankings = src
	return &copied
}

// marketHeatmap draws the top of the crypto market by capitalisation rank:
// size is capitalisation or 24h volume, color the change over the window.
// Reads the cached ranking and writes nothing.
func (h *Handler) marketHeatmap(ctx context.Context, msg *apiv1.GetHeatmapRequest) (*connect.Response[apiv1.GetHeatmapResponse], error) {
	if h.rankings == nil {
		return nil, connect.NewError(connect.CodeUnimplemented, errors.New("market ranking source not configured"))
	}
	if err := validateMarketRequest(msg); err != nil {
		return nil, err
	}
	user, ok := middleware.UserFromContext(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("user not found in context"))
	}

	quoteAssetID, err := quoting.ResolveQuote(ctx, h.mdClient, marketQuote)
	if err != nil {
		return nil, err
	}
	if msg.QuoteAssetId != "" {
		asked, err := quoting.ResolveQuote(ctx, h.mdClient, msg.QuoteAssetId)
		if err != nil {
			return nil, err
		}
		if asked != quoteAssetID { // told so, rather than given USD under another name
			return nil, connect.NewError(connect.CodeInvalidArgument,
				errors.New("scope MARKET is quoted in USD only"))
		}
	}

	read, err := h.readRanking(ctx, user.ID)
	if err != nil {
		return nil, err
	}
	held, err := h.heldCoins(ctx, read.ranker)
	if err != nil {
		return nil, err
	}

	nodes := make([]*apiv1.HeatmapNode, 0, len(read.ranking.Coins))
	for _, coin := range read.ranking.Coins {
		color := 0.0 // no history: flat, as on the holdings maps
		if ch := windowChange(coin, msg.Window); ch != nil {
			color = *ch
		}
		label := coin.Symbol
		if label == "" {
			label = coin.Name
		}
		assetID, isHeld := held[coin.CoinID]
		nodes = append(nodes, &apiv1.HeatmapNode{
			Id:         read.ranking.Source + ":" + coin.CoinID,
			Label:      label,
			Size:       sizeOf(coin, msg.SizeMetric),
			ColorValue: color,
			Price:      proto.Float64(coin.Price),
			AssetId:    assetID,
			Rank:       proto.Int32(int32(coin.Rank)), // #nosec G115 -- RankedCoin.Rank fits int32 by contract; the adapter drops the rest
			Held:       isHeld,
		})
	}

	return connect.NewResponse(&apiv1.GetHeatmapResponse{
		Nodes:        nodes,
		QuoteAssetId: quoteAssetID,
		CalculatedAt: timestamppb.New(time.Now()),
		SourceReadAt: timestamppb.New(read.ranking.ReadAt),
		Source:       read.ranking.Source,
	}), nil
}

// validateMarketRequest refuses the metrics and grouping scope MARKET does not
// draw.
func validateMarketRequest(msg *apiv1.GetHeatmapRequest) error {
	if msg.ScopeId != "" {
		return connect.NewError(connect.CodeInvalidArgument, errors.New("scope_id must be empty for scope MARKET"))
	}
	switch msg.SizeMetric {
	case apiv1.HeatmapSizeMetric_HEATMAP_SIZE_METRIC_UNSPECIFIED,
		apiv1.HeatmapSizeMetric_HEATMAP_SIZE_METRIC_MARKET_CAP,
		apiv1.HeatmapSizeMetric_HEATMAP_SIZE_METRIC_VOLUME:
	default:
		return connect.NewError(connect.CodeInvalidArgument,
			fmt.Errorf("size metric %s does not apply to scope %s", msg.SizeMetric, msg.Scope))
	}
	switch msg.ColorMetric {
	case apiv1.HeatmapColorMetric_HEATMAP_COLOR_METRIC_UNSPECIFIED, apiv1.HeatmapColorMetric_HEATMAP_COLOR_METRIC_CHANGE_PCT:
	default:
		return connect.NewError(connect.CodeUnimplemented,
			fmt.Errorf("color metric %s is not implemented yet", msg.ColorMetric))
	}
	if msg.GroupBy != apiv1.HeatmapGroupBy_HEATMAP_GROUP_BY_UNSPECIFIED {
		return connect.NewError(connect.CodeUnimplemented,
			fmt.Errorf("grouping by %s is not implemented for scope MARKET", msg.GroupBy))
	}
	return nil
}

// sizeOf is a coin's tile size under a metric validateMarketRequest accepted.
func sizeOf(coin entity.RankedCoin, metric apiv1.HeatmapSizeMetric) float64 {
	if metric == apiv1.HeatmapSizeMetric_HEATMAP_SIZE_METRIC_VOLUME {
		return coin.Volume24h
	}
	return coin.MarketCap
}

// readRanking serves the cached ranking, reading it when the cache is empty or
// older than marketTTL. Rankers are resolved first, so a warm cache serves only
// a caller who could have read it. The read runs under the lock, so a cold burst
// costs one read. Keys are tried in order and only a refused key moves on;
// failures are not cached, so the map recovers with the source.
func (h *Handler) readRanking(ctx context.Context, userID string) (*cachedRanking, error) {
	rankers, err := h.rankings.MarketRankersFor(ctx, userID)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("resolve market rankers: %w", err))
	}
	if len(rankers) == 0 {
		return nil, connect.NewError(connect.CodeFailedPrecondition,
			errors.New("no market data provider can rank the market"))
	}

	cache := h.market
	cache.mu.Lock()
	defer cache.mu.Unlock()
	if last := cache.last; last != nil && cache.now().Sub(last.ranking.ReadAt) < marketTTL {
		return last, nil
	}

	var errs []error
	for _, ranker := range rankers {
		ranking, err := ranker.TopByMarketCap(ctx, marketTopN)
		if err == nil {
			if ranking.ReadAt.IsZero() {
				ranking.ReadAt = cache.now() // undated would never count as fresh
			}
			cache.last = &cachedRanking{ranking: ranking, ranker: ranker}
			return cache.last, nil
		}
		errs = append(errs, err)
		if !spend.KeyRefused(err) {
			break
		}
	}
	err = errors.Join(errs...)
	h.log.WarnContext(ctx, "heatmap: market ranking unavailable", "error", err)
	return nil, connect.NewError(connect.CodeUnavailable, fmt.Errorf("read market ranking: %w", err))
}

// heldCoins maps each ranked coin the caller holds to the asset that links to
// its card. It reads the same holdings scope BALANCE draws, and recognises an
// asset only under the identity the ranker prices it by, so a contract row
// carrying a famous ticker never marks the real coin. When two catalogue rows
// are the same coin the lowest id wins, so the link is stable between draws.
func (h *Handler) heldCoins(ctx context.Context, ranker entity.MarketRanker) (map[string]string, error) {
	holdings, err := h.holdings(ctx, &apiv1.ListHoldingsRequest{OmitExcludedFromTotals: true})
	if err != nil {
		return nil, err
	}
	var ids []string
	listed := map[string]bool{}
	for _, hld := range holdings {
		if !listed[hld.AssetID] {
			listed[hld.AssetID] = true
			ids = append(ids, hld.AssetID)
		}
	}
	held := map[string]string{}
	err = h.eachAsset(ctx, ids, func(a *apiv1.Asset) {
		coinID, ok := ranker.RankedCoinID(&entity.Asset{Symbol: a.GetSymbol(), Market: a.GetMarket()})
		if !ok {
			return
		}
		if prev, found := held[coinID]; !found || a.GetId() < prev {
			held[coinID] = a.GetId()
		}
	})
	if err != nil {
		return nil, err
	}
	return held, nil
}

// windowChange picks the change the request's window asks for.
func windowChange(c entity.RankedCoin, w apiv1.HeatmapWindow) *float64 {
	switch w {
	case apiv1.HeatmapWindow_HEATMAP_WINDOW_7D:
		return c.Change7d
	case apiv1.HeatmapWindow_HEATMAP_WINDOW_30D:
		return c.Change30d
	default:
		return c.Change24h
	}
}
