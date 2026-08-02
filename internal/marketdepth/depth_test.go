package marketdepth

import (
	"testing"

	apiv1 "github.com/foxcool/greedy-eye/api/v1"
	"github.com/shopspring/decimal"
)

// price builds a quote with volume scaled by 8 decimals, the way the CoinGecko
// adapter stores it. volume nil means the source reported none.
func price(volume *string) *apiv1.Price {
	return &apiv1.Price{Decimals: 8, Last: "139188600", Volume: volume}
}

func ptr(s string) *string { return &s }

func TestThin(t *testing.T) {
	one := decimal.NewFromInt(1)

	tests := []struct {
		name   string
		price  *apiv1.Price
		rate   decimal.Decimal
		want   bool
		reason string
	}{
		{
			name:   "MNEP: a real print over a market that cannot absorb the position",
			price:  price(ptr("4065500000000")), // $40,655
			rate:   one,
			want:   true,
			reason: "the case the gate was built for",
		},
		{
			name:   "ETH: volume orders of magnitude above the floor",
			price:  price(ptr("1500000000000000000")), // $15bn
			rate:   one,
			want:   false,
			reason: "the healthy mode of the distribution",
		},
		{
			name:   "aETHUSDC: no volume reported stays priced",
			price:  price(nil),
			rate:   one,
			want:   false,
			reason: "Aave receipt tokens are real money with no market of their own",
		},
		{
			name:   "exactly at the floor is not thin",
			price:  price(ptr("10000000000000")), // $100,000
			rate:   one,
			want:   false,
			reason: "the threshold is the lower edge of the empty bucket, inclusive",
		},
		{
			name:   "one cent below the floor is thin",
			price:  price(ptr("9999999000000")),
			rate:   one,
			want:   true,
			reason: "boundary belongs to the priced side only when met exactly",
		},
		{
			name:   "reported zero volume is thin",
			price:  price(ptr("0")),
			rate:   one,
			want:   true,
			reason: "nothing writes zero any more, but old rows are not trusted",
		},
		{
			name:   "negative volume is thin",
			price:  price(ptr("-100000000")),
			rate:   one,
			want:   true,
			reason: "same family as the BTL market_cap = -1 the adapter now rejects",
		},
		{
			name:   "unparseable volume is treated as unreported",
			price:  price(ptr("not a number")),
			rate:   one,
			want:   false,
			reason: "knowing nothing about the market is not grounds for dropping the holding",
		},
		{
			name:   "nil price",
			price:  nil,
			rate:   one,
			want:   false,
			reason: "no row, no claim",
		},
		{
			name: "cross path: volume in the traded base converts with the same rate",
			// 2 BTC of volume at $60,000 = $120,000, above the floor. Judged in
			// base units alone it would look like 2 and fail.
			price:  price(ptr("200000000")),
			rate:   decimal.NewFromInt(60000),
			want:   false,
			reason: "volume is denominated in the base the asset trades in",
		},
		{
			name:   "cross path: a thin market stays thin after conversion",
			price:  price(ptr("100000000")), // 1 BTC = $60,000
			rate:   decimal.NewFromInt(60000),
			want:   true,
			reason: "conversion is not a way to sneak past the floor",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Thin(tt.price, tt.rate); got != tt.want {
				t.Errorf("Thin() = %v, want %v — %s", got, tt.want, tt.reason)
			}
		})
	}
}
