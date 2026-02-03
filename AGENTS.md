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

Schema managed with Atlas CLI:
```bash
make schema-apply     # Apply schema to dev database
make schema-diff      # Show schema changes
```
