package marketdata

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"connectrpc.com/connect"
	apiv1 "github.com/foxcool/greedy-eye/api/v1"
	"github.com/foxcool/greedy-eye/api/v1/apiv1connect"
	"github.com/foxcool/greedy-eye/internal/entity"
	"github.com/foxcool/greedy-eye/internal/middleware"
	"github.com/foxcool/greedy-eye/internal/scamfilter"
	"github.com/foxcool/greedy-eye/internal/store"
	"google.golang.org/protobuf/types/known/emptypb"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// PriceProvider fetches prices from an external source.
type PriceProvider interface {
	// FetchPrices fetches current prices for the given assets.
	// Returned StoredPrice.BaseAssetID is intentionally empty — the handler resolves
	// the base asset UUID from BaseAssetSymbol() before persisting.
	FetchPrices(ctx context.Context, assets []*entity.Asset) ([]entity.StoredPrice, error)
	// BaseAssetSymbol returns the ticker of the quote currency (e.g. "USD", "USDT").
	// Used by FetchExternalPrices to resolve or create the base asset on demand.
	BaseAssetSymbol() string
	// BaseAssetType is the asset type to use when the base asset must be created.
	// Fiat quotes (USD) are forex; stablecoin quotes (USDT) are cryptocurrency.
	BaseAssetType() entity.AssetType
}

// RefDiscoverer is implemented by providers whose universe has identifiers of
// its own — a broker's FIGI, a data vendor's coin id — that the catalogue has
// no way to derive.
//
// Called before FetchPrices so the same sweep can already price by what it
// learns. The handler persists the bindings; an adapter has no store, and
// giving it one would put catalogue writes behind an HTTP client.
//
// Returning nothing is the correct answer for an ambiguous match. A binding is
// identity, it outlives the sweep that made it, and a wrong one prices a
// position as somebody else's paper until a human notices.
type RefDiscoverer interface {
	DiscoverRefs(ctx context.Context, assets []*entity.Asset) ([]entity.AssetExternalRef, error)
}

// HealthReportingProvider is implemented by providers that can tell whether
// their own credential is able to carry work right now.
//
// Asked on the PROVIDER rather than looked up by slug, because a plan is
// metered per credential: one slug can have several, of which one is spent and
// another fine, and nothing outside the provider knows which one it holds.
type HealthReportingProvider interface {
	// Unusable returns why this provider cannot be asked now, in a phrase fit
	// for a log line. False means go ahead.
	Unusable() (reason string, unusable bool)
}

// SelectiveProvider is implemented by providers that price only a subset of
// what they are handed, and can say which subset before being asked.
//
// It exists because a miss is evidence about an ASSET, not about a request. The
// sweep selects per source but cannot know what a source speaks for: MOEX is
// handed the whole due list and prices the Moscow-listed part of it, Binance the
// crypto part. Recording the remainder as misses files a provider's silence
// against assets it was never asked about, and the back-off doubles per miss up
// to a week — so on dev, moex carried 575 miss rows for crypto assets and cbr
// 533, driving assets those sources never cover to the ceiling and freezing the
// queue for the sources that DO cover them.
//
// Providers that do not implement it are asked about everything they are given,
// which is the old behaviour and correct for a source with no such subset.
type SelectiveProvider interface {
	// Asked returns the subset of assets this provider will actually request,
	// in the same order. Only these are recorded as attempts.
	Asked(assets []*entity.Asset) []*entity.Asset
}

// askedOf reports which of the selected assets a provider will actually ask
// about, so only those are credited with an attempt.
func askedOf(p PriceProvider, assets []*entity.Asset) []*entity.Asset {
	sp, ok := p.(SelectiveProvider)
	if !ok {
		return assets
	}
	return sp.Asked(assets)
}

// BudgetExemptProvider is implemented by providers whose request cost does not
// grow with the number of assets in some subset — CoinGecko covers up to 250
// curated coin ids in a single /coins/markets call. Those symbols bypass the
// per-sweep portion: refreshing them costs nothing extra, and they are the
// assets carrying most of the portfolio's value.
type BudgetExemptProvider interface {
	BudgetExemptSymbols() []string
}

// BudgetedProvider reports how many assets it may be asked for in one sweep,
// derived from what is left of its credential's plan for the period. The
// handler cannot compute this: it knows neither the credential nor how many
// assets fit in one request of this particular API.
//
// ok is false when the plan meters only rate, in which case the sweep is not
// capped by volume at all.
type BudgetedProvider interface {
	AssetBudget(now time.Time, window time.Duration) (n int, ok bool)
}

// ProviderSource resolves the effective price provider registry for a user
// from stored account credentials (see internal/service/credentials).
type ProviderSource interface {
	PriceProvidersFor(ctx context.Context, userID string) (map[string]PriceProvider, error)
}

// ContractResolver confirms a token's on-chain identity: given a chain and a
// contract address it reports the ticker the provider lists that contract
// under. A provider implements it opportunistically; FindOrCreateAsset uses it
// to decide whether an unknown contract may claim an existing ticker.
type ContractResolver interface {
	// ResolveContractSymbol returns the listed coin's symbol for the contract.
	// listed is false when the contract is not in the provider's universe (or
	// the chain is not covered); an error means the provider could not be
	// consulted and must never be read as "not listed".
	ResolveContractSymbol(ctx context.Context, chain, address string) (symbol string, listed bool, err error)
}

// Handler implements apiv1connect.MarketDataServiceHandler.
type Handler struct {
	apiv1connect.UnimplementedMarketDataServiceHandler
	store          Store
	providers      map[string]PriceProvider // keyed by source name e.g. "coingecko"
	providerSource ProviderSource           // optional; takes precedence over providers
	// refreshWindow is how long until the next unattended sweep. Zero disables
	// budgeting, which keeps tests and minimal configs on the old behaviour.
	refreshWindow time.Duration
	log           *slog.Logger
}

func NewHandler(store Store, log *slog.Logger) *Handler {
	return &Handler{store: store, log: log}
}

func (h *Handler) clone() *Handler {
	copied := *h
	return &copied
}

// WithProvider returns a new Handler with the named price provider added.
func (h *Handler) WithProvider(name string, p PriceProvider) *Handler {
	providers := make(map[string]PriceProvider, len(h.providers)+1)
	maps.Copy(providers, h.providers)
	providers[name] = p
	copied := h.clone()
	copied.providers = providers
	return copied
}

// WithProviderSource returns a new Handler resolving providers from stored
// account credentials; the static registry is the fallback.
func (h *Handler) WithProviderSource(src ProviderSource) *Handler {
	copied := h.clone()
	copied.providerSource = src
	return copied
}

// WithRefreshWindow enables budgeted sweeps: window is the time until the next
// one, which is what a provider divides its remaining plan allowance by. Zero
// (the default) leaves sweeps unbudgeted.
func (h *Handler) WithRefreshWindow(window time.Duration) *Handler {
	copied := h.clone()
	copied.refreshWindow = window
	return copied
}

// CreateAsset creates a new asset.
func (h *Handler) CreateAsset(ctx context.Context, req *connect.Request[apiv1.CreateAssetRequest]) (*connect.Response[apiv1.Asset], error) {
	if req.Msg.Asset == nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("asset is required"))
	}

	asset := assetFromProto(req.Msg.Asset)
	created, err := h.store.CreateAsset(ctx, asset)
	if err != nil {
		return nil, toConnectError(err)
	}

	return connect.NewResponse(assetToProto(created)), nil
}

// GetAsset retrieves an asset by ID.
func (h *Handler) GetAsset(ctx context.Context, req *connect.Request[apiv1.GetAssetRequest]) (*connect.Response[apiv1.Asset], error) {
	if req.Msg.Id == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("asset ID is required"))
	}

	asset, err := h.store.GetAsset(ctx, req.Msg.Id)
	if err != nil {
		return nil, toConnectError(err)
	}

	// The single-asset read is where the bindings belong: this is the call
	// behind an asset card, and a card that shows a verdict without showing
	// what the asset was bound through can state a suspicion it cannot explain.
	// Best-effort — a failure to load them must not cost the caller the asset.
	h.attachExternalRefs(ctx, []*entity.Asset{asset})
	h.attachRiskFlags(ctx, asset)

	return connect.NewResponse(assetToProto(asset)), nil
}

