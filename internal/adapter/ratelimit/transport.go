package ratelimit

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/foxcool/greedy-eye/internal/spend"
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
	// rejectedRetry is how long a key the provider refused (401) is
	// reported unusable, so a sweep passes it over and its owner sees why.
	// Hours, like maxEscalatedBackoff: a revoked key stays revoked, and the
	// usual repair is a new key, which is a new bucket; this bounds only how
	// long a refusal the provider took back keeps the key out of the sweep.
	rejectedRetry = 6 * time.Hour
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
	// keyed is whether requests carry a key a 401 can be about. A keyless
	// feed refusing an unnamed client is not refusing a credential.
	keyed bool

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
	caller := spend.CallerFromContext(ctx)
	if err := t.bucket.reserve(spend.ClassFromContext(ctx), caller, t.clock()); err != nil {
		return nil, err
	}

	if err := t.waitOutFreeze(ctx); err != nil {
		return nil, err
	}
	if err := t.waitCost(ctx, costOf(ctx)); err != nil {
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
		streak := t.bucket.noteBackoff(caller)
		base := t.bucket.backoffBase()
		t.bucket.freezeUntil(t.clock().Add(escalate(retryAfter(resp, base), base, streak)))
	} else if t.keyed && spend.RejectsCredential(resp.StatusCode) {
		// Recorded, not enforced: the request path stays open, so a person
		// checking whether a repaired key works gets an answer. Unattended work
		// reads it through Budget.Unusable and passes the key over.
		t.bucket.noteRejected(resp.StatusCode, t.clock().Add(rejectedRetry))
	} else {
		t.bucket.noteSuccess()
	}

	return resp, nil
}

// waitCost takes a request's cost (WithCost) from the bucket. WaitN refuses n
// past the burst outright, and an account may set a burst smaller than one
// call, so a larger cost is taken a full bucket at a time: it is charged in
// full and waits as long as its units take to refill.
func (t *limitedTransport) waitCost(ctx context.Context, cost int) error {
	for cost > 0 {
		n := min(cost, t.bucket.limiter.Burst())
		if err := t.bucket.limiter.WaitN(ctx, n); err != nil {
			return err
		}
		cost -= n
	}
	return nil
}

// escalate doubles the provider's own pause once per consecutive refusal,
// bounded by maxEscalatedBackoff. A streak of one returns base unchanged, so a
// single 429 behaves exactly as it did before. A zero base (Retry-After: 0 or
// a past date) is replaced by fallback, the plan's Backoff.
func escalate(base, fallback time.Duration, streak int64) time.Duration {
	if base <= 0 {
		base = fallback
	}
	d := base
	for i := int64(1); i < streak && d < maxEscalatedBackoff; i++ {
		d *= 2
	}
	return min(d, maxEscalatedBackoff)
}

// ErrFrozen is a request refused at once because the back-off after a
// provider's refusal outlasts the request's deadline. It also matches
// context.DeadlineExceeded, which waiting would have ended in.
var ErrFrozen = errors.New("ratelimit: rate budget frozen past the request deadline")

// waitOutFreeze blocks until an active back-off expires, or the context ends.
// A freeze that ends after the deadline fails at once with ErrFrozen: the
// freeze is read here, as the wait starts, so a pause another refusal has
// just doubled is seen.
func (t *limitedTransport) waitOutFreeze(ctx context.Context) error {
	now := t.clock()
	d := t.bucket.frozenFor(now)
	if d <= 0 {
		return nil
	}
	if deadline, ok := ctx.Deadline(); ok && now.Add(d).After(deadline) {
		return fmt.Errorf("%w for %s: %w", ErrFrozen, d.Round(time.Second), context.DeadlineExceeded)
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
func retryAfter(resp *http.Response, fallback time.Duration) time.Duration {
	raw := resp.Header.Get("Retry-After")
	if raw == "" {
		return fallback
	}

	if secs, err := strconv.Atoi(raw); err == nil {
		return clampBackoff(time.Duration(secs) * time.Second)
	}
	if at, err := http.ParseTime(raw); err == nil {
		return clampBackoff(time.Until(at))
	}

	return fallback
}

func clampBackoff(d time.Duration) time.Duration {
	if d < 0 {
		return 0
	}
	return min(d, maxBackoff)
}
