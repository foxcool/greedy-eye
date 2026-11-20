package analytics

import (
	"context"
	"errors"
	"fmt"
	"math"
	"slices"
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
	// are 24h/7d/30d changes, which half an hour does not move visibly, and it
	// caps the map's spend at 48 source calls a day whatever the traffic.
	marketTTL = 30 * time.Minute
	// marketQuote is the currency the ranking is read in; see marketHeatmap.
	marketQuote = "USD"
)

// MarketRankingSource resolves the rankers a caller may read, in the order to
// try them. Satisfied by the credentials resolver.
type MarketRankingSource interface {
	MarketRankersFor(ctx context.Context, userID string) ([]entity.MarketRanker, error)
}

// marketCache is a read-through cache of one ranking read. In memory only, for
// the reason the macro series cache gives: a restart costs one read, and a
// table would put a universe nobody holds next to the catalogue.
type marketCache struct {
	mu      sync.Mutex
	ranking entity.MarketRanking
	ranker  entity.MarketRanker // the one that read it, for identity matching
	now     func() time.Time
}

// WithMarketRankingSource returns a new Handler that serves scope MARKET from
// the rankings src resolves. Without it the scope stays Unimplemented.
func (h *Handler) WithMarketRankingSource(src MarketRankingSource) *Handler {
	copied := *h
	copied.rankings = src
	return &copied
}

// marketHeatmap draws the top of the crypto market by capitalisation rank:
// size is capitalisation or 24h volume, color the change over the window. The
// coins come from a cached ranking, never from the catalogue, and nothing here
// writes one: a top-N import would put coins nobody holds into the rows
// valuation and scam scoring iterate.
//
// Quoted in USD only. The source reads in one currency per request, and a
// cache entry per display currency would multiply the spend by the currencies
// in use; a caller naming another quote asset is told so rather than given USD
// under its name.
func (h *Handler) marketHeatmap(ctx context.Context, msg *apiv1.GetHeatmapRequest) (*connect.Response[apiv1.GetHeatmapResponse], error) {
	if h.rankings == nil {
		return nil, connect.NewError(connect.CodeUnimplemented, errors.New("market ranking source not configured"))
	}
	if msg.ScopeId != "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("scope_id must be empty for scope MARKET"))
	}
	byVolume := false
	switch msg.SizeMetric {
	case apiv1.HeatmapSizeMetric_HEATMAP_SIZE_METRIC_UNSPECIFIED, apiv1.HeatmapSizeMetric_HEATMAP_SIZE_METRIC_MARKET_CAP:
	case apiv1.HeatmapSizeMetric_HEATMAP_SIZE_METRIC_VOLUME:
		byVolume = true
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
	if msg.GroupBy != apiv1.HeatmapGroupBy_HEATMAP_GROUP_BY_UNSPECIFIED {
		return nil, connect.NewError(connect.CodeUnimplemented,
			fmt.Errorf("grouping by %s is not implemented for scope MARKET", msg.GroupBy))
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
		if asked != quoteAssetID {
			return nil, connect.NewError(connect.CodeInvalidArgument,
				errors.New("scope MARKET is quoted in USD only"))
		}
	}

	ranking, ranker, err := h.readRanking(ctx, user.ID)
	if err != nil {
		return nil, err
	}
	held, err := h.heldCoins(ctx, ranker)
	if err != nil {
		return nil, err
	}

	nodes := make([]*apiv1.HeatmapNode, 0, len(ranking.Coins))
	for _, c := range ranking.Coins {
		if c.Rank > math.MaxInt32 {
			continue // not a rank any source means
		}
		size := c.MarketCap
		if byVolume {
			size = c.Volume24h
		}
		color := 0.0 // no history: flat, as on the holdings maps
		if ch := windowChange(c, msg.Window); ch != nil {
			color = *ch
		}
		label := c.Symbol
		if label == "" {
			label = c.Name
		}
		assetID, isHeld := held[c.CoinID]
		nodes = append(nodes, &apiv1.HeatmapNode{
			Id:         ranking.Source + ":" + c.CoinID,
			Label:      label,
			Size:       size,
			ColorValue: color,
			Price:      proto.Float64(c.Price),
			AssetId:    assetID,
			Rank:       proto.Int32(int32(c.Rank)), // #nosec G115 -- checked against MaxInt32 above
			Held:       isHeld,
		})
	}

	return connect.NewResponse(&apiv1.GetHeatmapResponse{
		Nodes:        nodes,
		QuoteAssetId: quoteAssetID,
		CalculatedAt: timestamppb.New(time.Now()),
		SourceReadAt: timestamppb.New(ranking.ReadAt),
		Source:       ranking.Source,
	}), nil
}

