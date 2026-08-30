package quoting

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"connectrpc.com/connect"
	apiv1 "github.com/foxcool/greedy-eye/api/v1"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func testLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// fakeReader answers GetLatestPrice from a map keyed "asset|base"; the empty
// base is the any-base lookup, exactly as the service clients behave. A key
// that is absent answers NotFound, which is the "nobody quoted it" case rather
// than an error.
type fakeReader struct {
	latest map[string]*apiv1.Price
	err    error
	calls  int
	// tickers models the service resolving a symbol to a UUID on every call
	// (marketdata.resolveAssetID). Without it a fake makes an unresolved quote
	// look unpriced, when what it really is, is priced expensively.
	tickers map[string]string
}

func (f *fakeReader) alias(id string) string {
	if to, ok := f.tickers[id]; ok {
		return to
	}
	return id
}

func (f *fakeReader) GetLatestPrice(_ context.Context, req *connect.Request[apiv1.GetLatestPriceRequest]) (*connect.Response[apiv1.Price], error) {
	f.calls++
	if f.err != nil {
		return nil, f.err
	}
	p, ok := f.latest[f.alias(req.Msg.AssetId)+"|"+f.alias(req.Msg.BaseAssetId)]
	if !ok {
		return nil, connect.NewError(connect.CodeNotFound, errors.New("no price"))
	}
	return connect.NewResponse(p), nil
}

// price builds a stored quote. at is when it was observed; the zero time leaves
// the row without a timestamp, which is the "cannot claim currency" case.
func price(asset, base, last string, decimals uint32, at time.Time) *apiv1.Price {
	p := &apiv1.Price{AssetId: asset, BaseAssetId: base, Last: last, Decimals: decimals}
	if !at.IsZero() {
		p.Timestamp = timestamppb.New(at)
	}
	return p
}

func TestCandidates(t *testing.T) {
	older := time.Date(2026, 8, 11, 0, 0, 0, 0, time.UTC)
	newer := time.Date(2026, 8, 14, 0, 0, 0, 0, time.UTC)

	tests := []struct {
		name    string
		latest  map[string]*apiv1.Price
		want    []string // expected candidate unit prices, in resolution order
		freshes string   // unit price of the candidate Freshest must pick; "" when none
		missing Outcome  // which absence is reported when nothing was found
		reason  string
	}{
		{
			name: "direct only: quoted straight in the quote asset",
			latest: map[string]*apiv1.Price{
				"eth|":    price("eth", "usd", "200000", 2, newer),
				"eth|usd": price("eth", "usd", "200000", 2, newer),
			},
			want:    []string{"2000"},
			freshes: "2000",
			reason:  "the freshest print is already in the quote asset, so there is nothing to choose between",
		},
		{
			name: "cross only: quoted in another base, converted through its rate",
			latest: map[string]*apiv1.Price{
				"sol|":    price("sol", "eur", "10000", 2, newer),
				"eur|usd": price("eur", "usd", "110", 2, newer),
			},
			want:    []string{"110"},
			freshes: "110",
			reason:  "100 EUR crossed at 1.10",
		},
		{
			name: "cross through the inverse rate",
			latest: map[string]*apiv1.Price{
				"sol|":    price("sol", "eur", "10000", 2, newer),
				"usd|eur": price("usd", "eur", "50", 2, newer),
			},
			want:    []string{"200"},
			freshes: "200",
			reason:  "no EUR/USD row, so 1/(USD/EUR) = 1/0.5 converts it",
		},
		{
			name: "both paths, the freshest wins even though it is the crossed one",
			latest: map[string]*apiv1.Price{
				// Binance-shaped: current, quoted in USDT.
				"btc|":     price("btc", "usdt", "6000000", 2, newer),
				"usdt|usd": price("usdt", "usd", "100", 2, newer),
				// CoinGecko-shaped: a direct USD row frozen days earlier.
				"btc|usd": price("btc", "usd", "5000000", 2, older),
			},
			want:    []string{"60000", "50000"},
			freshes: "60000",
			reason:  "prod 2026-08-14: a stale direct row must not shadow a fresher crossed one",
		},
		{
			name: "both paths, the freshest wins when it is the direct one",
			latest: map[string]*apiv1.Price{
				"btc|":     price("btc", "usdt", "6000000", 2, older),
				"usdt|usd": price("usdt", "usd", "100", 2, older),
				"btc|usd":  price("btc", "usd", "5000000", 2, newer),
			},
			want:    []string{"60000", "50000"},
			freshes: "50000",
			reason:  "selection is by observation time, not by path — in either direction",
		},
		{
			name: "a base exists but no rate converts it: no candidate at all",
			latest: map[string]*apiv1.Price{
				// The USDT twin case: the asset IS quoted, in USDT, and the
				// USDT/USD rate is written against a different asset row.
				"ai|": price("ai", "usdt-crypto", "150", 2, newer),
			},
			want:    nil,
			freshes: "",
			missing: NoCrossRate,
			reason:  "74 holdings looked unquoted while the only thing missing was the cross rate",
		},
		{
			name:    "no price in any base",
			latest:  map[string]*apiv1.Price{},
			want:    nil,
			freshes: "",
			missing: NoQuote,
			reason:  "nobody has ever quoted it",
		},
		{
			name: "a quote with no timestamp sorts oldest",
			latest: map[string]*apiv1.Price{
				"btc|":     price("btc", "usdt", "6000000", 2, time.Time{}),
				"usdt|usd": price("usdt", "usd", "100", 2, time.Time{}),
				"btc|usd":  price("btc", "usd", "5000000", 2, older),
			},
			want:    []string{"60000", "50000"},
			freshes: "50000",
			reason:  "an undated row cannot claim currency it never stated",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := &fakeReader{latest: tt.latest}
			got, missing, err := Candidates(context.Background(), r, testLogger(), assetOf(tt.latest), "usd")
			require.NoError(t, err)
			if len(got) > 0 {
				assert.Equal(t, Priced, missing, "candidates were found, so nothing is missing")
			}

			units := make([]string, 0, len(got))
			for _, c := range got {
				units = append(units, c.Unit.String())
			}
			assert.Equal(t, tt.want, nilIfEmpty(units), tt.reason)

			if tt.freshes == "" {
				assert.Empty(t, got, "nothing to choose between")
				assert.Equal(t, tt.missing, missing,
					"which absence this is, is the thing the coverage block reports")
				return
			}
			assert.Equal(t, tt.freshes, Freshest(got).Unit.String(), tt.reason)
		})
	}
}

