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
//
// The budget has two dimensions. Rate (RPS/burst) is what a provider meters per
// second; volume (Quota per Period) is what a plan allows per month or day.
// Rate alone is not enough: CoinGecko's free plan was exhausted in eight days
// by a sweep that never exceeded its per-second ceiling.
package ratelimit

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"golang.org/x/time/rate"
)

// ErrQuotaExhausted is returned instead of performing a request when the
// credential's plan volume for the period is spent. Unlike a back-off, which
// waits, this cannot be retried into success before the period rolls over.
var ErrQuotaExhausted = errors.New("provider quota exhausted for the period")

// QuotaPeriod is the window a plan's volume allowance resets on.
type QuotaPeriod string

const (
	// QuotaNone marks a plan that meters only rate, not volume.
	QuotaNone QuotaPeriod = ""
	// QuotaDay resets at midnight UTC.
	QuotaDay QuotaPeriod = "day"
	// QuotaMonth resets on the first of the month, UTC. Providers bill and
	// meter in calendar months, so the reset must match theirs, not a rolling
	// 30 days.
	QuotaMonth QuotaPeriod = "month"
)

// backgroundReserve is the share of a period's quota that unattended work may
// spend. The rest is held for requests a person is waiting on: a sweep that
// empties the allowance on the 20th must not also take the Sync button with it.
const backgroundReserve = 0.8

// Limit is the sustained request rate and plan volume allowed on one credential.
type Limit struct {
	// RPS is the steady-state rate. Fractional values are meaningful: 0.5
	// is one request every two seconds.
	RPS float64
	// Burst is how many requests may go out back-to-back before pacing
	// applies. Keep it at 1 for providers that measure per-second rates —
	// a larger burst is exactly what trips them.
	Burst int
	// Quota is how many requests the plan allows per Period. Zero means the
	// provider meters only rate, which is the usual case for keyless tiers
	// and public infrastructure.
	Quota int
	// Period is the window Quota resets on. Ignored when Quota is zero.
	Period QuotaPeriod
}

// keylessTier is the tier assumed when a credential carries no API key.
// Keyless quotas are per IP and are usually far tighter than keyed ones.
const keylessTier = "keyless"

// Credential identifies whose budget a client draws on.
type Credential struct {
	// Provider is the adapter slug: "coingecko", "subscan", ...
	Provider string
	// APIKey is the secret itself; only its fingerprint is ever retained.
	APIKey string
	// Tier names the plan, matching the suffix in defaultLimits ("demo",
	// "pro"). Empty means infer it: keyless without a key, the provider's
	// free keyed plan with one. Accounts carry it in data["tier"], so a plan
	// change is a settings edit rather than a release.
	Tier string
	// Limit overrides the plan the tier resolves to, field by field; a zero
	// field means "leave the plan's value alone".
	//
	// It belongs to the credential because a plan does: the allowance is that
	// key's, and the operator who owns the key is the one who knows how much of
	// it this deployment may spend. Two deployments sharing one key cannot see
	// each other's spend, so each is told its share here rather than each
	// assuming it owns the whole plan — which is how CoinGecko's month ran out
	// on 2026-08-11 with both instances still reporting room.
	Limit Limit
}

// tier resolves the plan name used to look up a limit.
func (c Credential) tier() string {
	if c.Tier != "" {
		return c.Tier
	}
	if c.APIKey == "" {
		return keylessTier
	}
	return ""
}

// defaultLimits are deliberately below each provider's published ceiling.
// Being slow costs a few seconds on a periodic sync; being banned costs the
// portfolio its data until someone notices.
//
// Keys are "<provider>" for a provider's free keyed plan and
// "<provider>:<tier>" for anything else. Volume allowances are the free plan's
// unless the tier says otherwise — an operator on a paid plan names the tier on
// the account rather than editing this table.
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
	"blockchair":                {RPS: 1, Burst: 1},
	"blockchair:" + keylessTier: {RPS: 0.2, Burst: 1},

	// CoinGecko: ~30 calls/min keyless (shared per IP, in practice less),
	// 30-500/min on demo and paid tiers. The demo plan also caps volume at
	// 10k calls a month, which is the limit that actually bit: the sweep
	// stayed under the rate ceiling and still ran the month dry in a week.
	"coingecko":                {RPS: 1.6, Burst: 1, Quota: 10000, Period: QuotaMonth},
	"coingecko:" + keylessTier: {RPS: 0.5, Burst: 1},
	"coingecko:pro":            {RPS: 8, Burst: 2, Quota: 500000, Period: QuotaMonth},

	"moralis": {RPS: 5, Burst: 2},
	"binance": {RPS: 10, Burst: 5},

	"tonapi":                {RPS: 5, Burst: 2},
	"tonapi:" + keylessTier: {RPS: 1, Burst: 1},

	// Helius free tier; the public RPC it falls back to is far stricter.
	"helius":                {RPS: 5, Burst: 2},
	"helius:" + keylessTier: {RPS: 1, Burst: 1},

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