// UpdateAsset updates an asset.
func (h *Handler) UpdateAsset(ctx context.Context, req *connect.Request[apiv1.UpdateAssetRequest]) (*connect.Response[apiv1.Asset], error) {
	if req.Msg.Asset == nil || req.Msg.Asset.Id == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("asset with ID is required"))
	}

	var fields []string
	if req.Msg.UpdateMask != nil {
		fields = req.Msg.UpdateMask.Paths
	}

	asset := assetFromProto(req.Msg.Asset)
	updated, err := h.store.UpdateAsset(ctx, asset, fields)
	if err != nil {
		return nil, toConnectError(err)
	}

	return connect.NewResponse(assetToProto(updated)), nil
}

// DeleteAsset deletes an asset by ID.
func (h *Handler) DeleteAsset(ctx context.Context, req *connect.Request[apiv1.DeleteAssetRequest]) (*connect.Response[emptypb.Empty], error) {
	if req.Msg.Id == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("asset ID is required"))
	}

	if err := h.store.DeleteAsset(ctx, req.Msg.Id); err != nil {
		return nil, toConnectError(err)
	}

	return connect.NewResponse(&emptypb.Empty{}), nil
}

// ListAssets lists assets with pagination and optional tag filtering.
func (h *Handler) ListAssets(ctx context.Context, req *connect.Request[apiv1.ListAssetsRequest]) (*connect.Response[apiv1.ListAssetsResponse], error) {
	opts := ListAssetsOpts{
		Tags: req.Msg.Tags,
	}
	if req.Msg.PageSize != nil {
		opts.PageSize = int(*req.Msg.PageSize)
	}
	if req.Msg.PageToken != nil {
		opts.PageToken = *req.Msg.PageToken
	}
	if req.Msg.IdentityVerdict != nil {
		opts.IdentityVerdict = *req.Msg.IdentityVerdict
	}

	assets, nextPageToken, err := h.store.ListAssets(ctx, opts)
	if err != nil {
		return nil, toConnectError(err)
	}

	protoAssets := make([]*apiv1.Asset, 0, len(assets))
	for _, a := range assets {
		protoAssets = append(protoAssets, assetToProto(a))
	}

	return connect.NewResponse(&apiv1.ListAssetsResponse{
		Assets:        protoAssets,
		NextPageToken: nextPageToken,
	}), nil
}

// CreatePrice creates a new price record.
func (h *Handler) CreatePrice(ctx context.Context, req *connect.Request[apiv1.CreatePriceRequest]) (*connect.Response[apiv1.Price], error) {
	if req.Msg.Price == nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("price is required"))
	}

	price, err := priceFromProto(req.Msg.Price)
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	created, err := h.store.CreatePrice(ctx, price)
	if err != nil {
		return nil, toConnectError(err)
	}

	return connect.NewResponse(priceToProto(created)), nil
}

// CreatePrices creates multiple price records in bulk.
func (h *Handler) CreatePrices(ctx context.Context, req *connect.Request[apiv1.CreatePricesRequest]) (*connect.Response[apiv1.CreatePricesResponse], error) {
	prices := make([]*entity.StoredPrice, 0, len(req.Msg.Prices))
	for _, p := range req.Msg.Prices {
		price, err := priceFromProto(p)
		if err != nil {
			return nil, connect.NewError(connect.CodeInvalidArgument, err)
		}
		prices = append(prices, price)
	}

	count, err := h.store.CreatePrices(ctx, prices)
	if err != nil {
		return nil, toConnectError(err)
	}

	return connect.NewResponse(&apiv1.CreatePricesResponse{
		CreatedCount: int32(count), // #nosec G115 -- count of rows created, bounded by request size
	}), nil
}

// GetLatestPrice returns the most recent price for an asset. When base_asset_id is
// provided it returns the price in that specific pair; when omitted it returns the
// latest price in whatever base the asset actually trades against (the response's
// base_asset_id tells the caller which). This lets callers value an asset without
// knowing its quote currency in advance.
func (h *Handler) GetLatestPrice(ctx context.Context, req *connect.Request[apiv1.GetLatestPriceRequest]) (*connect.Response[apiv1.Price], error) {
	if req.Msg.AssetId == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("asset_id is required"))
	}

	assetID, err := h.resolveAssetID(ctx, req.Msg.AssetId)
	if err != nil {
		return nil, toConnectError(err)
	}

	var sourceID string
	if req.Msg.SourceId != nil {
		sourceID = *req.Msg.SourceId
	}

	// base_asset_id is optional: empty means "any base", letting the store return the
	// asset's latest price in whatever pair it trades against.
	var baseAssetID string
	if req.Msg.BaseAssetId != "" {
		baseAssetID, err = h.resolveAssetID(ctx, req.Msg.BaseAssetId)
		if err != nil {
			return nil, toConnectError(err)
		}
	}

	price, err := h.store.GetLatestPrice(ctx, assetID, baseAssetID, sourceID)
	if err != nil {
		return nil, toConnectError(err)
	}

	return connect.NewResponse(priceToProto(price)), nil
}

// resolveAssetID returns id unchanged if it is a valid UUID, otherwise treats it
// as a symbol and looks up the asset by symbol. This allows callers to pass either
// a UUID or a well-known ticker (e.g. "USD", "usd") interchangeably.
func (h *Handler) resolveAssetID(ctx context.Context, id string) (string, error) {
	if _, err := uuid.Parse(id); err == nil {
		return id, nil
	}
	asset, err := h.store.GetAssetBySymbol(ctx, id) // store normalizes symbol case
	if err != nil {
		return "", fmt.Errorf("resolve asset %q: %w", id, err)
	}
	return asset.ID, nil
}

// ListPriceHistory returns price history for an asset pair.
func (h *Handler) ListPriceHistory(ctx context.Context, req *connect.Request[apiv1.ListPriceHistoryRequest]) (*connect.Response[apiv1.ListPriceHistoryResponse], error) {
	if req.Msg.AssetId == "" || req.Msg.BaseAssetId == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("asset_id and base_asset_id are required"))
	}

	baseAssetID, err := h.resolveAssetID(ctx, req.Msg.BaseAssetId)
	if err != nil {
		return nil, toConnectError(err)
	}

	opts := ListPriceHistoryOpts{
		AssetID:     req.Msg.AssetId,
		BaseAssetID: baseAssetID,
	}
	if req.Msg.SourceId != nil {
		opts.SourceID = *req.Msg.SourceId
	}
	if req.Msg.From != nil {
		t := req.Msg.From.AsTime()
		opts.From = &t
	}
	if req.Msg.To != nil {
		t := req.Msg.To.AsTime()
		opts.To = &t
	}
	if req.Msg.PageSize != nil {
		opts.PageSize = int(*req.Msg.PageSize)
	}
	if req.Msg.PageToken != nil {
		opts.PageToken = *req.Msg.PageToken
	}

	prices, nextPageToken, err := h.store.ListPriceHistory(ctx, opts)
	if err != nil {
		return nil, toConnectError(err)
	}

	protoPrices := make([]*apiv1.Price, 0, len(prices))
	for _, p := range prices {
		protoPrices = append(protoPrices, priceToProto(p))
	}

	return connect.NewResponse(&apiv1.ListPriceHistoryResponse{
		Prices:        protoPrices,
		NextPageToken: nextPageToken,
	}), nil
}

// ListPricesByInterval aggregates prices by interval (stub for MVP).
func (h *Handler) ListPricesByInterval(ctx context.Context, req *connect.Request[apiv1.ListPricesByIntervalRequest]) (*connect.Response[apiv1.ListPriceHistoryResponse], error) {
	// MVP: fallback to ListPriceHistory
	return h.ListPriceHistory(ctx, connect.NewRequest(&apiv1.ListPriceHistoryRequest{
		AssetId:     req.Msg.AssetId,
		BaseAssetId: req.Msg.BaseAssetId,
		From:        req.Msg.From,
		To:          req.Msg.To,
		SourceId:    req.Msg.SourceId,
		PageSize:    req.Msg.PageSize,
		PageToken:   req.Msg.PageToken,
	}))
}

// DeletePrice deletes a price record by ID.
func (h *Handler) DeletePrice(ctx context.Context, req *connect.Request[apiv1.DeletePriceRequest]) (*connect.Response[emptypb.Empty], error) {
	if req.Msg.Id == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("price ID is required"))
	}

	if err := h.store.DeletePrice(ctx, req.Msg.Id); err != nil {
		return nil, toConnectError(err)
	}

	return connect.NewResponse(&emptypb.Empty{}), nil
}

