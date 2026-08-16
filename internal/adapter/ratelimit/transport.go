package ratelimit

import (
	"context"
	"net/http"
	"strconv"
	"time"
)

const (
	// defaultBackoff applies when a provider says "too many requests" without
	// saying for how long.
	defaultBackoff = time.Minute
	// maxBackoff caps what a provider can talk us into. A Retry-After of a
	// day would silently take the sync offline until a restart.
	maxBackoff = 15 * time.Minute
	// maxEscalatedBackoff caps the pause we impose on ourselves after repeated
	// refusals. It is deliberately larger than maxBackoff: that one bounds what
	// a provider may ask for, this one bounds how long we keep believing the
	// provider will change its mind.
	//
	// Hours rather than days. A plan that has run dry stays dry until the period
	// rolls over, so a longer ceiling would be closer to the truth — and would
	// also mean that one bad afternoon costs a day of prices when the cause was
	// transient. Retrying hourly against an exhausted plan is cheap; staying
	// silent through a recovery is not.
	maxEscalatedBackoff = 2 * time.Hour
)

// backoffStatuses are the responses that mean "stop sending for a while".
//
//   - 429 is the standard one.
//   - 418 is Binance telling us an IP ban is already in effect.
//   - 430 is Blockchair's "IP temporarily blacklisted", which its free tier
//     returns under very little load. It is not a registered status code, but
//     it is what that provider actually sends.
var backoffStatuses = map[int]bool{
	http.StatusTooManyRequests: true,
	http.StatusTeapot:          true,
	430:                        true,
}

// limitedTransport paces requests against a shared bucket and honours the
// provider's own back-off signals.
type limitedTransport struct {
	base   http.RoundTripper
	bucket *bucket

	// now is injectable so tests do not have to wait out a freeze.
	now func() time.Time
}

func (t *limitedTransport) clock() time.Time {
	if t.now != nil {
		return t.now()
	}
	return time.Now()
}

// RoundTrip waits for the budget, then performs the request.
//
// A back-off response is returned to the caller unchanged rather than
// converted into an error. Adapters already read status codes and bodies —
// Blockchair puts the reason for its 430 in the body, and CoinGecko's partial
// failure handling depends on seeing the response — so turning it into a
// transport error would destroy information the callers use. The rate limiter
// records the back-off and gets out of the way.
func (t *limitedTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	ctx := req.Context()

	// The volume check comes before the waits: a request that has no allowance
	// left should fail now rather than after sitting out a freeze for it.
	if err := t.bucket.reserve(ClassFromContext(ctx), t.clock()); err != nil {
		return nil, err
	}

	if err := t.waitOutFreeze(ctx); err != nil {
		return nil, err
	}
	if err := t.bucket.limiter.Wait(ctx); err != nil {
		return nil, err
	}

	resp, err := t.base.RoundTrip(req)
	if err != nil {
		return nil, err
	}

	if backoffStatuses[resp.StatusCode] {
		// The pause grows with each refusal that no success has interrupted.
		//
		// A provider says "too many requests" for two different reasons and does
		// not distinguish them: the rate was too high, or the plan is spent. The
		// first clears in seconds; the second does not clear at all before the
		// period rolls over. Honouring Retry-After alone treats both as the
		// first, so an instance that has run into a spent plan thaws and asks
		// again, indefinitely — dev spent four and a half days doing exactly
		// that, 70 refusals deep, every request certain to be refused.
		//
		// Escalating asserts nothing about which reason applies. It only stops
		// paying for an answer we keep being given, and it heals itself: one
		// successful response resets the run.
		streak := t.bucket.noteBackoff()
		t.bucket.freezeUntil(t.clock().Add(escalate(retryAfter(resp), streak)))
	} else {
		t.bucket.noteSuccess()
	}

	return resp, nil
}

// escalate doubles the provider's own pause once per consecutive refusal,
// bounded by maxEscalatedBackoff. A streak of one returns base unchanged, so a
// single 429 behaves exactly as it did before.
func escalate(base time.Duration, streak int64) time.Duration {
	if base <= 0 {
		base = defaultBackoff
	}
	d := base
	for i := int64(1); i < streak && d < maxEscalatedBackoff; i++ {
		d *= 2
	}
	return min(d, maxEscalatedBackoff)
}

// waitOutFreeze blocks until an active back-off expires, or the context ends.
func (t *limitedTransport) waitOutFreeze(ctx context.Context) error {
	d := t.bucket.frozenFor(t.clock())
	if d <= 0 {
		return nil
	}

	timer := time.NewTimer(d)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// retryAfter reads the header in both of its permitted forms, falling back to
// a fixed pause when it is absent or unreadable. The result is always within
// [0, maxBackoff].
func retryAfter(resp *http.Response) time.Duration {
	raw := resp.Header.Get("Retry-After")
	if raw == "" {
		return defaultBackoff
	}

	if secs, err := strconv.Atoi(raw); err == nil {
		return clampBackoff(time.Duration(secs) * time.Second)
	}
	if at, err := http.ParseTime(raw); err == nil {
		return clampBackoff(time.Until(at))
	}

	return defaultBackoff
}

func clampBackoff(d time.Duration) time.Duration {
	if d < 0 {
		return 0
	}
	return min(d, maxBackoff)
}
