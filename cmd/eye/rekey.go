package main

import (
	"context"
	"encoding/base64"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/getsentry/sentry-go"
	"github.com/jackc/pgx/v5/pgxpool"

	storecrypto "github.com/foxcool/greedy-eye/internal/store/crypto"
	"github.com/foxcool/greedy-eye/internal/store/postgres"
)

// rekeyLockID namespaces the advisory lock the rekey job takes. Postgres
// advisory locks share one keyspace per database, so the number is arbitrary but
// must not collide with another job's.
const rekeyLockID int64 = 0x67655F72656B6579 // "ge_rekey"

// rekeyTimeout bounds the background pass. It walks every account row twice
// (rewrap, then verify), which is seconds even on a large instance; a pass still
// running after this is stuck, and holding the advisory lock forever would keep
// every later boot from trying again.
const rekeyTimeout = 10 * time.Minute

// buildEncryptor reads the master keys from config (ADR-005).
//
// EYE_SECURITY_MASTERKEY holds a comma-separated list: the FIRST key is current
// and the only one written with, the rest are accepted on read so rows sealed
// before a rotation stay readable. Rotating is prepending a key; finishing is
// truncating the tail once the rekey job says every row is readable without it.
func buildEncryptor(config *Config, log *slog.Logger) (*storecrypto.Encryptor, error) {
	raw := strings.Split(config.Security.MasterKey, ",")
	keys := make([][]byte, 0, len(raw))
	for _, item := range raw {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		key, err := base64.StdEncoding.DecodeString(item)
		if err != nil {
			return nil, fmt.Errorf("decode security master key %d: %w", len(keys), err)
		}
		keys = append(keys, key)
	}
	if len(keys) == 0 {
		log.Warn("SECURITY_MASTERKEY is not set: accounts.data is stored in plaintext")
		return nil, nil
	}

	encryptor, err := storecrypto.NewEncryptor(keys...)
	if err != nil {
		return nil, fmt.Errorf("init encryptor: %w", err)
	}
	return encryptor, nil
}

// startRekey re-seals every accounts.data row under the current master key in the
// background, when more than one key is configured.
//
// Rotating a master key is otherwise a data-loss event: the rows already written
// stay sealed under the old key, and a decryption failure fails the WHOLE account
// row, so wallet addresses go down with the credentials. Reading with several
// keys keeps the instance alive; this job is what makes the old key droppable.
//
// Three things it does NOT do, each on purpose:
//
//   - It does not re-encrypt on the read path. That would turn every list into a
//     burst of writes, break against a read-only replica, and never converge —
//     a row nobody reads is a row nobody rewrites.
//   - It does not block startup. The service is fully functional mid-rotation;
//     making boot wait on a full table rewrite buys nothing.
//   - It does not run when a single key is configured. Nothing to converge, and
//     rewriting every credential on every deploy is not free.
//
// A Postgres advisory lock keeps it to one instance: several replicas booting
// together would otherwise all rewrite the same rows.
func startRekey(ctx context.Context, pool *pgxpool.Pool, encryptor *storecrypto.Encryptor, log *slog.Logger) {
	if encryptor == nil || encryptor.StaleKeys() == 0 {
		return
	}
	log.Info("stale master keys configured: re-sealing accounts.data in the background",
		"stale_keys", encryptor.StaleKeys())

	go func() {
		ctx, cancel := context.WithTimeout(ctx, rekeyTimeout)
		defer cancel()

		var locked bool
		if err := pool.QueryRow(ctx, `SELECT pg_try_advisory_lock($1)`, rekeyLockID).Scan(&locked); err != nil {
			reportRekeyFailure(log, "rekey: failed to take the advisory lock", err)
			return
		}
		if !locked {
			log.Info("rekey: another instance is already re-sealing accounts.data")
			return
		}
		defer func() {
			if _, err := pool.Exec(context.WithoutCancel(ctx),
				`SELECT pg_advisory_unlock($1)`, rekeyLockID); err != nil {
				log.Error("rekey: failed to release the advisory lock", slog.Any("error", err))
			}
		}()

		store := postgres.NewPortfolioStore(pool, postgres.WithEncryptor(encryptor))
		res, err := store.RewrapAccountData(ctx)
		if err != nil {
			// Counters first: how far a partial pass got is the difference
			// between rerunning it and investigating one row.
			log.Error("rekey: re-sealing stopped", slog.Any("error", err),
				slog.Int("scanned", res.Scanned), slog.Int("rewritten", res.Rewritten))
			sentry.CaptureException(err)
			return
		}

		// The counters cannot say whether the stale keys are still load bearing;
		// only a read with the current key alone can. Skipping this check is how
		// the gate.io row — an encrypted empty map the pass used to skip — stayed
		// under a retired key while the pass reported success.
		verifier := postgres.NewPortfolioStore(pool, postgres.WithEncryptor(encryptor.Current()))
		checked, err := verifier.VerifyAccountDataReadable(ctx)
		if err != nil {
			reportRekeyFailure(log, "rekey: re-sealed rows are NOT all readable with the current key alone; keep the stale keys", err)
			return
		}

		log.Info("rekey: finished — every row is sealed under the current key, stale keys can be removed from SECURITY_MASTERKEY",
			slog.Int("scanned", res.Scanned), slog.Int("rewritten", res.Rewritten), slog.Int("verified", checked))
	}()
}

// reportRekeyFailure makes a failed rotation loud. Nobody is watching a
// background goroutine, and the silent outcome — an operator dropping a key that
// was still load bearing — is unrecoverable.
func reportRekeyFailure(log *slog.Logger, msg string, err error) {
	log.Error(msg, slog.Any("error", err))
	sentry.CaptureException(err)
}