// DeletePrices deletes price records by criteria.
func (h *Handler) DeletePrices(ctx context.Context, req *connect.Request[apiv1.DeletePricesRequest]) (*connect.Response[emptypb.Empty], error) {
	opts := DeletePricesOpts{}
	if req.Msg.AssetId != nil {
		opts.AssetID = *req.Msg.AssetId
	}
	if req.Msg.BaseAssetId != nil {
		opts.BaseAssetID = *req.Msg.BaseAssetId
	}
	if req.Msg.SourceId != nil {
		opts.SourceID = *req.Msg.SourceId
	}
	if req.Msg.From != nil {
		t := req.Msg.From.AsTime()
		opts.From = &t
	}
	if req.Msg.To != nil {
		t := req.Msg.To.AsTime()
		opts.To = &t
	}

	if err := h.store.DeletePrices(ctx, opts); err != nil {
		return nil, toConnectError(err)
	}

	return connect.NewResponse(&emptypb.Empty{}), nil
}

// EnrichAssetData enriches asset with external data (stub).
func (h *Handler) EnrichAssetData(ctx context.Context, req *connect.Request[apiv1.EnrichAssetDataRequest]) (*connect.Response[apiv1.Asset], error) {
	// TODO: Implement with external data sources
	return nil, connect.NewError(connect.CodeUnimplemented, errors.New("EnrichAssetData not implemented"))
}

// FindSimilarAssets finds similar assets (stub).
func (h *Handler) FindSimilarAssets(ctx context.Context, req *connect.Request[apiv1.FindSimilarAssetsRequest]) (*connect.Response[apiv1.ListAssetsResponse], error) {
	// TODO: Implement similarity search
	return nil, connect.NewError(connect.CodeUnimplemented, errors.New("FindSimilarAssets not implemented"))
}

// FindOrCreateAsset resolves an asset by its composite identity (symbol,
// market, type), creating it only when nothing matches and dry_run is false.
// Find-first keeps batch imports from polluting the catalog with duplicates.
func (h *Handler) FindOrCreateAsset(ctx context.Context, req *connect.Request[apiv1.FindOrCreateAssetRequest]) (*connect.Response[apiv1.FindOrCreateAssetResponse], error) {
	symbol := entity.NormalizeSymbol(req.Msg.Symbol)

	typ := entity.AssetType(req.Msg.Type)
	if typ == entity.AssetTypeUnspecified {
		typ = entity.AssetTypeCryptocurrency
	}

	market := ""
	if req.Msg.Market != nil {
		market = entity.NormalizeMarket(*req.Msg.Market)
	}
	if market == "" {
		market = entity.DefaultMarket(typ)
	}
	if market == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("market is required for this asset type"))
	}

	refSource := strings.TrimSpace(req.Msg.GetExternalRefSource())
	ref := strings.TrimSpace(req.Msg.GetExternalRef())
	hasRef := refSource != "" && ref != ""

	// Resolve by external ref first: a token's contract is its identity, so a
	// bound contract wins over symbol matching and a scam clone of a real ticker
	// stays its own asset instead of resolving to the real one.
	if hasRef {
		if assetID, ferr := h.store.FindAssetIDByExternalRef(ctx, refSource, ref); ferr == nil {
			if a, gerr := h.store.GetAsset(ctx, assetID); gerr == nil {
				return h.respondScored(ctx, a, req.Msg, false), nil
			}
			// A ref pointing at a missing asset should not happen (FK CASCADE);
			// fall through to identity resolution rather than fail the sync.
		} else if !errors.Is(ferr, store.ErrNotFound) {
			return nil, toConnectError(ferr)
		}
	}

	// Past the ref lookup, identity has nothing left to match on but the symbol.
	// The check sits here rather than at the top because a contract that the
	// catalogue already knows IS an identity: a provider that reports a balance
	// with an empty symbol still names the token by address, and refusing it made
	// the caller lose a position it could resolve. Sync pays for that twice — the
	// balance is dropped AND the account's snapshot counts as incomplete, which
	// keeps it from removing positions that really are gone.
	if symbol == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument,
			errors.New("symbol is required unless the external ref resolves a known asset"))
	}

	// An unbound contract may only claim the global market once its identity is
	// confirmed; otherwise it gets a market of its own (see marketForContract).
	if hasRef {
		resolved, merr := h.marketForContract(ctx, market, symbol, refSource, ref)
		if merr != nil {
			return nil, connect.NewError(connect.CodeUnavailable, merr)
		}
		market = resolved
	}

	found, err := h.store.FindAssetByIdentity(ctx, symbol, market, typ)
	if err == nil {
		// Bind the contract to the resolved asset so the next sync short-circuits
		// on the ref (and cross-chain contracts of the same symbol collapse onto
		// one asset).
		if hasRef {
			h.bindExternalRef(ctx, found.ID, refSource, ref)
		}
		return h.respondScored(ctx, found, req.Msg, false), nil
	}
	if !errors.Is(err, store.ErrNotFound) {
		return nil, toConnectError(err)
	}

	if req.Msg.DryRun {
		// Would create: report the plan without an asset payload.
		return connect.NewResponse(&apiv1.FindOrCreateAssetResponse{Created: true}), nil
	}

	name := symbol
	if req.Msg.Name != nil && strings.TrimSpace(*req.Msg.Name) != "" {
		name = strings.TrimSpace(*req.Msg.Name)
	}
	// Mirror the contract as a tag on an on-chain create so the price providers
	// that look tokens up by address (coingecko) keep resolving; the external ref
	// is the identity, the tag is the pricing hint.
	tags := []string{}
	if hasRef && strings.HasPrefix(refSource, "onchain:") {
		tags = append(tags, "contract:"+ref)
	}
	created, err := h.store.CreateAsset(ctx, &entity.Asset{
		Symbol: symbol,
		Name:   name,
		Type:   typ,
		Market: market,
		Tags:   tags,
	})
	if err != nil {
		if errors.Is(err, store.ErrConstraint) {
			// Concurrent insert won the race — read back the existing row.
			if existing, ferr := h.store.FindAssetByIdentity(ctx, symbol, market, typ); ferr == nil {
				if hasRef {
					h.bindExternalRef(ctx, existing.ID, refSource, ref)
				}
				return h.respondScored(ctx, existing, req.Msg, false), nil
			}
		}
		return nil, toConnectError(err)
	}

	if hasRef {
		h.bindExternalRef(ctx, created.ID, refSource, ref)
	}
	return h.respondScored(ctx, created, req.Msg, true), nil
}

// respondScored scores the asset's identity, persists the verdict (never over a
// user verdict) and returns the response with the effective verdict stamped on
// the asset so the sync path can derive holdings.excluded from it.
func (h *Handler) respondScored(ctx context.Context, a *entity.Asset, req *apiv1.FindOrCreateAssetRequest, created bool) *connect.Response[apiv1.FindOrCreateAssetResponse] {
	verdict := h.scoreAndPersistVerdict(ctx, a, req)
	p := assetToProto(a)
	if verdict != "" {
		p.IdentityVerdict = &verdict
	}
	return connect.NewResponse(&apiv1.FindOrCreateAssetResponse{Asset: p, Created: created})
}

// scoreAndPersistVerdict scores an asset with scamfilter and stores the verdict
// under the "heuristic" source, which the store guards against overwriting a
// user verdict. It returns the effective verdict: the freshly scored one when
// written, or the asset's existing (possibly user) verdict when the write was
// skipped or failed — so the caller never acts on a verdict the store rejected.
func (h *Handler) scoreAndPersistVerdict(ctx context.Context, a *entity.Asset, req *apiv1.FindOrCreateAssetRequest) string {
	in := scamfilter.Input{
		Symbol:           a.Symbol,
		Name:             a.Name,
		ProviderSpam:     req.ProviderSpam,
		ContractVerified: req.ContractVerified,
		ClaimsHeldTicker: h.claimsHeldTicker(ctx, a.ID),
	}
	res := scamfilter.Score(in, scamfilter.DefaultWeights())
	score := res.Score
	written, err := h.store.SetAssetVerdict(ctx, a.ID, string(res.Verdict), &score, res.Signals, rescoreVerdictSource)
	if err != nil {
		if h.log != nil {
			h.log.Warn("persist identity verdict failed", "asset_id", a.ID, "error", err)
		}
		return a.IdentityVerdict
	}
	if !written {
		// A user verdict is terminal; report what actually stands.
		return a.IdentityVerdict
	}
	return string(res.Verdict)
}

