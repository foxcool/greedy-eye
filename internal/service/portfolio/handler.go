package portfolio

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math/big"
	"slices"
	"strings"
	"time"

	"connectrpc.com/connect"
	apiv1 "github.com/foxcool/greedy-eye/api/v1"
	"github.com/foxcool/greedy-eye/api/v1/apiv1connect"
	"github.com/foxcool/greedy-eye/internal/entity"
	"github.com/foxcool/greedy-eye/internal/middleware"
	"github.com/foxcool/greedy-eye/internal/pricefresh"
	"github.com/foxcool/greedy-eye/internal/quoting"
	"github.com/foxcool/greedy-eye/internal/scamfilter"
	"github.com/foxcool/greedy-eye/internal/store"
	"github.com/shopspring/decimal"
	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/emptypb"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// WalletSyncerSource resolves a wallet syncer from stored account credentials
// for a given user, able to sync the requested chains (see
// internal/service/credentials). An empty chains list means auto-discovery,
// which is routed by the address's shape.
type WalletSyncerSource interface {
	WalletSyncerFor(ctx context.Context, userID, address string, chains []string) (entity.WalletSyncer, error)
}

// ExchangeSyncerSource builds an exchange syncer from a specific account's own
// stored credentials (see internal/service/credentials).
type ExchangeSyncerSource interface {
	ExchangeSyncerForAccount(a *entity.Account) (entity.ExchangeSyncer, error)
}

// BrokerSyncerSource builds a broker syncer from a specific account's own
// stored credentials (see internal/service/credentials). Separate from
// ExchangeSyncerSource because a broker syncer speaks for ONE account at the
// broker, named by the account itself.
type BrokerSyncerSource interface {
	BrokerSyncerForAccount(a *entity.Account) (entity.BrokerSyncer, error)
}

// BrokerAccountListerSource builds, from an account's stored credentials, the
// reader that says which accounts its token reaches (see
// internal/service/credentials).
type BrokerAccountListerSource interface {
	BrokerAccountListerForAccount(a *entity.Account) (entity.BrokerAccountLister, error)
}

// Handler implements apiv1connect.PortfolioServiceHandler.
type Handler struct {
	apiv1connect.UnimplementedPortfolioServiceHandler
	store          Store
	mdClient       MarketDataClient     // optional; nil if not configured
	setClient      SettingsClient       // optional; nil means valuation defaults
	walletSyncer   entity.WalletSyncer  // optional; nil if not configured
	syncerSource   WalletSyncerSource   // optional; takes precedence over walletSyncer
	exchangeSource ExchangeSyncerSource // optional; resolves per-account exchange syncers
	brokerSource   BrokerSyncerSource   // optional; resolves per-account broker syncers
	// brokerListerSource resolves the reader that says which accounts a token
	// reaches. Optional: without it a broker account must name its own.
	brokerListerSource BrokerAccountListerSource
	providers          ProviderCatalog // optional; describes the providers an account may name
	log                *slog.Logger
}

func NewHandler(store Store, log *slog.Logger) *Handler {
	return &Handler{store: store, log: log}
}

func (h *Handler) clone() *Handler {
	copied := *h
	return &copied
}

// WithMarketDataClient returns a new Handler with the MarketData client injected.
func (h *Handler) WithMarketDataClient(mc MarketDataClient) *Handler {
	copied := h.clone()
	copied.mdClient = mc
	return copied
}

// WithSettingsClient returns a new Handler that reads valuation rules from the
// caller's settings. Without it the built-in defaults apply.
func (h *Handler) WithSettingsClient(sc SettingsClient) *Handler {
	copied := h.clone()
	copied.setClient = sc
	return copied
}

// WithWalletSyncer returns a new Handler with the wallet syncer injected.
func (h *Handler) WithWalletSyncer(ws entity.WalletSyncer) *Handler {
	copied := h.clone()
	copied.walletSyncer = ws
	return copied
}

// WithWalletSyncerSource returns a new Handler resolving wallet syncers from
// stored account credentials, with walletSyncer as the fallback.
func (h *Handler) WithWalletSyncerSource(src WalletSyncerSource) *Handler {
	copied := h.clone()
	copied.syncerSource = src
	return copied
}

// WithExchangeSyncerSource returns a new Handler resolving exchange syncers from
// stored account credentials.
func (h *Handler) WithExchangeSyncerSource(src ExchangeSyncerSource) *Handler {
	copied := h.clone()
	copied.exchangeSource = src
	return copied
}

// WithBrokerSyncerSource returns a new Handler resolving broker syncers from
// stored account credentials.
func (h *Handler) WithBrokerSyncerSource(src BrokerSyncerSource) *Handler {
	copied := h.clone()
	copied.brokerSource = src
	return copied
}

// WithBrokerAccountListerSource returns a new Handler able to ask a broker
// which accounts its token reaches.
func (h *Handler) WithBrokerAccountListerSource(src BrokerAccountListerSource) *Handler {
	copied := h.clone()
	copied.brokerListerSource = src
	return copied
}

// --- Portfolio CRUD ---

// ownedPortfolio loads a portfolio and enforces ownership.
func (h *Handler) ownedPortfolio(ctx context.Context, id string) (*entity.Portfolio, error) {
	p, err := h.store.GetPortfolio(ctx, id)
	if err != nil {
		return nil, toConnectError(err)
	}
	if err := middleware.EnsureOwner(ctx, p.UserID); err != nil {
		return nil, err
	}
	return p, nil
}

// ownedAccount loads an account and enforces ownership.
func (h *Handler) ownedAccount(ctx context.Context, id string) (*entity.Account, error) {
	a, err := h.store.GetAccount(ctx, id)
	if err != nil {
		return nil, toConnectError(err)
	}
	if err := middleware.EnsureOwner(ctx, a.UserID); err != nil {
		return nil, err
	}
	return a, nil
}

// ownedHolding loads a holding and enforces ownership via its account.
func (h *Handler) ownedHolding(ctx context.Context, id string) (*entity.Holding, error) {
	hld, err := h.store.GetHolding(ctx, id)
	if err != nil {
		return nil, toConnectError(err)
	}
	if _, err := h.ownedAccount(ctx, hld.AccountID); err != nil {
		return nil, err
	}
	return hld, nil
}

// ownedTransaction loads a transaction and enforces ownership via its account.
func (h *Handler) ownedTransaction(ctx context.Context, id string) (*entity.Transaction, error) {
	t, err := h.store.GetTransaction(ctx, id)
	if err != nil {
		return nil, toConnectError(err)
	}
	if _, err := h.ownedAccount(ctx, t.AccountID); err != nil {
		return nil, err
	}
	return t, nil
}

func (h *Handler) CreatePortfolio(ctx context.Context, req *connect.Request[apiv1.CreatePortfolioRequest]) (*connect.Response[apiv1.Portfolio], error) {
	if req.Msg.Portfolio == nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("portfolio is required"))
	}

	user, ok := middleware.UserFromContext(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("user not found in context"))
	}

	p := portfolioFromProto(req.Msg.Portfolio)
	p.UserID = user.ID
	created, err := h.store.CreatePortfolio(ctx, p)
	if err != nil {
		return nil, toConnectError(err)
	}

	return connect.NewResponse(portfolioToProto(created)), nil
}

func (h *Handler) GetPortfolio(ctx context.Context, req *connect.Request[apiv1.GetPortfolioRequest]) (*connect.Response[apiv1.Portfolio], error) {
	if req.Msg.Id == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("portfolio ID is required"))
	}

	p, err := h.ownedPortfolio(ctx, req.Msg.Id)
	if err != nil {
		return nil, err
	}

	return connect.NewResponse(portfolioToProto(p)), nil
}

func (h *Handler) UpdatePortfolio(ctx context.Context, req *connect.Request[apiv1.UpdatePortfolioRequest]) (*connect.Response[apiv1.Portfolio], error) {
	if req.Msg.Portfolio == nil || req.Msg.Portfolio.Id == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("portfolio with ID is required"))
	}

	if _, err := h.ownedPortfolio(ctx, req.Msg.Portfolio.Id); err != nil {
		return nil, err
	}

	fields, err := resolveMask(req.Msg.UpdateMask, portfolioUpdatable)
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}

	p := portfolioFromProto(req.Msg.Portfolio)
	updated, err := h.store.UpdatePortfolio(ctx, p, fields)
	if err != nil {
		return nil, toConnectError(err)
	}

	return connect.NewResponse(portfolioToProto(updated)), nil
}

func (h *Handler) DeletePortfolio(ctx context.Context, req *connect.Request[apiv1.DeletePortfolioRequest]) (*connect.Response[emptypb.Empty], error) {
	if req.Msg.Id == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("portfolio ID is required"))
	}

	if _, err := h.ownedPortfolio(ctx, req.Msg.Id); err != nil {
		return nil, err
	}

	if err := h.store.DeletePortfolio(ctx, req.Msg.Id); err != nil {
		return nil, toConnectError(err)
	}

	return connect.NewResponse(&emptypb.Empty{}), nil
}

func (h *Handler) ListPortfolios(ctx context.Context, req *connect.Request[apiv1.ListPortfoliosRequest]) (*connect.Response[apiv1.ListPortfoliosResponse], error) {
	user, ok := middleware.UserFromContext(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("user not found in context"))
	}

	opts := ListPortfoliosOpts{UserID: user.ID}
	if req.Msg.UserId != nil && *req.Msg.UserId != "" && user.IsAdmin() {
		opts.UserID = *req.Msg.UserId // explicit override is admin-only
	}
	if req.Msg.PageSize != nil {
		opts.PageSize = int(*req.Msg.PageSize)
	}
	if req.Msg.PageToken != nil {
		opts.PageToken = *req.Msg.PageToken
	}

	portfolios, nextPageToken, err := h.store.ListPortfolios(ctx, opts)
	if err != nil {
		return nil, toConnectError(err)
	}

	protoPortfolios := make([]*apiv1.Portfolio, 0, len(portfolios))
	for _, p := range portfolios {
		protoPortfolios = append(protoPortfolios, portfolioToProto(p))
	}

	return connect.NewResponse(&apiv1.ListPortfoliosResponse{
		Portfolios:    protoPortfolios,
		NextPageToken: nextPageToken,
	}), nil
}

// --- Portfolio business logic (stubs) ---

