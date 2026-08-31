package tinvest

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/foxcool/greedy-eye/internal/entity"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The registry's factory signature is what really binds these together; this
// keeps the adapter honest before that wiring exists.
var _ entity.BrokerSyncer = (*BrokerSyncer)(nil)

// newBrokerSyncerWith serves an instrument universe and one portfolio.
func newBrokerSyncerWith(t *testing.T, universe map[string]string, portfolio string) *BrokerSyncer {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body := universe[r.URL.Path]
		if r.URL.Path == pathPortfolio {
			body = portfolio
		}
		if body == "" {
			body = `{}`
		}
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)

	c, err := NewClient(Config{Token: "t", BaseURL: srv.URL, Transport: srv.Client().Transport})
	require.NoError(t, err)
	return NewBrokerSyncer(c, "acc-1")
}

// A universe covering the three shapes a position can point at.
var brokerUniverse = map[string]string{
	pathShares: `{"instruments":[
		{"figi":"BBG004730N88","ticker":"GAZP","name":"Gazprom","currency":"rub",
		 "realExchange":"REAL_EXCHANGE_MOEX","apiTradeAvailableFlag":true},
		{"figi":"BBG000BPH459","ticker":"US5543821012","name":"Lowe's","currency":"usd",
		 "realExchange":"REAL_EXCHANGE_RTS","apiTradeAvailableFlag":true},
		{"figi":"BBG00NOVENUE","ticker":"WEIRD","name":"Nowhere","currency":"rub",
		 "realExchange":"REAL_EXCHANGE_UNSPECIFIED","apiTradeAvailableFlag":true}
	]}`,
	pathEtfs: `{"instruments":[
		{"figi":"BBG000FINEX","ticker":"IE00BK224L29","name":"FinEx","currency":"rub",
		 "realExchange":"REAL_EXCHANGE_MOEX","apiTradeAvailableFlag":true}
	]}`,
	pathBonds: `{"instruments":[
		{"figi":"BBG00BONDXX","ticker":"SU26238","name":"OFZ 26238","currency":"rub",
		 "realExchange":"REAL_EXCHANGE_MOEX","apiTradeAvailableFlag":true}
	]}`,
}

func positions(t *testing.T, portfolio string) ([]entity.BrokerPosition, entity.BrokerSkips) {
	t.Helper()
	got, skips, err := newBrokerSyncerWith(t, brokerUniverse, portfolio).SyncBroker(context.Background())
	require.NoError(t, err)
	return got, skips
}

func byRef(rows []entity.BrokerPosition, ref string) []entity.BrokerPosition {
	var out []entity.BrokerPosition
	for _, r := range rows {
		if r.Ref == ref {
			out = append(out, r)
		}
	}
	return out
}

// TestBrokerPositionsTakeTheirMarketFromTheVenue: an asset's identity is
// (symbol, market, type), so a position that creates one has to say which
// market. The venue is the only place that answer exists.
func TestBrokerPositionsTakeTheirMarketFromTheVenue(t *testing.T) {
	rows, skips := positions(t, `{"accountId":"acc-1","positions":[
		{"figi":"BBG004730N88","instrumentType":"share","ticker":"GAZP",
		 "quantity":{"units":"90","nano":0},"currentPrice":{"currency":"rub","units":"140","nano":0}},
		{"figi":"BBG000FINEX","instrumentType":"etf","ticker":"IE00BK224L29",
		 "quantity":{"units":"5","nano":0},"currentPrice":{"currency":"rub","units":"90","nano":0}},
		{"figi":"BBG00BONDXX","instrumentType":"bond","ticker":"SU26238",
		 "quantity":{"units":"2","nano":0},"currentPrice":{"currency":"rub","units":"800","nano":0}}
	]}`)

	require.Len(t, rows, 3)
	assert.Equal(t, entity.BrokerSkips{}, skips, "everything here is resolvable")

	gazp := byRef(rows, "BBG004730N88")[0]
	assert.Equal(t, "GAZP", gazp.Symbol)
	assert.Equal(t, MarketMOEX, gazp.Market)
	assert.Equal(t, entity.AssetTypeStock, gazp.Type)
	assert.Equal(t, "rub", gazp.Currency, "the base belongs to the row")
	assert.Equal(t, "90000000000", gazp.Amount, "quantity is scaled by 9, the wire format's own")

	assert.Equal(t, entity.AssetTypeFund, byRef(rows, "BBG000FINEX")[0].Type)

	// The bond is ingested as a quantity. Valuing it needs accrued interest and
	// a model, which is personal-b7l; dropping it here would be the silent
	// omission this system spent a release removing.
	bond := byRef(rows, "BBG00BONDXX")[0]
	assert.Equal(t, entity.AssetTypeBond, bond.Type)
	assert.Equal(t, MarketMOEX, bond.Market)
}