// Plans returns the built-in plans for a provider, keyed by tier name — "" for
// the free keyed plan, "pro" and "keyless" for what the table names explicitly.
//
// Read by the provider catalogue, so a person choosing a tier in a form is
// choosing from the same table the limiter looks up. Anywhere else the choices
// would be a second copy, and a plan the limiter does not know is a plan that
// silently falls back.
//
// A provider absent from the table has no entry here either: it is metered by
// fallbackLimit, and offering "" as a plan would suggest a published free tier
// that nobody wrote down.
func Plans(provider string) map[string]Limit {
	plans := make(map[string]Limit)
	for key, limit := range defaultLimits {
		switch {
		case key == provider:
			plans[""] = limit
		case strings.HasPrefix(key, provider+":"):
			plans[strings.TrimPrefix(key, provider+":")] = limit
		}
	}
	return plans
}

// Usage is one credential's spend within one period.
type Usage struct {
	Provider string
	// Fingerprint identifies the key without keeping it.
	Fingerprint string
	PeriodStart time.Time
	Requests    int64
	Backoffs    int64
}

// UsageStore persists spend so a restart does not forget the month. A monthly
// allowance tracked only in memory is no allowance at all: a deploy would hand
// the process a fresh one.
type UsageStore interface {
	// LoadUsage returns every credential's spend within the period.
	LoadUsage(ctx context.Context, periodStart time.Time) ([]Usage, error)
	// AddUsage adds deltas to the stored counters, inserting rows that do not
	// exist yet.
	AddUsage(ctx context.Context, deltas []Usage) error
}

// flushInterval is how often unflushed spend reaches the store. Writing per
// request would put a database round trip in front of every provider call;
// losing up to this much spend to a crash is the cheaper error.
const flushInterval = 30 * time.Second

// Registry hands out transports that share a token bucket per credential.
// The zero value is not usable; call NewRegistry.
type Registry struct {
	overrides map[string]Limit
	usage     UsageStore
	now       func() time.Time

	mu      sync.Mutex
	buckets map[string]*bucket
	// loaded is spend read back from the store at start, applied to buckets
	// as they are created. Buckets are made lazily on first use, so the
	// restored counters cannot be pushed into them up front.
	loaded map[string]Usage

	stop chan struct{}
	done chan struct{}
}

// Option configures a Registry.
type Option func(*Registry)

// WithUsageStore makes period spend survive a restart. Without it the counters
// are per process, which is right for tests and wrong for a monthly quota.
func WithUsageStore(s UsageStore) Option {
	return func(r *Registry) { r.usage = s }
}

// WithClock replaces the clock, so tests do not have to wait out a period.
func WithClock(now func() time.Time) Option {
	return func(r *Registry) { r.now = now }
}

// NewRegistry creates a registry. Overrides are keyed by provider slug and
// replace the built-in limit for that provider on every tier, so operators can
// dial a provider down (or up, on a paid plan) without a rebuild.
func NewRegistry(overrides map[string]Limit, opts ...Option) *Registry {
	r := &Registry{
		overrides: overrides,
		now:       time.Now,
		buckets:   make(map[string]*bucket),
		loaded:    make(map[string]Usage),
	}
	for _, opt := range opts {
		opt(r)
	}
	return r
}

// Start restores this period's spend and begins flushing it back. It is a no-op
// without a usage store. A load failure is returned rather than swallowed: a
// silently empty counter would hand the process a fresh monthly allowance.
func (r *Registry) Start(ctx context.Context) error {
	if r == nil || r.usage == nil {
		return nil
	}

	rows, err := r.usage.LoadUsage(ctx, periodStart(r.now(), QuotaMonth))
	if err != nil {
		return err
	}
	dayStart, err := r.usage.LoadUsage(ctx, periodStart(r.now(), QuotaDay))
	if err != nil {
		return err
	}

	r.mu.Lock()
	for _, u := range append(rows, dayStart...) {
		r.loaded[usageKey(u.Provider, u.Fingerprint, u.PeriodStart)] = u
	}
	r.mu.Unlock()

	r.stop = make(chan struct{})
	r.done = make(chan struct{})
	// ctx is the process lifetime, not the startup load's: the flush loop
	// outlives this call and each flush takes its own timeout from it.
	go r.flushLoop(ctx)
	return nil
}