func (h *Handler) CalculatePortfolioValue(ctx context.Context, req *connect.Request[apiv1.CalculatePortfolioValueRequest]) (*connect.Response[apiv1.PortfolioValueResponse], error) {
	if h.mdClient == nil {
		return nil, connect.NewError(connect.CodeUnimplemented, errors.New("market data client not configured"))
	}
	if req.Msg.PortfolioId == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("portfolio_id is required"))
	}

	if _, err := h.ownedPortfolio(ctx, req.Msg.PortfolioId); err != nil {
		return nil, err
	}

	// The policy carries both rules this valuation runs under: how old a quote
	// may be, and what currency the total is expressed in when the caller names
	// none. One read, because they are one statement about how this instance
	// reports money.
	valuation := pricefresh.PolicyFrom(ctx, h.setClient, h.log)

	quoteAssetID := req.Msg.QuoteAssetId
	if quoteAssetID == "" {
		quoteAssetID = valuation.QuoteAsset()
	}
	// Resolved once, before any holding is priced: the policy names the currency
	// by ticker while price rows carry a UUID. See quoting.ResolveQuote.
	quoteAssetID, err := quoting.ResolveQuote(ctx, h.mdClient, quoteAssetID)
	if err != nil {
		return nil, err
	}

	// Fetch all holdings (excluded included) and partition in code so the total
	// can exclude the quarantined ones while still disclosing them — a silently
	// shrunk total looks like a real outflow.
	holdings, err := h.allHoldings(ctx, ListHoldingsOpts{PortfolioID: req.Msg.PortfolioId})
	if err != nil {
		return nil, toConnectError(err)
	}

	const resultDecimals = 2
	total := decimal.Zero
	excludedTotal := decimal.Zero
	var excludedCount uint32
	coverage := &apiv1.ValuationCoverage{}
	var unpriced []unpricedHolding
	// The oldest amount in the total is what the total can honestly claim to be
	// as of: one week-old position dates the whole number, however fresh the
	// prices under it are.
	//
	// Synced rows only. A manual amount cannot be refreshed by anything, so
	// including it pins this date forever and the field stops reporting the
	// sweep it exists to watch. What a stale hand-entered position needs is
	// disclosure per row — `Holding.updated_at` beside `Holding.source` — not a
	// portfolio-wide date that can never move.
	var oldestAmount time.Time
	// The same statement about the other axis. A quote can outlive its market —
	// a delisted security keeps its last print forever — and until it is dated,
	// nothing tells that apart from a price observed a minute ago.
	//
	// Fresh quotes only, and for the reason the amounts line above gives: an age
	// is a symptom only where something was responsible for updating. A quote
	// past the freshness policy is one nothing is refreshing any more — the
	// sweep asks and the source no longer answers for that asset — so letting it
	// date the total freezes the field on the one position that stopped being
	// covered, and stops it reporting the sweep it exists to watch. Those rows
	// are not dropped or hidden: they stay in the total and are counted in
	// StaleCount below, which is where "how much of this is doubtful" belongs.
	// Dating and disclosure answer different questions and must not share a
	// number.
	var oldestQuote time.Time
	valuedAt := time.Now()

	for _, hld := range holdings {
		priced, err := h.unitPrice(ctx, hld.AssetID, quoteAssetID)
		if err != nil {
			return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("get price for %s: %w", hld.AssetID, err))
		}
		if hld.Excluded {
			// Count every quarantined holding; add its value only when priced.
			// Quarantine is its own axis: an excluded holding is out of the total
			// by decision, not for lack of a quote, so it stays out of coverage.
			excludedCount++
			if priced.outcome == quoting.Priced {
				excludedTotal = excludedTotal.Add(hld.Amount.Shift(-decI32(hld.Decimals)).Mul(priced.unit))
			}
			continue
		}
		if priced.outcome != quoting.Priced {
			// No usable price: report the holding instead of letting it contribute
			// zero. Zero is an assertion about the market; this is missing data —
			// either no quote at all, or a quote with no market behind it.
			coverage.UnpricedCount++
			unpriced = append(unpriced, unpricedHolding{holding: hld, reason: priced.outcome.Reason()})
			continue
		}
		coverage.PricedCount++
		if hld.Source.Swept() {
			oldestAmount = olderOf(oldestAmount, hld.UpdatedAt)
		}
		// A stale quote still counts toward the total: see ValuationCoverage on
		// why naming it beats removing it. Counted per holding, not per asset —
		// two positions in the same delisted security are two positions whose
		// value is in question. It does not date the total; see oldestQuote.
		if valuation.StaleAt(priced.quotedAt, valuedAt) {
			coverage.StaleCount++
		} else {
			oldestQuote = olderOf(oldestQuote, priced.quotedAt)
		}

		// value = (amount / 10^holding.Decimals) * unitPrice
		holdingValue := hld.Amount.Shift(-decI32(hld.Decimals)).Mul(priced.unit)
		total = total.Add(holdingValue)
	}
	if !oldestAmount.IsZero() {
		coverage.AmountsAsOf = timestamppb.New(oldestAmount)
	}
	if !oldestQuote.IsZero() {
		coverage.PricesAsOf = timestamppb.New(oldestQuote)
	}

	coverage.Unpriced, coverage.UnpricedTruncated = h.describeUnpriced(ctx, unpriced)

	// Convert to result decimals (e.g., 2 for USD cents) as a raw integer decimal string.
	scale := decimal.New(1, int32(resultDecimals))
	resultAmount := total.Mul(scale).Round(0).String()
	excludedAmount := excludedTotal.Mul(scale).Round(0).String()

	return connect.NewResponse(&apiv1.PortfolioValueResponse{
		PortfolioId:         req.Msg.PortfolioId,
		QuoteAssetId:        quoteAssetID,
		TotalValueAmount:    resultAmount,
		Decimals:            resultDecimals,
		CalculationTime:     timestamppb.New(time.Now()),
		ExcludedCount:       excludedCount,
		ExcludedValueAmount: excludedAmount,
		Coverage:            coverage,
	}), nil
}

// holdingsPageSize is how many holdings one store round trip asks for. It is a
// batch size, not a limit: allHoldings keeps asking until the store runs out.
const holdingsPageSize = 1000

// allHoldings reads every holding matching opts, following the store's cursor
// instead of hoping one page covers the portfolio.
//
// The single-page read this replaces was the failure section 8.3 exists to
// forbid. `ListHoldings` honours PageSize as given — there is no clamp — so a
// portfolio larger than one page had its tail silently absent: not excluded,
// not unpriced, just never read. A total computed that way looks like an
// outflow, and `ValuationCoverage` cannot disclose the gap either, because
// nothing knew the rows existed. Prod carried 312 rows on one portfolio against
// a ceiling of 1000, and the quarantined half grows on its own as synced spam
// arrives, so the margin was being consumed by nobody's decision.
//
// PageSize in opts is ignored; the caller asks for a set, not for a page.
func (h *Handler) allHoldings(ctx context.Context, opts ListHoldingsOpts) ([]*entity.Holding, error) {
	var all []*entity.Holding
	opts.PageSize = holdingsPageSize
	opts.PageToken = ""
	for {
		page, next, err := h.store.ListHoldings(ctx, opts)
		if err != nil {
			return nil, err
		}
		all = append(all, page...)
		// A store that keeps returning the same cursor would spin here. Guard on
		// the page being empty as well as the token being blank: either one means
		// there is nothing further to read.
		if next == "" || len(page) == 0 {
			return all, nil
		}
		opts.PageToken = next
	}
}

// maxUnpricedDisclosed caps the per-holding detail in a coverage block: the
// count is always exact, the list is a sample bounded by read size.
const maxUnpricedDisclosed = 50

// olderOf keeps the earlier of two timestamps, treating a zero value as "no
// observation yet" rather than as the year 1.
func olderOf(current, candidate time.Time) time.Time {
	if candidate.IsZero() {
		return current
	}
	if current.IsZero() || candidate.Before(current) {
		return candidate
	}
	return current
}

// unpricedHolding pairs a position left out of the total with the reason it was.
type unpricedHolding struct {
	holding *entity.Holding
	reason  apiv1.UnpricedReason
}

// describeUnpriced labels unpriced holdings for disclosure, up to the cap, and
// reports whether the list was truncated.
//
// A failed asset lookup degrades to an empty symbol rather than failing the
// valuation: the asset_id still identifies the position, and losing the whole
// total over a display label would be a worse trade than a missing name.
func (h *Handler) describeUnpriced(ctx context.Context, holdings []unpricedHolding) ([]*apiv1.UnpricedHolding, bool) {
	if len(holdings) == 0 {
		return nil, false
	}
	listed := holdings
	truncated := false
	if len(listed) > maxUnpricedDisclosed {
		listed = listed[:maxUnpricedDisclosed]
		truncated = true
	}

	return h.labelUnpriced(ctx, listed), truncated
}

// labelUnpriced turns classified holdings into their wire form: symbol resolved,
// and NO_QUOTE sharpened to NEVER_PRICED where the attempt log says every source
// has already been asked.
//
// Shared by the coverage sample and by ListUnpricedHoldings so the two describe
// the same holding the same way. They differ in how many rows they carry, which
// is a paging question, not a labelling one.
//
// A failed asset lookup degrades to an empty symbol rather than failing the
// call: the asset_id still identifies the position, and losing a whole valuation
// over a display label would be a worse trade than a missing name.
func (h *Handler) labelUnpriced(ctx context.Context, listed []unpricedHolding) []*apiv1.UnpricedHolding {
	if len(listed) == 0 {
		return nil
	}
	exhausted := h.exhaustedSources(ctx, listed)

	out := make([]*apiv1.UnpricedHolding, 0, len(listed))
	for _, u := range listed {
		hld := u.holding
		item := &apiv1.UnpricedHolding{HoldingId: hld.ID, AssetId: hld.AssetID, Reason: u.reason}
		// "No quote" is where the pricing path stops; the attempt log says
		// whether that is because nothing has looked yet or because everything
		// that could look already has.
		if u.reason == apiv1.UnpricedReason_UNPRICED_REASON_NO_QUOTE {
			if since, ok := exhausted[hld.AssetID]; ok {
				item.Reason = apiv1.UnpricedReason_UNPRICED_REASON_NEVER_PRICED
				item.AskedSince = timestamppb.New(since)
			}
		}
		resp, err := h.mdClient.GetAsset(ctx, connect.NewRequest(&apiv1.GetAssetRequest{Id: hld.AssetID}))
		if err != nil {
			h.log.WarnContext(ctx, "unpriced holding: asset lookup failed, reporting without a symbol",
				"asset_id", hld.AssetID, "holding_id", hld.ID, "error", err)
		} else {
			item.Symbol = resp.Msg.GetSymbol()
		}
		out = append(out, item)
	}
	return out
}

// exhaustedSources returns, for each disclosed asset that every source has been
// asked about and none has ever priced, when the asking started.
//
// One batched call for the whole disclosure: the set is already capped, and the
// question is per asset while the list is per holding, so two positions in the
// same dead instrument cost nothing extra.
//
// Best-effort by design. Losing this detail downgrades the reason to NO_QUOTE,
// which is exactly what the caller would have reported without it — a weaker
// statement, never a wrong one, and not worth failing a valuation over.
func (h *Handler) exhaustedSources(ctx context.Context, listed []unpricedHolding) map[string]time.Time {
	ids := make([]string, 0, len(listed))
	seen := make(map[string]struct{}, len(listed))
	for _, u := range listed {
		if u.reason != apiv1.UnpricedReason_UNPRICED_REASON_NO_QUOTE {
			continue
		}
		if _, dup := seen[u.holding.AssetID]; dup {
			continue
		}
		seen[u.holding.AssetID] = struct{}{}
		ids = append(ids, u.holding.AssetID)
	}
	if len(ids) == 0 {
		return nil
	}

	resp, err := h.mdClient.GetPricingStatus(ctx, connect.NewRequest(&apiv1.GetPricingStatusRequest{AssetIds: ids}))
	if err != nil {
		h.log.WarnContext(ctx, "pricing status unavailable, reporting unpriced holdings without it",
			"asset_count", len(ids), "error", err)
		return nil
	}

	out := make(map[string]time.Time, len(resp.Msg.GetStatuses()))
	for _, st := range resp.Msg.GetStatuses() {
		if st.GetEverPriced() || st.GetFirstAskedAt() == nil {
			continue
		}
		out[st.GetAssetId()] = st.GetFirstAskedAt().AsTime()
	}
	return out
}

// pricing is one asset's resolved price: the number, whether it may be used, and
// when it was observed. The observation time travels with the price because the
// caller has to date the total by the oldest quote in it, and by then the price
// row it came from is gone.
type pricing struct {
	unit     decimal.Decimal
	outcome  quoting.Outcome
	quotedAt time.Time // zero when unpriced, or when the quote carried no time
}

