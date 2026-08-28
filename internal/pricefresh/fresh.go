// Package pricefresh answers one question about a quote: was it observed
// recently enough to value a position with?
//
// It exists because GetLatestPrice is ORDER BY timestamp DESC LIMIT 1 with no
// age bound, so a print from before a delisting, a halt or a chain going dark
// was used exactly like one from a minute ago. The nine FinEx securities in the
// prod 'stocks' portfolio are the live case: three last traded on 2023-08-08,
// six over the counter until 2026-06-02, and nothing in a valuation said so.
//
// Staleness does not remove a holding from a total. Dropping it would swing the
// number on every provider outage and swing it back on recovery, which is a
// worse claim than a labelled one: a total that moves reads as a total that is
// current. The policy names what it is unsure of and leaves the arithmetic alone.
package pricefresh

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"connectrpc.com/connect"
	apiv1 "github.com/foxcool/greedy-eye/api/v1"
	"github.com/foxcool/greedy-eye/internal/entity"
)

// SettingKey is where an instance's valuation rules are stored. The name lives
// here rather than in the settings service because the shape behind it does:
// the service allowlists the key, this package is what the value means.
const SettingKey = "valuation.v1"

// DefaultMaxAge is how old a quote may be before a valuation stops calling it
// current.
//
// Two days rather than the hourly cadence of the price sweep, because the sweep
// is not the only clock in play. The CBR publishes one rate per business day and
// MOEX prints only during a session, so an hour-scale threshold would report
// every rouble-denominated position as stale over a weekend — a false alarm on
// every instrument that trades on a calendar rather than continuously. Two days
// clears a weekend plus a holiday and still catches what this is for: a quote
// that outlived its market, which is months old, not hours.
const DefaultMaxAge = 48 * time.Hour

// Policy is the rule set in force for one valuation.
type Policy struct {
	// MaxAge is how old a quote may be and still count as current. Zero means
	// DefaultMaxAge.
	MaxAge time.Duration
	// DisplayCurrency is the asset a total is expressed in when the caller does
	// not name one. Empty means DefaultDisplayCurrency.
	//
	// It rides this document rather than one of its own because it is the same
	// kind of statement: the rules a valuation runs under, chosen by whoever
	// owns the numbers. A person who reads their portfolio in roubles has said
	// something about every total the instance produces, not about one request.
	DisplayCurrency string
}

// DefaultDisplayCurrency is the asset a total is expressed in when nothing says
// otherwise. A symbol rather than a UUID: the catalogue row it names differs per
// instance, and every caller already resolves a quote asset by ticker.
const DefaultDisplayCurrency = "USD"

// DefaultPolicy is what a caller uses when the instance has never been
// configured, or when the settings service is not reachable from it.
func DefaultPolicy() Policy {
	return Policy{MaxAge: DefaultMaxAge, DisplayCurrency: DefaultDisplayCurrency}
}

// settingValue is the stored shape of the valuation setting. The duration is a
// string ("48h") rather than a number: a bare number leaves the unit to a
// convention nobody reads, and the value is JSON text precisely so numbers do
// not get retyped along the way.
type settingValue struct {
	PriceMaxAge     string `json:"price_max_age"`
	DisplayCurrency string `json:"display_currency,omitempty"`
}

// ParsePolicy reads a stored valuation setting. An empty or absent value is not
// an error: never-configured is the normal state and means the default.
func ParsePolicy(raw []byte) (Policy, error) {
	if len(raw) == 0 {
		return DefaultPolicy(), nil
	}
	var v settingValue
	if err := json.Unmarshal(raw, &v); err != nil {
		return DefaultPolicy(), fmt.Errorf("parse valuation setting: %w", err)
	}

	// The two fields fail independently. A broken duration must not silently
	// revert a currency somebody chose, and vice versa: each falls back to its
	// own default and the error names which one was unreadable.
	out := DefaultPolicy()
	var errs []error

	if v.PriceMaxAge != "" {
		switch d, err := time.ParseDuration(v.PriceMaxAge); {
		case err != nil:
			errs = append(errs, fmt.Errorf("parse price_max_age %q: %w", v.PriceMaxAge, err))
		case d <= 0:
			errs = append(errs, fmt.Errorf("price_max_age must be positive, got %q", v.PriceMaxAge))
		default:
			out.MaxAge = d
		}
	}

	// Only the shape is checked here. Whether the symbol names a row in this
	// instance's catalogue is the resolver's question, and it answers loudly:
	// an unknown ticker fails the valuation rather than quietly reverting to
	// dollars, which would report one currency's number under another's name.
	if v.DisplayCurrency != "" {
		if cur := entity.NormalizeSymbol(v.DisplayCurrency); cur == "" {
			errs = append(errs, fmt.Errorf("display_currency %q is blank", v.DisplayCurrency))
		} else {
			out.DisplayCurrency = cur
		}
	}

	return out, errors.Join(errs...)
}

