package pricefresh

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/timestamppb"

	apiv1 "github.com/foxcool/greedy-eye/api/v1"
)

// now is the clock every case is measured from, so a case reads as an age
// rather than as two dates the reader has to subtract.
var now = time.Date(2026, 8, 8, 12, 0, 0, 0, time.UTC)

// quoteAgo builds a price observed d before now.
func quoteAgo(d time.Duration) *apiv1.Price {
	return &apiv1.Price{Last: "139188600", Decimals: 8, Timestamp: timestamppb.New(now.Add(-d))}
}

func TestStale(t *testing.T) {
	tests := []struct {
		name   string
		price  *apiv1.Price
		policy Policy
		want   bool
		reason string
	}{
		{
			name:   "FXUS: the last over-the-counter print, two months back",
			price:  quoteAgo(67 * 24 * time.Hour),
			want:   true,
			reason: "the case the gate was built for (personal-qc05)",
		},
		{
			name:   "FXRU: last data 2023-08-08, three years dead",
			price:  quoteAgo(3 * 365 * 24 * time.Hour),
			want:   true,
			reason: "a quote that outlived its market by years",
		},
		{
			name:   "an hour old is current",
			price:  quoteAgo(time.Hour),
			want:   false,
			reason: "the hourly sweep's normal output",
		},
		{
			name:   "a rouble rate over a long weekend",
			price:  quoteAgo(47 * time.Hour),
			want:   false,
			reason: "the CBR publishes once a business day; a weekend must not read as staleness",
		},
		{
			name:   "exactly at the threshold is not stale",
			price:  quoteAgo(48 * time.Hour),
			want:   false,
			reason: "the boundary belongs to the fresh side, as in marketdepth",
		},
		{
			name:   "a second past the threshold is stale",
			price:  quoteAgo(48*time.Hour + time.Second),
			want:   true,
			reason: "strictly older than MaxAge",
		},
		{
			name:   "a quote from the future is not stale",
			price:  quoteAgo(-time.Hour),
			want:   false,
			reason: "clock skew between provider and instance is not an age claim",
		},
		{
			name:   "no timestamp",
			price:  &apiv1.Price{Last: "139188600", Decimals: 8},
			want:   false,
			reason: "prices.timestamp is NOT NULL, so an empty one is a plumbing bug, not an old quote",
		},
		{
			name:   "nil price",
			price:  nil,
			want:   false,
			reason: "no row, no claim",
		},
		{
			name:   "a configured threshold overrides the default",
			price:  quoteAgo(2 * time.Hour),
			policy: Policy{MaxAge: time.Hour},
			want:   true,
			reason: "an instance sweeping more often may demand fresher quotes",
		},
		{
			name:   "a zero policy means the default",
			price:  quoteAgo(72 * time.Hour),
			want:   true,
			reason: "an unset MaxAge must not read as 'never stale'",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.policy.Stale(tt.price, now)
			assert.Equal(t, tt.want, got, tt.reason)
		})
	}
}

func TestParsePolicy(t *testing.T) {
	t.Run("a configured duration is honoured", func(t *testing.T) {
		p, err := ParsePolicy([]byte(`{"price_max_age":"6h"}`))
		require.NoError(t, err)
		assert.Equal(t, 6*time.Hour, p.MaxAge)
	})

	t.Run("never configured means the default", func(t *testing.T) {
		p, err := ParsePolicy(nil)
		require.NoError(t, err)
		assert.Equal(t, DefaultMaxAge, p.MaxAge)
	})

	t.Run("an empty duration means the default", func(t *testing.T) {
		p, err := ParsePolicy([]byte(`{}`))
		require.NoError(t, err)
		assert.Equal(t, DefaultMaxAge, p.MaxAge)
	})

	// A broken setting must not decide that everything is current: the caller
	// gets the default back alongside the error, so a valuation still runs under
	// a known rule instead of under whatever the bad value implied.
	t.Run("malformed JSON returns the default and an error", func(t *testing.T) {
		p, err := ParsePolicy([]byte(`{"price_max_age":`))
		require.Error(t, err)
		assert.Equal(t, DefaultMaxAge, p.MaxAge)
	})

	t.Run("an unparseable duration returns the default and an error", func(t *testing.T) {
		p, err := ParsePolicy([]byte(`{"price_max_age":"two days"}`))
		require.Error(t, err)
		assert.Equal(t, DefaultMaxAge, p.MaxAge)
	})

	t.Run("a non-positive duration returns the default and an error", func(t *testing.T) {
		p, err := ParsePolicy([]byte(`{"price_max_age":"0s"}`))
		require.Error(t, err)
		assert.Equal(t, DefaultMaxAge, p.MaxAge)
	})

	t.Run("round trip", func(t *testing.T) {
		raw, err := Policy{MaxAge: 90 * time.Minute}.Encode()
		require.NoError(t, err)
		p, err := ParsePolicy(raw)
		require.NoError(t, err)
		assert.Equal(t, 90*time.Minute, p.MaxAge)
	})
}