// claimsHeldTicker asks the catalogue whether this asset's ticker is already
// held on one of its own chains by an older listed asset with a different
// contract. A lookup error answers false and logs: the signal is terminal, and
// condemning an asset because a query failed would be a worse failure than
// missing one impostor until the next rescore.
func (h *Handler) claimsHeldTicker(ctx context.Context, assetID string) bool {
	if assetID == "" {
		return false
	}
	incumbent, err := h.store.FindTickerIncumbent(ctx, assetID)
	if err != nil {
		if !errors.Is(err, store.ErrNotFound) && h.log != nil {
			h.log.Warn("ticker incumbent lookup failed", "asset_id", assetID, "error", err)
		}
		return false
	}
	if h.log != nil {
		h.log.Warn("ticker already held on this chain by another contract",
			"asset_id", assetID, "incumbent_asset_id", incumbent)
	}
	return true
}

// settableVerdicts are the identity verdicts a human may assign. "unknown" is
// the never-scored default and is not a judgement, so it cannot be set.
var settableVerdicts = map[string]bool{
	string(scamfilter.VerdictLegit):         true,
	string(scamfilter.VerdictSuspect):       true,
	string(scamfilter.VerdictScam):          true,
	string(scamfilter.VerdictImpersonation): true,
}

// SetAssetVerdict records a human identity verdict, provenanced to the user so
// the automated scorer never overwrites it. Admin-only until per-asset RBAC
// lands (personal-rme).
func (h *Handler) SetAssetVerdict(ctx context.Context, req *connect.Request[apiv1.SetAssetVerdictRequest]) (*connect.Response[apiv1.Asset], error) {
	user, ok := middleware.UserFromContext(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}
	if !user.IsAdmin() {
		return nil, connect.NewError(connect.CodePermissionDenied, errors.New("setting an asset verdict is admin-only"))
	}
	if req.Msg.AssetId == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("asset_id is required"))
	}
	verdict := req.Msg.Verdict
	if !settableVerdicts[verdict] {
		return nil, connect.NewError(connect.CodeInvalidArgument,
			fmt.Errorf("verdict must be one of legit, suspect, scam, impersonation; got %q", verdict))
	}

	// A user verdict carries no automated score/signals and is terminal.
	if _, err := h.store.SetAssetVerdict(ctx, req.Msg.AssetId, verdict, nil, nil, "user:"+user.ID); err != nil {
		return nil, toConnectError(err)
	}
	updated, err := h.store.GetAsset(ctx, req.Msg.AssetId)
	if err != nil {
		return nil, toConnectError(err)
	}
	return connect.NewResponse(assetToProto(updated)), nil
}

// riskFlagKinds are the situational risks that can be recorded on axis 2. The
// list is closed on purpose: a free-text kind cannot be filtered, counted or
// acted on, and the axis is only worth having if the flags are comparable.
var riskFlagKinds = map[string]bool{
	"exploit":          true,
	"depeg":            true,
	"frozen_transfers": true,
	"deprecation":      true,
	"delisting":        true,
	"sanctions_freeze": true,
}

// riskActionHints are the action directions a flag may carry (axis 3).
var riskActionHints = map[string]bool{
	"none":      true,
	"hold":      true,
	"exit_soon": true,
}

// AddAssetRiskFlag records a situational risk on an asset: an exploit, a depeg,
// frozen transfers, a deprecation, a delisting, a sanctions freeze.
//
// The flag does NOT touch the sum. It does not derive holdings.excluded and it
// does not enter ValuationCoverage: the asset is real and so is its value, and
// the only thing in question is what happens to it next. An identity verdict
// makes the opposite claim — that the asset is not what it says — which is why
// that one removes money from the total and this one must never do so.
//
// Admin-only, like a verdict: a flag is catalogue-wide and every user sees it.
func (h *Handler) AddAssetRiskFlag(ctx context.Context, req *connect.Request[apiv1.AddAssetRiskFlagRequest]) (*connect.Response[apiv1.AssetRiskFlag], error) {
	user, ok := middleware.UserFromContext(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}
	if !user.IsAdmin() {
		return nil, connect.NewError(connect.CodePermissionDenied,
			errors.New("adding an asset risk flag is admin-only"))
	}
	if req.Msg.AssetId == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("asset_id is required"))
	}
	if !riskFlagKinds[req.Msg.Kind] {
		return nil, connect.NewError(connect.CodeInvalidArgument,
			fmt.Errorf("kind must be one of exploit, depeg, frozen_transfers, deprecation, delisting, sanctions_freeze; got %q", req.Msg.Kind))
	}
	hint := req.Msg.GetActionHint()
	if hint == "" {
		hint = "none"
	}
	if !riskActionHints[hint] {
		return nil, connect.NewError(connect.CodeInvalidArgument,
			fmt.Errorf("action_hint must be one of none, hold, exit_soon; got %q", hint))
	}
	// A flag with no review date outlives the situation it describes and turns
	// the axis into background noise, so there is no way to write one.
	if req.Msg.ReviewAt == nil {
		return nil, connect.NewError(connect.CodeInvalidArgument,
			errors.New("review_at is required: a flag with no review date never expires"))
	}
	reviewAt := req.Msg.ReviewAt.AsTime()

	flag, err := h.store.CreateAssetRiskFlag(ctx, &entity.AssetRiskFlag{
		AssetID:    req.Msg.AssetId,
		Kind:       req.Msg.Kind,
		Note:       req.Msg.GetNote(),
		ActionHint: hint,
		ReviewAt:   &reviewAt,
		SetBy:      "user:" + user.ID,
	})
	if err != nil {
		return nil, toConnectError(err)
	}
	return connect.NewResponse(riskFlagToProto(flag)), nil
}

// DeleteAssetRiskFlag removes one flag. Admin-only, and naming the asset as
// well as the flag so a mistyped id cannot reach an unrelated asset's row.
//
// Removal is ordinary here, unlike removing an external ref: review_at exists
// precisely so flags are revisited and retired.
func (h *Handler) DeleteAssetRiskFlag(ctx context.Context, req *connect.Request[apiv1.DeleteAssetRiskFlagRequest]) (*connect.Response[emptypb.Empty], error) {
	user, ok := middleware.UserFromContext(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}
	if !user.IsAdmin() {
		return nil, connect.NewError(connect.CodePermissionDenied,
			errors.New("removing an asset risk flag is admin-only"))
	}
	if req.Msg.AssetId == "" || req.Msg.Id == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("asset_id and id are required"))
	}
	if err := h.store.DeleteAssetRiskFlag(ctx, req.Msg.AssetId, req.Msg.Id); err != nil {
		return nil, toConnectError(err)
	}
	return connect.NewResponse(&emptypb.Empty{}), nil
}

// discoverRefs lets a provider bind assets to identifiers in its own namespace
// and persists what it finds, so the binding outlives the sweep.
//
// Best-effort, and deliberately so: a binding that cannot be written costs this
// sweep a price, not the whole fetch. The refs are also attached in memory,
// because the provider is about to be asked for prices and re-reading them from
// the store would be a round trip to learn what we just wrote.
//
// A duplicate is not an error. CreateAssetExternalRef refuses to overwrite an
// existing (source, ref) — identity is stable once bound — so re-discovering
// the same instrument every sweep is the expected steady state.
func (h *Handler) discoverRefs(ctx context.Context, name string, p PriceProvider, assets []*entity.Asset) {
	d, ok := p.(RefDiscoverer)
	if !ok {
		return
	}
	refs, err := d.DiscoverRefs(ctx, assets)
	if err != nil && h.log != nil {
		// Partial results still travel: the error names one asset's lookup, not
		// the whole batch.
		h.log.Warn("provider ref discovery failed", "source", name, "error", err)
	}
	if len(refs) == 0 {
		return
	}

	byID := make(map[string]*entity.Asset, len(assets))
	for _, a := range assets {
		byID[a.ID] = a
	}
	for i := range refs {
		created, err := h.store.CreateAssetExternalRef(ctx, &refs[i])
		if err != nil {
			if !errors.Is(err, store.ErrConstraint) && h.log != nil {
				h.log.Warn("persist discovered ref failed", "source", name,
					"asset_id", refs[i].AssetID, "error", err)
			}
			continue
		}
		if a, ok := byID[created.AssetID]; ok {
			a.ExternalRefs = append(a.ExternalRefs, *created)
		}
	}
}

