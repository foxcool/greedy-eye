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
		t.bucket.freezeUntil(t.clock().Add(retryAfter(resp)))
	}

	return resp, nil
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
