# Development Guide - Greedy Eye

Complete guide for developing and maintaining the Greedy Eye universal portfolio management system supporting diverse
asset types including cryptocurrencies, securities, derivatives, and alternative assets.

> **Source of truth**: `api/v1/*.proto` (API), `schema.hcl` (database),
> `cmd/eye/main.go` (wiring). This guide is hand-maintained prose — when it
> disagrees with the code, the code wins. The backend is a 4-service Connect-RPC
> modular monolith (MarketDataService, PortfolioService, AutomationService,
> AnalyticsService) on pgx raw SQL — not Ent, not gRPC-Gateway, not the older
> 8-service design.

## Quick Start

### Prerequisites
- Go 1.25+
- Docker (for testcontainers and dev environment)
- Atlas CLI (`curl -sSf https://atlasgo.sh | sh`)
- buf CLI (for Protocol Buffers)
- Make (for build automation)

### Setup
```bash
git clone https://github.com/foxcool/greedy-eye.git
cd greedy-eye

# Install dependencies
go mod download

# Generate protobuf code
make buf-gen

# Start dev environment (migrations run automatically before the app starts)
make up
```

### Alternative Quick Start (Docker Compose)
```bash
# Start entire system with Docker (the migrate service runs first)
make up

# View logs
make logs

# Stop system
make down
```

The application starts:
- HTTP/2 (h2c) server on port 8080 — serves both Connect RPC and REST
- Health check at http://localhost:8080/eye/health

### Essential Development Commands
```bash
# Development workflow
make up               # Start dev environment (runs pending migrations first)
make migrate-apply    # Run pending migrations by hand
make buf-gen          # Generate protobuf code

# Testing
make test             # Run all tests (unit + integration)
make test-unit        # Run unit tests only
make test-integration # Run integration tests (uses testcontainers)

# Database operations
make migrate-diff name=what_changed  # Turn a schema.hcl edit into a migration
make migrate-apply    # Run pending migrations
make migrate-status   # Executed vs pending
make migrate-drift    # What the dev database has that schema.hcl does not

# Build and deployment
make clean            # Clean up docker resources
docker build .        # Build Docker image
```

## Multi-Service Dev Stack (Traefik)

greedy-eye integrates into a shared Traefik proxy for path-based routing across multiple local services. Connect RPC uses h2c (HTTP/2 cleartext), configured via `server.scheme=h2c` in Traefik labels.

```
Internet / curl
      │
  Traefik :80/:443
      │
      └── Host($EYE_DOMAIN) && PathPrefix(/eye)  → eye-dev:8080  [h2c]
```

Connect RPC paths follow the pattern `/{package}.{ServiceName}/{Method}`.
With package `eye.v1`, all endpoints are under `/eye.v1.*` which matches the `/eye` prefix.

### Prerequisites

- External Docker network `proxy` with a running Traefik instance
- `deploy/.env` with domain configuration (copy from `.env.example`)

```bash
docker network create proxy
cp deploy/.env.example deploy/.env
# edit deploy/.env if needed
```

### Start Order

```bash
# 1. Start Traefik (external, managed separately)
# 2. Start greedy-eye dev stack
make up

# Debug profile (Delve on :40000)
docker compose -f deploy/compose.yaml --profile debug up
```

### Routing

Traefik routes `PathPrefix("/eye")` on `$EYE_DOMAIN` to `eye-dev:8080` via h2c (configured in `deploy/.env`).
Domain is set in `deploy/.env` and passed to compose via `--env-file deploy/.env` in the Makefile.

Delve debugger is available directly on `localhost:40000` (not routed through Traefik).

### Troubleshooting

```bash
# Verify container is in both networks
docker inspect eye-dev --format '{{json .NetworkSettings.Networks}}' | jq 'keys'
# Expected: ["eye_default", "proxy"]

# Check Traefik picked up the routes
curl https://$EYE_DOMAIN/eye/health

# Direct health check (bypassing Traefik)
docker exec eye-dev curl -s localhost:8080/eye/health
```

## Current Development Status

The backend is a working 3-service Connect-RPC monolith. Core CRUD, portfolio
valuation, credential-backed external integrations, account sync (wallet +
exchange), and ownership enforcement are in place. The automation rule engines
(DCA / rebalancing / stop-loss) and the cron scheduler are the main open items.

### Service Status

