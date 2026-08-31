package tinvest

import (
	"context"
	"fmt"

	"github.com/shopspring/decimal"
)

// MoneyValue is a Quotation that also says which currency it is in.
//
// Kept apart from Quotation deliberately: a portfolio response mixes roubles,
// dollars and euros inside ONE account, so the currency belongs to the row and
// never to the provider. Reading a price without it is the hundredfold error
// this adapter already bought once on quotes (personal-gip.2).
type MoneyValue struct {
	Currency string `json:"currency"`
	Units    string `json:"units"`
	Nano     int32  `json:"nano"`
}

// Decimal converts the amount, ignoring the currency. Callers that value a
// position must read Currency too — the number alone is not money.
func (m MoneyValue) Decimal() (decimal.Decimal, bool) {
	return Quotation{Units: m.Units, Nano: m.Nano}.Decimal()
}

// BrokerAccount is one account held at the broker. A single API token reaches
// several, and each is a separate account in our model: merging them would
// collapse two holdings of the same share into one row and make a transfer
// between them invisible.
type BrokerAccount struct {
	ID          string `json:"id"`
	Type        string `json:"type"`
	Name        string `json:"name"`
	Status      string `json:"status"`
	AccessLevel string `json:"accessLevel"`
}

// Account status and type values this adapter distinguishes.
const (
	AccountStatusOpen = "ACCOUNT_STATUS_OPEN"
	// AccountTypeInvestBox is the broker's round-up savings pot. Deliberately
	// not synced: it held 2.67 roubles at the 2026-08-27 measurement.
	AccountTypeInvestBox = "ACCOUNT_TYPE_INVEST_BOX"
)

// PortfolioPosition is one line of a broker account's portfolio.
//
// It carries what entity.ExchangeBalance cannot: the FIGI that is the only
// honest identity here, the currency of THIS row, the instrument type, and how
// much of the quantity the broker has blocked.
type PortfolioPosition struct {
	FIGI           string `json:"figi"`
	InstrumentType string `json:"instrumentType"`
	// Ticker is frequently NOT a ticker: the 2026-08-27 measurement found
	// US5543821012, DE0005190003 and IE00BK224L29 — ISINs — sitting in this
	// field. Never match on it; it is here to make a log line readable.
	Ticker string `json:"ticker"`
	// ClassCode is the board the position was reported on (TQBR, SPBXM_OTC,
	// FINEX_OTC). Weaker than the catalogue's RealExchange, but it is present on
	// every line and is the fallback when the catalogue does not carry the
	// instrument.
	ClassCode string    `json:"classCode"`
	Quantity  Quotation `json:"quantity"`
	// QuantityLots is the same holding counted in lots. Carried so the two
	// units are visible side by side: BlockedLots below is in THESE, not in the
	// pieces Quantity reports.
	QuantityLots Quotation `json:"quantityLots"`
	// CurrentPrice carries the row's own currency.
	CurrentPrice MoneyValue `json:"currentPrice"`
	// Blocked reports that the broker has restricted the position; BlockedLots
	// says how much of it. Both are read because the two answer different
	// questions and the API populates them independently — see
	// splitByLiquidity.
	Blocked       bool      `json:"blocked"`
	BlockedLots   Quotation `json:"blockedLots"`
	InstrumentUID string    `json:"instrumentUid"`
	PositionUID   string    `json:"positionUid"`
}

// Instrument types the portfolio response uses.
const (
	InstrumentTypeShare    = "share"
	InstrumentTypeEtf      = "etf"
	InstrumentTypeBond     = "bond"
	InstrumentTypeCurrency = "currency"
)

// Portfolio is one account's positions plus the totals the broker computed.
// The totals are not used for valuation — this system values positions itself,
// and a second author for the same number is how the two drift.
type Portfolio struct {
	AccountID string              `json:"accountId"`
	Positions []PortfolioPosition `json:"positions"`
}

// Accounts lists the broker accounts the token reaches.
func (c *Client) Accounts(ctx context.Context) ([]BrokerAccount, error) {
	var out struct {
		Accounts []BrokerAccount `json:"accounts"`
	}
	if err := c.post(ctx, pathAccounts, map[string]string{}, &out); err != nil {
		return nil, err
	}
	return out.Accounts, nil
}

// Portfolio reads one account's positions. The account id is the broker's, not
// ours: it comes from the account's data["broker_account_id"].
func (c *Client) Portfolio(ctx context.Context, brokerAccountID string) (*Portfolio, error) {
	if brokerAccountID == "" {
		return nil, fmt.Errorf("tinvest: portfolio: no broker account id")
	}
	var out Portfolio
	if err := c.post(ctx, pathPortfolio, map[string]string{"accountId": brokerAccountID}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// Bonds lists the tradable bond universe. Loaded because a bond position needs
// a venue to take its market from, and the portfolio response does not carry
// one.
func (c *Client) Bonds(ctx context.Context) ([]Instrument, error) {
	return c.instruments(ctx, pathBonds)
}