// unitPrice returns the per-token price of assetID expressed in quoteAssetID as a
// real-unit decimal (i.e. already divided by the price's decimals).
//
// A position is priced in whatever pair it actually trades in (USDT, RUB, BTC, …); the
// quote currency is not assumed. Among the ways to express it, the FRESHEST wins,
// and every candidate that reports a volume has to clear the market-depth gate.
// Both rules, and why each is the way it is, live in internal/quoting — the
// heatmap resolves its prices through the same code, so the total and the map
// cannot answer under different rules.
//
// A non-nil error signals an unexpected failure, not an unpriced holding.
func (h *Handler) unitPrice(ctx context.Context, assetID, quoteAssetID string) (pricing, error) {
	candidates, missing, err := quoting.Candidates(ctx, h.mdClient, h.log, assetID, quoteAssetID)
	if err != nil {
		return pricing{outcome: quoting.NoQuote}, err
	}
	if len(candidates) == 0 {
		return pricing{outcome: missing}, nil
	}

	if _, thin := quoting.AnyThin(candidates); thin {
		return pricing{outcome: quoting.ThinMarket}, nil
	}

	best := quoting.Freshest(candidates)
	// Dated by the asset's own quote, not by the cross rate. The rate is a
	// currency pair on the same footing as the market-depth gate above: judging
	// it here would let one stale FX row date every position converted through
	// it, which says nothing about whether THIS asset still trades.
	return pricing{unit: best.Unit, outcome: quoting.Priced, quotedAt: pricefresh.QuotedAt(best.Row)}, nil
}

// GetPortfolioPerformance calculates return over a time range using stored price history.
// If no `from` is set, defaults to 30 days ago. Requires marketStore.
func (h *Handler) GetPortfolioPerformance(ctx context.Context, req *connect.Request[apiv1.GetPortfolioPerformanceRequest]) (*connect.Response[apiv1.PortfolioPerformanceResponse], error) {
	if h.mdClient == nil {
		return nil, connect.NewError(connect.CodeUnimplemented, errors.New("market data client not configured"))
	}
	if req.Msg.PortfolioId == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("portfolio_id is required"))
	}

	if _, err := h.ownedPortfolio(ctx, req.Msg.PortfolioId); err != nil {
		return nil, err
	}

	from := time.Now().AddDate(0, 0, -30) // default: 30 days
	if req.Msg.From != nil {
		from = req.Msg.From.AsTime()
	}

	quoteAssetID := pricefresh.PolicyFrom(ctx, h.setClient, h.log).QuoteAsset()
	if req.Msg.BenchmarkAssetId != "" {
		quoteAssetID = req.Msg.BenchmarkAssetId
	}
	quoteAssetID, err := quoting.ResolveQuote(ctx, h.mdClient, quoteAssetID)
	if err != nil {
		return nil, err
	}

	holdings, err := h.allHoldings(ctx, ListHoldingsOpts{PortfolioID: req.Msg.PortfolioId})
	if err != nil {
		return nil, toConnectError(err)
	}

	var currentValue, fromValue decimal.Decimal

	for _, hld := range holdings {
		// Current price
		latestResp, err := h.mdClient.GetLatestPrice(ctx, connect.NewRequest(&apiv1.GetLatestPriceRequest{
			AssetId: hld.AssetID, BaseAssetId: quoteAssetID,
		}))
		if err != nil {
			if connect.CodeOf(err) == connect.CodeNotFound {
				continue
			}
			return nil, err
		}
		latestPrice := latestResp.Msg

		latestLast, err := decimal.NewFromString(latestPrice.Last)
		if err != nil {
			h.log.Warn("skip price with unparseable last", "asset_id", hld.AssetID, "last", latestPrice.Last, "error", err)
			continue
		}

		divisorCurrent := decimal.New(1, decI32(hld.Decimals)+decI32(latestPrice.Decimals))
		holdingCurrent := hld.Amount.
			Mul(latestLast).
			Div(divisorCurrent)
		currentValue = currentValue.Add(holdingCurrent)

		// Historical price (first available at or after `from`)
		pageSize := int32(1)
		histResp, err := h.mdClient.ListPriceHistory(ctx, connect.NewRequest(&apiv1.ListPriceHistoryRequest{
			AssetId: hld.AssetID, BaseAssetId: quoteAssetID,
			From:     timestamppb.New(from),
			PageSize: &pageSize,
		}))
		if err != nil {
			return nil, err
		}
		if len(histResp.Msg.Prices) == 0 {
			// No historical data — use current price as baseline (0% return for this asset)
			fromValue = fromValue.Add(holdingCurrent)
			continue
		}
		fromPrice := histResp.Msg.Prices[0]

		fromLast, err := decimal.NewFromString(fromPrice.Last)
		if err != nil {
			h.log.Warn("skip historical price with unparseable last", "asset_id", hld.AssetID, "last", fromPrice.Last, "error", err)
			fromValue = fromValue.Add(holdingCurrent)
			continue
		}

		divisorFrom := decimal.New(1, decI32(hld.Decimals)+decI32(fromPrice.Decimals))
		holdingFrom := hld.Amount.
			Mul(fromLast).
			Div(divisorFrom)
		fromValue = fromValue.Add(holdingFrom)
	}

	var returnPct float64
	if !fromValue.IsZero() {
		returnPct, _ = currentValue.Sub(fromValue).Div(fromValue).Mul(decimal.NewFromInt(100)).Float64()
	}

	return connect.NewResponse(&apiv1.PortfolioPerformanceResponse{
		PortfolioId:      req.Msg.PortfolioId,
		ReturnPercentage: returnPct,
		// Volatility and SharpeRatio require daily price series — not yet implemented
	}), nil
}

// --- Holding CRUD ---

func (h *Handler) CreateHolding(ctx context.Context, req *connect.Request[apiv1.CreateHoldingRequest]) (*connect.Response[apiv1.Holding], error) {
	if req.Msg.Holding == nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("holding is required"))
	}

	holding, err := holdingFromProto(req.Msg.Holding)
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	if holding.Amount.IsNegative() {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("amount must not be negative"))
	}
	// Provenance is server-stamped: everything created through this RPC is manual.
	holding.Source = entity.SourceManual
	// The target account (and portfolio, when set) must belong to the caller.
	if _, err := h.ownedAccount(ctx, holding.AccountID); err != nil {
		return nil, err
	}
	if holding.PortfolioID != "" {
		if _, err := h.ownedPortfolio(ctx, holding.PortfolioID); err != nil {
			return nil, err
		}
	}
	created, err := h.store.CreateHolding(ctx, holding)
	if err != nil {
		return nil, toConnectError(err)
	}

	return connect.NewResponse(holdingToProto(created)), nil
}

func (h *Handler) GetHolding(ctx context.Context, req *connect.Request[apiv1.GetHoldingRequest]) (*connect.Response[apiv1.Holding], error) {
	if req.Msg.Id == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("holding ID is required"))
	}

	holding, err := h.ownedHolding(ctx, req.Msg.Id)
	if err != nil {
		return nil, err
	}

	return connect.NewResponse(holdingToProto(holding)), nil
}

func (h *Handler) UpdateHolding(ctx context.Context, req *connect.Request[apiv1.UpdateHoldingRequest]) (*connect.Response[apiv1.Holding], error) {
	if req.Msg.Holding == nil || req.Msg.Holding.Id == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("holding with ID is required"))
	}

	existing, err := h.ownedHolding(ctx, req.Msg.Holding.Id)
	if err != nil {
		return nil, err
	}

	fields, err := resolveMask(req.Msg.UpdateMask, holdingUpdatable)
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}

	holding, err := holdingFromProto(req.Msg.Holding)
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	// The destination portfolio must belong to the caller, exactly as it must on
	// create: visibility of a holding is scoped by its portfolio's owner, so an
	// unchecked reassignment would push a row of the caller's choosing —
	// arbitrary asset, arbitrary amount — into a stranger's portfolio. Clearing
	// the field, or naming the portfolio the row already sits in, needs no check.
	if slices.Contains(fields, "portfolio_id") && holding.PortfolioID != "" && holding.PortfolioID != existing.PortfolioID {
		if _, err := h.ownedPortfolio(ctx, holding.PortfolioID); err != nil {
			return nil, err
		}
	}
	updated, err := h.store.UpdateHolding(ctx, holding, fields)
	if err != nil {
		return nil, toConnectError(err)
	}

	return connect.NewResponse(holdingToProto(updated)), nil
}

func (h *Handler) DeleteHolding(ctx context.Context, req *connect.Request[apiv1.DeleteHoldingRequest]) (*connect.Response[emptypb.Empty], error) {
	if req.Msg.Id == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("holding ID is required"))
	}

	if _, err := h.ownedHolding(ctx, req.Msg.Id); err != nil {
		return nil, err
	}

	if err := h.store.DeleteHolding(ctx, req.Msg.Id); err != nil {
		return nil, toConnectError(err)
	}

	return connect.NewResponse(&emptypb.Empty{}), nil
}

func (h *Handler) ListHoldings(ctx context.Context, req *connect.Request[apiv1.ListHoldingsRequest]) (*connect.Response[apiv1.ListHoldingsResponse], error) {
	user, ok := middleware.UserFromContext(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("user not found in context"))
	}

	opts := ListHoldingsOpts{UserID: user.ID}
	if req.Msg.PortfolioId != nil {
		opts.PortfolioID = *req.Msg.PortfolioId
	}
	if req.Msg.AccountId != nil {
		opts.AccountID = *req.Msg.AccountId
	}
	if req.Msg.AssetId != nil {
		opts.AssetID = *req.Msg.AssetId
	}
	if req.Msg.PageSize != nil {
		opts.PageSize = int(*req.Msg.PageSize)
	}
	if req.Msg.PageToken != nil {
		opts.PageToken = *req.Msg.PageToken
	}

	holdings, nextPageToken, err := h.store.ListHoldings(ctx, opts)
	if err != nil {
		return nil, toConnectError(err)
	}

	protoHoldings := make([]*apiv1.Holding, 0, len(holdings))
	for _, h := range holdings {
		protoHoldings = append(protoHoldings, holdingToProto(h))
	}

	return connect.NewResponse(&apiv1.ListHoldingsResponse{
		Holdings:      protoHoldings,
		NextPageToken: nextPageToken,
	}), nil
}

// defaultUnpricedPageSize is what one page of the worklist holds when the caller
// does not say. Larger than the coverage sample on purpose: this endpoint exists
// to be walked, and a small page turns a 71-row tail into many round trips.
const defaultUnpricedPageSize = 100

// maxUnpricedPageSize bounds what a caller can ask for in one response. The cap
// this RPC exists to relieve was a cap with no relief valve; this one has the
// page token.
const maxUnpricedPageSize = 500