// Stop ends the flush loop and writes what is still pending.
func (r *Registry) Stop(ctx context.Context) error {
	if r == nil || r.stop == nil {
		return nil
	}
	close(r.stop)
	<-r.done
	r.stop = nil
	return r.Flush(ctx)
}

func (r *Registry) flushLoop(ctx context.Context) {
	defer close(r.done)
	ticker := time.NewTicker(flushInterval)
	defer ticker.Stop()

	for {
		select {
		case <-r.stop:
			return
		case <-ctx.Done():
			// The lifetime this registry was started for is over. Stop still
			// writes what is pending, under a context of its own.
			return
		case <-ticker.C:
			flushCtx, cancel := context.WithTimeout(ctx, flushInterval)
			_ = r.Flush(flushCtx)
			cancel()
		}
	}
}

// Flush writes pending spend to the store.
func (r *Registry) Flush(ctx context.Context) error {
	if r == nil || r.usage == nil {
		return nil
	}

	r.mu.Lock()
	deltas := make([]Usage, 0, len(r.buckets))
	for _, b := range r.buckets {
		if u, ok := b.takePending(); ok {
			deltas = append(deltas, u)
		}
	}
	r.mu.Unlock()

	if len(deltas) == 0 {
		return nil
	}
	return r.usage.AddUsage(ctx, deltas)
}

// Transport returns an http.RoundTripper that paces requests against the
// budget for this credential. Every caller passing the same provider and key
// shares one budget, however many clients they build.
//
// A nil base means http.DefaultTransport. A nil Registry returns base
// unchanged, so tests and callers with no budget wiring keep working.
func (r *Registry) Transport(c Credential, base http.RoundTripper) http.RoundTripper {
	if r == nil {
		return base
	}
	if base == nil {
		base = http.DefaultTransport
	}
	return &limitedTransport{
		base:   base,
		bucket: r.bucket(c),
		now:    r.now,
	}
}

// Remaining reports what is left of a credential's period allowance and when
// the period ends. A sweep sizes its portion from this instead of a hardcoded
// cap, so changing the plan on the account changes the budget with it.
// ok is false when the plan meters only rate — there is nothing to divide.
func (r *Registry) Remaining(c Credential) (requests int, periodEnd time.Time, ok bool) {
	if r == nil {
		return 0, time.Time{}, false
	}
	b := r.bucket(c)
	return b.remaining(r.now())
}

// Budget binds one credential to its registry, so a client can size its own
// work without being handed the registry and having to know which credential
// it was built from.
type Budget struct {
	reg  *Registry
	cred Credential
}

// Budget returns the handle for a credential. A nil Registry yields a handle
// that reports no allowance, which callers read as "not metered by volume".
func (r *Registry) Budget(c Credential) *Budget {
	return &Budget{reg: r, cred: c}
}

// Remaining reports the background allowance left in the period and when the
// period ends. ok is false when the plan meters only rate.
func (b *Budget) Remaining() (requests int, periodEnd time.Time, ok bool) {
	if b == nil || b.reg == nil {
		return 0, time.Time{}, false
	}
	return b.reg.Remaining(b.cred)
}

// Unusable reports why this budget's credential cannot carry background work
// now. It is the question a sweep asks before selecting anything for a
// provider: being refused per asset costs a request each time, and each refusal
// is recorded against the ASSET, so its own back-off grows because of an
// allowance it has nothing to do with.
func (b *Budget) Unusable() (string, bool) {
	if b == nil || b.reg == nil {
		return "", false
	}
	return b.reg.Unusable(b.cred)
}

// Snapshot reports spend per credential since the period began. This is the
// number an operator divides by the plan's allowance; there is no metrics
// system in this process to publish it to.
func (r *Registry) Snapshot() []Usage {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()

	out := make([]Usage, 0, len(r.buckets))
	for _, b := range r.buckets {
		out = append(out, b.snapshot())
	}
	return out
}

// bucket returns the shared budget for one credential, creating it on first
// use. Different keys on one provider get different buckets, which is correct:
// providers meter per key, and the keyless tier meters per IP.
func (r *Registry) bucket(c Credential) *bucket {
	fp := fingerprint(c.APIKey)
	key := c.Provider + "/" + fp + "/" + c.tier()

	r.mu.Lock()
	defer r.mu.Unlock()

	if b, ok := r.buckets[key]; ok {
		return b
	}

	limit := r.limitFor(c)
	b := newBucket(c.Provider, fp, limit, periodStart(r.now(), limit.Period))
	if u, ok := r.loaded[usageKey(c.Provider, fp, b.periodStart)]; ok {
		b.restore(u)
	}
	r.buckets[key] = b
	return b
}