// readRanking serves the cached ranking, reading it from the source when the
// cache is empty or older than marketTTL.
//
// The read happens under the cache lock, so a burst of cold requests costs one
// source call rather than one each; the others wait for it. Only a success is
// cached: remembering a failure would keep the map dark after the cause is
// gone, and the rate-limited transport already bounds how hard an outage is
// retried.
//
// Keys are tried in order, moving on only when one is refused (plan spent, key
// rejected); an outage is the source's answer and spending another plan on it
// changes nothing.
func (h *Handler) readRanking(ctx context.Context, userID string) (entity.MarketRanking, entity.MarketRanker, error) {
	c := h.market
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.ranker != nil && c.now().Sub(c.ranking.ReadAt) < marketTTL {
		return c.ranking, c.ranker, nil
	}

	rankers, err := h.rankings.MarketRankersFor(ctx, userID)
	if err != nil {
		return entity.MarketRanking{}, nil, connect.NewError(connect.CodeInternal, fmt.Errorf("resolve market rankers: %w", err))
	}
	if len(rankers) == 0 {
		return entity.MarketRanking{}, nil, connect.NewError(connect.CodeFailedPrecondition,
			errors.New("no market data provider can rank the market"))
	}
	var errs []error
	for _, r := range rankers {
		ranking, err := r.TopByMarketCap(ctx, marketTopN)
		if err == nil {
			if ranking.ReadAt.IsZero() {
				ranking.ReadAt = c.now() // undated would never count as fresh
			}
			c.ranking, c.ranker = ranking, r
			return ranking, r, nil
		}
		errs = append(errs, err)
		if !spend.KeyRefused(err) {
			break
		}
	}
	err = errors.Join(errs...)
	h.log.WarnContext(ctx, "heatmap: market ranking unavailable", "error", err)
	return entity.MarketRanking{}, nil, connect.NewError(connect.CodeUnavailable, fmt.Errorf("read market ranking: %w", err))
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
	seen := map[string]bool{}
	for _, hld := range holdings {
		if !seen[hld.AssetID] {
			seen[hld.AssetID] = true
			ids = append(ids, hld.AssetID)
		}
	}
	held := map[string]string{}
	for chunk := range slices.Chunk(ids, maxBatch) {
		req := &apiv1.ListAssetsRequest{Ids: chunk, PageSize: proto.Int32(int32(len(chunk)))} // #nosec G115 -- bounded by maxBatch
		for {
			resp, err := h.mdClient.ListAssets(ctx, connect.NewRequest(req))
			if err != nil {
				return nil, err
			}
			for _, a := range resp.Msg.GetAssets() {
				coinID, ok := ranker.RankedCoinID(&entity.Asset{Symbol: a.GetSymbol(), Market: a.GetMarket()})
				if !ok {
					continue
				}
				if cur, seen := held[coinID]; !seen || a.GetId() < cur {
					held[coinID] = a.GetId()
				}
			}
			next := resp.Msg.GetNextPageToken()
			if next == "" || len(resp.Msg.GetAssets()) == 0 {
				break
			}
			req.PageToken = &next
		}
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