// ListUnpricedHoldings walks the positions a valuation leaves out of its total.
//
// Why this is a query and not a bigger array in the coverage block: that block
// rides along on every valuation, called by the FE, the MCP server and the
// sweep, so an unbounded per-holding list there is a response-size problem.
// Capping it was right. Capping it with no way to ask for the rest was not —
// the list is a worklist, every row is an asset to bind, a market to add or a
// verdict to reach, and a tail nobody can request is a tail nobody works.
//
// Unpricedness is not stored anywhere: it is what the pricing path concludes
// about a holding, so it is recomputed here exactly as the valuation computes
// it. That costs a full walk per page and buys the guarantee that this list
// cannot disagree with the total it explains.
//
// Paging therefore filters after reading: the store cannot select "unpriced",
// so each store page is classified and only the survivors fill the response
// page. The token is a holding id — the same cursor the store already orders
// by — which keeps it stable while the answer underneath changes. A holding
// that gets priced between two calls simply stops appearing; a page can come
// back shorter than asked without meaning the walk is over. Only an empty
// next_page_token means that.
func (h *Handler) ListUnpricedHoldings(ctx context.Context, req *connect.Request[apiv1.ListUnpricedHoldingsRequest]) (*connect.Response[apiv1.ListUnpricedHoldingsResponse], error) {
	if h.mdClient == nil {
		return nil, connect.NewError(connect.CodeUnimplemented, errors.New("market data client not configured"))
	}
	user, ok := middleware.UserFromContext(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("user not found in context"))
	}

	opts := ListHoldingsOpts{UserID: user.ID, PageSize: holdingsPageSize}
	if req.Msg.PortfolioId != nil && *req.Msg.PortfolioId != "" {
		// Naming a portfolio asserts ownership of it; leaving it out scopes to
		// everything this user owns, which UserID already does. Checking here
		// keeps a foreign id a NotFound rather than an empty page, which would
		// read as "that portfolio has no gaps".
		if _, err := h.ownedPortfolio(ctx, *req.Msg.PortfolioId); err != nil {
			return nil, err
		}
		opts.PortfolioID = *req.Msg.PortfolioId
	}
	if req.Msg.PageToken != nil {
		opts.PageToken = *req.Msg.PageToken
	}

	want := defaultUnpricedPageSize
	if req.Msg.PageSize != nil && *req.Msg.PageSize > 0 {
		want = int(*req.Msg.PageSize)
		if want > maxUnpricedPageSize {
			want = maxUnpricedPageSize
		}
	}

	quoteAssetID := pricefresh.PolicyFrom(ctx, h.setClient, h.log).QuoteAsset()
	if req.Msg.QuoteAssetId != nil && *req.Msg.QuoteAssetId != "" {
		quoteAssetID = *req.Msg.QuoteAssetId
	}
	quoteAssetID, err := quoting.ResolveQuote(ctx, h.mdClient, quoteAssetID)
	if err != nil {
		return nil, err
	}

	var (
		found []unpricedHolding
		next  string
	)
	for len(found) < want {
		page, nextToken, err := h.store.ListHoldings(ctx, opts)
		if err != nil {
			return nil, toConnectError(err)
		}
		for _, hld := range page {
			// Quarantine is a decision, not a missing price. An excluded holding
			// is out of the total on purpose and is disclosed by its own counter,
			// so mixing it into this worklist would merge two axes the coverage
			// block keeps apart.
			if hld.Excluded {
				continue
			}
			priced, err := h.unitPrice(ctx, hld.AssetID, quoteAssetID)
			if err != nil {
				return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("get price for %s: %w", hld.AssetID, err))
			}
			if priced.outcome == quoting.Priced {
				continue
			}
			// Not filtered here: the reason is not final yet. The pricing path
			// only distinguishes NO_QUOTE from THIN_MARKET, and NEVER_PRICED is
			// a refinement labelUnpriced makes afterwards from the attempt log.
			// Matching against the provisional value made reason=never_priced
			// return nothing at all, and reason=no_quote return rows that are
			// really NEVER_PRICED.
			found = append(found, unpricedHolding{holding: hld, reason: priced.outcome.Reason()})
		}
		if nextToken == "" || len(page) == 0 {
			// The store is exhausted: whatever survived is the end of the walk,
			// unless the trim below has to hold some of it back.
			next = ""
			break
		}
		opts.PageToken = nextToken
		next = nextToken
	}

	// Label before filtering and before trimming, because labelling is what
	// decides a row's final reason: NEVER_PRICED is only known once the attempt
	// log has been consulted. It is one batched call for the whole scan, the
	// same question describeUnpriced asks.
	out := h.labelUnpriced(ctx, found)

	if req.Msg.Reason != nil && *req.Msg.Reason != apiv1.UnpricedReason_UNPRICED_REASON_UNSPECIFIED {
		kept := out[:0]
		for _, u := range out {
			if u.GetReason() == *req.Msg.Reason {
				kept = append(kept, u)
			}
		}
		out = kept
	}

	// Trim to the requested size and resume from the last row actually returned.
	//
	// The cursor names a row that WAS returned, not the first one held back,
	// because the store compares strictly (`h.id > token`): a token naming the
	// next row would skip it, silently dropping exactly the position this
	// endpoint exists to surface. Encoded the way the store encodes its own
	// tokens, since that is what it will decode.
	if len(out) > want {
		out = out[:want]
		next = base64.StdEncoding.EncodeToString([]byte(out[len(out)-1].GetHoldingId()))
	}

	return connect.NewResponse(&apiv1.ListUnpricedHoldingsResponse{
		Holdings:      out,
		NextPageToken: next,
	}), nil
}

// --- Account CRUD ---

func (h *Handler) CreateAccount(ctx context.Context, req *connect.Request[apiv1.CreateAccountRequest]) (*connect.Response[apiv1.Account], error) {
	if req.Msg.Account == nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("account is required"))
	}

	user, ok := middleware.UserFromContext(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("user not found in context"))
	}

	account := accountFromProto(req.Msg.Account)
	account.UserID = user.ID
	for k, v := range account.Data {
		if strings.HasPrefix(v, maskPrefix) {
			return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("data key %q holds a masked value; send the real secret", k))
		}
	}
	if len(account.SystemScopes) > 0 && !user.IsAdmin() {
		return nil, connect.NewError(connect.CodePermissionDenied, errors.New("only admins may set system scopes"))
	}
	created, err := h.store.CreateAccount(ctx, account)
	if err != nil {
		return nil, toConnectError(err)
	}

	return connect.NewResponse(accountToProto(created)), nil
}

func (h *Handler) GetAccount(ctx context.Context, req *connect.Request[apiv1.GetAccountRequest]) (*connect.Response[apiv1.Account], error) {
	if req.Msg.Id == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("account ID is required"))
	}

	account, err := h.ownedAccount(ctx, req.Msg.Id)
	if err != nil {
		return nil, err
	}

	return connect.NewResponse(accountToProto(account)), nil
}

func (h *Handler) UpdateAccount(ctx context.Context, req *connect.Request[apiv1.UpdateAccountRequest]) (*connect.Response[apiv1.Account], error) {
	if req.Msg.Account == nil || req.Msg.Account.Id == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("account with ID is required"))
	}

	existing, err := h.ownedAccount(ctx, req.Msg.Account.Id)
	if err != nil {
		return nil, err
	}

	// system_scopes is only touched when the mask names it, and only by admins.
	fields, err := resolveMask(req.Msg.UpdateMask, accountUpdatable)
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}

	if slices.Contains(fields, "system_scopes") {
		user, ok := middleware.UserFromContext(ctx)
		if !ok {
			return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("user not found in context"))
		}
		if !user.IsAdmin() {
			return nil, connect.NewError(connect.CodePermissionDenied, errors.New("only admins may change system scopes"))
		}
	}

	account := accountFromProto(req.Msg.Account)
	if slices.Contains(fields, "portfolio_id") && account.PortfolioID != "" && account.PortfolioID != existing.PortfolioID {
		if _, err := h.ownedPortfolio(ctx, account.PortfolioID); err != nil {
			return nil, err
		}
	}
	if slices.Contains(fields, "data") {
		if err := restoreMaskedSecrets(account, existing); err != nil {
			return nil, err
		}
	}
	updated, err := h.store.UpdateAccount(ctx, account, fields)
	if err != nil {
		return nil, toConnectError(err)
	}

	return connect.NewResponse(accountToProto(updated)), nil
}

// restoreMaskedSecrets implements write-only secret semantics on update:
// masked incoming values are replaced with the currently stored ones, so
// clients can echo back the masked data map without wiping credentials.
func restoreMaskedSecrets(account, existing *entity.Account) error {
	for k, v := range account.Data {
		if !strings.HasPrefix(v, maskPrefix) {
			continue
		}
		stored, ok := existing.Data[k]
		if !ok {
			return connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("data key %q holds a masked value but no stored secret exists", k))
		}
		account.Data[k] = stored
	}
	return nil
}

func (h *Handler) DeleteAccount(ctx context.Context, req *connect.Request[apiv1.DeleteAccountRequest]) (*connect.Response[emptypb.Empty], error) {
	if req.Msg.Id == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("account ID is required"))
	}

	if _, err := h.ownedAccount(ctx, req.Msg.Id); err != nil {
		return nil, err
	}

	del := h.store.DeleteAccount
	if req.Msg.Cascade {
		del = h.store.DeleteAccountWithHoldings
	}

	if err := del(ctx, req.Msg.Id); err != nil {
		// Holdings and transactions reference the account, so deleting one that
		// still owns rows is refused by the database. "Existing dependencies"
		// does not tell the caller what to clear, so name the positions.
		if errors.Is(err, store.ErrConstraint) {
			// With cascade the holdings are already gone, so whatever remains
			// is transaction history — which this path never removes.
			if req.Msg.Cascade {
				return nil, connect.NewError(connect.CodeFailedPrecondition,
					errors.New("account has transaction history, which is never deleted automatically; remove the transactions first"))
			}
			if n, countErr := h.countHoldings(ctx, req.Msg.Id); countErr == nil && n > 0 {
				return nil, connect.NewError(connect.CodeFailedPrecondition,
					fmt.Errorf("account still holds %d position(s); delete them before deleting the account", n))
			}
			return nil, connect.NewError(connect.CodeFailedPrecondition,
				errors.New("account still has transactions; they must be removed before deleting the account"))
		}
		return nil, toConnectError(err)
	}

	return connect.NewResponse(&emptypb.Empty{}), nil
}

// countHoldings reports how many holdings the account still owns, for error
// messages only — a best-effort count, never a gate on the delete itself.
func (h *Handler) countHoldings(ctx context.Context, accountID string) (int, error) {
	var total int
	pageToken := ""
	for {
		holdings, next, err := h.store.ListHoldings(ctx, ListHoldingsOpts{
			AccountID: accountID,
			PageSize:  100,
			PageToken: pageToken,
		})
		if err != nil {
			return 0, err
		}
		total += len(holdings)
		if next == "" {
			return total, nil
		}
		pageToken = next
	}
}

func (h *Handler) ListAccounts(ctx context.Context, req *connect.Request[apiv1.ListAccountsRequest]) (*connect.Response[apiv1.ListAccountsResponse], error) {
	user, ok := middleware.UserFromContext(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("user not found in context"))
	}

	opts := ListAccountsOpts{UserID: user.ID}
	if req.Msg.UserId != nil && *req.Msg.UserId != "" && user.IsAdmin() {
		opts.UserID = *req.Msg.UserId // explicit override is admin-only
	}
	if req.Msg.Type != nil {
		opts.Type = entity.AccountType(*req.Msg.Type)
	}
	if req.Msg.PageSize != nil {
		opts.PageSize = int(*req.Msg.PageSize)
	}
	if req.Msg.PageToken != nil {
		opts.PageToken = *req.Msg.PageToken
	}

	accounts, nextPageToken, err := h.store.ListAccounts(ctx, opts)
	if err != nil {
		return nil, toConnectError(err)
	}

	protoAccounts := make([]*apiv1.Account, 0, len(accounts))
	for _, a := range accounts {
		protoAccounts = append(protoAccounts, accountToProto(a))
	}

	return connect.NewResponse(&apiv1.ListAccountsResponse{
		Accounts:      protoAccounts,
		NextPageToken: nextPageToken,
	}), nil
}

// --- Account sync ---