// limitFor resolves the limit for a credential: the plan tier decides, and an
// operator override adjusts it field by field.
//
// Field by field, not wholesale, because the two dimensions belong to different
// people. Rate is a property of this deployment — providers meter it per IP as
// much as per key, and the process has one address, so throttling a provider
// after an enforcement notice must reach every credential including a user's
// own. Volume is that key's plan and that key's money, and an operator dialling
// their own rate down has no business touching it.
//
// Returning the override wholesale did exactly that: the config surface carries
// no quota, so its zero silently disabled volume accounting for the whole
// provider — the protection this package exists for. An unset field now means
// "leave the plan's value alone"; only a value an operator actually wrote wins.
func (r *Registry) limitFor(c Credential) Limit {
	limit := fallbackLimit
	if l, ok := defaultLimits[c.Provider]; ok {
		limit = l
	}
	if tier := c.tier(); tier != "" {
		if l, ok := defaultLimits[c.Provider+":"+tier]; ok {
			limit = l
		}
	}

	if o, ok := r.overrides[c.Provider]; ok {
		applyOverride(&limit, o)
	}
	// The credential's own override wins last: it is the most specific thing
	// anyone said about this key.
	applyOverride(&limit, c.Limit)
	return limit
}

// applyOverride merges the fields an operator actually wrote. A zero field is
// silence, not an instruction — returning an override wholesale is what once
// let a surface carrying no quota disable volume accounting for a whole
// provider, which is the protection this package exists for.
func applyOverride(limit *Limit, o Limit) {
	if o.RPS > 0 {
		limit.RPS = o.RPS
	}
	if o.Burst > 0 {
		limit.Burst = o.Burst
	}
	// A quota can only be set deliberately, and it comes with its period.
	if o.Quota > 0 && o.Period != QuotaNone {
		limit.Quota = o.Quota
		limit.Period = o.Period
	}
}

// periodStart truncates a moment to the beginning of its quota period, in UTC —
// providers meter on calendar boundaries, not on a rolling window.
func periodStart(now time.Time, p QuotaPeriod) time.Time {
	utc := now.UTC()
	switch p {
	case QuotaDay:
		return time.Date(utc.Year(), utc.Month(), utc.Day(), 0, 0, 0, 0, time.UTC)
	case QuotaMonth:
		return time.Date(utc.Year(), utc.Month(), 1, 0, 0, 0, 0, time.UTC)
	default:
		return time.Time{}
	}
}

// periodEnd is when the current allowance resets.
func periodEnd(start time.Time, p QuotaPeriod) time.Time {
	switch p {
	case QuotaDay:
		return start.AddDate(0, 0, 1)
	case QuotaMonth:
		return start.AddDate(0, 1, 0)
	default:
		return time.Time{}
	}
}

func usageKey(provider, fp string, periodStart time.Time) string {
	return provider + "/" + fp + "/" + periodStart.UTC().Format(time.RFC3339)
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

// bucket is one credential's budget: a token bucket for rate, a counter for
// period volume, plus a freeze deadline set when the provider tells us to back
// off.
type bucket struct {
	limiter  *rate.Limiter
	limit    Limit
	provider string
	fp       string

	mu          sync.Mutex
	frozenUntil time.Time
	periodStart time.Time
	// requests and backoffs count the whole period, including spend restored
	// from the store; pending counts only what the store has not seen yet.
	requests        int64
	backoffs        int64
	pendingRequests int64
	pendingBackoffs int64
	// streak is how many refusals have arrived with no success between them.
	// It sizes the escalating pause and is deliberately NOT persisted: the
	// stored counters add rather than set (so a reset cannot be expressed) and
	// count the whole period rather than the current run, so nothing in them
	// distinguishes "the last answer was a refusal" from "the last answer was
	// fine". A restart therefore starts the run over — which costs one
	// provider-sized pause, not a burn, because the quota check reads spend
	// that IS persisted and refuses before any request goes out.
	streak int64
}

func newBucket(provider, fp string, l Limit, start time.Time) *bucket {
	burst := max(l.Burst, 1)
	return &bucket{
		limiter:     rate.NewLimiter(rate.Limit(l.RPS), burst),
		limit:       l,
		provider:    provider,
		fp:          fp,
		periodStart: start,
	}
}

// restore seeds the counters with spend already recorded for this period.
func (b *bucket) restore(u Usage) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.requests = u.Requests
	b.backoffs = u.Backoffs
}