// TestIdentityIsTheFIGINotTheTicker: the measurement found ISINs sitting in the
// ticker field. Matching on it attaches a position to whatever asset shares the
// string — the collapse this project has already paid for once.
func TestIdentityIsTheFIGINotTheTicker(t *testing.T) {
	rows, _ := positions(t, `{"accountId":"acc-1","positions":[
		{"figi":"BBG000BPH459","instrumentType":"share","ticker":"US5543821012",
		 "quantity":{"units":"3","nano":0},"currentPrice":{"currency":"usd","units":"200","nano":0}}
	]}`)

	require.Len(t, rows, 1)
	assert.Equal(t, "BBG000BPH459", rows[0].Ref, "the ref is the FIGI")
	assert.Equal(t, MarketSPBEX, rows[0].Market)
	assert.Equal(t, "usd", rows[0].Currency)
}

// TestCashTakesItsCurrencyFromTheTicker is the trap the capture exposed, and
// the reason a rouble-only test is not enough: on a cash line currentPrice says
// what the position is WORTH IN, not what is held. 0.88 dollars is priced in
// roubles, so that field reads "rub" — reading the code from there books
// dollars as roubles, an eighty-fivefold error.
//
// Every cash line in the capture opens its ticker with the ISO code
// (USD000UTSTOM, RUB000UTSTOM, EUR_RUB__TOM_CETS); neither figi does
// (USD800UTSTOM, BBG0013HJJ31).
func TestCashTakesItsCurrencyFromTheTicker(t *testing.T) {
	rows, skips := positions(t, `{"accountId":"acc-1","positions":[
		{"figi":"USD800UTSTOM","instrumentType":"currency","ticker":"USD000UTSTOM","classCode":"CNGDOTC",
		 "quantity":{"units":"0","nano":880000000},"currentPrice":{"currency":"rub","units":"85","nano":0}},
		{"figi":"RUB000UTSTOM","instrumentType":"currency","ticker":"RUB000UTSTOM","classCode":"CETS",
		 "quantity":{"units":"3205","nano":980000000},"currentPrice":{"currency":"rub","units":"1","nano":0}},
		{"figi":"BBG0013HJJ31","instrumentType":"currency","ticker":"EUR_RUB__TOM_CETS","classCode":"EES_CETS",
		 "quantity":{"units":"12","nano":0},"currentPrice":{"currency":"rub","units":"95","nano":0}}
	]}`)

	require.Len(t, rows, 3)
	got := map[string]entity.BrokerPosition{}
	for _, r := range rows {
		got[r.Symbol] = r
	}

	require.Contains(t, got, "USD", "a dollar line priced in roubles is still dollars")
	assert.Equal(t, "880000000", got["USD"].Amount, "0.88, not 0.88 roubles")
	assert.Contains(t, got, "EUR")
	assert.Equal(t, "3205980000000", got["RUB"].Amount)

	for symbol, row := range got {
		assert.Empty(t, row.Ref, "cash binds no provider ref (%s)", symbol)
		assert.Equal(t, entity.AssetTypeForex, row.Type)
		assert.Equal(t, "forex", row.Market)
	}
	assert.Equal(t, entity.BrokerSkips{}, skips)
}

// TestBlockedQuantitySplitsThePosition: the pools must not overlap and must sum
// to what the broker reported. Both report shapes are handled because the API
// populates them independently.
func TestBlockedQuantitySplitsThePosition(t *testing.T) {
	t.Run("partially blocked by lots", func(t *testing.T) {
		rows, _ := positions(t, `{"accountId":"acc-1","positions":[
			{"figi":"BBG004730N88","instrumentType":"share","ticker":"GAZP","blocked":true,
			 "blockedLots":{"units":"30","nano":0},
			 "quantity":{"units":"90","nano":0},"currentPrice":{"currency":"rub","units":"140","nano":0}}
		]}`)

		require.Len(t, rows, 2)
		got := map[entity.Liquidity]string{}
		for _, r := range rows {
			got[r.Liquidity] = r.Amount
		}
		assert.Equal(t, "60000000000", got[entity.LiquidityLiquid])
		assert.Equal(t, "30000000000", got[entity.LiquidityLocked],
			"60 + 30 is the reported 90: the pools partition, they do not overlap")
	})

	t.Run("whole line blocked by flag alone", func(t *testing.T) {
		rows, _ := positions(t, `{"accountId":"acc-1","positions":[
			{"figi":"BBG000FINEX","instrumentType":"etf","ticker":"IE00BK224L29","blocked":true,
			 "quantity":{"units":"5","nano":0},"currentPrice":{"currency":"rub","units":"90","nano":0}}
		]}`)

		require.Len(t, rows, 1)
		assert.Equal(t, entity.LiquidityLocked, rows[0].Liquidity)
		assert.Equal(t, "5000000000", rows[0].Amount)
	})

	t.Run("unblocked stays one liquid row", func(t *testing.T) {
		rows, _ := positions(t, `{"accountId":"acc-1","positions":[
			{"figi":"BBG004730N88","instrumentType":"share","ticker":"GAZP",
			 "quantity":{"units":"90","nano":0},"currentPrice":{"currency":"rub","units":"140","nano":0}}
		]}`)
		require.Len(t, rows, 1)
		assert.Equal(t, entity.LiquidityLiquid, rows[0].Liquidity)
	})
}