// syncedBalance is the provider-agnostic balance shape the upsert path consumes.
// Wallet, exchange and broker syncers all normalize into this.
type syncedBalance struct {
	symbol   string
	name     string
	amount   string // raw integer string scaled by decimals
	decimals int
	// assetType and market classify the position for the catalogue. A ticker is
	// not an identity on its own — (symbol, market, type) is — and until broker
	// positions arrived every synced balance was crypto, so both were implied.
	// GAZP on moex and a GAZP token would otherwise be one asset.
	assetType entity.AssetType
	// market is empty where the type implies one (crypto, forex) and set where
	// it does not: a share, a bond or a fund is listed per venue.
	market string
	// refSource / ref are the source's own id for this thing and the namespace
	// it lives in: "onchain:<chain>" for a token contract, a broker's slug for
	// an instrument id. Both empty means the position is identified by its
	// symbol alone. It is the strongest identity available — a scam clone of a
	// real ticker resolves to its own asset — so it is tried before the symbol.
	refSource string
	ref       string
	chain     string           // network the balance is on; empty off-chain
	liquidity entity.Liquidity // how reachable it is; empty when the source cannot say
	// providerSpam / contractVerified carry a source's identity signals for scam
	// scoring at intake; nil when the source does not report them.
	providerSpam     *bool
	contractVerified *bool
}

// positionKey identifies one row of an account's snapshot.
type positionKey struct {
	assetID string
	chain   string // empty for venues with no chain of their own (exchanges)
	// liquidity is empty unless the adapter partitioned the balance. Staked
	// and liquid value on one chain are different money — one is spendable
	// today and the other is not — so they are different rows.
	liquidity entity.Liquidity
}

// accumulated is the quantity merged onto one positionKey across the addresses
// of a wallet.
type accumulated struct {
	qty      decimal.Decimal // real token quantity, decimals applied
	decimals int             // max decimals seen on this chain → stored scale
	excluded bool            // derived from a scam/impersonation verdict
}

// syncTimeout bounds one SyncAccount call server-side.
//
// A sync is a long operation by nature — it fans out to a provider per chain,
// resolves every balance through MarketData and then rewrites the account's
// positions. Measured on dev 2026-08-02, a heavy EVM account takes ~22s end to
// end, of which the last ~9s are asset resolution and the holdings write.
//
// Until now the handler simply inherited whatever deadline the caller sent.
// That was the browser's blanket 10s fetch timeout, so prod 2026-07-25 logged
// three syncs dying at exactly 10.005s with 'failed to list holdings: context
// canceled' — cancellation landing inside the write, leaving assets created and
// holdings half refreshed. The deadline belongs to the operation, not to
// whoever asked for it; the value is deliberately far above the measurement,
// because what it exists to stop is a provider retry storm, not a slow sync.
const syncTimeout = 3 * time.Minute

// SyncAccount fetches external holdings for a wallet or exchange account and
// upserts assets+holdings.
func (h *Handler) SyncAccount(ctx context.Context, req *connect.Request[apiv1.SyncAccountRequest]) (*connect.Response[apiv1.SyncAccountResponse], error) {
	if req.Msg.AccountId == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("account_id is required"))
	}
	if h.mdClient == nil {
		return nil, connect.NewError(connect.CodeUnimplemented, errors.New("account sync not configured"))
	}

	account, err := h.ownedAccount(ctx, req.Msg.AccountId)
	if err != nil {
		return nil, err
	}

	// From here on the sync runs on the server's clock, not the caller's. See
	// syncTimeout: a client hanging up must not tear a write in progress.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), syncTimeout)
	defer cancel()

	// A broker account carrying only a token is not one account, it is the key
	// to several. It syncs them all rather than refusing: the ids are the
	// broker's to know, and an operator copying them off a phone screen was the
	// only way to learn them until this asked.
	//
	// Fanning out CREATES accounts, it does not merge them. Merging is what the
	// design refused and still refuses: two accounts holding the same share
	// would become one row, and a transfer between them would move no sum —
	// for anything watching, the event would not have happened.
	var out syncOutcome
	if account.Type == entity.AccountTypeBroker && account.Data[entity.BrokerAccountDataKey] == "" {
		out, err = h.syncBrokerAccounts(ctx, account)
	} else {
		out, err = h.syncOneAccount(ctx, account)
	}
	if err != nil {
		return nil, err
	}

	// Price what this sync touched, so CalculatePortfolioValue returns current
	// values. Naming the assets matters: an unfiltered request re-prices the
	// whole catalogue, which on a monthly provider quota costs as much as an
	// hour of the cron sweep and buys nothing the sync changed. One call for
	// the whole fan-out, not one per account: three accounts of the same broker
	// hold the same paper, and asking twice about it spends the plan twice.
	if len(out.syncedAssetIDs) > 0 {
		if _, err := h.mdClient.FetchExternalPrices(ctx, connect.NewRequest(&apiv1.FetchExternalPricesRequest{
			AssetIds: out.syncedAssetIDs,
		})); err != nil {
			h.log.Warn("fetch prices after sync failed", "error", err)
		}
	}

	return connect.NewResponse(&apiv1.SyncAccountResponse{
		AccountId:             req.Msg.AccountId,
		AssetsUpserted:        out.assetsUpserted,
		HoldingsUpserted:      out.holdingsUpserted,
		HoldingsZeroed:        out.holdingsZeroed,
		PositionsSkipped:      intToI32(out.skips.Total()),
		AssetsDefaultedMarket: intToI32(out.skips.DefaultedMarket),
		AccountsCreated:       out.accountsCreated,
		Errors:                out.errors,
	}), nil
}

// syncOutcome is what one account's sync contributed, in the shape the response
// reports. A fan-out sums several of them; a plain sync has exactly one.
type syncOutcome struct {
	assetsUpserted   int32
	holdingsUpserted int32
	holdingsZeroed   int32
	accountsCreated  int32
	skips            entity.BrokerSkips
	syncedAssetIDs   []string
	errors           []string
}

// merge folds one account's outcome into the aggregate, naming the account on
// every error it carries. Without the name, three accounts syncing together
// produce a list of errors nobody can attribute — and attributing them is the
// whole reason the accounts stayed separate.
func (o *syncOutcome) merge(other syncOutcome, accountName string) {
	o.assetsUpserted += other.assetsUpserted
	o.holdingsUpserted += other.holdingsUpserted
	o.holdingsZeroed += other.holdingsZeroed
	o.accountsCreated += other.accountsCreated
	o.skips.UnknownInstrument += other.skips.UnknownInstrument
	o.skips.UnknownMarket += other.skips.UnknownMarket
	o.skips.Unparsable += other.skips.Unparsable
	o.skips.DefaultedMarket += other.skips.DefaultedMarket
	o.syncedAssetIDs = append(o.syncedAssetIDs, other.syncedAssetIDs...)
	for _, e := range other.errors {
		o.errors = append(o.errors, fmt.Sprintf("%s: %s", accountName, e))
	}
}

// syncOneAccount fetches one account's positions and writes them. It does not
// price: a fan-out prices once for everything it touched.
func (h *Handler) syncOneAccount(ctx context.Context, account *entity.Account) (syncOutcome, error) {
	var (
		balances   []syncedBalance
		syncErrors []string
		skips      entity.BrokerSkips
		err        error
	)
	switch account.Type {
	case entity.AccountTypeWallet:
		balances, syncErrors, err = h.syncWalletBalances(ctx, account)
	case entity.AccountTypeExchange:
		balances, syncErrors, err = h.syncExchangeBalances(ctx, account)
	case entity.AccountTypeBroker:
		balances, skips, syncErrors, err = h.syncBrokerBalances(ctx, account)
	case entity.AccountTypeManual:
		// FailedPrecondition (not InvalidArgument): the account exists and is fine,
		// this kind is just never syncable — positions are entered manually.
		return syncOutcome{}, connect.NewError(connect.CodeFailedPrecondition, errors.New("manual account has nothing to sync: positions are entered manually"))
	default:
		return syncOutcome{}, connect.NewError(connect.CodeInvalidArgument, errors.New("only wallet, exchange and broker accounts can be synced"))
	}
	if err != nil {
		return syncOutcome{}, err
	}

	// Whether the fetch stage came back whole decides if this snapshot may
	// remove positions: a chain that failed reports no balances, which is
	// indistinguishable from a chain whose balances are gone.
	//
	// A skipped position counts the same way, and deliberately so. It is a
	// position the source DID report and this system could not name, so the
	// snapshot does not speak for it — and a holding written for it by an
	// earlier sync (paper the catalogue has since dropped is the live case)
	// would otherwise be zeroed while it is still held. That is lying in the
	// minus, which is the one direction this system refuses. The price is that
	// an account holding one unnameable position never sheds a sold one until
	// it is repaired; the count in the response is what makes that visible
	// rather than silent.
	result, err := h.upsertSyncedBalances(ctx, account, balances, len(syncErrors) == 0 && skips.Total() == 0)
	if err != nil {
		return syncOutcome{}, err
	}

	// Skips reach the caller as a count; the log is where the operator looking
	// at a total that seems short will actually be. Four indistinguishable
	// silent branches cost seventeen days of blindness on prod once already
	// (personal-1y6i), and a count with no account or provider beside it is the
	// same shape of silence one level up.
	if skips.Total() > 0 || skips.DefaultedMarket > 0 {
		h.log.Warn("broker sync did not account for every position",
			"account_id", account.ID,
			"provider", account.Data[providerDataKey],
			"unknown_instrument", skips.UnknownInstrument,
			"unknown_market", skips.UnknownMarket,
			"unparsable", skips.Unparsable,
			"defaulted_market", skips.DefaultedMarket,
			"removal_withheld", skips.Total() > 0)
	}

	return syncOutcome{
		assetsUpserted:   result.assetsUpserted,
		holdingsUpserted: result.holdingsUpserted,
		holdingsZeroed:   result.holdingsZeroed,
		skips:            skips,
		syncedAssetIDs:   result.syncedAssetIDs,
		errors:           append(syncErrors, result.syncErrors...),
	}, nil
}

// syncWalletBalances resolves the wallet syncer for the account owner and
// returns its balances normalized to syncedBalance. A partial failure surfaces
// as a sync error string, not a hard error.
func (h *Handler) syncWalletBalances(ctx context.Context, account *entity.Account) ([]syncedBalance, []string, error) {
	// An account normally holds one address, but UTXO chains spread a wallet
	// over many, so "addresses" accepts a list. Every address on an account
	// belongs to the same ecosystem: the syncer is resolved once, from the
	// first, and reused for the rest.
	addresses := splitAddresses(account.Data["addresses"])
	if len(addresses) == 0 {
		if single := strings.TrimSpace(account.Data["address"]); single != "" {
			addresses = []string{single}
		}
	}
	if len(addresses) == 0 {
		return nil, nil, connect.NewError(connect.CodeInvalidArgument,
			errors.New("account.data.address or account.data.addresses is required for wallet sync"))
	}
	address := addresses[0]

	// Resolve which chains to sync from the account config.
	// Empty or "auto" → let the syncer auto-discover (pass nil).
	// Otherwise a comma-separated list: "eth,base,arbitrum".
	var chains []string
	if chainRaw := strings.TrimSpace(account.Data["chain"]); chainRaw != "" && chainRaw != "auto" {
		chains = splitChains(chainRaw)
	}

	// Resolve the syncer routed by the account's chains: each provider covers
	// one ecosystem, so a Substrate account must not land on an EVM syncer.
	// A wired source is the sole authority — it knows every provider's chains
	// and carries the env-configured syncer as its own fallback. The statically
	// wired syncer only serves setups without a source (tests, minimal configs)
	// and is assumed EVM-only.
	walletSyncer := h.walletSyncer
	if h.syncerSource != nil {
		resolved, err := h.syncerSource.WalletSyncerFor(ctx, account.UserID, address, chains)
		if err != nil {
			return nil, nil, toConnectError(err)
		}
		walletSyncer = resolved
	}
	if walletSyncer == nil {
		if len(chains) > 0 {
			return nil, nil, connect.NewError(connect.CodeUnimplemented,
				fmt.Errorf("no wallet syncer configured for chain(s) %s", strings.Join(chains, ",")))
		}
		return nil, nil, connect.NewError(connect.CodeUnimplemented, errors.New("wallet sync not configured"))
	}

	// The syncer owns all provider mechanics (discovery, fan-out, native vs token).
	// Partial failures arrive as a joined error alongside the balances gathered so far.
	// Balances from several addresses are concatenated here and merged
	// downstream per (asset, chain), so a wallet split across addresses reports
	// one holding per asset per chain — several addresses on one chain are one
	// place, two chains are not.
	var (
		syncErrors []string
		balances   []entity.WalletBalance
	)
	for _, addr := range addresses {
		got, err := walletSyncer.SyncWallet(ctx, addr, chains)
		if err != nil {
			// Name the address: with several in play, an unqualified provider
			// error says nothing about which part of the wallet went missing.
			if len(addresses) > 1 {
				syncErrors = append(syncErrors, fmt.Sprintf("%s: %s", addr, err.Error()))
			} else {
				syncErrors = append(syncErrors, err.Error())
			}
		}
		balances = append(balances, got...)
	}

	result := make([]syncedBalance, 0, len(balances))
	for _, b := range balances {
		sb := syncedBalance{
			symbol: b.Symbol,
			name:   b.Name,
			amount: b.Amount,
			// A chain reader returns tokens and native coins, and both are
			// crypto: the market that implies ("crypto") is global, so no
			// venue is named here.
			assetType:        entity.AssetTypeCryptocurrency,
			decimals:         b.Decimals,
			chain:            b.Chain,
			liquidity:        b.Liquidity,
			providerSpam:     b.ProviderSpam,
			contractVerified: b.ContractVerified,
		}
		// A contract is an identity only together with its chain: the same
		// address on two networks is two assets.
		if b.ContractAddress != "" && b.Chain != "" {
			sb.refSource = entity.OnchainSource(b.Chain)
			sb.ref = b.ContractAddress
		}
		result = append(result, sb)
	}
	return result, syncErrors, nil
}