| Service | Location | Status |
|---------|----------|--------|
| MarketDataService | `internal/service/marketdata/` | ✅ Assets + prices CRUD, FetchExternalPrices (resolver) |
| PortfolioService | `internal/service/portfolio/` | ✅ CRUD, CalculatePortfolioValue, SyncAccount (wallet + exchange), ownership |
| AutomationService | `internal/service/automation/` | 🔄 Rule/execution CRUD + status ops done; execution engines stubbed |
| AnalyticsService | `internal/service/analytics/` | 🔄 GetHeatmap scope=portfolio+balance (flat / by account / by portfolio, change% color); market/basket pending data prerequisites |
| Credentials resolver | `internal/service/credentials/` | ✅ Per-account provider clients (user → system → env) |
| User provisioning | `internal/middleware/user.go` | ✅ Header-based (psina), lazy provision, roles per request |

Stores: `MarketDataStore`, `PortfolioStore`, `AutomationStore`, `UserStore`
(`internal/store/postgres/`, pgx raw SQL). There is no Ent, no StorageService,
and no standalone Settings/Asset/Price/Rule/Messenger service — those were the
pre-ADR-003 8-service design and no longer exist.

### External Adapters Status

The credentials resolver builds per-account clients from stored credentials. No
credential comes from configuration: a key names a plan, a plan names money, and
both belong to the account rather than to the service running beside it.

| Adapter | Provider | Status |
|---------|----------|--------|
| Blockchain | Moralis (`internal/adapter/moralis/`) | ✅ Multi-chain wallet balance sync (`WalletSyncer`) |
| Exchange | Binance (`internal/adapter/binance/`) | ✅ Signed spot balance sync (`ExchangeSyncer`); ticker prices (batch has a known issue) |
| Price Data | CoinGecko (`internal/adapter/coingecko/`) | ✅ Live prices |
| Messenger | Telegram (`internal/adapter/telegram/`) | Notifications |

### Recent Milestones

- **Account capabilities & system scopes**: credential model on accounts, admin-shared system scopes, RPC + FE UI.
- **Encryption at rest (ADR-005)**: AES-256-GCM + per-record HKDF for `accounts.data`; write-only masked secrets over the API.
- **Credentials resolver**: per-account adapter factories replacing startup singletons; credentials come only from accounts.
- **Ownership audit (IDOR)**: every by-ID / list RPC enforces caller ownership; `user_id` overrides admin-only.
- **Account sync**: wallet balances (Moralis) and exchange balances (Binance, signed `GET /api/v3/account`).

## Development Workflow

### Protocol Buffers
```bash
# Generate all protobuf code
make buf-gen

# Lint proto files
make buf-lint

# Format proto files  
make buf-format
```

### Database Operations
```bash
# Turn a schema.hcl edit into an ordered migration file
make migrate-diff name=holdings_excluded_source

# Run what this database has not run yet
make migrate-apply

# What it has run, and what is pending
make migrate-status

# What a live database holds that schema.hcl does not (read-only)
make migrate-drift
```

