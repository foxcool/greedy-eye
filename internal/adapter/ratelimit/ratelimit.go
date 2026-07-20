// Package ratelimit shares one request budget between every client built on
// the same provider credential.
//
// Adapter clients are not long-lived: the credentials resolver constructs a
// fresh one per account, per sync, from whichever source won (user → system →
// env). A limiter living inside a client therefore paces one account and is
// blind to the rest — three Substrate accounts in one sweep produce three
// independent budgets and the provider sees their sum. The budget has to be
// keyed by the credential, held for the lifetime of the process, and handed to
// clients rather than owned by them.
package ratelimit

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"sync"
	"time"

	"golang.org/x/time/rate"
)

// Limit is the sustained request rate allowed on one credential.
type Limit struct {
	// RPS is the steady-state rate. Fractional values are meaningful: 0.5
	// is one request every two seconds.
	RPS float64
	// Burst is how many requests may go out back-to-back before pacing
	// applies. Keep it at 1 for providers that measure per-second rates —
	// a larger burst is exactly what trips them.
	Burst int
}

// keylessSuffix marks the limit that applies when no API key is present.
// Keyless quotas are per IP and are usually far tighter than keyed ones.
const keylessSuffix = ":keyless"

// defaultLimits are deliberately below each provider's published ceiling.
// Being slow costs a few seconds on a periodic sync; being banned costs the
// portfolio its data until someone notices.
//
// Rates that came from an enforcement notice rather than documentation are
// marked, since those are measured facts about what the provider actually
// counts.
var defaultLimits = map[string]Limit{
	// Subscan plan #31202 enforces 2 rps and reported us at 3 — a sweep
	// fires one request per network with nothing between them. 1.8 leaves
	// room for the boundary case where evenly spaced requests still land
	// three to a wall-clock second.
	"subscan": {RPS: 1.8, Burst: 1},

	// Blockchair answers 430 ("IP temporarily blacklisted") from the free
	// tier under very little load, so keyless is throttled hard.
	"blockchair":                 {RPS: 1, Burst: 1},
	"blockchair" + keylessSuffix: {RPS: 0.2, Burst: 1},

	// CoinGecko: ~30 calls/min keyless (shared per IP, in practice less),
	// 30-500/min on demo and paid tiers.
	"coingecko":                 {RPS: 1.6, Burst: 1},
	"coingecko" + keylessSuffix: {RPS: 0.5, Burst: 1},

	"moralis": {RPS: 5, Burst: 2},
	"binance": {RPS: 10, Burst: 5},

	"tonapi":                 {RPS: 5, Burst: 2},
	"tonapi" + keylessSuffix: {RPS: 1, Burst: 1},

	// Helius free tier; the public RPC it falls back to is far stricter.
	"helius":                 {RPS: 5, Burst: 2},
	"helius" + keylessSuffix: {RPS: 1, Burst: 1},

	// Keyless public infrastructure run as a courtesy — mempool.space,
	// cosmos LCD endpoints, tzkt.
	"esplora": {RPS: 1, Burst: 1},
	"cosmos":  {RPS: 1, Burst: 1},
	"tzkt":    {RPS: 5, Burst: 2},
}

// fallbackLimit applies to a provider with no entry above. One request per
// second is slow enough to be safe with an unknown quota and fast enough that
// a periodic sync still finishes.
var fallbackLimit = Limit{RPS: 1, Burst: 1}

// Registry hands out transports that share a token bucket per credential.
// The zero value is not usable; call NewRegistry.
type Registry struct {
	overrides map[string]Limit

	mu      sync.Mutex
	buckets map[string]*bucket
}

// NewRegistry creates a registry. Overrides are keyed by provider slug and
// replace the built-in limit for that provider on both tiers, so operators can
// dial a provider down (or up, on a paid plan) without a rebuild.
func NewRegistry(overrides map[string]Limit) *Registry {
	return &Registry{
		overrides: overrides,
		buckets:   make(map[string]*bucket),
	}
}

// Transport returns an http.RoundTripper that paces requests against the
// budget for this provider and key. Every caller passing the same provider and
// key shares one budget, however many clients they build.
//
// A nil base means http.DefaultTransport. A nil Registry returns base
// unchanged, so tests and callers with no budget wiring keep working.
func (r *Registry) Transport(provider, apiKey string, base http.RoundTripper) http.RoundTripper {
	if r == nil {
		return base
	}
	if base == nil {
		base = http.DefaultTransport
	}
	return &limitedTransport{
		base:   base,
		bucket: r.bucket(provider, apiKey),
	}
}

// bucket returns the shared budget for one credential, creating it on first
// use. Different keys on one provider get different buckets, which is correct:
// providers meter per key, and the keyless tier meters per IP.
func (r *Registry) bucket(provider, apiKey string) *bucket {
	key := provider + "/" + fingerprint(apiKey)

	r.mu.Lock()
	defer r.mu.Unlock()

	if b, ok := r.buckets[key]; ok {
		return b
	}
	b := newBucket(r.limitFor(provider, apiKey))
	r.buckets[key] = b
	return b
}

// limitFor resolves the limit for a provider and tier: an operator override
// first, then the keyless entry when there is no key, then the provider's
// keyed entry, then the fallback.
func (r *Registry) limitFor(provider, apiKey string) Limit {
	if l, ok := r.overrides[provider]; ok {
		return l
	}
	if apiKey == "" {
		if l, ok := defaultLimits[provider+keylessSuffix]; ok {
			return l
		}
	}
	if l, ok := defaultLimits[provider]; ok {
		return l
	}
	return fallbackLimit
}

// fingerprint identifies a key without keeping it. The registry outlives every
// request and is reachable from a panic dump, so it holds a digest instead of
// the secret.
func fingerprint(apiKey string) string {
	if apiKey == "" {
		return "keyless"
	}
	sum := sha256.Sum256([]byte(apiKey))
	return hex.EncodeToString(sum[:8])
}

// bucket is one credential's budget: a token bucket plus a freeze deadline set
// when the provider tells us to back off.
type bucket struct {
	limiter *rate.Limiter

	mu          sync.Mutex
	frozenUntil time.Time
}

func newBucket(l Limit) *bucket {
	burst := max(l.Burst, 1)
	return &bucket{limiter: rate.NewLimiter(rate.Limit(l.RPS), burst)}
}

// freezeUntil pushes the deadline out, never in: a later 429 while a freeze is
// already running must not shorten it.
func (b *bucket) freezeUntil(t time.Time) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if t.After(b.frozenUntil) {
		b.frozenUntil = t
	}
}

// frozenFor reports how long the caller must wait before the next request.
func (b *bucket) frozenFor(now time.Time) time.Duration {
	b.mu.Lock()
	defer b.mu.Unlock()
	if now.Before(b.frozenUntil) {
		return b.frozenUntil.Sub(now)
	}
	return 0
}
