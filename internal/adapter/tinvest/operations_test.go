package tinvest

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/foxcool/greedy-eye/internal/entity"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var _ entity.BrokerIncomeReader = (*BrokerSyncer)(nil)

// operationsServer serves the operations pages in testdata by cursor and
// records the requests it was sent. The pages are shaped from the API
// contract (operations.proto), not captured from a live account.
func operationsServer(t *testing.T) (*BrokerSyncer, *[]operationsRequest) {
	t.Helper()
	pages := map[string][]byte{}
	for cursor, file := range map[string]string{"": "operations_page1.json", "cursor-page-2": "operations_page2.json"} {
		raw, err := os.ReadFile(filepath.Join("testdata", file))
		require.NoError(t, err)
		pages[cursor] = raw
	}
	var (
		mu   sync.Mutex
		seen []operationsRequest
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != pathOperationsByCursor {
			http.NotFound(w, r)
			return
		}
		body, _ := io.ReadAll(r.Body)
		var req operationsRequest
		if err := json.Unmarshal(body, &req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		mu.Lock()
		seen = append(seen, req)
		mu.Unlock()
		_, _ = w.Write(pages[req.Cursor])
	}))
	t.Cleanup(srv.Close)
	c, err := NewClient(Config{Token: "t", BaseURL: srv.URL, Transport: srv.Client().Transport})
	require.NoError(t, err)
	return NewBrokerSyncer(c, "acc-1"), &seen
}

// TestReadIncome_RecordedPages: both pages are read, only executed income
// operations come back, signed, in their own currency.
func TestReadIncome_RecordedPages(t *testing.T) {
	s, seen := operationsServer(t)
	got, err := s.ReadIncome(context.Background(), time.Time{}, time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC))
	require.NoError(t, err)

	byID := map[string]entity.BrokerIncome{}
	for _, inc := range got {
		byID[inc.ID] = inc
	}
	assert.Len(t, byID, 10, "canceled, in-progress and redemption operations are not income")
	for id, want := range map[string]struct {
		kind     entity.IncomeKind
		currency string
		amount   string
	}{
		"op-coupon-1":                {entity.IncomeCoupon, "RUB", "1000"},
		"op-bond-tax-1":              {entity.IncomeCoupon, "RUB", "-130"},
		"op-dividend-1":              {entity.IncomeDividend, "RUB", "517.45"},
		"op-dividend-tax-1":          {entity.IncomeDividend, "RUB", "-67.27"},
		"op-overnight-1":             {entity.IncomeInterest, "RUB", "0.42"},
		"op-overnight-2":             {entity.IncomeInterest, "RUB", "0.42"},
		"op-usd-dividend-1":          {entity.IncomeDividend, "USD", "2.75"},
		"op-tax-correction-coupon-1": {entity.IncomeCoupon, "RUB", "13"},
		"op-tax-1":                   {entity.IncomeTax, "RUB", "-50"},
		"op-tax-correction-1":        {entity.IncomeTax, "RUB", "10"},
	} {
		inc, ok := byID[id]
		if !assert.True(t, ok, id) {
			continue
		}
		assert.Equal(t, want.kind, inc.Kind, id)
		assert.Equal(t, want.currency, inc.Currency, id)
		assert.Equal(t, want.amount, inc.Amount.String(), id)
		assert.NotEmpty(t, inc.Raw, id)
	}
	assert.True(t, time.Date(2026, 10, 15, 9, 12, 0, 123456000, time.UTC).Equal(byID["op-coupon-1"].At))
	assert.Equal(t, "uid-ofz-26238", byID["op-coupon-1"].Instrument, "the instrument uid first")
	assert.Equal(t, "BBG00BONDXX", byID["op-bond-tax-1"].Instrument, "the figi when there is no uid")
	assert.Empty(t, byID["op-overnight-1"].Instrument)

	require.Len(t, *seen, 2)
	first := (*seen)[0]
	assert.Equal(t, "acc-1", first.AccountID)
	assert.Nil(t, first.From, "a zero from reads the whole history")
	assert.Equal(t, operationStateExecuted, first.State)
	assert.Contains(t, first.OperationTypes, "OPERATION_TYPE_COUPON")
	assert.NotContains(t, first.OperationTypes, "OPERATION_TYPE_BOND_REPAYMENT")
	assert.Equal(t, "cursor-page-2", (*seen)[1].Cursor)
}

// TestReadIncome_Window: operations the source returns outside [from, to)
// are passed over, so a re-read window cannot reach past what was asked.
func TestReadIncome_Window(t *testing.T) {
	s, seen := operationsServer(t)
	from := time.Date(2026, 10, 3, 6, 0, 1, 0, time.UTC)
	to := time.Date(2026, 10, 15, 9, 12, 1, 0, time.UTC)
	got, err := s.ReadIncome(context.Background(), from, to)
	require.NoError(t, err)
	var ids []string
	for _, inc := range got {
		ids = append(ids, inc.ID)
	}
	assert.ElementsMatch(t, []string{"op-coupon-1", "op-dividend-tax-1"}, ids)
	require.NotNil(t, (*seen)[0].From)
	assert.True(t, from.Equal(*(*seen)[0].From))
}