// TestUnresolvedPositionsAreCountedNotDropped: a sum has to disclose what is
// not in it, and the sync's own report is where that starts.
func TestUnresolvedPositionsAreCountedNotDropped(t *testing.T) {
	rows, skips := positions(t, `{"accountId":"acc-1","positions":[
		{"figi":"BBG00DELISTED","instrumentType":"share","ticker":"GONE",
		 "quantity":{"units":"10","nano":0},"currentPrice":{"currency":"rub","units":"5","nano":0}},
		{"figi":"BBG00EXOTIC","instrumentType":"share","ticker":"EXOT",
		 "quantity":{"units":"10","nano":0},"currentPrice":{"currency":"chf","units":"5","nano":0}},
		{"figi":"BBG00NOVENUE","instrumentType":"share","ticker":"WEIRD",
		 "quantity":{"units":"1","nano":0},"currentPrice":{"currency":"rub","units":"5","nano":0}},
		{"figi":"BBG00FUTURES","instrumentType":"futures","ticker":"SiZ5",
		 "quantity":{"units":"1","nano":0},"currentPrice":{"currency":"rub","units":"5","nano":0}},
		{"figi":"BBG004730N88","instrumentType":"share","ticker":"GAZP",
		 "quantity":{"units":"0","nano":0},"currentPrice":{"currency":"rub","units":"140","nano":0}}
	]}`)

	// Only the delisted rouble line comes back, on a market inferred from its
	// currency. That is the single place this adapter guesses, and the count is
	// what stops the guess being silent.
	require.Len(t, rows, 1)
	assert.Equal(t, "BBG00DELISTED", rows[0].Ref)
	assert.Equal(t, MarketMOEX, rows[0].Market)
	assert.Equal(t, 1, skips.DefaultedMarket)

	assert.Equal(t, 2, skips.UnknownInstrument,
		"an exotic currency and a futures line are both unresolvable")
	assert.Equal(t, 1, skips.UnknownMarket,
		"the catalogue knows this instrument but settles it somewhere we have no market for")
	assert.Equal(t, 3, skips.Total(), "a defaulted market is returned, so it is not a gap")
}

// TestAccountsAreListedSoAnOperatorCanNameThem: one token reaches several
// accounts and each is a separate account here, so their ids have to be
// readable from outside.
func TestAccountsAreListedSoAnOperatorCanNameThem(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, pathAccounts, r.URL.Path)
		_, _ = w.Write([]byte(`{"accounts":[
			{"id":"2000000001","type":"ACCOUNT_TYPE_TINKOFF","name":"Брокерский счёт","status":"ACCOUNT_STATUS_OPEN"},
			{"id":"2000000002","type":"ACCOUNT_TYPE_INVEST_BOX","name":"Инвесткопилка","status":"ACCOUNT_STATUS_OPEN"}
		]}`))
	}))
	t.Cleanup(srv.Close)

	c, err := NewClient(Config{Token: "t", BaseURL: srv.URL, Transport: srv.Client().Transport})
	require.NoError(t, err)
	accounts, err := c.Accounts(context.Background())
	require.NoError(t, err)
	require.Len(t, accounts, 2)
	assert.Equal(t, "2000000001", accounts[0].ID)
	assert.Equal(t, AccountTypeInvestBox, accounts[1].Type,
		"the round-up pot is recognisable so an operator can decline to sync it")
}

// TestPortfolioNeedsAnAccountID: the broker's account id comes from our
// account's data, and an empty one would otherwise be sent as a valid request
// asking about nothing.
func TestPortfolioNeedsAnAccountID(t *testing.T) {
	c, err := NewClient(Config{Token: "t", BaseURL: "http://127.0.0.1:1", Transport: http.DefaultTransport})
	require.NoError(t, err)
	_, err = c.Portfolio(context.Background(), "")
	require.Error(t, err)
}

// TestMoneyValueKeepsItsCurrency guards the rule this provider already bought
// once: one response mixes roubles, dollars and euros, so the amount alone is
// not money.
func TestMoneyValueKeepsItsCurrency(t *testing.T) {
	var m MoneyValue
	require.NoError(t, json.Unmarshal([]byte(`{"currency":"usd","units":"12","nano":500000000}`), &m))
	v, ok := m.Decimal()
	require.True(t, ok)
	assert.Equal(t, "12.5", v.String())
	assert.Equal(t, "usd", m.Currency)
}