// syncExchangeBalances builds the exchange syncer from the account's own stored
// credentials and returns its balances normalized to syncedBalance.
func (h *Handler) syncExchangeBalances(ctx context.Context, account *entity.Account) ([]syncedBalance, []string, error) {
	if h.exchangeSource == nil {
		return nil, nil, connect.NewError(connect.CodeUnimplemented, errors.New("exchange sync not configured"))
	}
	syncer, err := h.exchangeSource.ExchangeSyncerForAccount(account)
	if err != nil {
		return nil, nil, toConnectError(err)
	}
	if syncer == nil {
		return nil, nil, connect.NewError(connect.CodeInvalidArgument, errors.New("no exchange adapter for account provider; set account.data.provider (e.g. \"binance\")"))
	}

	var syncErrors []string
	balances, err := syncer.SyncExchange(ctx)
	if err != nil {
		syncErrors = append(syncErrors, err.Error())
	}

	result := make([]syncedBalance, 0, len(balances))
	for _, b := range balances {
		result = append(result, syncedBalance{
			symbol:   b.Symbol,
			name:     b.Symbol, // exchanges report only the ticker; use it as the name
			amount:   b.Amount,
			decimals: b.Decimals,
			// Spot balances on a crypto exchange, and the adapter reports no
			// identity beyond the ticker.
			assetType: entity.AssetTypeCryptocurrency,
		})
	}
	return result, syncErrors, nil
}

