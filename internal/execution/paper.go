package execution

import "fmt"

// paperFillResult is a simulated fill: the absolute positions the account
// ends with, or why there is no fill.
type paperFillResult struct {
	Reason string
	Base   Holding
	Quote  Holding
	Fill   Fill
}

// paperFill fills a buy against the account's own balances, as a simulated
// exchange would: the base grows by quote_amount / price rounded down to the
// base holding's scale, the quote shrinks by quote_amount. A rejection leaves
// every balance as it was.
func paperFill(t Trade, holdings []Holding) paperFillResult {
	reject := func(format string, args ...any) paperFillResult {
		return paperFillResult{Reason: fmt.Sprintf(format, args...)}
	}
	if t.Side != SideBuy {
		return reject("paper venue fills buys only, got %q", t.Side)
	}
	snap := Snapshot{Holdings: holdings}
	quote, quoteFound, quoteAmbiguous := snap.Holding(t.QuoteAssetID)
	base, baseFound, baseAmbiguous := snap.Holding(t.AssetID)
	switch {
	case quoteAmbiguous:
		return reject("account holds %s in more than one holding", t.QuoteAssetID)
	case baseAmbiguous:
		return reject("account holds %s in more than one holding", t.AssetID)
	case !quoteFound:
		return reject("insufficient %s: have 0, need %s", t.QuoteAssetID, t.QuoteAmount)
	case quote.Amount.LessThan(t.QuoteAmount):
		return reject("insufficient %s: have %s, need %s", t.QuoteAssetID, quote.Amount, t.QuoteAmount)
	case !t.QuoteAmount.Shift(int32(quote.Decimals)).IsInteger(): // #nosec G115 -- decimals is a small storage scale
		return reject("quote_amount %s has more decimal places than the %s holding keeps (%d)", t.QuoteAmount, t.QuoteAssetID, quote.Decimals)
	}
	if !baseFound {
		base = Holding{AssetID: t.AssetID, Decimals: defaultDecimals}
	}
	bought := baseBought(t, base.Decimals)
	if !bought.IsPositive() {
		return reject("%s %s buys no %s at %s with %d decimal places", t.QuoteAmount, t.QuoteAssetID, t.AssetID, t.Price, base.Decimals)
	}
	base.Amount = base.Amount.Add(bought)
	quote.Amount = quote.Amount.Sub(t.QuoteAmount)
	return paperFillResult{
		Base:  base,
		Quote: quote,
		Fill:  Fill{BaseAmount: bought, QuoteAmount: t.QuoteAmount, Price: t.Price, PriceAsOf: t.PriceAsOf},
	}
}