// attachRiskFlags loads an asset's axis-2 flags for the single-asset read.
//
// Best-effort, like the bindings next to it: a card that cannot list flags is
// worse without the asset than with an incomplete card. The cost of the
// omission is bounded because the flag never enters a number — nothing is
// miscounted when this fails, only unsaid.
func (h *Handler) attachRiskFlags(ctx context.Context, asset *entity.Asset) {
	flags, err := h.store.ListAssetRiskFlags(ctx, asset.ID)
	if err != nil {
		if h.log != nil {
			h.log.Warn("load asset risk flags failed", "asset_id", asset.ID, "error", err)
		}
		return
	}
	for _, f := range flags {
		asset.RiskFlags = append(asset.RiskFlags, *f)
	}
}

// DeleteAssetExternalRef removes one binding between an asset and an external
// identifier.
//
// Admin-only, like setting a verdict: a binding is catalogue identity and is
// shared by every user, so undoing one is not a personal preference.
//
// What happens next is deliberately left to the sync rather than done here. Once
// the ref is gone, FindOrCreateAsset stops short-circuiting on it, marketForContract
// judges the contract on its own merits, and a contract that cannot confirm the
// ticker it claims gets a market of its own. The old holding then falls out of the
// account snapshot and is zeroed by the ordinary path. Reassigning holdings from
// here would mean this RPC deciding what the balance IS, which is exactly the
// judgement that produced the wrong binding in the first place.
func (h *Handler) DeleteAssetExternalRef(ctx context.Context, req *connect.Request[apiv1.DeleteAssetExternalRefRequest]) (*connect.Response[emptypb.Empty], error) {
	user, ok := middleware.UserFromContext(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}
	if !user.IsAdmin() {
		return nil, connect.NewError(connect.CodePermissionDenied,
			errors.New("removing an asset external ref is admin-only"))
	}
	if req.Msg.AssetId == "" || req.Msg.Id == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument,
			errors.New("asset_id and id are both required"))
	}

	if err := h.store.DeleteAssetExternalRef(ctx, req.Msg.AssetId, req.Msg.Id); err != nil {
		return nil, toConnectError(err)
	}
	if h.log != nil {
		// An identity change with no trace is how the next investigation starts
		// from nothing.
		h.log.Info("asset external ref removed",
			"asset_id", req.Msg.AssetId, "ref_id", req.Msg.Id, "by", user.ID)
	}
	return connect.NewResponse(&emptypb.Empty{}), nil
}

// bindExternalRef maps a contract identity to an asset, best-effort: a conflict
// means the ref is already bound (identity is stable), which is not an error for
// the sync path. Other failures are logged, not surfaced — a missing mapping
// only costs the next sync a symbol lookup, it does not corrupt the holding.
func (h *Handler) bindExternalRef(ctx context.Context, assetID, source, ref string) {
	_, err := h.store.CreateAssetExternalRef(ctx, &entity.AssetExternalRef{
		AssetID: assetID,
		Source:  source,
		Ref:     ref,
		Origin:  entity.RefOriginAuto,
	})
	if err != nil && !errors.Is(err, store.ErrConstraint) && h.log != nil {
		h.log.Warn("bind external ref failed",
			"asset_id", assetID, "source", source, "ref", ref, "error", err)
	}
}

// marketForContract decides which market an unbound contract may be resolved
// in. The global crypto market is reserved for contracts a price provider
// actually lists under that ticker; anything else lands in a market of its own
// (entity.ContractMarket) so it can neither merge into the genuine asset nor be
// priced as it.
//
// This is the fix for personal-c3b: matching by ticker alone let the first
// appearance of a counterfeit bind its contract to the real asset, after which
// its balance was summed into the real position and valued at the real price.
// Name, ticker and amount are all copyable — the contract is the only thing
// that is not.
//
// Without a resolver (no market-data credential configured) nothing can be
// confirmed, so every unbound contract is isolated: a duplicate row is visible
// and mergeable, an inflated total is neither.
func (h *Handler) marketForContract(ctx context.Context, market, symbol, refSource, ref string) (string, error) {
	chain, ok := entity.ChainFromOnchainSource(refSource)
	if !ok {
		// Non-chain namespaces (broker/FIGI, provider coin IDs) are assigned by
		// an authority, not minted by whoever pays the gas.
		return market, nil
	}

	resolver, err := h.contractResolver(ctx)
	if err != nil {
		return "", err
	}
	if resolver == nil {
		return entity.ContractMarket(chain, ref), nil
	}

	listed, found, err := resolver.ResolveContractSymbol(ctx, chain, ref)
	if err != nil {
		// Fail loud: treating an unreachable provider as "not listed" would
		// scatter genuine multi-chain tokens into per-contract rows for good.
		return "", fmt.Errorf("confirm contract %s on %s: %w", ref, chain, err)
	}
	if found && entity.NormalizeSymbol(listed) == symbol {
		return market, nil
	}
	if found && h.log != nil {
		// The contract is listed but under another ticker: the balance claims a
		// symbol its own contract does not have.
		h.log.Warn("contract listed under a different symbol",
			"chain", chain, "contract", ref, "claimed", symbol, "listed", listed)
	}
	return entity.ContractMarket(chain, ref), nil
}

// contractResolver picks a price provider that can confirm contract identity,
// preferring CoinGecko as the one with a full-universe contract catalog. A nil
// resolver means no configured provider can confirm anything; an error means the
// registry itself could not be read, which is not evidence about the contract.
// It is consulted only for contracts that are not bound yet, so a settled
// catalog costs nothing per sync.
func (h *Handler) contractResolver(ctx context.Context) (ContractResolver, error) {
	providers := h.providers
	if h.providerSource != nil {
		userID := ""
		if user, ok := middleware.UserFromContext(ctx); ok {
			userID = user.ID
		}
		resolved, err := h.providerSource.PriceProvidersFor(ctx, userID)
		if err != nil {
			return nil, fmt.Errorf("resolve price providers: %w", err)
		}
		providers = resolved
	}

	if r, ok := providers[coingeckoProvider].(ContractResolver); ok {
		return r, nil
	}
	for _, name := range slices.Sorted(maps.Keys(providers)) {
		if r, ok := providers[name].(ContractResolver); ok {
			return r, nil
		}
	}
	return nil, nil
}

// coingeckoProvider is the provider slug whose contract catalog is preferred for
// identity confirmation. Named here to avoid importing the adapter package.
const coingeckoProvider = "coingecko"

// Freshness policy for unattended sweeps. These are product decisions, not
// operator knobs: the sweep's size already follows the plan's remaining
// allowance, and nobody has needed to tune these separately.
const (
	// priceTTL is how long a fetched price counts as current. An hour matches
	// the sweep cadence — fresher spends quota on prices nobody read, staler
	// defeats the point of sweeping at all.
	priceTTL = time.Hour
	// missBackoffCap bounds the exponential push-out for assets a provider
	// keeps not listing. A week means a newly listed token is picked up within
	// days, while the permanently unlistable tail costs one request a week
	// instead of one per sweep.
	missBackoffCap = 7 * 24 * time.Hour
)

// baseAssetKey identifies a quote currency the way the catalogue does: a ticker
// is not an identity on its own.
type baseAssetKey struct {
	symbol string
	typ    entity.AssetType
}

// quarantineVerdicts are excluded from unattended pricing: their holdings are
// already excluded from the portfolio sums, so a price for them buys nothing.
var quarantineVerdicts = []string{
	string(scamfilter.VerdictScam),
	string(scamfilter.VerdictImpersonation),
}