// syncBrokerBalances builds the broker syncer from the account's own stored
// credentials and returns the positions of the ONE broker account it names.
//
// The skips come back beside the balances rather than folded into the error
// strings: they are not failures of this sync, they are positions the source
// returned and this system could not name. The caller reports them as a count,
// and — for the ones it could not account for at all — refuses to treat the
// snapshot as complete.
func (h *Handler) syncBrokerBalances(ctx context.Context, account *entity.Account) ([]syncedBalance, entity.BrokerSkips, []string, error) {
	var skips entity.BrokerSkips
	if h.brokerSource == nil {
		return nil, skips, nil, connect.NewError(connect.CodeUnimplemented, errors.New("broker sync not configured"))
	}
	syncer, err := h.brokerSource.BrokerSyncerForAccount(account)
	if err != nil {
		// Every way this factory fails today is the account's own stored
		// configuration: no broker account named, a malformed host, a missing
		// trust anchor. toConnectError's fallback is Internal, which tells the
		// caller "server fault, retry" about the one class of problem retrying
		// cannot fix — and gets counted as a 5xx by anything watching the RPC.
		// The message is kept: it names the field to fix, which is the whole
		// point of refusing here instead of later inside TLS.
		return nil, skips, nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	if syncer == nil {
		return nil, skips, nil, connect.NewError(connect.CodeInvalidArgument, errors.New("no broker adapter for account provider; set account.data.provider (e.g. \"tinvest\")"))
	}

	var syncErrors []string
	positions, skips, err := syncer.SyncBroker(ctx)
	if err != nil {
		syncErrors = append(syncErrors, err.Error())
	}

	// The namespace an instrument id lives in is the provider that issued it:
	// two brokers may spell the same paper differently, and a ref is only ever
	// resolved against the source that wrote it. The adapter returns the id
	// alone because it does not know which account slug it was built for.
	refSource := strings.TrimSpace(account.Data[providerDataKey])

	result := make([]syncedBalance, 0, len(positions))
	for _, p := range positions {
		sb := syncedBalance{
			symbol:    p.Symbol,
			name:      p.Name,
			amount:    p.Amount,
			decimals:  p.Decimals,
			assetType: p.Type,
			market:    p.Market,
			liquidity: p.Liquidity,
		}
		// Cash carries no ref by design: a currency line's instrument id names
		// a settlement contract, not the currency, so binding by it would tie
		// dollars to whatever the broker settles them through.
		if p.Ref != "" && refSource != "" {
			sb.refSource = refSource
			sb.ref = p.Ref
		}
		result = append(result, sb)
	}
	return result, skips, syncErrors, nil
}

// upsertSyncedBalances resolves each balance to an asset and upserts holdings
// for the account. Resolution is by contract identity (external ref) first and
// by symbol otherwise, so a scam clone of a real ticker resolves to its own
// asset while cross-chain instances of the same asset collapse onto one
// asset_id.
//
// Positions are keyed by (asset, chain, liquidity), not by asset alone.
// Collapsing chains destroyed the only copy of where an amount sits: USDC on
// Base and USDC on Arbitrum became one row, summed across mismatched decimals
// (USDC is 6 on Ethereum and 18 on BSC) and stored at the largest scale seen.
// Liquidity is the same argument in time rather than space: staked ATOM and
// bank-balance ATOM are one asset on one chain, but only one of them can be
// spent today. Within one chain and one liquidity state, several addresses of
// the same wallet still merge — that is the same place, so quantities are
// summed as real amounts (raw / 10^decimals).
//
// syncedAssetIDs lists the assets whose holdings this sync actually wrote. It
// is what the caller prices afterwards: a provider quota is monthly, and
// re-pricing the whole catalogue on every sync spends it on data the sync did
// not touch.
//
// complete says the fetch stage returned the whole account: every address and
// every chain answered. Only a complete snapshot may zero the positions it no
// longer contains — see writeSyncedHoldings.
func (h *Handler) upsertSyncedBalances(ctx context.Context, account *entity.Account, balances []syncedBalance, complete bool) (syncResult, error) {
	var (
		assetsUpserted int32
		syncErrors     []string
	)
	byPosition := make(map[positionKey]*accumulated)
	// Insertion order, so a resync writes rows in a stable order and the legacy
	// row below is adopted by the first chain seen rather than by map luck.
	var order []positionKey

	for _, b := range balances {
		amt, ok := new(big.Int).SetString(strings.TrimSpace(b.amount), 10)
		if !ok {
			syncErrors = append(syncErrors, fmt.Sprintf("parse amount %q for %s", b.amount, b.symbol))
			continue
		}
		if amt.Sign() == 0 {
			continue
		}
		// A balance with no symbol at all is unidentifiable: the catalogue is
		// still asked, because a ref it already knows names the position on its
		// own, but if that lookup fails there is nothing left to call the position
		// and nothing to key it by. Dropping it is a filter, not a failed
		// observation — it costs the snapshot no right to remove positions.
		// Counting it as a failure did, and one such balance on dev's 'hot' wallet
		// kept the account from ever shedding a position sold in July.
		unnamed := strings.TrimSpace(b.symbol) == ""
		if unnamed && strings.TrimSpace(b.ref) == "" {
			h.log.Warn("skipping unidentifiable balance", "chain", b.chain, "amount", b.amount)
			continue
		}

		assetID, created, verdict, rerr := h.resolveSyncedAsset(ctx, b)
		if rerr != nil {
			if unnamed {
				// Named by neither symbol nor a ref the catalogue knows. The
				// residual risk is a pre-external-ref asset whose token stopped
				// reporting its symbol: it would resolve by symbol and no longer
				// can, so its row may be zeroed while still held.
				h.log.Warn("skipping unidentifiable balance",
					"chain", b.chain, "ref_source", b.refSource, "ref", b.ref, "amount", b.amount, "error", rerr)
				continue
			}
			syncErrors = append(syncErrors, fmt.Sprintf("resolve asset %s: %v", b.symbol, rerr))
			continue
		}
		if created {
			assetsUpserted++
		}

		qty := decimal.NewFromBigInt(amt, -intToI32(b.decimals)) // raw / 10^decimals
		key := positionKey{assetID: assetID, chain: b.chain, liquidity: b.liquidity}
		if entry, ok := byPosition[key]; ok {
			entry.qty = entry.qty.Add(qty)
			if b.decimals > entry.decimals {
				entry.decimals = b.decimals
			}
			entry.excluded = entry.excluded || isQuarantineVerdict(verdict)
		} else {
			byPosition[key] = &accumulated{
				qty:      qty,
				decimals: b.decimals,
				excluded: isQuarantineVerdict(verdict),
			}
			order = append(order, key)
		}
	}

	// A balance that failed to parse or to resolve is a position this snapshot
	// cannot speak for, so the snapshot is no longer whole.
	complete = complete && len(syncErrors) == 0

	// Build existing holdings map for this account
	// Every existing row, not one page: a holding missed here is not adopted but
	// created again, so an unpaged read turns into a duplicated position rather
	// than a missing one.
	existingHoldings, err := h.allHoldings(ctx, ListHoldingsOpts{AccountID: account.ID})
	if err != nil {
		return syncResult{}, toConnectError(err)
	}
	holdingByPosition := make(map[positionKey]*entity.Holding, len(existingHoldings))
	// Rows written before positions carried a chain or a liquidity state: one
	// summed row per asset, both fields empty. The first position of that asset
	// adopts the row instead of leaving it beside the new ones, where it would
	// double the holding until someone deleted it by hand. Adoption keeps the
	// row's id, provenance, portfolio assignment and manual excluded override.
	//
	// Provenance is deliberately NOT a filter here. Before positions had
	// dimensions, sync updated whatever row it found for the asset — including
	// an imported one — so an imported ATOM position was refreshed in place.
	// Adopting only sync-written rows looked tidier and doubled that position on
	// the first resync: caught on dev, where an llm_import row of 66.54 ATOM sat
	// beside a freshly synced 21.84 liquid + 45.00 staked.
	legacyByAsset := make(map[string][]*entity.Holding)
	for _, hld := range existingHoldings {
		holdingByPosition[positionKey{assetID: hld.AssetID, chain: hld.Chain, liquidity: hld.Liquidity}] = hld
		if hld.Chain == "" && hld.Liquidity == "" {
			legacyByAsset[hld.AssetID] = append(legacyByAsset[hld.AssetID], hld)
		}
	}

	// An empty result from a provider that reported no error is the one case
	// where "gone" and "not answered" are impossible to tell apart, and reading
	// it as an emptied account would wipe the whole snapshot in one sync. Say so
	// instead: the rows survive, and the caller learns why they were left alone.
	if complete && len(order) == 0 {
		if untouched := countSyncWritten(existingHoldings); untouched > 0 {
			complete = false
			syncErrors = append(syncErrors, fmt.Sprintf(
				"provider returned no positions: %d synced holding(s) left untouched rather than zeroed", untouched))
		}
	}

	// Every row of the new snapshot is written in one transaction. A sync does
	// not add positions, it replaces the picture of an account, and half a
	// picture is not a smaller truth — it is a total that looks whole and is
	// wrong. A row that fails therefore aborts the set instead of being
	// collected into syncErrors: the account keeps the snapshot it had, which
	// is coherent, rather than a mix of two syncs.
	plan := writePlan{
		order:              order,
		byPosition:         byPosition,
		holdingByPosition:  holdingByPosition,
		legacyByAsset:      legacyByAsset,
		existing:           existingHoldings,
		zeroVanished:       complete,
		accountID:          account.ID,
		defaultPortfolioID: account.PortfolioID,
	}
	result := syncResult{assetsUpserted: assetsUpserted, syncErrors: syncErrors}
	err = h.store.InHoldingsTx(ctx, func(w HoldingWriter) error {
		written, werr := h.writeSyncedHoldings(ctx, w, plan)
		result.holdingsUpserted = written.holdingsUpserted
		result.holdingsZeroed = written.holdingsZeroed
		result.syncedAssetIDs = written.syncedAssetIDs
		return werr
	})
	if err != nil {
		return syncResult{}, toConnectError(err)
	}

	return result, nil
}

// syncResult is what one SyncAccount write produced, reported back to the
// caller: positions written, positions zeroed because the provider no longer
// reports them, the assets to re-price, and the non-fatal errors met on the way.
type syncResult struct {
	assetsUpserted   int32
	holdingsUpserted int32
	holdingsZeroed   int32
	syncedAssetIDs   []string
	syncErrors       []string
}

// countSyncWritten counts the rows a sync is allowed to zero: the ones a sync
// wrote in the first place.
func countSyncWritten(holdings []*entity.Holding) int {
	n := 0
	for _, hld := range holdings {
		if hld.Source == entity.SourceSync && !hld.Amount.IsZero() {
			n++
		}
	}
	return n
}

// writePlan is what upsertSyncedBalances resolved before touching the database:
// the positions to write, in insertion order, and the rows already on the
// account that they map onto.
type writePlan struct {
	order             []positionKey
	byPosition        map[positionKey]*accumulated
	holdingByPosition map[positionKey]*entity.Holding
	legacyByAsset     map[string][]*entity.Holding
	// existing is every row the account already had, needed whole because the
	// rows this snapshot does NOT contain are as much a part of it as the ones
	// it does.
	existing []*entity.Holding
	// zeroVanished says the snapshot is trustworthy enough to remove with: a
	// complete fetch, no unresolved balance, not empty. See writeSyncedHoldings.
	zeroVanished       bool
	accountID          string
	defaultPortfolioID string
}

// writtenSnapshot is what one transactional write of a snapshot produced.
type writtenSnapshot struct {
	holdingsUpserted int32
	holdingsZeroed   int32
	syncedAssetIDs   []string
}

// writeSyncedHoldings applies the plan through w, which is transactional: it
// runs whole or not at all. A retried transaction re-runs it from the plan, so
// it must not mutate one.
func (h *Handler) writeSyncedHoldings(ctx context.Context, w HoldingWriter, plan writePlan) (written writtenSnapshot, err error) {
	// Adoption consumes from legacyByAsset, so a retried transaction must start
	// from the same list rather than one the previous attempt already drained.
	legacyByAsset := make(map[string][]*entity.Holding, len(plan.legacyByAsset))
	for assetID, rows := range plan.legacyByAsset {
		legacyByAsset[assetID] = slices.Clone(rows)
	}

	// Which existing rows this snapshot spoke for. Adoption decides some of them
	// here rather than in the plan, so the vanished set can only be known after
	// the write loop has run.
	refreshed := make(map[string]struct{}, len(plan.existing))

	for _, key := range plan.order {
		entry := plan.byPosition[key]
		assetID := key.assetID
		decimals := intToU32(entry.decimals)
		// holdings.amount is NUMERIC: store the merged quantity as a raw integer at the
		// holding's decimals scale (exact — qty has at most `decimals` fractional digits).
		amount := entry.qty.Shift(intToI32(entry.decimals))

		existing, ok := plan.holdingByPosition[key]
		adopted := false
		if !ok && (key.chain != "" || key.liquidity != "") {
			if legacy := legacyByAsset[assetID]; len(legacy) > 0 {
				existing, ok, adopted = legacy[0], true, true
				legacyByAsset[assetID] = legacy[1:]
			}
		}

		if ok {
			// Update existing holding: only refresh amount/decimals (and the
			// chain when adopting a pre-chain row); never touch portfolio assignment.
			existing.Amount = amount
			existing.Decimals = decimals
			fields := []string{"amount", "decimals"}
			if adopted {
				existing.Chain = key.chain
				existing.Liquidity = key.liquidity
				fields = append(fields, "chain", "liquidity")
			}
			// A verdict that quarantines reaches rows that already exist, not
			// only new ones: an impostor is usually unmasked after its position
			// has been syncing for a while (personal-go65 — the ticker collision
			// is only visible once the real asset is listed). The flag is raised,
			// never lowered, so a user's own exclusion survives a resync just as
			// it did before.
			if entry.excluded && !existing.Excluded {
				existing.Excluded = true
				fields = append(fields, "excluded")
			}
			if _, err := w.UpdateHolding(ctx, existing, fields); err != nil {
				return writtenSnapshot{}, fmt.Errorf("update holding for asset %s: %w", assetID, err)
			}
			refreshed[existing.ID] = struct{}{}
		} else {
			// Create new holding; inherit account's default portfolio if configured.
			// A scam/impersonation verdict on the asset excludes the new holding
			// from the sums; the position still syncs (no frozen holding), it is
			// just quarantined. An existing holding's excluded flag is left alone
			// on update so a user's manual override survives resync.
			_, err := w.CreateHolding(ctx, &entity.Holding{
				AssetID:     assetID,
				AccountID:   plan.accountID,
				PortfolioID: plan.defaultPortfolioID,
				Amount:      amount,
				Decimals:    decimals,
				Chain:       key.chain,
				Liquidity:   key.liquidity,
				Source:      entity.SourceSync,
				Excluded:    entry.excluded,
			})
			if err != nil {
				return writtenSnapshot{}, fmt.Errorf("create holding for asset %s: %w", assetID, err)
			}
		}
		written.holdingsUpserted++
		written.syncedAssetIDs = append(written.syncedAssetIDs, assetID)
	}

	// A sync is a snapshot, not an append. Until now it only ever wrote the rows
	// the provider returned, so a position that stopped being returned — sold,
	// moved out, or newly rejected by a spam filter — kept its last amount
	// forever. That is worse than never filtering it: an untouched row looks like
	// a live position that merely stopped moving, and nothing distinguishes the
	// two. Observed on dev 2026-08-02: 563.99 USDT sold on 2026-07-07 still
	// counted as $564 of a $6917 portfolio, and no amount of resyncing removed it.
	//
	// Zero rather than delete: the row keeps its id, provenance and history, falls
	// out of every sum, and a later sync that sees the position again refreshes it
	// in place. Only rows this sync's own kind wrote are eligible — an imported or
	// manual position is the user's claim, not the provider's to erase.
	//
	// plan.zeroVanished is the guard the whole pass hangs on: a failed chain, an
	// unresolved balance or an empty result must never be read as an emptied
	// wallet. See upsertSyncedBalances.
	if plan.zeroVanished {
		for _, hld := range plan.existing {
			if _, ok := refreshed[hld.ID]; ok {
				continue
			}
			if hld.Source != entity.SourceSync || hld.Amount.IsZero() {
				continue
			}
			hld.Amount = decimal.Zero
			if _, err := w.UpdateHolding(ctx, hld, []string{"amount"}); err != nil {
				return writtenSnapshot{}, fmt.Errorf("zero vanished holding %s: %w", hld.ID, err)
			}
			// Deliberately not added to syncedAssetIDs: a zero position needs no
			// price, and the provider quota is monthly.
			written.holdingsZeroed++
		}
	}

	return written, nil
}

// resolveSyncedAsset resolves (creating when needed) the asset for one synced
// balance through the MarketData service. A balance that carries an external
// ref is resolved by that identity first, so a token is matched by its contract
// before its symbol and cross-chain instances of the same asset collapse onto
// one asset_id. A broker instrument is the same mechanism in a different
// namespace: the FIGI is the only honest identity a broker line has, since its
// ticker field frequently holds an ISIN.
//
// Type and market come from the balance rather than being assumed. They were
// hardcoded to cryptocurrency while every source was a chain or an exchange,
// and a share resolved that way would be created — or found — as a crypto asset
// with the same ticker.
func (h *Handler) resolveSyncedAsset(ctx context.Context, b syncedBalance) (assetID string, created bool, verdict string, err error) {
	msg := &apiv1.FindOrCreateAssetRequest{
		Symbol: b.symbol,
		// The enum values are shared with entity.AssetType by construction; see
		// the conversion in marketdata's handler.
		Type:             apiv1.AssetType(b.assetType),
		ProviderSpam:     b.providerSpam,
		ContractVerified: b.contractVerified,
	}
	if b.name != "" {
		msg.Name = &b.name
	}
	// Sent when the source named one, and only then. A chain reader names none,
	// so MarketData derives "crypto" as it always did; a broker names one on
	// every line, including cash — where it spells out the same "forex" the
	// type implies. What must never happen is this end inventing a venue: a
	// stock, a bond or a fund has no implied market and is refused without one,
	// which is the refusal that keeps a guessed listing out of the catalogue.
	if b.market != "" {
		market := b.market
		msg.Market = &market
	}
	if b.refSource != "" && b.ref != "" {
		source, ref := b.refSource, b.ref
		msg.ExternalRefSource = &source
		msg.ExternalRef = &ref
	}

	resp, err := h.mdClient.FindOrCreateAsset(ctx, connect.NewRequest(msg))
	if err != nil {
		return "", false, "", err
	}
	if resp.Msg.Asset == nil {
		return "", false, "", fmt.Errorf("marketdata returned no asset for %s", b.symbol)
	}
	return resp.Msg.Asset.Id, resp.Msg.Created, resp.Msg.Asset.GetIdentityVerdict(), nil
}

// isQuarantineVerdict reports whether an identity verdict excludes a synced
// holding from the sums: a scam or an impersonation is not the user's money to
// count, while a real asset's situational risk (a separate axis) never excludes.
func isQuarantineVerdict(verdict string) bool {
	return verdict == string(scamfilter.VerdictScam) ||
		verdict == string(scamfilter.VerdictImpersonation)
}

// --- Transaction CRUD ---

func (h *Handler) CreateTransaction(ctx context.Context, req *connect.Request[apiv1.CreateTransactionRequest]) (*connect.Response[apiv1.Transaction], error) {
	if req.Msg.Transaction == nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("transaction is required"))
	}

	tx := transactionFromProto(req.Msg.Transaction)
	// Provenance is server-stamped: everything created through this RPC is manual.
	tx.Source = entity.SourceManual
	// The target account must belong to the caller.
	if _, err := h.ownedAccount(ctx, tx.AccountID); err != nil {
		return nil, err
	}
	created, err := h.store.CreateTransaction(ctx, tx)
	if err != nil {
		return nil, toConnectError(err)
	}

	return connect.NewResponse(transactionToProto(created)), nil
}

