package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/foxcool/greedy-eye/internal/adapter/ratelimit"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ProviderUsageStore persists how much of each provider credential's plan has
// been spent, so a monthly allowance survives a restart. Without it a deploy
// hands the process a fresh quota and the real one runs out unannounced.
type ProviderUsageStore struct {
	pool *pgxpool.Pool
}

// Compile-time interface implementation check.
var _ ratelimit.UsageStore = (*ProviderUsageStore)(nil)

func NewProviderUsageStore(pool *pgxpool.Pool) *ProviderUsageStore {
	return &ProviderUsageStore{pool: pool}
}

// LoadUsage returns every credential's spend within one period.
func (s *ProviderUsageStore) LoadUsage(ctx context.Context, periodStart time.Time) ([]ratelimit.Usage, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT provider, key_fingerprint, period_start, requests, backoffs
		FROM provider_usage
		WHERE period_start = $1`, periodStart)
	if err != nil {
		return nil, fmt.Errorf("failed to load provider usage: %w", err)
	}
	defer rows.Close()

	var out []ratelimit.Usage
	for rows.Next() {
		var u ratelimit.Usage
		if err := rows.Scan(&u.Provider, &u.Fingerprint, &u.PeriodStart, &u.Requests, &u.Backoffs); err != nil {
			return nil, fmt.Errorf("failed to scan provider usage: %w", err)
		}
		out = append(out, u)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed to iterate provider usage: %w", err)
	}
	return out, nil
}

// AddUsage adds deltas to the stored counters. Adding rather than setting keeps
// two instances of the backend from overwriting each other's spend — the
// provider meters their sum, and so should this.
func (s *ProviderUsageStore) AddUsage(ctx context.Context, deltas []ratelimit.Usage) error {
	if len(deltas) == 0 {
		return nil
	}

	providers := make([]string, len(deltas))
	fingerprints := make([]string, len(deltas))
	starts := make([]time.Time, len(deltas))
	requests := make([]int64, len(deltas))
	backoffs := make([]int64, len(deltas))
	for i, d := range deltas {
		providers[i] = d.Provider
		fingerprints[i] = d.Fingerprint
		starts[i] = d.PeriodStart
		requests[i] = d.Requests
		backoffs[i] = d.Backoffs
	}

	_, err := s.pool.Exec(ctx, `
		INSERT INTO provider_usage
			(provider, key_fingerprint, period_start, requests, backoffs, updated_at)
		SELECT * FROM unnest(
			$1::varchar[], $2::varchar[], $3::timestamptz[],
			$4::bigint[], $5::bigint[]
		) AS d(provider, key_fingerprint, period_start, requests, backoffs),
		LATERAL (SELECT now()) AS t(updated_at)
		ON CONFLICT (provider, key_fingerprint, period_start) DO UPDATE SET
			requests   = provider_usage.requests + EXCLUDED.requests,
			backoffs   = provider_usage.backoffs + EXCLUDED.backoffs,
			updated_at = EXCLUDED.updated_at`,
		providers, fingerprints, starts, requests, backoffs)
	if err != nil {
		return fmt.Errorf("failed to add provider usage: %w", err)
	}
	return nil
}