// FetchExternalPrices fetches prices from configured providers and stores them.
// If source_ids is specified in the request, only those providers are called.
//
// Naming asset_ids makes the request a deliberate reconciliation: those assets
// are priced whatever their freshness, and the per-sweep portion does not apply
// (the credential's plan quota still does — a ceiling is a ceiling). An empty
// request is an unattended sweep and takes only what is due, oldest first,
// within what the plan can afford until the next sweep.
func (h *Handler) FetchExternalPrices(ctx context.Context, req *connect.Request[apiv1.FetchExternalPricesRequest]) (*connect.Response[apiv1.FetchExternalPricesResponse], error) {
	providers, err := h.resolveProviders(ctx)
	if err != nil {
		return nil, err
	}

	// Reconciliation reads exactly the assets it names. Paging the catalogue and
	// filtering in Go is both wasteful and lossy — the discarded page token used
	// to cap the whole sweep at 500 assets.
	var named []*entity.Asset
	if len(req.Msg.AssetIds) > 0 {
		var err error
		named, _, err = h.store.ListAssets(ctx, ListAssetsOpts{
			IDs:      req.Msg.AssetIds,
			PageSize: len(req.Msg.AssetIds),
		})
		if err != nil {
			return nil, toConnectError(err)
		}
		if len(named) == 0 {
			return connect.NewResponse(&apiv1.FetchExternalPricesResponse{}), nil
		}
	}

	var allPrices []*entity.StoredPrice
	var fetchErrs []string
	var totalFetched int
	// Sources this run had nothing to ask, keyed by why. Reported so an
	// unattended sweep can say "postponed" rather than only "zero".
	idleSources := map[string]string{}

	// Cache base asset UUIDs to avoid repeated lookups across providers. Keyed by
	// symbol AND type: identity is composite, and two providers naming the same
	// ticker for different kinds of thing (a stablecoin USDT against a fiat USDT
	// row) do not mean the same asset.
	baseAssetCache := map[baseAssetKey]string{}

	for name, provider := range providers {
		if len(req.Msg.SourceIds) > 0 && !slices.Contains(req.Msg.SourceIds, name) {
			continue
		}

		// A provider that cannot answer is skipped before anything is selected
		// for it. Asking anyway costs a refusal per asset, and recordAttempts
		// files each one against the ASSET — so a token's own back-off would
		// grow because a plan it never heard of ran out.
		//
		// Only the reconciliation path (named assets, a person waiting) goes
		// through regardless: that is not background work, it is not sized by
		// the background share, and the request may well be somebody checking
		// whether the provider is back.
		if hp, ok := provider.(HealthReportingProvider); ok && named == nil {
			if reason, unusable := hp.Unusable(); unusable {
				h.log.Info("price sweep: provider skipped", "provider", name, "reason", reason)
				continue
			}
		}

		// Selection is per source because freshness is: an asset Binance priced
		// a minute ago is still stale for CoinGecko.
		assets := named
		outcome := outcomeSelected
		if assets == nil {
			var err error
			assets, outcome, err = h.refreshTargets(ctx, name, provider)
			if err != nil {
				fetchErrs = append(fetchErrs, fmt.Sprintf("%s: select targets: %v", name, err))
				continue
			}
		}
		if len(assets) == 0 {
			// Named with its reason, the way a skipped provider already is. A
			// sweep that selected nobody used to be indistinguishable from one
			// with nothing to select, and that silence is what let prod age a
			// week while every surface reported health.
			deferredUntil := ""
			if outcome == outcomeAllDeferred {
				if until := h.soonestDue(ctx, name); !until.IsZero() {
					deferredUntil = until.Format(time.RFC3339)
				}
			}
			h.log.Info("price sweep: nothing selected",
				"provider", name, "reason", string(outcome), "soonest_due", deferredUntil)
			idleSources[name] = string(outcome)
			continue
		}
		// Resolve base asset UUID for this provider (create the asset if it doesn't exist yet).
		resolveBase := func(sym string) (string, error) {
			key := baseAssetKey{symbol: strings.ToUpper(sym), typ: provider.BaseAssetType()}
			if id, ok := baseAssetCache[key]; ok {
				return id, nil
			}
			baseAsset, err := h.store.GetOrCreateAssetBySymbol(ctx, key.symbol, key.symbol, key.typ)
			if err != nil {
				return "", fmt.Errorf("resolve base asset %s: %w", key.symbol, err)
			}
			baseAssetCache[key] = baseAsset.ID
			return baseAsset.ID, nil
		}

		// Resolved BEFORE the provider is asked for anything. The base is a
		// constant of the provider, so a failure here is a failure of the whole
		// batch either way — and doing it afterwards means paying for the
		// request, spending the quota and recording the attempts, only to throw
		// every price away at the last step. That is precisely what Binance did
		// on dev, hourly, from 2026-06-02 until this was moved.
		defaultBaseID, err := resolveBase(provider.BaseAssetSymbol())
		if err != nil {
			fetchErrs = append(fetchErrs, fmt.Sprintf("%s: %v", name, err))
			continue
		}

		h.attachExternalRefs(ctx, assets)
		h.discoverRefs(ctx, name, provider, assets)

		results, err := provider.FetchPrices(ctx, assets)
		if err != nil {
			fetchErrs = append(fetchErrs, fmt.Sprintf("%s: %v", name, err))
			// A transport-level failure is evidence about the SOURCE, not about
			// any asset in the batch: Binance answers 400 for the whole request
			// when one symbol is not a tradable pair, and crediting every asset
			// in it with a miss doubles the back-off of assets the provider
			// would happily have priced. Their schedule must not move because
			// they shared a request with a jetton (personal-7994).
			continue
		}

		// Only what this provider actually requested counts as an attempt. The
		// rest was never asked, and a miss recorded against it would be one
		// source's silence filed against an asset it does not cover. Asked AFTER
		// the fetch on purpose: a provider that consults a cached listing set
		// has it warm by now, so the first sweep filters as well as the tenth.
		h.recordAttempts(ctx, name, askedOf(provider, assets), results)

		totalFetched += len(results)
		for i := range results {
			// Most sources quote everything in one currency and say so once. A
			// broker prices a foreign share in dollars and a domestic one in
			// roubles out of the same response, so a row may name its own base;
			// storing those under the provider's default would be a hundredfold
			// error, not a rounding one.
			baseID := defaultBaseID
			if sym := results[i].BaseSymbol; sym != "" {
				resolved, err := resolveBase(sym)
				if err != nil {
					fetchErrs = append(fetchErrs, fmt.Sprintf("%s: %v", name, err))
					continue
				}
				baseID = resolved
			}
			results[i].BaseAssetID = baseID
			allPrices = append(allPrices, &results[i])
		}
	}

	var stored int
	if len(allPrices) > 0 {
		var err error
		stored, err = h.store.CreatePrices(ctx, allPrices)
		if err != nil {
			// Partial failures are non-fatal: surface them in the response errors field.
			h.log.Warn("some prices failed to store", "error", err)
			fetchErrs = append(fetchErrs, err.Error())
		}
	}

	return connect.NewResponse(&apiv1.FetchExternalPricesResponse{
		PricesFetched: int32(totalFetched), // #nosec G115 -- count of fetched prices, bounded by request size
		PricesStored:  int32(stored),       // #nosec G115 -- count of rows stored, bounded by request size
		Errors:        fetchErrs,
		IdleSources:   idleSources,
	}), nil
}

// refreshTargets picks what an unattended sweep asks one source for: assets
// whose next attempt is due, oldest first, capped by the portion the source's
// remaining plan allowance affords between now and the next sweep.
//
// The symbols a provider prices for free are selected separately and uncapped —
// one /coins/markets call covers them however many there are, and they carry
// most of the portfolio's value, so budgeting them would cost freshness and
// save nothing.
func (h *Handler) refreshTargets(ctx context.Context, sourceID string, p PriceProvider) ([]*entity.Asset, selectionOutcome, error) {
	now := time.Now()
	base := StalePricingOpts{
		SourceID:        sourceID,
		Now:             now,
		ExcludeVerdicts: quarantineVerdicts,
	}

	var exempt []string
	if bp, ok := p.(BudgetExemptProvider); ok {
		exempt = bp.BudgetExemptSymbols()
	}

	var targets []*entity.Asset
	if len(exempt) > 0 {
		free := base
		free.Symbols = exempt
		got, err := h.store.ListStalePricingTargets(ctx, free)
		if err != nil {
			return nil, "", err
		}
		targets = append(targets, got...)
	}

	budgeted := base
	budgeted.ExcludeSymbols = exempt
	if bp, ok := p.(BudgetedProvider); ok && h.refreshWindow > 0 {
		n, ok := bp.AssetBudget(now, h.refreshWindow)
		if ok {
			if n <= 0 {
				// Nothing outside the exempt set is worth asking for. That is
				// not the same statement as "this provider has nothing left":
				// CBR answers zero permanently, because its whole feed is the
				// one exempt document. Whether a provider can be asked at all
				// is HealthReportingProvider's question, asked before this.
				return targets, outcomeBudgetZero, nil
			}
			budgeted.Limit = n
		}
	}
	got, err := h.store.ListStalePricingTargets(ctx, budgeted)
	if err != nil {
		return nil, "", err
	}
	targets = append(targets, got...)
	if len(targets) > 0 {
		return targets, outcomeSelected, nil
	}

	// Nothing came back, and the empty slice cannot say which silence this is:
	// a catalogue backed off until next week produces exactly the same empty
	// slice as one that is entirely up to date. Asking the schedule is the only
	// way to tell, and it is only asked on the empty path — a sweep that found
	// work pays nothing for this.
	return targets, h.explainEmptySelection(ctx, sourceID, now), nil
}