// TestIncomeKindOf pins the mapping: every income type and its tax are
// filed under one kind, and what is not income is not.
func TestIncomeKindOf(t *testing.T) {
	for typ, want := range map[string]entity.IncomeKind{
		"OPERATION_TYPE_COUPON":                     entity.IncomeCoupon,
		"OPERATION_TYPE_BOND_TAX":                   entity.IncomeCoupon,
		"OPERATION_TYPE_BOND_TAX_PROGRESSIVE":       entity.IncomeCoupon,
		"OPERATION_TYPE_TAX_CORRECTION_COUPON":      entity.IncomeCoupon,
		"OPERATION_TYPE_DIVIDEND":                   entity.IncomeDividend,
		"OPERATION_TYPE_DIV_EXT":                    entity.IncomeDividend,
		"OPERATION_TYPE_DIVIDEND_TAX":               entity.IncomeDividend,
		"OPERATION_TYPE_DIVIDEND_TAX_PROGRESSIVE":   entity.IncomeDividend,
		"OPERATION_TYPE_OVERNIGHT":                  entity.IncomeInterest,
		"OPERATION_TYPE_TAX_REPO":                   entity.IncomeInterest,
		"OPERATION_TYPE_TAX_REPO_REFUND":            entity.IncomeInterest,
		"OPERATION_TYPE_TAX":                        entity.IncomeTax,
		"OPERATION_TYPE_TAX_PROGRESSIVE":            entity.IncomeTax,
		"OPERATION_TYPE_TAX_CORRECTION":             entity.IncomeTax,
		"OPERATION_TYPE_TAX_CORRECTION_PROGRESSIVE": entity.IncomeTax,
		"OPERATION_TYPE_BENEFIT_TAX":                entity.IncomeTax,
		"OPERATION_TYPE_BENEFIT_TAX_PROGRESSIVE":    entity.IncomeTax,
	} {
		got, ok := IncomeKindOf(typ)
		assert.True(t, ok, typ)
		assert.Equal(t, want, got, typ)
	}
	for _, typ := range []string{
		"OPERATION_TYPE_BUY", "OPERATION_TYPE_SELL", "OPERATION_TYPE_INPUT", "OPERATION_TYPE_OUTPUT",
		"OPERATION_TYPE_BOND_REPAYMENT", "OPERATION_TYPE_BOND_REPAYMENT_FULL", "OPERATION_TYPE_BROKER_FEE",
		"OPERATION_TYPE_DIVIDEND_TRANSFER", "OPERATION_TYPE_OUT_STAMP_DUTY", "OPERATION_TYPE_SERVICE_FEE", "",
	} {
		_, ok := IncomeKindOf(typ)
		assert.False(t, ok, typ)
	}
}

// TestReadIncome_Refusals: an answer the ledger cannot trust fails the read
// whole rather than coming back short.
func TestReadIncome_Refusals(t *testing.T) {
	for name, body := range map[string]string{
		"more without a cursor": `{"hasNext":true,"nextCursor":"","items":[]}`,
		"unknown currency": `{"hasNext":false,"items":[{"id":"x","date":"2026-10-01T00:00:00Z",
			"type":"OPERATION_TYPE_COUPON","state":"OPERATION_STATE_EXECUTED","payment":{"currency":"xau","units":"1"}}]}`,
		"unreadable payment": `{"hasNext":false,"items":[{"id":"x","date":"2026-10-01T00:00:00Z",
			"type":"OPERATION_TYPE_COUPON","state":"OPERATION_STATE_EXECUTED","payment":{"currency":"rub","units":"1e"}}]}`,
		"no payment": `{"hasNext":false,"items":[{"id":"x","date":"2026-10-01T00:00:00Z",
			"type":"OPERATION_TYPE_COUPON","state":"OPERATION_STATE_EXECUTED"}]}`,
		"a payment with no currency": `{"hasNext":false,"items":[{"id":"x","date":"2026-10-01T00:00:00Z",
			"type":"OPERATION_TYPE_COUPON","state":"OPERATION_STATE_EXECUTED","payment":{"units":"0"}}]}`,
		"no id": `{"hasNext":false,"items":[{"date":"2026-10-01T00:00:00Z",
			"type":"OPERATION_TYPE_COUPON","state":"OPERATION_STATE_EXECUTED","payment":{"currency":"rub","units":"1"}}]}`,
	} {
		t.Run(name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte(body))
			}))
			t.Cleanup(srv.Close)
			c, err := NewClient(Config{Token: "t", BaseURL: srv.URL, Transport: srv.Client().Transport})
			require.NoError(t, err)
			_, err = NewBrokerSyncer(c, "acc-1").ReadIncome(context.Background(), time.Time{}, time.Time{})
			assert.Error(t, err)
		})
	}

	t.Run("a zero payment moved nothing", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(`{"hasNext":false,"items":[{"id":"x","date":"2026-10-01T00:00:00Z",
				"type":"OPERATION_TYPE_COUPON","state":"OPERATION_STATE_EXECUTED","payment":{"currency":"rub","units":"0"}}]}`))
		}))
		t.Cleanup(srv.Close)
		c, err := NewClient(Config{Token: "t", BaseURL: srv.URL, Transport: srv.Client().Transport})
		require.NoError(t, err)
		got, err := NewBrokerSyncer(c, "acc-1").ReadIncome(context.Background(), time.Time{}, time.Time{})
		require.NoError(t, err)
		assert.Empty(t, got)
	})

	t.Run("a cursor that repeats", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(`{"hasNext":true,"nextCursor":"same","items":[]}`))
		}))
		t.Cleanup(srv.Close)
		c, err := NewClient(Config{Token: "t", BaseURL: srv.URL, Transport: srv.Client().Transport})
		require.NoError(t, err)
		_, err = NewBrokerSyncer(c, "acc-1").ReadIncome(context.Background(), time.Time{}, time.Time{})
		assert.ErrorContains(t, err, "no new cursor")
	})
}
