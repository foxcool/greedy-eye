# ADR-001: Encryption at rest for accounts.data

- **Status**: accepted
- **Date**: 2026-07-03
- **Related**: account capability model (PR #22) introduces provider credentials
  (API keys/secrets) stored in `accounts.data`

## Context

`accounts.data` is a `jsonb` `map[string]string` holding per-account payloads:
wallet addresses and chains today, provider API keys and secrets once credentials
move from env config into the database. Until now the column was stored in
plaintext. Requirements:

- Secrets must be encrypted at rest; a database dump alone must not leak keys.
- The application reads the map only as a whole through the store layer
  (no SQL touches individual `data` fields), so queryability of the column
  content is not required.
- Existing plaintext rows must remain readable (backward compatibility).
- Development setups without key material must keep working.

## Decision

### Application-level AES-256-GCM (not pgcrypto)

Encryption/decryption happens in the Go store layer. The key never reaches the
database process, SQL statements, or server logs; the scheme is portable across
PostgreSQL versions and is unit-testable without a database.

### Encrypt the whole data map

The entire JSON-serialized map is encrypted as one blob. No per-field secret
classification to maintain and no partially readable states. The trade-off —
wallet addresses are no longer visible in raw SQL — is acceptable because
nothing queries `data` via SQL.

### On-disk format: jsonb wrapper, no schema migration

Encrypted rows store:

```json
{"enc": "v1:<base64(nonce || ciphertext)>"}
```

- The column stays `jsonb`; no Atlas migration is needed.
- Read path: a map with the single key `enc` is decrypted; any other shape is a
  legacy plaintext row and is returned as is.
- Write path always encrypts (when a key is configured), so legacy rows converge
  to encrypted form on their next update.
- The `v1:` prefix versions the format for future key/algorithm rotation.
- Reading an `enc` wrapper without a configured key fails loudly rather than
  returning ciphertext as data.

### Key management

- `EYE_SECURITY_MASTERKEY` — 32-byte master key, base64-encoded, from the
  environment (koanf `security.masterKey`).
- Per-record key: `HKDF-SHA256(master, info="greedy-eye/accounts/<account_id>")`
  (Go stdlib `crypto/hkdf`). Ciphertext is bound to its row: copying an
  encrypted value onto another account fails authentication.
- Random 12-byte nonce per encryption, prepended to the ciphertext.
- Empty/unset key = plaintext mode with a prominent startup warning. Production
  deployments must set the key; this is a deploy-time contract, not enforced in
  code, to keep dev environments frictionless.

## Out of scope (follow-ups)

- Master key rotation (`v2:` format and/or previous-key fallback) and a backfill
  job re-encrypting legacy plaintext rows.
- API response masking of secret fields — handled by the RPC-surface task of the
  credentials epic; this ADR only covers storage.

## Alternatives considered

1. **pgcrypto** — key material travels through SQL and can surface in
   `pg_stat_statements`/server logs; couples the scheme to PostgreSQL. Rejected.
2. **Per-field encryption of secret keys only** — keeps addresses readable in
   psql but requires maintaining a secret-field classification and produces
   mixed-plaintext states; more code for marginal benefit. Rejected.
3. **Single static key without HKDF** — simpler, but ciphertexts become
   swappable between rows and one nonce space is shared across all records.
   Per-record derivation is cheap insurance. Rejected.