// assetOf picks the asset under test out of a fixture: it is the one carrying an
// any-base key, or "ghost" when the fixture is empty.
func assetOf(latest map[string]*apiv1.Price) string {
	for k, p := range latest {
		if len(k) > 0 && k[len(k)-1] == '|' {
			return p.AssetId
		}
	}
	return "ghost"
}

func nilIfEmpty(s []string) []string {
	if len(s) == 0 {
		return nil
	}
	return s
}

// TestCandidatesValueBase pins the field the heatmap reads: change is computed
// in the pair the asset trades in, so a moving cross rate does not read as a
// moving asset.
func TestCandidatesValueBase(t *testing.T) {
	at := time.Date(2026, 8, 14, 0, 0, 0, 0, time.UTC)
	r := &fakeReader{latest: map[string]*apiv1.Price{
		"sol|":    price("sol", "eur", "10000", 2, at),
		"eur|usd": price("eur", "usd", "110", 2, at),
	}}

	got, _, err := Candidates(context.Background(), r, testLogger(), "sol", "usd")
	require.NoError(t, err)
	require.Len(t, got, 1)

	assert.Equal(t, "110", got[0].Unit.String(), "converted into the quote asset")
	assert.Equal(t, "100", got[0].ValueBase.String(), "and kept in its own base for change")
	assert.Equal(t, "eur", got[0].BaseID, "which is the pair the map reads history in")
}

func TestCrossRate(t *testing.T) {
	at := time.Date(2026, 8, 14, 0, 0, 0, 0, time.UTC)

	t.Run("direct", func(t *testing.T) {
		r := &fakeReader{latest: map[string]*apiv1.Price{"eur|usd": price("eur", "usd", "110", 2, at)}}
		rate, ok, err := CrossRate(context.Background(), r, testLogger(), "eur", "usd")
		require.NoError(t, err)
		require.True(t, ok)
		assert.Equal(t, "1.1", rate.String())
	})

	t.Run("inverse", func(t *testing.T) {
		r := &fakeReader{latest: map[string]*apiv1.Price{"usd|eur": price("usd", "eur", "50", 2, at)}}
		rate, ok, err := CrossRate(context.Background(), r, testLogger(), "eur", "usd")
		require.NoError(t, err)
		require.True(t, ok)
		assert.Equal(t, "2", rate.String())
	})

	t.Run("a zero inverse is not a rate", func(t *testing.T) {
		r := &fakeReader{latest: map[string]*apiv1.Price{"usd|eur": price("usd", "eur", "0", 2, at)}}
		_, ok, err := CrossRate(context.Background(), r, testLogger(), "eur", "usd")
		require.NoError(t, err)
		assert.False(t, ok, "dividing by it would be worse than having no rate")
	})

	t.Run("neither direction", func(t *testing.T) {
		r := &fakeReader{latest: map[string]*apiv1.Price{}}
		_, ok, err := CrossRate(context.Background(), r, testLogger(), "eur", "usd")
		require.NoError(t, err)
		assert.False(t, ok)
	})
}

