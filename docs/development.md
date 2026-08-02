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

# Start dev environment
make up
make schema-apply
```

### Alternative Quick Start (Docker Compose)
```bash
# Start entire system with Docker
make up

# Apply database schema
make schema-apply

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
make up               # Start dev environment
make schema-apply     # Apply database schema (Atlas)
make buf-gen          # Generate protobuf code

# Testing
make test             # Run all tests (unit + integration)
make test-unit        # Run unit tests only
make test-integration # Run integration tests (uses testcontainers)

# Database operations
make schema-apply     # Apply schema to dev database
make schema-diff      # Show schema changes

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

The credentials resolver builds per-account clients from stored credentials,
falling back to env-configured clients (env fallback is deprecated — being
migrated to system accounts).

| Adapter | Provider | Status |
|---------|----------|--------|
| Blockchain | Moralis (`internal/adapter/moralis/`) | ✅ Multi-chain wallet balance sync (`WalletSyncer`) |
| Exchange | Binance (`internal/adapter/binance/`) | ✅ Signed spot balance sync (`ExchangeSyncer`); ticker prices (batch has a known issue) |
| Price Data | CoinGecko (`internal/adapter/coingecko/`) | ✅ Live prices |
| Messenger | Telegram (`internal/adapter/telegram/`) | Notifications |

### Recent Milestones

- **Account capabilities & system scopes**: credential model on accounts, admin-shared system scopes, RPC + FE UI.
- **Encryption at rest (ADR-005)**: AES-256-GCM + per-record HKDF for `accounts.data`; write-only masked secrets over the API.
- **Credentials resolver**: per-account adapter factories replacing startup singletons; env fallback deprecated.
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
# Apply schema to dev database (Atlas)
make schema-apply

# Show schema diff
make schema-diff

# Inspect current schema
atlas schema inspect --url "file://schema.hcl"
```

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

# External API keys (env fallback — deprecated, prefer system accounts)
EYE_MORALIS_APIKEY=your_key
EYE_COINGECKO_APIKEY=your_key
EYE_BINANCE_APIKEY=your_key
EYE_BINANCE_APISECRET=your_secret

# Telegram notifications
EYE_TELEGRAM_TOKEN=your_token
EYE_TELEGRAM_CHATIDS="-1001234567890,987654321"

# Background scheduler (periodic rules + price fetch)
EYE_SCHEDULER_ENABLED=true              # default true; enable on ONE instance only
EYE_SCHEDULER_PRICEFETCHCRON="0 * * * *"  # empty = price job off
# The interval is also the sweep's budget window: a provider divides what is
# left of its monthly plan by it. Shortening it makes each sweep smaller, not
# the month more expensive.

# Provider request budget (see docs/providers.md)
# Plans belong to accounts, not to this file: an account's data.tier picks the
# rate AND the volume allowance for its own key. Nothing here can change a
# user's plan.
#
# These knobs throttle the whole deployment's rate towards one provider, which
# is the right scope for an enforcement notice — providers meter rate per IP as
# much as per key, and the process has one address. They do not touch anyone's
# monthly allowance.
EYE_RATELIMIT_SUBSCAN_RPS=1.8
EYE_RATELIMIT_SUBSCAN_BURST=1

# Credentials for the deprecated env-configured provider path (no account row).
# EYE_COINGECKO_PRO selects the paid host and plan for THAT client only —
# accounts carry their own tier.
EYE_COINGECKO_APIKEY=your_key
EYE_COINGECKO_PRO=false

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
- Account sync: wallet (Moralis) + exchange (Binance spot)

**In progress / next**
- Automation rule engines: DCA, rebalancing, stop-loss (execution stubs today)
- Cron scheduler for periodic rules
- Binance batch price fix; Gate.io exchange adapter
- Deprecate env provider keys once fully migrated to system accounts

**Later**
- Non-EVM chain sync (Solana, Cosmos, TON, ...)
- Portfolio analytics / performance metrics
- Additional broker integrations (T-Invest)

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