// Encode renders a policy back into the stored shape, so a caller writing the
// setting and a caller reading it agree on the field without restating it.
func (p Policy) Encode() ([]byte, error) {
	return json.Marshal(settingValue{
		PriceMaxAge:     p.maxAge().String(),
		DisplayCurrency: p.displayCurrency(),
	})
}

// SettingReader is the one call this package needs from the settings service.
// Both the in-process handler and the generated Connect client satisfy it.
type SettingReader interface {
	GetSetting(context.Context, *connect.Request[apiv1.GetSettingRequest]) (*connect.Response[apiv1.GetSettingResponse], error)
}

// PolicyFrom loads the caller's valuation policy, falling back to the default
// whenever it cannot.
//
// It never fails the caller. A valuation must still produce a number when the
// settings service is absent, when the key was never written (NotFound is the
// documented "use your default" answer), or when the stored value is broken —
// what it must not do is silently adopt a rule nobody chose, so anything other
// than a clean read is logged.
func PolicyFrom(ctx context.Context, r SettingReader, log *slog.Logger) Policy {
	if r == nil {
		return DefaultPolicy()
	}
	resp, err := r.GetSetting(ctx, connect.NewRequest(&apiv1.GetSettingRequest{Key: SettingKey}))
	if err != nil {
		if connect.CodeOf(err) != connect.CodeNotFound && log != nil {
			log.WarnContext(ctx, "valuation settings unreadable, using defaults",
				"key", SettingKey, "error", err)
		}
		return DefaultPolicy()
	}
	policy, err := ParsePolicy([]byte(resp.Msg.GetSetting().GetValue()))
	if err != nil && log != nil {
		log.WarnContext(ctx, "valuation setting is malformed, using defaults",
			"key", SettingKey, "error", err)
	}
	return policy
}

// Stale reports whether p was observed too long ago to be treated as the current
// price.
//
// A nil price, or one carrying no timestamp, is NOT stale. The same rule governs
// marketdepth.Thin for a missing volume: absence of evidence is not evidence of
// absence, and prices.timestamp is NOT NULL in the schema, so an empty one means
// a path that failed to populate it rather than a quote that is genuinely old.
// Reading it as stale would label a plumbing bug as a market fact.
func (p Policy) Stale(price *apiv1.Price, now time.Time) bool {
	return p.StaleAt(QuotedAt(price), now)
}

// StaleAt is Stale for a caller that already carries the observation time and no
// longer has the price row it came from — which is the usual case once a
// valuation has resolved a price and moved on.
//
// A zero time is not stale, for the same reason an absent timestamp is not.
func (p Policy) StaleAt(quotedAt, now time.Time) bool {
	if quotedAt.IsZero() {
		return false
	}
	return now.Sub(quotedAt) > p.maxAge()
}

// QuotedAt returns when p was observed, or the zero time when it says nothing.
// Callers date a valuation by the oldest quote behind it, and a zero value is
// how they recognise "no observation" rather than the year 1.
func QuotedAt(price *apiv1.Price) time.Time {
	if price == nil || price.Timestamp == nil {
		return time.Time{}
	}
	return price.Timestamp.AsTime()
}

func (p Policy) maxAge() time.Duration {
	if p.MaxAge <= 0 {
		return DefaultMaxAge
	}
	return p.MaxAge
}

// DisplayCurrency is the symbol a total is expressed in under this policy,
// substituting the default for an unset one so callers need not repeat it.
func (p Policy) displayCurrency() string {
	if p.DisplayCurrency == "" {
		return DefaultDisplayCurrency
	}
	return p.DisplayCurrency
}

// QuoteAsset returns the asset a valuation should be expressed in when the
// caller named none. Exported because that choice belongs to the policy, not to
// a constant repeated in every service that produces a total.
func (p Policy) QuoteAsset() string { return p.displayCurrency() }