// selectionOutcome says why a sweep selected what it did, so the log can tell
// "nothing was due" from "everything is postponed".
type selectionOutcome string

const (
	outcomeSelected    selectionOutcome = "selected"
	outcomeNothingDue  selectionOutcome = "nothing_due"
	outcomeAllDeferred selectionOutcome = "all_deferred"
	outcomeBudgetZero  selectionOutcome = "budget_exhausted"
	outcomeUnknown     selectionOutcome = "unknown"
)

// soonestDue reports when this source's earliest deferred asset comes due, for
// the log line that says how long a frozen queue stays frozen. Zero when
// unknown — a log line is not worth failing a sweep over.
func (h *Handler) soonestDue(ctx context.Context, sourceID string) time.Time {
	scheds, err := h.store.SweepSchedule(ctx, SweepScheduleOpts{
		SourceIDs:       []string{sourceID},
		Now:             time.Now(),
		ExcludeVerdicts: quarantineVerdicts,
	})
	if err != nil || len(scheds) == 0 {
		return time.Time{}
	}
	return scheds[0].SoonestDue
}

// explainEmptySelection asks the attempt log why a selection came back empty.
//
// Best-effort by design: this runs to produce a log line, and failing to
// explain an empty sweep must not fail the sweep itself.
func (h *Handler) explainEmptySelection(ctx context.Context, sourceID string, now time.Time) selectionOutcome {
	scheds, err := h.store.SweepSchedule(ctx, SweepScheduleOpts{
		SourceIDs:       []string{sourceID},
		Now:             now,
		ExcludeVerdicts: quarantineVerdicts,
	})
	if err != nil || len(scheds) == 0 {
		return outcomeUnknown
	}
	if scheds[0].Deferred > 0 {
		return outcomeAllDeferred
	}
	return outcomeNothingDue
}

// attachExternalRefs loads the assets' identities in external namespaces so a
// provider can route a contract to the platform it actually lives on. Without
// the chain, a Base address asked for under Ethereum is a request spent on a
// certain miss — or worse, an address collision priced as somebody else's token.
//
// Best-effort: refs that fail to load leave the assets as they were, and a
// provider that needs them skips those rather than guessing a chain.
func (h *Handler) attachExternalRefs(ctx context.Context, assets []*entity.Asset) {
	ids := make([]string, 0, len(assets))
	byID := make(map[string]*entity.Asset, len(assets))
	for _, a := range assets {
		ids = append(ids, a.ID)
		byID[a.ID] = a
	}

	refs, err := h.store.ListAssetExternalRefs(ctx, ids)
	if err != nil {
		if h.log != nil {
			h.log.Warn("load asset external refs failed", "error", err)
		}
		return
	}
	for _, ref := range refs {
		if a, ok := byID[ref.AssetID]; ok {
			a.ExternalRefs = append(a.ExternalRefs, *ref)
		}
	}
}

// GetPricingStatus reads the attempt log back so a caller can tell an asset
// nothing has looked at from one every source has looked at and none could
// price. Both produce no price row; only the second says anything about the
// instrument.
//
// Read-only and unauthenticated beyond the usual interceptor: the attempt log
// holds no user data, only which catalogue assets this instance has asked its
// own providers about.
func (h *Handler) GetPricingStatus(ctx context.Context, req *connect.Request[apiv1.GetPricingStatusRequest]) (*connect.Response[apiv1.GetPricingStatusResponse], error) {
	ids := req.Msg.GetAssetIds()
	if len(ids) == 0 {
		return connect.NewResponse(&apiv1.GetPricingStatusResponse{}), nil
	}

	statuses, err := h.store.PricingStatus(ctx, ids)
	if err != nil {
		return nil, toConnectError(err)
	}

	out := make([]*apiv1.AssetPricingStatus, 0, len(statuses))
	for _, st := range statuses {
		item := &apiv1.AssetPricingStatus{
			AssetId:      st.AssetID,
			EverPriced:   st.EverPriced,
			SourcesAsked: st.SourcesAsked,
		}
		if !st.FirstAskedAt.IsZero() {
			item.FirstAskedAt = timestamppb.New(st.FirstAskedAt)
		}
		if !st.LastAskedAt.IsZero() {
			item.LastAskedAt = timestamppb.New(st.LastAskedAt)
		}
		out = append(out, item)
	}
	return connect.NewResponse(&apiv1.GetPricingStatusResponse{Statuses: out}), nil
}

// GetSweepSchedule reports, per source, the queue the next sweep would find.
//
// This exists because the sweep's own summary cannot tell two states apart: a
// run that asked nobody logs the same "fetched=0, stored=0" as a run with
// nothing to ask. The facts that separate them live in price_fetch_attempts,
// and until now reading them meant a psql session on the host — which is to
// say, an instance could sit with its whole catalogue backed off for a week
// and look healthy from every surface anyone actually consults.
//
// The counts come from the attempt log; skip_reason comes from the provider
// itself, so the answer is the sweep's own judgement rather than a second one
// computed here and free to drift from it.
func (h *Handler) GetSweepSchedule(ctx context.Context, req *connect.Request[apiv1.GetSweepScheduleRequest]) (*connect.Response[apiv1.GetSweepScheduleResponse], error) {
	providers, err := h.resolveProviders(ctx)
	if err != nil {
		return nil, err
	}

	sourceIDs := make([]string, 0, len(providers))
	for name := range providers {
		if want := req.Msg.GetSourceId(); want != "" && want != name {
			continue
		}
		sourceIDs = append(sourceIDs, name)
	}
	if len(sourceIDs) == 0 {
		return nil, connect.NewError(connect.CodeNotFound,
			fmt.Errorf("no price provider named %q", req.Msg.GetSourceId()))
	}
	slices.Sort(sourceIDs)

	schedules, err := h.store.SweepSchedule(ctx, SweepScheduleOpts{
		SourceIDs:       sourceIDs,
		Now:             time.Now(),
		ExcludeVerdicts: quarantineVerdicts,
	})
	if err != nil {
		return nil, toConnectError(err)
	}

	out := make([]*apiv1.SourceSchedule, 0, len(schedules))
	for _, sched := range schedules {
		item := &apiv1.SourceSchedule{
			SourceId:       sched.SourceID,
			DueNow:         sched.DueNow,
			Deferred:       sched.Deferred,
			NeverAttempted: sched.NeverAttempted,
			MaxMisses:      sched.MaxMisses,
		}
		if !sched.SoonestDue.IsZero() {
			item.SoonestDue = timestamppb.New(sched.SoonestDue)
		}
		if !sched.LatestDeferred.IsZero() {
			item.LatestDeferred = timestamppb.New(sched.LatestDeferred)
		}
		// Asked of the provider rather than inferred: "would this be skipped"
		// is a question only the provider can answer, and answering it here
		// from the counts would be a guess dressed as a fact.
		if p, ok := providers[sched.SourceID]; ok {
			if hp, ok := p.(HealthReportingProvider); ok {
				if reason, unusable := hp.Unusable(); unusable {
					item.SkipReason = &reason
				}
			}
		}
		out = append(out, item)
	}

	return connect.NewResponse(&apiv1.GetSweepScheduleResponse{Sources: out}), nil
}

// ResetSweepSchedule forgives the back-off accrued against the named sources.
//
// Validated against the resolved provider registry rather than applied blind:
// a typo in a source name would otherwise report a confident zero, which reads
// exactly like "the schedule was already clear" — the answer most likely to
// send an operator looking for the problem somewhere else.
func (h *Handler) ResetSweepSchedule(ctx context.Context, req *connect.Request[apiv1.ResetSweepScheduleRequest]) (*connect.Response[apiv1.ResetSweepScheduleResponse], error) {
	want := req.Msg.GetSourceIds()
	if len(want) == 0 {
		return nil, connect.NewError(connect.CodeInvalidArgument,
			errors.New("source_ids is required: name the sources to reset"))
	}

	providers, err := h.resolveProviders(ctx)
	if err != nil {
		return nil, err
	}
	for _, name := range want {
		if _, ok := providers[name]; !ok {
			return nil, connect.NewError(connect.CodeNotFound,
				fmt.Errorf("no price provider named %q", name))
		}
	}

	// One timestamp for the whole call, so every source freed by it becomes due
	// at the same instant rather than staggered by however long the loop took.
	at := time.Now()
	freed := make(map[string]uint32, len(want))
	for _, name := range want {
		n, err := h.store.ResetPriceAttempts(ctx, name, at)
		if err != nil {
			return nil, toConnectError(err)
		}
		freed[name] = uint32(n)
		h.log.Info("sweep schedule reset", "provider", name, "assets_freed", n)
	}

	return connect.NewResponse(&apiv1.ResetSweepScheduleResponse{AssetsFreed: freed}), nil
}