See [Schema](#schema) for what these mean and when a database needs a baseline.

### Testing
```bash
# Run all tests
make test

# Run unit tests only
make test-unit

# Run integration tests (uses testcontainers - ephemeral PostgreSQL)
make test-integration

# Run specific store tests
go test -v -tags=integration ./internal/store/postgres/...
```

## Project Structure

```
greedy-eye/
├── api/v1/                 # Protocol Buffer definitions (domain-based)
│   ├── marketdata.proto    # Asset + Price management
│   ├── portfolio.proto     # Portfolio + Holding + Account + Transaction
│   └── automation.proto    # Rule + RuleExecution
├── cmd/eye/                # Main application entry point
├── internal/
│   ├── adapter/            # External service adapters
│   │   ├── binance/        # Binance exchange client
│   │   ├── coingecko/      # CoinGecko price data client
│   │   ├── moralis/        # Moralis blockchain client
│   │   └── telegram/       # Telegram bot client
│   ├── entity/             # Domain entities
│   ├── middleware/         # Connect interceptors (user provisioning, ownership)
│   ├── service/            # Connect handlers + business logic
│   │   ├── marketdata/     # MarketDataService handler
│   │   ├── portfolio/      # PortfolioService handler
│   │   ├── analytics/      # AnalyticsService handler (heatmaps)
│   │   ├── automation/     # AutomationService handler
│   │   └── credentials/    # Per-account provider client resolver
│   ├── store/              # Data persistence layer
│   │   ├── postgres/       # PostgreSQL implementation (pgx raw SQL)
│   │   └── crypto/         # accounts.data encryption (ADR-005)
│   └── testutil/           # Test utilities (testcontainers)
├── api/v1/                 # Generated protobuf + Connect code (committed)
├── schema.hcl              # Database schema (Atlas HCL)
├── atlas.hcl               # Atlas configuration
├── docs/                   # Documentation
└── deploy/                 # Docker and deployment configs
```

## Configuration

### Environment Variables

Config is loaded via koanf; env vars use the `EYE_` prefix and map to the config
struct (see `cmd/eye/config.go`). A single Connect-RPC server listens on one HTTP
port (h2c) — there is no separate gRPC port or gateway.

```env
# Database
EYE_DB_URL=postgres://user:pass@localhost:5432/greedy_eye?sslmode=disable

# Server (single h2c port serving Connect + gRPC)
EYE_SERVER_PORT=8080

# Logging
EYE_LOG_LEVEL=INFO            # DEBUG, INFO, WARN, ERROR

# Secrets: accounts.data encryption (ADR-005). base64 of 32 random bytes each,
# comma-separated. The FIRST key is current and the only one written with; any
# others are read-only, for rows sealed before a rotation.
# Empty = plaintext mode with a startup warning (dev only).
EYE_SECURITY_MASTERKEY=...    # openssl rand -base64 32
# mid-rotation it looks like: EYE_SECURITY_MASTERKEY=<new>,<old>

# No provider API keys here. A credential is entered as an ACCOUNT, through the
# UI or MCP, and carries its own tier and limits in data. Unattended work uses
# the sole operator's accounts when nobody granted a system scope, so a
# single-user instance needs no admin role to get its sweep running.
#
# A fresh instance therefore prices nothing until someone enters a key — which
# is the honest state, rather than a variable quietly working until it does not.

# Telegram notifications
EYE_TELEGRAM_TOKEN=your_token
EYE_TELEGRAM_CHATIDS="-1001234567890,987654321"

# Background scheduler (periodic rules + price fetch)
EYE_SCHEDULER_ENABLED=true              # default true; enable on ONE instance only
EYE_SCHEDULER_PRICEFETCHCRON="0 * * * *"  # empty = price job off
# The interval is also the sweep's budget window: a provider divides what is
# left of its monthly plan by it. Shortening it makes each sweep smaller, not
# the month more expensive.

# Provider request budgets are NOT set here. A plan belongs to the key it was
# issued for, so both the rate and the volume live on the provider account,
# beside that key — see docs/providers.md. This file configures the service,
# not the providers it talks to.

# Observability
EYE_SENTRY_DSN=your_dsn
```

### Configuration File Example (config.yaml)
```yaml
# Logging settings
logging:
  output: "STDOUT"     # Can be "STDOUT" or a file path
  level: "INFO"        # DEBUG, INFO, WARN, ERROR, FATAL
  format: "JSON"       # TEXT or JSON

# Telegram Bot settings
telegram:
  token: "YOUR_TELEGRAM_BOT_TOKEN"
  chatIDs:
    - "-1001234567890"  # Group chat ID
    - "987654321"       # Private chat ID

# Enabled services
services:
  - marketdata
  - portfolio
  - automation
```

### Rotating the master key

`accounts.data` is sealed per row under a key derived from `EYE_SECURITY_MASTERKEY`
(ADR-005). **Replacing** that value re-encrypts nothing: every existing row stays sealed under
the old key, and the store fails the whole account row when it cannot decrypt — so wallet
addresses stop being readable along with the credentials. So you prepend rather than replace:

```bash
# 1. prepend the new key; the old one stays for reads
EYE_SECURITY_MASTERKEY=<new>,<old>      # openssl rand -base64 32

# 2. restart. The instance writes with <new>, reads with either, and starts a
#    background rekey job because more than one key is configured.

# 3. wait for the log line:
#    "rekey: finished — every row is sealed under the current key, stale keys can be removed"

# 4. drop the tail: EYE_SECURITY_MASTERKEY=<new>, restart
```

The instance is fully functional between steps 1 and 4; step 4 is what actually retires the old
key. Going straight to it is what makes the data unrecoverable.

Do not skip step 3. The job does not just re-seal, it then re-reads every row with the current
key **alone** and only claims completion if all of them open — counters alone cannot tell you
whether a stale key is still load bearing. On failure it logs at ERROR and reports to Sentry;
if you see no completion line, the old key is still needed.

Notes on how it behaves:

- One key configured: the job does not run. Nothing to converge, and rewriting every credential
  on every deploy is not free.
- Several instances: a Postgres advisory lock keeps the pass to one of them.
- Interrupted or repeated runs are safe — re-sealing a row already current just gives it a fresh
  nonce.
- The same pass converges the pre-ADR-005 plaintext rows, which were previously sealed only if
  something happened to update them.
- Rolling back to a binary that does not know `<new>` after a partial pass will fail to read the
  rows already re-sealed. Roll the key list back too, or roll forward.

### Money Precision and Decimal Handling
All monetary amounts use decimal precision to avoid floating-point errors:
```
real_value = amount / 10^precision
```
This applies to transaction amounts, prices, holdings, and other financial values.

## Service Architecture

### Dependency Graph
```
Store Layer (PostgreSQL + pgx raw SQL)
├── MarketDataStore (assets, prices)
├── PortfolioStore (portfolios, holdings, accounts, transactions; crypto for accounts.data)
├── AutomationStore (rules, rule_executions)
└── UserStore (users)

Service Layer (Connect handlers)
├── MarketDataService → MarketDataStore, credentials resolver (price providers)
├── PortfolioService  → PortfolioStore, MarketDataService, resolver (wallet/exchange syncers)
├── AutomationService → AutomationStore
└── AnalyticsService  → PortfolioStore (read-only), MarketDataService (prices, assets)

Cross-cutting
├── credentials.Resolver → PortfolioStore (accounts), adapter factories
└── middleware (UserProvisioningInterceptor, EnsureOwner)
```

### Service Communication
- **Internal**: gRPC with Protocol Buffers (Connect-RPC)
- **External**: HTTP API via Connect-RPC
- **Database**: PostgreSQL with pgx (raw SQL)
- **Schema Management**: Atlas declarative migrations
- **External APIs**: HTTP clients in adapter layer

## Implementation Guidelines

### Service Implementation Pattern
1. **Start with stubs** returning `codes.Unimplemented`
2. **Add comprehensive tests** for all methods
3. **Implement business logic** incrementally
4. **Add integration tests** with real dependencies
5. **Performance optimization** as needed

### Error Handling
```go
// Use gRPC status codes consistently
return nil, status.Errorf(codes.NotFound, "user not found: %s", userID)
return nil, status.Errorf(codes.InvalidArgument, "invalid portfolio ID")
return nil, status.Errorf(codes.Internal, "database error: %v", err)
```

### Logging
```go
// Use structured logging with slog
log.Info("method called",
    slog.String("user_id", req.UserId),
    slog.String("operation", "create_portfolio"))

log.Error("operation failed",
    slog.Any("error", err),
    slog.String("context", "database_query"))
```

### Testing Pattern

**Unit tests** (no build tag):
```go
func TestService_Method(t *testing.T) {
    service := NewService()
    ctx := context.Background()

    resp, err := service.Method(ctx, req)

    // For stubs: expect Unimplemented
    assert.Nil(t, resp)
    assert.Error(t, err)
}
```

**Integration tests** (with testcontainers):
```go
//go:build integration

func TestStore_CreateAsset(t *testing.T) {
    pool := getTestPool(t)  // Uses testcontainers, truncates tables
    store := NewStore(pool)

    asset, err := store.CreateAsset(ctx, &entity.Asset{...})
    require.NoError(t, err)
    assert.NotEmpty(t, asset.ID)
}
```
```

## Development Roadmap

Tracked in beads (`bd ready`). High-level state:

**Done**
- Infrastructure, schema (Atlas), 3-service Connect-RPC monolith
- Assets + prices CRUD; external price fetch (CoinGecko)
- Portfolio/holding/account/transaction CRUD; portfolio valuation
- Account capability model + system scopes; encrypted credentials (ADR-005)
- Per-account credential resolver; ownership enforcement (IDOR audit)
- Account sync: wallet (Moralis) + exchange (Binance spot) + broker (T-Invest positions)

**In progress / next**
- Automation rule engines: DCA, rebalancing, stop-loss (execution stubs today)
- Cron scheduler for periodic rules
- Binance batch price fix; Gate.io exchange adapter
- Deprecate env provider keys once fully migrated to system accounts

**Later**
- Non-EVM chain sync (Solana, Cosmos, TON, ...)
- Portfolio analytics / performance metrics
- Broker coverage beyond T-Invest

## Release Policy

A tag is what reaches production: the release job in `.github/workflows/ci.yml` runs
GoReleaser on tag push and publishes `ghcr.io/foxcool/greedy-eye:<version>` (no `v`
prefix in the image tag). Production pins that version explicitly in the infra
inventory, so nothing ships without a tag.

**Patch (`x.y.Z`) — deploy-driven.** Anything production needs that does not change
what the system claims about itself: bug fixes, added proto fields, new RPCs,
settings, dependency bumps. Cut one as soon as something is merged that makes
production wrong or stale. No ceremony beyond green CI on `main`.

**Minor (`x.Y.0`) — stage-driven.** The claim changes: a way the total could lie is
closed, a beta gate is closed, a new class of source or surface exists. The tag fixes
a roadmap stage, and it carries one sentence saying what is now true — and, just as
importantly, what is still not. Example, v0.3.0: "the total is correct and names what
it omits", but *not* "the portfolio is no longer zero".

**The three repositories are tagged independently.** `greedy-eye`, `greedy-eye-fe` and
`greedy-eye-mcp` release when they have something to ship; a repository with no changes
is not tagged just to keep numbers aligned — a tag that contains nothing lies about
what it contains. Compatibility is recorded, not implied:

- MCP ↔ backend: `greedy-eye-mcp/go.mod` requires a published `github.com/foxcool/greedy-eye`
  tag. That requirement *is* the contract; nothing else is needed.
- FE ↔ backend: by proto. Recorded in the release matrix alongside the deployed versions.

**Release order.** Tag backend → wait for the GHCR image → bump the pinned version in
the infra inventory → run the deploy playbook → **confirm the build that is serving**
→ verify the claim by direct RPC against production → only then bump the MCP dependency
and tag MCP. Verification means checking that the new claim holds on live data, not that
the container started.

Confirming the build comes first because it is the step that says whether the rest of the
verification is about this release at all. The binary is stamped at link time and reports
itself in two places — the `version` field of `GET /eye/health`, and the `greedy-eye
starting` line it logs before anything else. A local build reports `dev`; anything else is
the tag it was cut from, and it must equal the version just pinned in the inventory. If it
does not, the deploy did not land and every later observation is about the old image.

This step exists because five releases running could not be verified: nothing in a live
instance could say what it was, so "the pin says X" and "X is serving" were the same
sentence, and a stale image kept answering health checks and sweeping balances while the
prices it was supposed to fix stood still for ten days.

**Schema.** `atlas migrate apply` runs on deploy against the ordered files in
`migrations/`, which are baked into the release image next to the binary. Before tagging,
check `git diff <last-tag>..main -- migrations/`: no new file means the deploy changes no
schema. A new file means reading it — it is the exact SQL that will run, in that order,
on every instance. See [Schema](#schema) for the workflow and for what a pre-existing
database needs before its first versioned deploy.

## Schema

`schema.hcl` says what the database should look like. `migrations/` says how any
database gets there. Both are in the repository, both are in the release image, and a
test keeps them from disagreeing.

### Why not just apply schema.hcl

That is what this project did until v0.12.0, and it has two failure modes that cost
real data:

- **Declarative apply computes the diff against whatever the target currently holds,
  and applies it in both directions.** Bringing the stack up from a branch that
  predates a column DROPS that column, data included. This is not hypothetical:
  `prices.market_cap` died this way on 2026-08-02, because `make up` runs the migrate
  service and the migrate service applied the checked-out schema.
- **It is not an upgrade path for somebody else's instance.** It works for one
  operator who is also the author and can read a plan before saying yes. Beta gate 4
  asks for an instance that upgrades without that person.

`migrate apply` runs only files the database has not run, in order, and records each one
in `atlas_schema_revisions`. It never invents a `DROP`, because a migration contains
only what somebody wrote into it.

### Changing the schema

```bash
# 1. Edit schema.hcl — it stays the surface you author on.
# 2. Turn the edit into an ordered file. The name says what changed, not "update".
make migrate-diff name=holdings_excluded_source
# 3. Read the generated SQL. It is what will run on production.
# 4. Apply it to dev.
make migrate-apply
```

Never hand-write a migration file and never edit one that has been applied anywhere:
`atlas.sum` carries a hash of each file, and an instance that already ran an older
version of the file will not run it again.

`TestMigrationsMatchSchema` (integration, so it runs in CI) replays `migrations/` into
an empty database and fails when the result differs from `schema.hcl`. That is what
lets `schema.hcl` stay the authoring surface without the two drifting apart: forgetting
step 2 fails the build with the missing DDL printed.

### A database that predates migrations

dev and production were built by declarative apply, so they carry the schema and no
record of how they got it. `migrate apply` against such a database would try to create
tables that are already there. It has to be told, once, that it is already at a known
version:

```bash
# 1. Confirm what the database actually holds. It must be synced with the schema of
#    the release the baseline version belongs to — if it is not, fix the drift first,
#    because baselining marks migrations as applied WITHOUT running them.
make migrate-drift

# 2. Mark it. The version is the filename prefix in migrations/.
make migrate-baseline version=20260902112400
```

On production the same thing runs through the image, since the migrations ship inside
it:

```bash
docker compose --profile migrate run --rm --entrypoint atlas migrate \
  migrate apply \
  --url "postgres://USER:PASSWORD@eye-postgres:5432/greedy_eye?sslmode=disable" \
  --dir "file:///migrations" --revisions-schema atlas \
  --baseline <version>
```

Pick the version by what the database holds, not by what is newest: an instance at
v0.11.0 baselines at the baseline file and then RUNS the later ones. The deploy playbook
refuses to run migrations on a populated database with no revisions table, and prints
this procedure, rather than guessing which version it is at.

Atlas keeps its bookkeeping in a separate `atlas` schema, so `migrate-drift` compares
the application's schema against `schema.hcl` without reading the revisions table as
drift.

### What is still not true

An instance can now be upgraded from the image without reading a diff, which is beta
gate 4's criterion. What no longer holds is the older claim that "the schema is whatever
schema.hcl says": for an existing database that is true only after every migration has
run. A database that skipped one is not repaired by applying schema.hcl to it — that is
the declarative path, and it is exactly the one that drops columns.

## Common Development Tasks

### Manual Cross-Platform Docker Build

GoReleaser handles production builds on tag push. For a manual multiarch build to GHCR:

```bash
docker login ghcr.io -u <github-username> --password-stdin

docker buildx create --name mybuilder --use
docker buildx inspect --bootstrap

docker buildx build \
  --platform linux/amd64,linux/arm64 \
  -f build/release.Dockerfile \
  -t ghcr.io/foxcool/greedy-eye:latest \
  --push .
```

> Note: `release.Dockerfile` expects a pre-built `eye` binary in the build context.
> Use GoReleaser (`goreleaser release --snapshot --clean`) to produce it first.

### Adding a New Service Method
1. **Define in proto**: Add method to service definition
2. **Generate code**: Run `make buf-gen`
3. **Implement stub**: Add method returning `Unimplemented`
4. **Write tests**: Add comprehensive test coverage
5. **Integrate**: Update main.go if needed
6. **Implement logic**: Replace stub with business logic

### Adding External Integration
1. **Create adapter**: Add client in `internal/adapters/`
2. **Add configuration**: Environment variables and config struct
3. **Implement interface**: Create service-specific interface
4. **Add to service**: Inject adapter into service
5. **Test integration**: Add integration tests

### Debugging Tips
```bash
# Run with debug logging
go run cmd/eye/main.go

# Use delve debugger
dlv debug cmd/eye/main.go

# Check service health
curl http://localhost:8080/eye/health

# Verify Atlas schema
atlas schema inspect --url "file://schema.hcl"

# Run single integration test
go test -v -tags=integration -run TestCreateAsset ./internal/store/postgres/...
```

## Performance Considerations

### Current Optimizations
- Connection pooling for database
- Efficient protobuf serialization
- Structured logging with levels
- Health check endpoint for monitoring

### Future Optimizations
- Response caching for frequently accessed data
- Database query optimization
- Connection pooling for external APIs
- Request rate limiting and throttling

## Troubleshooting

### Common Issues
1. **Port conflicts**: Kill the process on 8080
2. **Database connection**: Check PostgreSQL status and credentials
3. **Proto generation**: Ensure buf is installed and updated
4. **Module issues**: Run `go mod tidy` and `go mod download`
5. **Integration tests fail**: Ensure Docker is running and Atlas CLI is installed
6. **Schema issues**: Run `atlas schema inspect --url "file://schema.hcl"` to validate

### Build Issues
```bash
# Clean build cache
go clean -cache -modcache

# Regenerate everything
make clean && make buf-gen

# Check dependencies
go mod why -m module_name

# Verify Atlas CLI
which atlas || curl -sSf https://atlasgo.sh | sh
```

### Testing Issues
```bash
# Integration tests require Docker and Atlas CLI
make test-integration

# If testcontainers fail, check Docker daemon
docker ps

# Run with verbose output
go test -v -tags=integration ./internal/store/postgres/...
```