func (h *Handler) GetTransaction(ctx context.Context, req *connect.Request[apiv1.GetTransactionRequest]) (*connect.Response[apiv1.Transaction], error) {
	if req.Msg.Id == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("transaction ID is required"))
	}

	tx, err := h.ownedTransaction(ctx, req.Msg.Id)
	if err != nil {
		return nil, err
	}

	return connect.NewResponse(transactionToProto(tx)), nil
}

func (h *Handler) UpdateTransaction(ctx context.Context, req *connect.Request[apiv1.UpdateTransactionRequest]) (*connect.Response[apiv1.Transaction], error) {
	if req.Msg.Transaction == nil || req.Msg.Transaction.Id == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("transaction with ID is required"))
	}

	if _, err := h.ownedTransaction(ctx, req.Msg.Transaction.Id); err != nil {
		return nil, err
	}

	fields, err := resolveMask(req.Msg.UpdateMask, transactionUpdatable)
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}

	tx := transactionFromProto(req.Msg.Transaction)
	updated, err := h.store.UpdateTransaction(ctx, tx, fields)
	if err != nil {
		return nil, toConnectError(err)
	}

	return connect.NewResponse(transactionToProto(updated)), nil
}

func (h *Handler) ListTransactions(ctx context.Context, req *connect.Request[apiv1.ListTransactionsRequest]) (*connect.Response[apiv1.ListTransactionsResponse], error) {
	user, ok := middleware.UserFromContext(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("user not found in context"))
	}

	// Admins see everything; everyone else only their own accounts' transactions.
	opts := ListTransactionsOpts{}
	if !user.IsAdmin() {
		opts.UserID = user.ID
	}
	if req.Msg.AccountId != nil {
		opts.AccountID = *req.Msg.AccountId
	}
	if req.Msg.Type != nil {
		opts.Type = entity.TransactionType(*req.Msg.Type)
	}
	if req.Msg.Status != nil {
		opts.Status = entity.TransactionStatus(*req.Msg.Status)
	}
	if req.Msg.PageSize != nil {
		opts.PageSize = int(*req.Msg.PageSize)
	}
	if req.Msg.PageToken != nil {
		opts.PageToken = *req.Msg.PageToken
	}

	transactions, nextPageToken, err := h.store.ListTransactions(ctx, opts)
	if err != nil {
		return nil, toConnectError(err)
	}

	protoTransactions := make([]*apiv1.Transaction, 0, len(transactions))
	for _, t := range transactions {
		protoTransactions = append(protoTransactions, transactionToProto(t))
	}

	return connect.NewResponse(&apiv1.ListTransactionsResponse{
		Transactions:  protoTransactions,
		NextPageToken: nextPageToken,
	}), nil
}

// --- Converters ---

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

func portfolioFromProto(p *apiv1.Portfolio) *entity.Portfolio {
	result := &entity.Portfolio{
		ID:     p.Id,
		UserID: p.UserId,
		Name:   p.Name,
	}
	if p.Description != nil {
		result.Description = *p.Description
	}
	if p.Data != nil {
		result.Data = make(map[string]json.RawMessage)
		for k, v := range p.Data {
			if v != nil {
				result.Data[k] = v.Value
			}
		}
	}
	return result
}

func portfolioToProto(p *entity.Portfolio) *apiv1.Portfolio {
	result := &apiv1.Portfolio{
		Id:        p.ID,
		UserId:    p.UserID,
		Name:      p.Name,
		CreatedAt: timestamppb.New(p.CreatedAt),
		UpdatedAt: timestamppb.New(p.UpdatedAt),
	}
	if p.Description != "" {
		result.Description = &p.Description
	}
	if p.Data != nil {
		result.Data = make(map[string]*anypb.Any)
		for k, v := range p.Data {
			result.Data[k] = &anypb.Any{Value: v}
		}
	}
	return result
}

func holdingFromProto(h *apiv1.Holding) (*entity.Holding, error) {
	// Empty amount is treated as unset (zero) so partial updates that omit it still work;
	// a non-empty but malformed amount is rejected rather than silently coerced to zero.
	amount := decimal.Zero
	if h.Amount != "" {
		var err error
		amount, err = decimal.NewFromString(h.Amount)
		if err != nil {
			return nil, fmt.Errorf("invalid amount %q: %w", h.Amount, err)
		}
	}
	// Provenance fields (source, import_id) are deliberately not mapped:
	// they are output-only and stamped by the server.
	result := &entity.Holding{
		ID:        h.Id,
		Amount:    amount,
		Decimals:  h.Decimals,
		AssetID:   h.AssetId,
		AccountID: h.AccountId,
		Chain:     h.Chain,
		Liquidity: entity.Liquidity(h.Liquidity),
		Excluded:  h.Excluded,
	}
	if h.PortfolioId != nil {
		result.PortfolioID = *h.PortfolioId
	}
	return result, nil
}

func holdingToProto(h *entity.Holding) *apiv1.Holding {
	result := &apiv1.Holding{
		Id:        h.ID,
		Amount:    h.Amount.String(),
		Decimals:  h.Decimals,
		AssetId:   h.AssetID,
		AccountId: h.AccountID,
		Chain:     h.Chain,
		Liquidity: string(h.Liquidity),
		Excluded:  h.Excluded,
		Source:    provenanceToProto(h.Source),
		CreatedAt: timestamppb.New(h.CreatedAt),
		UpdatedAt: timestamppb.New(h.UpdatedAt),
	}
	if h.PortfolioID != "" {
		result.PortfolioId = &h.PortfolioID
	}
	if h.ImportID != "" {
		result.ImportId = &h.ImportID
	}
	return result
}

func provenanceToProto(s entity.ProvenanceSource) apiv1.ProvenanceSource {
	switch s {
	case entity.SourceSync:
		return apiv1.ProvenanceSource_PROVENANCE_SOURCE_SYNC
	case entity.SourceManual:
		return apiv1.ProvenanceSource_PROVENANCE_SOURCE_MANUAL
	case entity.SourceLLMImport:
		return apiv1.ProvenanceSource_PROVENANCE_SOURCE_LLM_IMPORT
	default:
		return apiv1.ProvenanceSource_PROVENANCE_SOURCE_UNSPECIFIED
	}
}

func accountFromProto(a *apiv1.Account) *entity.Account {
	result := &entity.Account{
		ID:           a.Id,
		UserID:       a.UserId,
		Name:         a.Name,
		Type:         entity.AccountType(a.Type),
		Data:         a.Data,
		Capabilities: capabilitiesFromProto(a.Capabilities),
		SystemScopes: capabilitiesFromProto(a.SystemScopes),
	}
	if a.Description != nil {
		result.Description = *a.Description
	}
	if a.PortfolioId != nil {
		result.PortfolioID = *a.PortfolioId
	}
	return result
}

func accountToProto(a *entity.Account) *apiv1.Account {
	result := &apiv1.Account{
		Id:           a.ID,
		UserId:       a.UserID,
		Name:         a.Name,
		Type:         apiv1.AccountType(a.Type),
		Data:         maskSecrets(a.Data),
		Capabilities: capabilitiesToProto(a.Capabilities),
		SystemScopes: capabilitiesToProto(a.SystemScopes),
		CreatedAt:    timestamppb.New(a.CreatedAt),
		UpdatedAt:    timestamppb.New(a.UpdatedAt),
	}
	if a.Description != "" {
		result.Description = &a.Description
	}
	if a.PortfolioID != "" {
		result.PortfolioId = &a.PortfolioID
	}
	return result
}

func capabilitiesFromProto(caps []string) []entity.AccountCapability {
	if len(caps) == 0 {
		return nil
	}
	result := make([]entity.AccountCapability, len(caps))
	for i, c := range caps {
		result[i] = entity.AccountCapability(c)
	}
	return result
}

func capabilitiesToProto(caps []entity.AccountCapability) []string {
	if len(caps) == 0 {
		return nil
	}
	result := make([]string, len(caps))
	for i, c := range caps {
		result[i] = string(c)
	}
	return result
}

// maskPrefix marks a secret value as masked in API responses; an incoming
// value with this prefix means "keep the stored secret".
const maskPrefix = "••••"

// providerDataKey names, in accounts.data, the adapter the account speaks
// through. It mirrors credentials.DataProviderKey, which this package does not
// import on purpose: a service here takes the interfaces it needs and knows
// nothing about who builds them.
const providerDataKey = "provider"

// nonSecretDataKeys are accounts.data keys that look secret-ish by name but
// are safe to return as is.
var nonSecretDataKeys = map[string]bool{
	providerDataKey: true,
	"address":       true,
	"addresses":     true,
	"chain":         true,
	"pro":           true,
}

// isSecretKey classifies accounts.data keys by name: anything containing
// key/secret/token/password is treated as a credential unless explicitly
// allowlisted. Fail-safe: an unknown provider's credential key gets masked
// without code changes.
func isSecretKey(key string) bool {
	if nonSecretDataKeys[key] {
		return false
	}
	k := strings.ToLower(key)
	for _, marker := range []string{"key", "secret", "token", "password"} {
		if strings.Contains(k, marker) {
			return true
		}
	}
	return false
}

// maskSecrets returns a copy of data with secret values replaced by
// maskPrefix+last4 (or just maskPrefix for short values). The input map is
// shared with the credentials resolver and must not be mutated.
func maskSecrets(data map[string]string) map[string]string {
	if data == nil {
		return nil
	}
	masked := make(map[string]string, len(data))
	for k, v := range data {
		if isSecretKey(k) {
			// last4 only when it reveals at most half of the secret
			if len(v) >= 8 {
				v = maskPrefix + v[len(v)-4:]
			} else {
				v = maskPrefix
			}
		}
		masked[k] = v
	}
	return masked
}

func transactionFromProto(t *apiv1.Transaction) *entity.Transaction {
	// Provenance fields (source, import_id) are deliberately not mapped:
	// they are output-only and stamped by the server.
	return &entity.Transaction{
		ID:        t.Id,
		Type:      entity.TransactionType(t.Type),
		Status:    entity.TransactionStatus(t.Status),
		AccountID: t.AccountId,
		Data:      t.Data,
	}
}

// splitChains splits a comma-separated chain string, defaulting to ["eth"] when empty.
func splitChains(raw string) []string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return []string{"eth"}
	}
	parts := strings.FieldsFunc(raw, func(r rune) bool { return r == ',' || r == ' ' })
	if len(parts) == 0 {
		return []string{"eth"}
	}
	result := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			result = append(result, p)
		}
	}
	return result
}

// splitAddresses reads the comma- or space-separated address list an account
// may carry instead of a single address. Unlike splitChains there is no default:
// an absent list means the account uses the single-address form.
func splitAddresses(raw string) []string {
	parts := strings.FieldsFunc(raw, func(r rune) bool { return r == ',' || r == ' ' })
	result := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			result = append(result, p)
		}
	}
	return result
}

func transactionToProto(t *entity.Transaction) *apiv1.Transaction {
	result := &apiv1.Transaction{
		Id:        t.ID,
		Type:      apiv1.TransactionType(t.Type),
		Status:    apiv1.TransactionStatus(t.Status),
		AccountId: t.AccountID,
		Data:      t.Data,
		Source:    provenanceToProto(t.Source),
		CreatedAt: timestamppb.New(t.CreatedAt),
		UpdatedAt: timestamppb.New(t.UpdatedAt),
	}
	if t.ImportID != "" {
		result.ImportId = &t.ImportID
	}
	return result
}
