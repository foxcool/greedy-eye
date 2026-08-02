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
	if symbol == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("symbol is required"))
	}

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
	// Resolve the effective provider registry from stored credentials of the
	// calling user; the static env-configured registry is the fallback.
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

	// Cache base asset UUIDs per symbol to avoid repeated lookups across providers.
	baseAssetCache := map[string]string{} // symbol → UUID

	for name, provider := range providers {
		if len(req.Msg.SourceIds) > 0 && !slices.Contains(req.Msg.SourceIds, name) {
			continue
		}

		// Selection is per source because freshness is: an asset Binance priced
		// a minute ago is still stale for CoinGecko.
		assets := named
		if assets == nil {
			var err error
			assets, err = h.refreshTargets(ctx, name, provider)
			if err != nil {
				fetchErrs = append(fetchErrs, fmt.Sprintf("%s: select targets: %v", name, err))
				continue
			}
		}
		if len(assets) == 0 {
			continue
		}
		h.attachExternalRefs(ctx, assets)

		results, err := provider.FetchPrices(ctx, assets)
		if err != nil {
			fetchErrs = append(fetchErrs, fmt.Sprintf("%s: %v", name, err))
			h.recordAttempts(ctx, name, assets, nil)
			continue
		}
		h.recordAttempts(ctx, name, assets, results)

		// Resolve base asset UUID for this provider (create the asset if it doesn't exist yet).
		sym := strings.ToUpper(provider.BaseAssetSymbol())
		baseID, ok := baseAssetCache[sym]
		if !ok {
			baseAsset, err := h.store.GetOrCreateAssetBySymbol(ctx, sym, sym, provider.BaseAssetType())
			if err != nil {
				fetchErrs = append(fetchErrs, fmt.Sprintf("%s: resolve base asset %s: %v", name, sym, err))
				continue
			}
			baseID = baseAsset.ID
			baseAssetCache[sym] = baseID
		}

		totalFetched += len(results)
		for i := range results {
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
func (h *Handler) refreshTargets(ctx context.Context, sourceID string, p PriceProvider) ([]*entity.Asset, error) {
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
			return nil, err
		}
		targets = append(targets, got...)
	}

	budgeted := base
	budgeted.ExcludeSymbols = exempt
	if bp, ok := p.(BudgetedProvider); ok && h.refreshWindow > 0 {
		n, ok := bp.AssetBudget(now, h.refreshWindow)
		if ok {
			if n <= 0 {
				// The plan's background allowance is spent; the free tier above
				// still went out, since it costs nothing extra.
				return targets, nil
			}
			budgeted.Limit = n
		}
	}
	got, err := h.store.ListStalePricingTargets(ctx, budgeted)
	if err != nil {
		return nil, err
	}
	return append(targets, got...), nil
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
	return &apiv1.Asset{
		Id:              e.ID,
		Name:            e.Name,
		Symbol:          optionalString(e.Symbol),
		Type:            apiv1.AssetType(e.Type),
		Market:          optionalString(e.Market),
		Quote:           optionalString(e.Quote),
		Tags:            e.Tags,
		IdentityVerdict: optionalString(e.IdentityVerdict),
		VerdictSource:   optionalString(e.VerdictSource),
		CreatedAt:       timestamppb.New(e.CreatedAt),
		UpdatedAt:       timestamppb.New(e.UpdatedAt),
	}
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