// rollPeriod resets the counters when now falls in a later period. Callers must
// hold b.mu.
func (b *bucket) rollPeriod(now time.Time) {
	if b.limit.Period == QuotaNone {
		return
	}
	start := periodStart(now, b.limit.Period)
	if start.Equal(b.periodStart) {
		return
	}
	b.periodStart = start
	b.requests = 0
	b.backoffs = 0
}

// reserve accounts for one request, or refuses it when the credential's
// allowance for its class is spent. It is called before the request goes out:
// a request that fails in transit may still have been metered by the provider,
// so the conservative direction is to count it.
func (b *bucket) reserve(class Class, now time.Time) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.rollPeriod(now)

	if b.limit.Quota > 0 {
		ceiling := b.limit.Quota
		if class == ClassBackground {
			ceiling = int(float64(b.limit.Quota) * backgroundReserve)
		}
		if b.requests >= int64(ceiling) {
			return ErrQuotaExhausted
		}
	}

	b.requests++
	b.pendingRequests++
	return nil
}

// noteBackoff records that the provider asked us to slow down and returns how
// many refusals have now arrived without a success between them.
func (b *bucket) noteBackoff() int64 {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.backoffs++
	b.pendingBackoffs++
	b.streak++
	return b.streak
}

// noteSuccess ends a run of refusals. Any answer that is not a back-off counts,
// including an error status: the provider talked to us, which is the thing a
// back-off said it would not do.
func (b *bucket) noteSuccess() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.streak = 0
}

// takePending returns and clears the spend the store has not seen.
func (b *bucket) takePending() (Usage, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.pendingRequests == 0 && b.pendingBackoffs == 0 {
		return Usage{}, false
	}
	u := Usage{
		Provider:    b.provider,
		Fingerprint: b.fp,
		PeriodStart: b.periodStart,
		Requests:    b.pendingRequests,
		Backoffs:    b.pendingBackoffs,
	}
	b.pendingRequests = 0
	b.pendingBackoffs = 0
	return u, true
}

func (b *bucket) snapshot() Usage {
	b.mu.Lock()
	defer b.mu.Unlock()
	return Usage{
		Provider:    b.provider,
		Fingerprint: b.fp,
		PeriodStart: b.periodStart,
		Requests:    b.requests,
		Backoffs:    b.backoffs,
	}
}

// remaining reports the background allowance left in the period: background
// work is what sizes itself from this, and it may not spend the interactive
// reserve.
func (b *bucket) remaining(now time.Time) (int, time.Time, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.limit.Quota == 0 {
		return 0, time.Time{}, false
	}
	b.rollPeriod(now)

	ceiling := int64(float64(b.limit.Quota) * backgroundReserve)
	left := max(ceiling-b.requests, 0)
	return int(left), periodEnd(b.periodStart, b.limit.Period), true
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

// unusableFor reports why this credential cannot carry a request of the given
// class right now, and when that ends. ok is false when it can.
//
// The two reasons are not the same kind of thing and the caller is told which.
// A spent share ends on a calendar boundary and nothing before it will help; a
// pause ends when it ends, and may end early on the next successful call from
// another caller. Collapsing them into "unavailable" would hide the difference
// between "come back next month" and "come back after lunch".
func (b *bucket) unusableFor(class Class, now time.Time) (string, time.Time, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.rollPeriod(now)

	if b.limit.Quota > 0 {
		ceiling := b.limit.Quota
		if class == ClassBackground {
			ceiling = int(float64(b.limit.Quota) * backgroundReserve)
		}
		if b.requests >= int64(ceiling) {
			end := periodEnd(b.periodStart, b.limit.Period)
			return "this deployment's share of the plan is spent for the period", end, true
		}
	}

	if now.Before(b.frozenUntil) {
		return "paused after repeated refusals", b.frozenUntil, true
	}

	return "", time.Time{}, false
}

// Unusable reports why this credential cannot carry background work now, in a
// phrase fit for a log line. False means go ahead.
//
// Asked per credential rather than per provider slug, because that is the unit
// a plan is metered in. A slug can have several credentials, of which one is
// spent and another fine, and scanning them all cannot tell which one the
// caller actually holds — the caller can, because it holds it.
func (r *Registry) Unusable(c Credential) (string, bool) {
	if r == nil {
		return "", false
	}
	now := r.now()
	reason, until, unusable := r.bucket(c).unusableFor(ClassBackground, now)
	if !unusable {
		return "", false
	}
	return fmt.Sprintf("%s; not before %s", reason, until.UTC().Format(time.RFC3339)), true
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