func TestParsePolicyDisplayCurrency(t *testing.T) {
	t.Run("a configured currency is honoured", func(t *testing.T) {
		p, err := ParsePolicy([]byte(`{"display_currency":"RUB"}`))
		require.NoError(t, err)
		assert.Equal(t, "RUB", p.QuoteAsset())
	})

	t.Run("never configured means dollars", func(t *testing.T) {
		p, err := ParsePolicy([]byte(`{"price_max_age":"6h"}`))
		require.NoError(t, err)
		assert.Equal(t, DefaultDisplayCurrency, p.QuoteAsset(),
			"an instance that never chose reports in the default, not in nothing")
	})

	t.Run("a ticker is normalized before it is an identity", func(t *testing.T) {
		p, err := ParsePolicy([]byte(`{"display_currency":"  rub "}`))
		require.NoError(t, err)
		assert.Equal(t, "RUB", p.QuoteAsset())
	})

	t.Run("a blank currency returns the default and an error", func(t *testing.T) {
		p, err := ParsePolicy([]byte(`{"display_currency":"   "}`))
		require.Error(t, err)
		assert.Equal(t, DefaultDisplayCurrency, p.QuoteAsset())
	})

	// The two fields are separate statements and fail separately. Reverting a
	// currency somebody chose because a duration is malformed would report one
	// currency's number under another currency's name — the failure mode the
	// whole display/quote split exists to prevent.
	t.Run("a broken duration does not revert a good currency", func(t *testing.T) {
		p, err := ParsePolicy([]byte(`{"price_max_age":"two days","display_currency":"RUB"}`))
		require.Error(t, err)
		assert.Equal(t, DefaultMaxAge, p.MaxAge, "the broken half falls back")
		assert.Equal(t, "RUB", p.QuoteAsset(), "the good half survives")
	})

	t.Run("a broken currency does not revert a good duration", func(t *testing.T) {
		p, err := ParsePolicy([]byte(`{"price_max_age":"6h","display_currency":"  "}`))
		require.Error(t, err)
		assert.Equal(t, 6*time.Hour, p.MaxAge)
		assert.Equal(t, DefaultDisplayCurrency, p.QuoteAsset())
	})

	t.Run("round trip carries both fields", func(t *testing.T) {
		raw, err := Policy{MaxAge: 90 * time.Minute, DisplayCurrency: "EUR"}.Encode()
		require.NoError(t, err)
		p, err := ParsePolicy(raw)
		require.NoError(t, err)
		assert.Equal(t, 90*time.Minute, p.MaxAge)
		assert.Equal(t, "EUR", p.QuoteAsset())
	})

	t.Run("an unset policy still names a currency", func(t *testing.T) {
		assert.Equal(t, DefaultDisplayCurrency, Policy{}.QuoteAsset(),
			"a zero policy must not resolve a quote asset to the empty string")
	})
}

func TestQuotedAt(t *testing.T) {
	t.Run("reports the observation time", func(t *testing.T) {
		assert.Equal(t, now.Add(-time.Hour), QuotedAt(quoteAgo(time.Hour)).UTC())
	})

	t.Run("says nothing for a price that carries no time", func(t *testing.T) {
		assert.True(t, QuotedAt(nil).IsZero())
		assert.True(t, QuotedAt(&apiv1.Price{}).IsZero())
	})
}
