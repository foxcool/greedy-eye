package execution

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect"
	apiv1 "github.com/foxcool/greedy-eye/api/v1"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
)

func d(s string) decimal.Decimal { return decimal.RequireFromString(s) }

func TestPaperFill(t *testing.T) {
	asOf := time.Date(2025, 3, 14, 12, 0, 0, 0, time.UTC)
	buy := func(quoteAmount, price string) Trade {
		return Trade{Side: SideBuy, AssetID: dcaBase, QuoteAssetID: dcaQuote, QuoteAmount: d(quoteAmount), Price: d(price), PriceAsOf: asOf}
	}
	quote := func(amount string, decimals uint32) Holding {
		return Holding{ID: "h-q", AssetID: dcaQuote, Amount: d(amount), Decimals: decimals}
	}
	base := func(amount string, decimals uint32) Holding {
		return Holding{ID: "h-b", AssetID: dcaBase, Amount: d(amount), Decimals: decimals}
	}

	tests := []struct {
		name      string
		trade     Trade
		holdings  []Holding
		wantBase  Holding
		wantQuote Holding
		wantFill  string
		rejected  string
	}{
		{
			name:      "adds to the base holding",
			trade:     buy("100", "40000"),
			holdings:  []Holding{quote("1000", 2), base("0.5", 8)},
			wantBase:  base("0.5025", 8),
			wantQuote: quote("900", 2),
			wantFill:  "0.0025",
		},
		{
			name:      "rounds down to the base holding's decimals",
			trade:     buy("100", "30000"),
			holdings:  []Holding{quote("1000", 2), base("1", 4)},
			wantBase:  base("1.0033", 4), // 0.003333... -> 0.0033
			wantQuote: quote("900", 2),
			wantFill:  "0.0033",
		},
		{
			name:      "missing base holding is created at 8 decimals",
			trade:     buy("100", "30000"),
			holdings:  []Holding{quote("100", 2)},
			wantBase:  Holding{AssetID: dcaBase, Amount: d("0.00333333"), Decimals: 8},
			wantQuote: quote("0", 2),
			wantFill:  "0.00333333",
		},
		{
			name:     "insufficient quote",
			trade:    buy("100", "40000"),
			holdings: []Holding{quote("99.99", 2), base("1", 8)},
			rejected: "insufficient " + dcaQuote,
		},
		{
			name:     "no quote holding",
			trade:    buy("100", "40000"),
			holdings: []Holding{base("1", 8)},
			rejected: "insufficient " + dcaQuote,
		},
		{
			name:     "quote amount finer than the quote holding",
			trade:    buy("100.001", "40000"),
			holdings: []Holding{quote("1000", 2)},
			rejected: "decimal places",
		},
		{
			name:     "buys nothing at the base scale",
			trade:    buy("0.01", "40000"),
			holdings: []Holding{quote("1000", 2), base("1", 2)},
			rejected: "buys no",
		},
		{
			name:     "quote held in two rows",
			trade:    buy("100", "40000"),
			holdings: []Holding{quote("1000", 2), {ID: "h-q2", AssetID: dcaQuote, Amount: d("5"), Decimals: 2}},
			rejected: "more than one",
		},
		{
			name:     "sell",
			trade:    Trade{Side: "sell", AssetID: dcaBase, QuoteAssetID: dcaQuote, QuoteAmount: d("1"), Price: d("1")},
			holdings: []Holding{quote("1000", 2)},
			rejected: "buys only",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			before := make([]Holding, len(tc.holdings))
			copy(before, tc.holdings)

			got := paperFill(tc.trade, tc.holdings)

			assert.Equal(t, before, tc.holdings, "the input balances are never changed in place")
			if tc.rejected != "" {
				assert.Contains(t, got.Reason, tc.rejected)
				assert.Equal(t, Holding{}, got.Base, "a rejected fill moves no balance")
				assert.Equal(t, Holding{}, got.Quote, "a rejected fill moves no balance")
				return
			}
			require.Empty(t, got.Reason)
			assertHolding(t, tc.wantBase, got.Base)
			assertHolding(t, tc.wantQuote, got.Quote)
			assert.True(t, got.Fill.BaseAmount.Equal(d(tc.wantFill)), "fill %s, want %s", got.Fill.BaseAmount, tc.wantFill)
			assert.True(t, got.Fill.QuoteAmount.Equal(tc.trade.QuoteAmount))
			assert.True(t, got.Fill.Price.Equal(tc.trade.Price))
			assert.Equal(t, asOf, got.Fill.PriceAsOf)
		})
	}
}

func assertHolding(t *testing.T, want, got Holding) {
	t.Helper()
	assert.Equal(t, want.ID, got.ID)
	assert.Equal(t, want.AssetID, got.AssetID)
	assert.Equal(t, want.Decimals, got.Decimals)
	assert.True(t, want.Amount.Equal(got.Amount), "amount %s, want %s", got.Amount, want.Amount)
}

// memPortfolio holds one account's holdings and applies imports as absolute
// amounts, like ImportPositions. Reads are slow enough that two unserialised
// read-modify-writes overlap.
type memPortfolio struct {
	mu       sync.Mutex
	holdings map[string]*apiv1.Holding
	imports  int
}

func (m *memPortfolio) ListHoldings(context.Context, *connect.Request[apiv1.ListHoldingsRequest]) (*connect.Response[apiv1.ListHoldingsResponse], error) {
	m.mu.Lock()
	var out []*apiv1.Holding
	for _, h := range m.holdings {
		out = append(out, proto.Clone(h).(*apiv1.Holding))
	}
	m.mu.Unlock()
	time.Sleep(20 * time.Millisecond)
	return connect.NewResponse(&apiv1.ListHoldingsResponse{Holdings: out}), nil
}

