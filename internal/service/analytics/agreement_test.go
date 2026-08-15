package analytics

import (
	"context"
	"sort"
	"testing"

	"connectrpc.com/connect"
	apiv1 "github.com/foxcool/greedy-eye/api/v1"
	"github.com/foxcool/greedy-eye/internal/entity"
	"github.com/foxcool/greedy-eye/internal/service/portfolio"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A total and a heatmap are two renderings of one statement about money, and
// they are computed by two independent implementations: portfolio.unitPrice and
// analytics.assetPricing each resolve a quote, each apply the market-depth gate,
// each classify the failure and each assemble a ValuationCoverage.
//
// Ф0 already paid for this shape once. Способ #6 was the frontend recomputing
// the total by its own rules, so a gate fixed on the server was undone by the
// client, and the rule that came out of it was: у числа один автор. That rule
// was applied to the frontend and not between two backend services.
//
// Until the duplication is removed (personal-psu.4), this test is what makes a
// divergence loud. It asserts nothing about WHICH pricing rule is right — only
// that both surfaces answer under the same one, so a change to the rule cannot
// land in one copy and not the other.

// agreementStore adapts the analytics fake to the much wider portfolio.Store.
// The embedded interface is nil on purpose: a method this test does not
// implement panics when called rather than quietly answering zero, so the
// fixture cannot drift into exercising a path it never set up.
type agreementStore struct {
	portfolio.Store
	inner *fakeStore
}

func (s agreementStore) GetPortfolio(ctx context.Context, id string) (*entity.Portfolio, error) {
	return s.inner.GetPortfolio(ctx, id)
}

func (s agreementStore) GetAccount(ctx context.Context, id string) (*entity.Account, error) {
	return s.inner.GetAccount(ctx, id)
}

func (s agreementStore) ListHoldings(ctx context.Context, opts portfolio.ListHoldingsOpts) ([]*entity.Holding, string, error) {
	return s.inner.ListHoldings(ctx, opts)
}

// agreementMD does the same for the wider portfolio.MarketDataClient.
type agreementMD struct {
	portfolio.MarketDataClient
	inner *fakeMD
}

func (m agreementMD) GetAsset(ctx context.Context, req *connect.Request[apiv1.GetAssetRequest]) (*connect.Response[apiv1.Asset], error) {
	return m.inner.GetAsset(ctx, req)
}

func (m agreementMD) GetLatestPrice(ctx context.Context, req *connect.Request[apiv1.GetLatestPriceRequest]) (*connect.Response[apiv1.Price], error) {
	return m.inner.GetLatestPrice(ctx, req)
}

func (m agreementMD) ListPriceHistory(ctx context.Context, req *connect.Request[apiv1.ListPriceHistoryRequest]) (*connect.Response[apiv1.ListPriceHistoryResponse], error) {
	return m.inner.ListPriceHistory(ctx, req)
}

func (m agreementMD) GetPricingStatus(ctx context.Context, req *connect.Request[apiv1.GetPricingStatusRequest]) (*connect.Response[apiv1.GetPricingStatusResponse], error) {
	return m.inner.GetPricingStatus(ctx, req)
}

// volumePrice is a quote carrying a reported 24h volume, which is the evidence
// the market-depth gate reads.
func volumePrice(asset, base, last, volume string, decimals uint32) *apiv1.Price {
	p := price(asset, base, last, decimals)
	p.Volume = &volume
	return p
}

// agreementFixture spans every branch the two implementations both have to
// walk, because a branch only one of them exercises proves nothing about
// agreement:
//
//   - eth   priced directly in the quote asset
//   - sol   priced through a cross rate (quoted in EUR, EUR/USD known)
//   - mnep  a real quote off a market too thin to sell into (ADR-009)
//   - ghost no quote in any base
//   - scam  quarantined: out of the total by decision, and out of coverage,
//     which is a different statement from unpriced
func agreementFixture() (*fakeStore, *fakeMD) {
	st := &fakeStore{
		portfolios: map[string]*entity.Portfolio{
			"p1": {ID: "p1", UserID: "u1", Name: "main"},
		},
		accounts: map[string]*entity.Account{
			"a1": {ID: "a1", UserID: "u1", Name: "wallet-1"},
		},
		holdings: []*entity.Holding{
			{ID: "h-eth", AssetID: "eth", AccountID: "a1", PortfolioID: "p1", Amount: dec("200"), Decimals: 2},
			{ID: "h-sol", AssetID: "sol", AccountID: "a1", PortfolioID: "p1", Amount: dec("1000"), Decimals: 2},
			{ID: "h-mnep", AssetID: "mnep", AccountID: "a1", PortfolioID: "p1", Amount: dec("30000000"), Decimals: 2},
			{ID: "h-ghost", AssetID: "ghost", AccountID: "a1", PortfolioID: "p1", Amount: dec("500"), Decimals: 2},
			{ID: "h-scam", AssetID: "scam", AccountID: "a1", PortfolioID: "p1", Amount: dec("999"), Decimals: 2, Excluded: true},
		},
	}
	md := &fakeMD{
		assets: map[string]*apiv1.Asset{
			"eth":   {Id: "eth", Name: "Ethereum", Symbol: strPtr("ETH")},
			"sol":   {Id: "sol", Name: "Solana", Symbol: strPtr("SOL")},
			"mnep":  {Id: "mnep", Name: "Minereum", Symbol: strPtr("MNEP")},
			"ghost": {Id: "ghost", Name: "Ghost", Symbol: strPtr("GHOST")},
			"scam":  {Id: "scam", Name: "Counterfeit", Symbol: strPtr("USDT")},
		},
		latest: map[string]*apiv1.Price{
			// Direct: a market deep enough to sell into.
			"eth|USD": volumePrice("eth", "USD", "200000", "500000000", 2),
			"eth|":    volumePrice("eth", "USD", "200000", "500000000", 2),
			// Cross: quoted in EUR only, with a EUR/USD rate to convert through.
			"sol|":    volumePrice("sol", "eur", "10000", "900000000", 2),
			"eur|USD": price("eur", "USD", "110", 2),
			// Thin: a real print off a market that cannot absorb the position.
			"mnep|USD": volumePrice("mnep", "USD", "1", "4065500", 2),
			"mnep|":    volumePrice("mnep", "USD", "1", "4065500", 2),
			// Quarantined, and priced — so that a surface counting it would
			// visibly disagree rather than coincidentally match.
			"scam|USD": volumePrice("scam", "USD", "100", "800000000", 2),
			"scam|":    volumePrice("scam", "USD", "100", "800000000", 2),
			// "ghost" deliberately absent from every key.
		},
		hist: map[string]*apiv1.Price{},
	}
	return st, md
}

func TestTotalAndHeatmapAgree(t *testing.T) {
	st, md := agreementFixture()
	ctx := userCtx("u1")

	heat := NewHandler(st, testLogger()).WithMarketDataClient(md)
	value := portfolio.NewHandler(agreementStore{inner: st}, testLogger()).
		WithMarketDataClient(agreementMD{inner: md})

	valueResp, err := value.CalculatePortfolioValue(ctx, connect.NewRequest(&apiv1.CalculatePortfolioValueRequest{
		PortfolioId: "p1",
	}))
	require.NoError(t, err)

	heatResp, err := heat.GetHeatmap(ctx, heatmapRequest())
	require.NoError(t, err)

	// The total is a raw integer scaled by its own decimals; the map draws
	// float sizes. Compare them as money.
	total := dec(valueResp.Msg.TotalValueAmount).Shift(-int32(valueResp.Msg.Decimals))
	drawn := decimal.Zero
	for _, n := range heatResp.Msg.Nodes {
		require.NotEmpty(t, n.AssetId, "flat map draws leaves only")
		drawn = drawn.Add(decimal.NewFromFloat(n.Size))
	}
	assert.InDelta(t, total.InexactFloat64(), drawn.InexactFloat64(), 1e-6,
		"the total and the sum of the tiles are the same number said twice")

	vc, hc := valueResp.Msg.Coverage, heatResp.Msg.Coverage
	require.NotNil(t, vc)
	require.NotNil(t, hc)

	assert.Equal(t, vc.PricedCount, hc.PricedCount, "same positions entered both")
	assert.Equal(t, vc.UnpricedCount, hc.UnpricedCount, "same positions were left out of both")
	assert.Equal(t, uint32(len(heatResp.Msg.Nodes)), hc.PricedCount,
		"every priced holding draws exactly one tile")

	assert.Equal(t, unpricedReasons(vc), unpricedReasons(hc),
		"a position kept out of the total and out of the map is kept out FOR THE SAME REASON")

	// Anchors, so that a change making both surfaces equally wrong still fails.
	assert.Equal(t, uint32(2), vc.PricedCount, "eth direct + sol crossed")
	assert.Equal(t, uint32(2), vc.UnpricedCount, "mnep thin + ghost unquoted")
	assert.Equal(t, uint32(1), valueResp.Msg.ExcludedCount, "the quarantined holding is disclosed")
	assert.Equal(t, 5100.0, total.InexactFloat64(), "2 ETH at 2000, plus 10 SOL at 100 EUR crossed at 1.10")
}

// unpricedReasons is the multiset of reasons a coverage block discloses, as a
// sorted list of "assetID:REASON" so two blocks can be compared without
// depending on the order either surface happened to walk its holdings in.
func unpricedReasons(c *apiv1.ValuationCoverage) []string {
	out := make([]string, 0, len(c.Unpriced))
	for _, u := range c.Unpriced {
		out = append(out, u.AssetId+":"+u.Reason.String())
	}
	sort.Strings(out)
	return out
}