// resolveProviders returns the price registry as the caller's credentials
// define it, falling back to the static env-configured one.
//
// Shared by the sweep and by GetSweepSchedule on purpose: a schedule computed
// against a different set of providers than the one that does the fetching
// would answer a question about an instance that does not exist.
func (h *Handler) resolveProviders(ctx context.Context) (map[string]PriceProvider, error) {
	providers := h.providers
	if h.providerSource != nil {
		userID := ""
		if user, ok := middleware.UserFromContext(ctx); ok {
			userID = user.ID
		}
		resolved, err := h.providerSource.PriceProvidersFor(ctx, userID)
		if err != nil {
			return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("resolve price providers: %w", err))
		}
		providers = resolved
	}
	if len(providers) == 0 {
		return nil, connect.NewError(connect.CodeUnimplemented, errors.New("no price providers configured"))
	}
	return providers, nil
}

// recordAttempts marks what was asked of a source and what came back, so the
// next sweep can skip what is still fresh and back off from what the provider
// does not price. Best-effort: failing to record costs the next sweep a
// repeated request, which is not worth failing the whole fetch over.
func (h *Handler) recordAttempts(ctx context.Context, sourceID string, asked []*entity.Asset, got []entity.StoredPrice) {
	priced := make(map[string]bool, len(got))
	for i := range got {
		priced[got[i].AssetID] = true
	}

	opts := RecordAttemptsOpts{
		SourceID:   sourceID,
		At:         time.Now(),
		TTL:        priceTTL,
		MaxBackoff: missBackoffCap,
	}
	for _, a := range asked {
		if priced[a.ID] {
			opts.Priced = append(opts.Priced, a.ID)
		} else {
			opts.Missed = append(opts.Missed, a.ID)
		}
	}

	if err := h.store.RecordPriceAttempts(ctx, opts); err != nil && h.log != nil {
		h.log.Warn("record price attempts failed", "source", sourceID, "error", err)
	}
}

// toConnectError converts store errors to Connect errors.
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

// Conversion helpers

func assetFromProto(p *apiv1.Asset) *entity.Asset {
	return &entity.Asset{
		ID:     p.Id,
		Name:   p.Name,
		Symbol: p.GetSymbol(),
		Type:   entity.AssetType(p.Type),
		Market: p.GetMarket(),
		Quote:  p.GetQuote(),
		Tags:   p.Tags,
	}
}

func assetToProto(e *entity.Asset) *apiv1.Asset {
	out := &apiv1.Asset{
		Id:              e.ID,
		Name:            e.Name,
		Symbol:          optionalString(e.Symbol),
		Type:            apiv1.AssetType(e.Type),
		Market:          optionalString(e.Market),
		Quote:           optionalString(e.Quote),
		Tags:            e.Tags,
		IdentityVerdict: optionalString(e.IdentityVerdict),
		VerdictSource:   optionalString(e.VerdictSource),
		IdentityScore:   e.IdentityScore,
		IdentitySignals: e.IdentitySignals,
		CreatedAt:       timestamppb.New(e.CreatedAt),
		UpdatedAt:       timestamppb.New(e.UpdatedAt),
	}
	if e.VerdictSetAt != nil {
		out.VerdictSetAt = timestamppb.New(*e.VerdictSetAt)
	}
	// Nil means "not loaded" on every path but GetAsset, and an empty list on
	// the wire cannot say which it is — so an unloaded field stays absent
	// rather than becoming a claim that the asset has no bindings.
	for _, ref := range e.ExternalRefs {
		out.ExternalRefs = append(out.ExternalRefs, &apiv1.AssetExternalRef{
			Id:        ref.ID,
			AssetId:   ref.AssetID,
			Source:    ref.Source,
			Ref:       ref.Ref,
			Origin:    ref.Origin,
			CreatedAt: timestamppb.New(ref.CreatedAt),
		})
	}
	// Same "nil means not loaded" contract as the bindings above.
	for _, f := range e.RiskFlags {
		out.RiskFlags = append(out.RiskFlags, riskFlagToProto(&f))
	}
	return out
}

func riskFlagToProto(f *entity.AssetRiskFlag) *apiv1.AssetRiskFlag {
	out := &apiv1.AssetRiskFlag{
		Id:         f.ID,
		AssetId:    f.AssetID,
		Kind:       f.Kind,
		Note:       optionalString(f.Note),
		ActionHint: optionalString(f.ActionHint),
		SetBy:      optionalString(f.SetBy),
		CreatedAt:  timestamppb.New(f.CreatedAt),
	}
	if f.ReviewAt != nil {
		out.ReviewAt = timestamppb.New(*f.ReviewAt)
	}
	return out
}

// optionalString maps "" to an absent proto optional field.
func optionalString(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// parseDecimal parses a raw integer decimal string. Empty is treated as unset (zero);
// a non-empty but malformed value is an error rather than a silent zero.
func parseDecimal(s string) (decimal.Decimal, error) {
	if s == "" {
		return decimal.Zero, nil
	}
	return decimal.NewFromString(s)
}

// parseNullDecimal converts an optional proto string into a NullDecimal. A nil pointer
// is a valid absent value; a non-nil but malformed value is an error.
func parseNullDecimal(s *string) (decimal.NullDecimal, error) {
	if s == nil {
		return decimal.NullDecimal{}, nil
	}
	d, err := decimal.NewFromString(*s)
	if err != nil {
		return decimal.NullDecimal{}, err
	}
	return decimal.NullDecimal{Decimal: d, Valid: true}, nil
}

// nullDecimalToProto converts a NullDecimal into an optional proto string.
func nullDecimalToProto(d decimal.NullDecimal) *string {
	if !d.Valid {
		return nil
	}
	s := d.Decimal.String()
	return &s
}

func priceFromProto(p *apiv1.Price) (*entity.StoredPrice, error) {
	last, err := parseDecimal(p.Last)
	if err != nil {
		return nil, fmt.Errorf("invalid last %q: %w", p.Last, err)
	}
	open, err := parseNullDecimal(p.Open)
	if err != nil {
		return nil, fmt.Errorf("invalid open: %w", err)
	}
	high, err := parseNullDecimal(p.High)
	if err != nil {
		return nil, fmt.Errorf("invalid high: %w", err)
	}
	low, err := parseNullDecimal(p.Low)
	if err != nil {
		return nil, fmt.Errorf("invalid low: %w", err)
	}
	closeVal, err := parseNullDecimal(p.Close)
	if err != nil {
		return nil, fmt.Errorf("invalid close: %w", err)
	}
	volume, err := parseNullDecimal(p.Volume)
	if err != nil {
		return nil, fmt.Errorf("invalid volume: %w", err)
	}
	marketCap, err := parseNullDecimal(p.MarketCap)
	if err != nil {
		return nil, fmt.Errorf("invalid market_cap: %w", err)
	}

	price := &entity.StoredPrice{
		ID:          p.Id,
		SourceID:    p.SourceId,
		AssetID:     p.AssetId,
		BaseAssetID: p.BaseAssetId,
		Interval:    p.Interval,
		Decimals:    p.Decimals,
		Last:        last,
		Open:        open,
		High:        high,
		Low:         low,
		Close:       closeVal,
		Volume:      volume,
		MarketCap:   marketCap,
	}
	if p.Timestamp != nil {
		price.Timestamp = p.Timestamp.AsTime()
	}
	return price, nil
}

func priceToProto(e *entity.StoredPrice) *apiv1.Price {
	return &apiv1.Price{
		Id:          e.ID,
		SourceId:    e.SourceID,
		AssetId:     e.AssetID,
		BaseAssetId: e.BaseAssetID,
		Interval:    e.Interval,
		Decimals:    e.Decimals,
		Last:        e.Last.String(),
		Open:        nullDecimalToProto(e.Open),
		High:        nullDecimalToProto(e.High),
		Low:         nullDecimalToProto(e.Low),
		Close:       nullDecimalToProto(e.Close),
		Volume:      nullDecimalToProto(e.Volume),
		MarketCap:   nullDecimalToProto(e.MarketCap),
		Timestamp:   timestamppb.New(e.Timestamp),
	}
}