// TestAnyThin pins that the gate reads EVERY candidate that reports a volume,
// not only the one whose number ends up being used: the freshest row is not
// always the one carrying the evidence.
func TestAnyThin(t *testing.T) {
	at := time.Date(2026, 8, 14, 0, 0, 0, 0, time.UTC)
	one := decimal.NewFromInt(1)

	volume := func(p *apiv1.Price, v string) *apiv1.Price {
		p.Volume = &v
		return p
	}

	t.Run("a thin candidate fires even when another is chosen", func(t *testing.T) {
		fresh := Quote{Row: price("mnep", "usd", "1", 2, at), Rate: one} // no volume reported
		thin := Quote{Row: volume(price("mnep", "usd", "1", 2, at), "4065500"), Rate: one}

		got, isThin := AnyThin([]Quote{fresh, thin})
		assert.True(t, isThin, "MNEP: $4,175 of tokens off a $40k/day market")
		assert.Same(t, thin.Row, got.Row, "the row carrying the evidence is the one returned")
	})

	t.Run("a deep market passes", func(t *testing.T) {
		deep := Quote{Row: volume(price("eth", "usd", "200000", 2, at), "500000000"), Rate: one}
		_, isThin := AnyThin([]Quote{deep})
		assert.False(t, isThin)
	})

	t.Run("no volume reported is not thin", func(t *testing.T) {
		silent := Quote{Row: price("btc", "usdt", "6000000", 2, at), Rate: one}
		_, isThin := AnyThin([]Quote{silent})
		assert.False(t, isThin, "Binance reports no volume; absence of evidence is not evidence")
	})
}

// TestUnparseableLastIsNotAnError pins that a corrupt stored value drops the
// row rather than failing the whole valuation.
func TestUnparseableLastIsNotAnError(t *testing.T) {
	at := time.Date(2026, 8, 14, 0, 0, 0, 0, time.UTC)
	r := &fakeReader{latest: map[string]*apiv1.Price{
		"eth|":    price("eth", "usd", "not-a-number", 2, at),
		"eth|usd": price("eth", "usd", "not-a-number", 2, at),
	}}

	got, _, err := Candidates(context.Background(), r, testLogger(), "eth", "usd")
	require.NoError(t, err)
	assert.Empty(t, got, "an unparseable price leaves the asset unpriced, it does not fail the request")
}

// TestReaderErrorPropagates pins the other half: an unexpected transport failure
// is not silently an unpriced holding.
func TestReaderErrorPropagates(t *testing.T) {
	r := &fakeReader{err: connect.NewError(connect.CodeUnavailable, errors.New("market data down"))}
	_, _, err := Candidates(context.Background(), r, testLogger(), "eth", "usd")
	require.Error(t, err)
	assert.Equal(t, connect.CodeUnavailable, connect.CodeOf(err))
}

func TestOutcomeReason(t *testing.T) {
	assert.Equal(t, apiv1.UnpricedReason_UNPRICED_REASON_THIN_MARKET, ThinMarket.Reason())
	assert.Equal(t, apiv1.UnpricedReason_UNPRICED_REASON_NO_QUOTE, NoQuote.Reason(),
		"no price row anywhere is the outcome of every path that simply fails to find one")
}

