package provider

import (
	"log/slog"
	"strconv"
	"strings"

	"github.com/foxcool/greedy-eye/internal/adapter/ratelimit"
	"github.com/foxcool/greedy-eye/internal/entity"
)

// accountCred is the rate-limit credential behind an account-backed client.
// The plan tier sits next to the key in accounts.data, so moving a provider to
// a paid plan is a settings edit rather than a release. CoinGecko's older
// "pro" flag is still honoured: it named the same thing before tiers existed.
func accountCred(provider string, a *entity.Account) ratelimit.Credential {
	tier := a.Data["tier"]
	if tier == "" && a.Data["pro"] == "true" {
		tier = "pro"
	}
	return ratelimit.Credential{
		Provider: provider,
		APIKey:   a.Data["api_key"],
		Tier:     tier,
		Limit:    accountLimit(a),
	}
}

// accountLimit reads a custom request budget out of the account, beside the key
// and the tier it belongs to.
//
// The plan is the key's, so the numbers that carve it up live with the key. An
// operator running two deployments off one credential gives each its share here;
// nothing else can, because the two instances cannot see each other's spend.
//
// A malformed field is dropped with a warning rather than refusing to start.
// This arrives from the database while the process is running — an account can
// be edited at any moment — so the alternative to ignoring one bad value is a
// service that will not boot because somebody mistyped a number in a form.
func accountLimit(a *entity.Account) ratelimit.Limit {
	var l ratelimit.Limit
	l.RPS = accountFloat(a, "rps")
	l.Burst = accountInt(a, "burst")

	quota := accountInt(a, "quota")
	period := ratelimit.QuotaPeriod(strings.TrimSpace(a.Data["period"]))
	switch {
	case quota <= 0:
	case period == ratelimit.QuotaDay || period == ratelimit.QuotaMonth:
		l.Quota, l.Period = quota, period
	default:
		// A volume with no window never resets: the counters roll on a period
		// boundary that does not exist, so the allowance would be spent once and
		// the provider would go quiet until someone noticed.
		slog.Warn("account quota ignored: it needs a period",
			slog.String("account_id", a.ID), slog.String("period", a.Data["period"]))
	}
	return l
}

func accountInt(a *entity.Account, key string) int {
	raw := strings.TrimSpace(a.Data[key])
	if raw == "" {
		return 0
	}
	v, err := strconv.Atoi(raw)
	if err != nil || v < 0 {
		slog.Warn("account rate limit field ignored: not a positive number",
			slog.String("account_id", a.ID), slog.String("field", key), slog.String("value", raw))
		return 0
	}
	return v
}

func accountFloat(a *entity.Account, key string) float64 {
	raw := strings.TrimSpace(a.Data[key])
	if raw == "" {
		return 0
	}
	v, err := strconv.ParseFloat(raw, 64)
	if err != nil || v < 0 {
		slog.Warn("account rate limit field ignored: not a positive number",
			slog.String("account_id", a.ID), slog.String("field", key), slog.String("value", raw))
		return 0
	}
	return v
}
