# Greedy Eye

## Key Files

- README.md - Project overview
- `docs/architecture.md` - Architecture overview
- `docs/development.md` - Development progress
- `cmd/eye/main.go` - Main application entry point
- Makefile - Build automation
- `schema.hcl` - Database schema (Atlas declarative)
- `atlas.hcl` - Atlas configuration

## Project Structure

- `internal/adapter/` - External API clients (Binance, CoinGecko, Telegram, Moralis)
- `internal/entity/` - Domain entities
- `internal/service/` - Business logic services
- `internal/store/postgres/` - PostgreSQL data layer (pgx)
- `internal/testutil/` - Test utilities (testcontainers)
- `api/v1/` - Protocol Buffer definitions

## Testing

```bash
make test-unit        # Unit tests
make test-integration # Integration tests (testcontainers)
make test             # All tests
```

## Database

schema.hcl is the authoring surface; `migrations/` is what instances run.
Never hand-write a migration and never apply schema.hcl to a database:

```bash
make migrate-diff name=what_changed  # schema.hcl edit -> ordered migration file
make migrate-apply                   # run what this database has not run
make migrate-status                  # executed vs pending
make migrate-drift                   # what a live database has that schema.hcl does not
```

`TestMigrationsMatchSchema` (integration) fails when the two surfaces disagree,
so a schema.hcl change without a migration cannot reach main.