// TestNoCrossRateYieldsToADirectQuote pins that the missing-rate verdict is
// provisional. The any-base path can fail to convert while a price quoted
// straight in the quote asset still exists — the asset is priced, and reporting
// NO_CROSS_RATE for it would be a false alarm about a holding that entered the
// total.
func TestNoCrossRateYieldsToADirectQuote(t *testing.T) {
	at := time.Date(2026, 8, 14, 0, 0, 0, 0, time.UTC)
	r := &fakeReader{latest: map[string]*apiv1.Price{
		// Freshest print is in a base nothing converts...
		"ai|": price("ai", "usdt-crypto", "150", 2, at),
		// ...but the catalogue also holds a direct USD row.
		"ai|usd": price("ai", "usd", "140", 2, at),
	}}

	got, missing, err := Candidates(context.Background(), r, testLogger(), "ai", "usd")
	require.NoError(t, err)

	require.Len(t, got, 1, "the direct row still prices it")
	assert.Equal(t, "1.4", got[0].Unit.String())
	assert.Equal(t, Priced, missing, "a priced asset reports no absence at all")
}

// TestNoCrossRateReason pins the wire value the coverage block carries, which is
// the whole point of splitting it from NO_QUOTE: the two ask for opposite work.
func TestNoCrossRateReason(t *testing.T) {
	assert.Equal(t, apiv1.UnpricedReason_UNPRICED_REASON_NO_CROSS_RATE, NoCrossRate.Reason())
	assert.NotEqual(t, NoQuote.Reason(), NoCrossRate.Reason(),
		"an unconvertible price is not the same fact as no price")
}

// fakeAssets resolves a ticker to a catalogue row, the way the market data
// service does. The ids are UUID-shaped because that is what price rows carry —
// which is the whole point of the test below.
type fakeAssets map[string]*apiv1.Asset

func (f fakeAssets) GetAsset(_ context.Context, req *connect.Request[apiv1.GetAssetRequest]) (*connect.Response[apiv1.Asset], error) {
	if a, ok := f[req.Msg.Id]; ok {
		return connect.NewResponse(a), nil
	}
	return nil, connect.NewError(connect.CodeNotFound, errors.New("no such asset"))
}

// TestResolvedQuoteTakesTheDirectPath pins what resolving the display currency
// up front buys, and it is not only speed.
//
// A price row carries base_asset_id as a UUID while the valuation policy names
// the currency by ticker, so an unresolved quote never equals the base of any
// row. `direct` was therefore unreachable on the default path: an asset already
// quoted in the display currency still went looking for a cross rate, spent two
// lookups discovering there is no USD/USD row — there cannot be one, CHECK
// price_pair_is_not_self forbids it — and set the provisional NoCrossRate before
// the direct quote it already had cleared it again.
//
// Resolved, the same asset is one lookup and never touches that branch, so
// NoCrossRate goes back to meaning what it says.
func TestResolvedQuoteTakesTheDirectPath(t *testing.T) {
	const usdID = "019f99ff-2ad2-773d-a6a8-39b7e47c7770"
	assets := fakeAssets{"USD": {Id: usdID, Name: "US Dollar"}}

	quoteID, err := ResolveQuote(t.Context(), assets, "USD")
	require.NoError(t, err)
	require.Equal(t, usdID, quoteID, "the ticker resolves to the id price rows are keyed by")

	rows := func() *fakeReader {
		return &fakeReader{
			tickers: map[string]string{"USD": usdID},
			latest: map[string]*apiv1.Price{
				"eth|":         price("eth", usdID, "200000", 2, time.Now()),
				"eth|" + usdID: price("eth", usdID, "200000", 2, time.Now()),
			},
		}
	}

	resolved := rows()
	got, outcome, err := Candidates(t.Context(), resolved, testLogger(), "eth", quoteID)
	require.NoError(t, err)
	require.Equal(t, Priced, outcome)
	require.Len(t, got, 1)
	assert.Equal(t, "2000", got[0].Unit.String())
	assert.Equal(t, 1, resolved.calls, "a quote already in the display currency is one lookup")

	// The same asset with the ticker passed through: still priced, because the
	// service resolves the symbol on every call — but it pays for the cross-rate
	// path first. That is the cost this resolution removes, per asset, per
	// valuation.
	unresolved := rows()
	_, outcome, err = Candidates(t.Context(), unresolved, testLogger(), "eth", "USD")
	require.NoError(t, err)
	require.Equal(t, Priced, outcome)
	assert.Equal(t, 4, unresolved.calls, "unresolved, the same answer costs four lookups")
}

func TestResolveQuoteFailsLoudly(t *testing.T) {
	_, err := ResolveQuote(t.Context(), fakeAssets{}, "XYZ")
	require.Error(t, err, "an unknown display currency fails the valuation instead of unpricing every holding")
	assert.Equal(t, connect.CodeNotFound, connect.CodeOf(err))
}