func (m *memPortfolio) ImportPositions(_ context.Context, req *connect.Request[apiv1.ImportPositionsRequest]) (*connect.Response[apiv1.ImportPositionsResponse], error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.imports++
	for _, p := range req.Msg.Positions {
		raw := d(p.Amount).Shift(int32(p.GetDecimals()))
		h, ok := m.holdings[p.GetAssetId()]
		if !ok {
			h = &apiv1.Holding{Id: fmt.Sprintf("h-%d", len(m.holdings)), AssetId: p.GetAssetId()}
			m.holdings[p.GetAssetId()] = h
		}
		h.Amount, h.Decimals = raw.String(), p.GetDecimals()
	}
	return connect.NewResponse(&apiv1.ImportPositionsResponse{Updated: int32(len(req.Msg.Positions))}), nil
}

func TestPaperVenue_SerializesPerAccount(t *testing.T) {
	pf := &memPortfolio{holdings: map[string]*apiv1.Holding{
		dcaQuote: {Id: "h-q", AssetId: dcaQuote, Amount: "100000", Decimals: 2}, // 1000.00
	}}
	venue := NewPaperVenue(pf)
	account := Account{ID: dcaAccount, Type: apiv1.AccountType_ACCOUNT_TYPE_MANUAL}
	trade := Trade{Side: SideBuy, AssetID: dcaBase, QuoteAssetID: dcaQuote, QuoteAmount: d("100"), Price: d("50000"), PriceAsOf: time.Now()}

	var wg sync.WaitGroup
	outcomes := make([]Outcome, 2)
	errs := make([]error, 2)
	for i := range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			outcomes[i], errs[i] = venue.Submit(context.Background(), account, trade, Key{ExecutionID: dcaAccount, Index: i})
		}()
	}
	wg.Wait()

	for i := range 2 {
		require.NoError(t, errs[i])
		assert.Equal(t, OutcomeFilled, outcomes[i].Status, outcomes[i].Reason)
	}
	assert.Equal(t, "80000", pf.holdings[dcaQuote].Amount, "two buys of 100 from 1000 leave 800.00")
	assert.Equal(t, "400000", pf.holdings[dcaBase].Amount, "two fills of 0.002 at 8 decimals")
}

func TestPaperVenue_RejectedFillWritesNothing(t *testing.T) {
	pf := &memPortfolio{holdings: map[string]*apiv1.Holding{
		dcaQuote: {Id: "h-q", AssetId: dcaQuote, Amount: "5000", Decimals: 2}, // 50.00
	}}
	venue := NewPaperVenue(pf)
	trade := Trade{Side: SideBuy, AssetID: dcaBase, QuoteAssetID: dcaQuote, QuoteAmount: d("100"), Price: d("50000")}

	out, err := venue.Submit(context.Background(), Account{ID: dcaAccount}, trade, Key{ExecutionID: dcaAccount})
	require.NoError(t, err)
	assert.Equal(t, OutcomeRejected, out.Status)
	assert.Contains(t, out.Reason, "insufficient "+dcaQuote)
	assert.Zero(t, pf.imports)
	assert.Equal(t, "5000", pf.holdings[dcaQuote].Amount)
}

func TestPaperVenue_ReportsCreatedHolding(t *testing.T) {
	pf := &memPortfolio{holdings: map[string]*apiv1.Holding{
		dcaQuote: {Id: "h-q", AssetId: dcaQuote, Amount: "100000", Decimals: 2},
	}}
	trade := Trade{Side: SideBuy, AssetID: dcaBase, QuoteAssetID: dcaQuote, QuoteAmount: d("100"), Price: d("50000")}

	out, err := NewPaperVenue(pf).Submit(context.Background(), Account{ID: dcaAccount}, trade, Key{ExecutionID: dcaAccount})
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{"h-q", pf.holdings[dcaBase].Id}, out.HoldingIDs)
}

// failingImport answers an import with a per-item failure, as ImportPositions
// does when one position cannot be written.
type failingImport struct{ *memPortfolio }

func (f failingImport) ImportPositions(context.Context, *connect.Request[apiv1.ImportPositionsRequest]) (*connect.Response[apiv1.ImportPositionsResponse], error) {
	msg := "update holding: boom"
	return connect.NewResponse(&apiv1.ImportPositionsResponse{Items: []*apiv1.ImportPositionResult{
		{AssetId: dcaQuote},
		{AssetId: dcaBase, Error: &msg},
	}, Failed: 1}), nil
}

func TestPaperVenue_ItemFailureIsAnError(t *testing.T) {
	pf := &memPortfolio{holdings: map[string]*apiv1.Holding{
		dcaQuote: {Id: "h-q", AssetId: dcaQuote, Amount: "100000", Decimals: 2},
	}}
	trade := Trade{Side: SideBuy, AssetID: dcaBase, QuoteAssetID: dcaQuote, QuoteAmount: d("100"), Price: d("50000")}

	_, err := NewPaperVenue(failingImport{pf}).Submit(context.Background(), Account{ID: dcaAccount}, trade, Key{ExecutionID: dcaAccount})
	require.Error(t, err, "a fill written in part must not be reported as filled")
	assert.Contains(t, err.Error(), "in part")
	assert.Contains(t, err.Error(), dcaBase+": update holding: boom")
}
