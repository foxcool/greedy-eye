# Greedy Eye - Architecture Documentation

## Overview

Universal portfolio management system based on arc42 + C4 Model

> **Source of truth**: the API contract lives in `api/v1/*.proto`, the schema in
> `schema.hcl`, and the wiring in `cmd/eye/main.go`. This document is
> hand-maintained prose about that code — when they disagree, the code wins.
> Some HTTP routes (e.g. `GET /eye/health`) are wired directly in `main.go`, not
> generated from proto. There is no OpenAPI spec: the service is Connect-RPC,
> which the OpenAPI generator does not cover.

---

## Document Navigation

This architecture document is organized according to the arc42 template and C4 model. Different sections serve
different audiences:

**Business & Product Context** (Sections 1-4)
- Introduction, requirements, constraints, and solution strategy
- Target audience: Product owners, business stakeholders, new team members

**Technical Implementation** (Sections 5-7)
- Building blocks, components, runtime behavior, and deployment
- Target audience: Developers, architects, DevOps engineers

**Operational & Quality Aspects** (Section 8)
- Security, performance, monitoring, and crosscutting concerns
- Target audience: SRE, operations team, security engineers

**Architecture Decisions & Risk Management** (Sections 9-11)
- ADRs, quality requirements, and technical debt tracking
- Target audience: Tech leads, architects, decision makers

**Reference** (Section 12)
- Glossary of domain terms
- Target audience: All stakeholders

---

## 1. Introduction and Goals

### 1.1 Requirements and Constraints

**Business Requirements:**
- Universal investment portfolio management (cryptocurrencies, securities, derivatives) platform
- Automated investment strategies (DCA, rebalancing, stop-losses)
- Different interfaces types and easy integration with client apps
- Integration with external APIs (exchanges, brokers, data providers)

**Functional Requirements:**
- Portfolio management with portfolio service
- Automated trading strategies using rule system
- Real-time price tracking from multiple sources
- Notifications and alerts about portfolio changes
- Support for multiple asset types and accounts

**Non-functional Requirements:**
- HTTP API response time < 200ms for simple operations
- Support for 100+ concurrent users
- 99.9% system availability
- Secure storage of API keys and user data

### 1.2 Quality Goals

**Top-3 priority quality goals:**

1. **Scalability** - Modular monolith architecture with microservices transition capability
   - Metrics: support 10x user growth without architectural changes
   - Horizontal scaling through configuration

2. **Security** - Multi-layered protection for financial data
   - Metrics: encryption of all external API keys, audit of all operations
   - JWT authentication + API key authorization with rate limiting
3. **Reliability** - Fault tolerance for financial operations
   - Metrics: 99.9% uptime, automatic recovery after failures
   - Graceful degradation when external services are unavailable

### 1.3 Stakeholders

| Role | Contact | Expectations |
| --- | --- | --- |
| Founder | fox@darkfox.info | Fast MVP delivery, universality for all asset types, maintainability |

---

## 2. Constraints

### 2.1 Technical Constraints

**Technology Stack:**
- **Language**: Go 1.25+
- **Database**: PostgreSQL 17+ (ACID compliance for financial data)
- **Protocol**: Connect-RPC over HTTP/2 (h2c) — serves both Connect and gRPC clients
- **Schema Management**: Atlas declarative migrations
- **Containerization**: Docker + Docker Compose

**Performance Constraints:**
- API response time < 200ms for 95% of requests
- Support up to 1000 concurrent connections
- Price fetch batch processing < 5 seconds

**Compatibility and Integrations:**
- Messenger Bot APIs (chat interface)
- Price data providers (cryptocurrency and securities market data)
- Trading platform APIs (exchanges and brokers)
- Blockchain data providers (on-chain analytics and wallet information)

### 2.2 Organizational Constraints

**Development Team:**
- 1 Developer (Go, docker, ansible, protobuf)

**Development Processes:**
- Code generation with proto using buf
- Declarative schema management with Atlas (schema.hcl)
- Integration tests with testcontainers (ephemeral PostgreSQL)
- Continuous Integration through GitHub Actions

### 2.3 External Constraints

**Legal Requirements:**
- GDPR compliance for user data

**Corporate Standards:**
- Open Source approach (MIT License)
- Documentation of architectural decisions (ADR)
- Security: encrypted at rest, encrypted in transit

---

## 3. Context and Scope

### 3.1 C1: Context Diagram

```mermaid
graph TB
    User[👤 Investor User]
    MessengerBot[📱 Messenger Bot]
    APIClient[🖥️ API Client]

    GreedyEye[🏦 Greedy Eye<br/>Portfolio Management Platform]

    Price[💰 Price Data Providers]
    Trading[🔄 Trading Platforms API]
    DataProviders[🔎 Blockchain & Market<br/>Data Providers]

    User -.-> MessengerBot
    User -.-> APIClient
    MessengerBot --> GreedyEye
    APIClient --> GreedyEye

    GreedyEye --> Price
    GreedyEye --> Trading
    GreedyEye --> DataProviders
```

### 3.2 Business Context

**External Systems and Their Roles:**
- **Messenger Bot API**: Chat user interface
- **Price Data Providers**: Cryptocurrency and securities price data and market information
- **Trading Platforms API**: Trading operations for various asset types
- **Blockchain & Market Data Providers**: On-chain data and market analytics
- **Speech APIs**: Voice to text conversion and vice versa

**User Roles:**
- **Individual Investors**: Primary system users
- **API Clients**: Integration with external systems through HTTP API

### 3.3 Technical Context

**Network Protocols:**
- **gRPC**: Internal communication between services
- **HTTP/HTTPS**: External API and webhook integrations
- **WebSocket**: Real-time notifications (planned)

**Data Formats:**
- **Protocol Buffers**: Internal API definitions
- **JSON**: HTTP API requests/responses
- **Encrypted**: Storage of API keys and sensitive data

---

## 4. Solution Strategy

### 4.1 Architectural Decisions

**Selected Architectural Patterns:**
- **Modular Monolith**: Balance between development simplicity and scalability
- **4-service domain API**: MarketDataService, PortfolioService, AutomationService, AnalyticsService
- **Layered architecture**: handler → store interface → postgres implementation
- **CQRS elements**: Separation of read and write operations in critical places

**Technology Stack:**
- **Backend**: Go 1.25+ for performance and type safety
- **Database**: PostgreSQL + pgx (raw SQL) for reliability
- **Schema**: Atlas declarative migrations (schema.hcl)
- **Communication**: Connect-RPC (h2c) — single server serves both Connect and gRPC clients
- **Deployment**: Docker containers for environment consistency

**Responsibility boundaries:**
- **User management / Auth**: delegated to external service (psina)
- **Messenger Bot**: separate future service
- **Greedy Eye core**: market data, portfolios, automation rules

### 4.2 Approach to Quality Goals

**Scalability:**
- Modular architecture with clear service boundaries
- Deployment capability as monolith or separate microservices
- Connection pooling and efficient resource usage

**Security:**
- Multi-layer authentication (JWT + API keys)
- Encryption of external API keys in database
- Rate limiting and audit logging of all operations

**Reliability:**
- Health checks for all services
- Graceful degradation when external APIs are unavailable
- Retry mechanisms with exponential backoff

---

## 5. Building Block View

### 5.1 C2: Container Diagram

```mermaid
graph TB
    subgraph "Greedy Eye System"
        App[⚙️ greedy-eye binary<br/>Connect-RPC h2c :8080]
        Database[🗄️ PostgreSQL<br/>:5432]
    end

    subgraph "External Services"
        Psina[👥 psina<br/>User Auth]
        PriceAPI[💰 Price Data API<br/>CoinGecko, Binance]
        BlockchainAPI[🔎 Blockchain Data API<br/>Moralis]
    end

    APIClient[🖥️ API Client] -->|Connect/gRPC| App
    App <--> Database
    App -.->|prices| PriceAPI
    App -.->|on-chain data| BlockchainAPI
    App -.->|auth| Psina
```

**Level 1 Containers:**

- **greedy-eye binary**:
  - Purpose: Single Go binary serving 4 domain services over Connect-RPC
  - Technologies: Go, Connect-RPC, Protocol Buffers, h2c
  - Services: MarketDataService, PortfolioService, AutomationService, AnalyticsService
  - Health check: `GET /eye/health`

- **PostgreSQL Database**:
  - Purpose: Persistent storage with ACID guarantees
  - Technologies: PostgreSQL 17+, pgx driver
  - Schema: Atlas declarative migrations (schema.hcl)
  - Interfaces: SQL, pgxpool connection pooling

### 5.2 C3: Component Diagrams

#### Service Layer - Component Details

```mermaid
graph TB
    subgraph "Connect-RPC Handlers"
        MDS[MarketDataService<br/>assets + prices]
        PS[PortfolioService<br/>portfolios, accounts<br/>holdings, transactions]
        AS[AutomationService<br/>rules + executions]
        ANS[AnalyticsService<br/>heatmaps]
    end

    subgraph "Store Interfaces"
        MDSI[marketdata.Store]
        PSI[portfolio.Store]
        ASI[automation.Store]
    end

    subgraph "Postgres Implementations"
        MDSP[MarketDataStore]
        PSP[PortfolioStore]
        ASP[AutomationStore]
        SSP[UserStore<br/>users]
    end

    subgraph "External Adapters"
        CoinGecko[coingecko adapter<br/>prices]
        Binance[binance adapter<br/>prices + balances]
        Moralis[moralis adapter<br/>wallet balances]
        Telegram[telegram adapter<br/>notifications]
    end

    MDS --> MDSI --> MDSP --> DB[(PostgreSQL)]
    PS  --> PSI  --> PSP  --> DB
    AS  --> ASI  --> ASP  --> DB
    ANS --> PSI
    ANS -.->|prices, assets| MDS

    MDS -.->|FetchExternalPrices via resolver| CoinGecko
    MDS -.->|FetchExternalPrices| Binance
    PS  -.->|SyncAccount wallet| Moralis
    PS  -.->|SyncAccount exchange| Binance
```

### 5.3 Level 3: Service Details

**MarketDataService** (`internal/service/marketdata/`):
- RPCs implemented: CreateAsset, GetAsset, UpdateAsset, DeleteAsset, ListAssets, CreatePrice,
  CreatePrices, GetLatestPrice, ListPriceHistory, ListPricesByInterval, DeletePrice, DeletePrices,
  FetchExternalPrices (via the credentials resolver: CoinGecko live, Binance batch has issues)
- RPCs stubbed: EnrichAssetData, FindSimilarAssets
- Store: `MarketDataStore` (PostgreSQL) — assets + prices

**PortfolioService** (`internal/service/portfolio/`):
- RPCs implemented: full CRUD for Portfolio, Account, Holding, Transaction; CalculatePortfolioValue;
  SyncAccount (wallet balances via Moralis, exchange balances via Binance)
- RPCs stubbed: GetPortfolioPerformance
- Ownership: every by-ID and list RPC enforces caller ownership (`middleware.EnsureOwner`);
  `user_id` list overrides are admin-only. See §8.1.
- Accounts carry a capability model (capabilities + admin-managed system_scopes) and encrypted
  `data` credentials (ADR-005). See the Account credential model below and §8.1.
- Store: `PortfolioStore` (PostgreSQL)

**AutomationService** (`internal/service/automation/`):
- RPCs implemented: CRUD for Rule and RuleExecution, EnableRule, DisableRule, PauseRule,
  ResumeRule, ValidateRule, ExecuteRule, ExecuteRuleAsync, CancelRuleExecution, SimulateRule
- Ownership enforced per rule (`ownedRule`); execution engines (DCA/rebalancing/stop-loss) are
  still stubs pending the rule-engine package.
- Store: `AutomationStore` (PostgreSQL) — `rules` + `rule_executions` tables

**AnalyticsService** (`internal/service/analytics/`):
- RPCs implemented: GetHeatmap — treemap nodes; tile size = holding value in the quote asset,
  tile color = price change % over a 24h/7d/30d window
- Scopes: PORTFOLIO (one portfolio; flat or grouped by account) and BALANCE (all caller's
  holdings across portfolios; flat, by account, or by portfolio — holdings inherit the account's
  portfolio, accounts outside any portfolio land in an "unassigned" group)
- Scopes MARKET/BASKET, PNL/target-drift color metrics and class/sector grouping return
  Unimplemented until their data prerequisites land (market metrics, asset identity, cost basis)
- Read-only derived views: no store of its own — reads via `portfolio.Store` (holdings) and the
  MarketData client (prices, asset labels); price resolution mirrors `portfolio.Handler.unitPrice`
  (duplicated consciously; extract a shared pricing package on the third consumer)
- Ownership enforced on the scope portfolio (`middleware.EnsureOwner`)

**User provisioning** (`internal/middleware/user.go` + `postgres.UserStore`):
- Users are provisioned lazily from `X-User-Id`/`X-User-Email`/`X-User-Roles` headers set by the
  auth proxy (psina). Roles are per-request and never persisted (psina owns them). There is no
  standalone settings/user Connect service.

**External Adapters** (Integration Layer):

The system uses the **Adapter Pattern** to isolate external API dependencies from core business logic.
Adapters are no longer singletons: the credentials resolver (`internal/service/credentials/`) builds
per-account clients from stored credentials, falling back to env-configured clients.

- **Price Adapters** (`internal/adapter/coingecko/`, `internal/adapter/binance/`): CoinGecko (live
  prices), Binance (`ticker/price`; batch fails on invalid symbols — tracked separately)
- **Exchange sync** (`internal/adapter/binance/`): Binance spot balances via the SIGNED
  `GET /api/v3/account` (HMAC-SHA256) → `entity.ExchangeSyncer`
- **Blockchain Adapters** (`internal/adapter/moralis/`): Moralis multi-chain wallet balances →
  `entity.WalletSyncer`
- **Messenger Adapters** (`internal/adapter/telegram/`): Telegram (notifications)

**Wallet syncer routing (chain-keyed registry)**:

One `WalletSyncer` per ecosystem, selected by the account's `data.chain`. Each provider is
registered as a `credentials.WalletProvider{Factory, Chains}`; the resolver picks the provider
covering *every* requested chain.

- No provider covers the chains → sync fails with `Unimplemented` naming the chain. This is
  deliberate: falling through to an EVM syncer for a Substrate address would report an empty
  wallet and silently zero the position instead of erroring.
- `Chains: nil` marks a catch-all provider, the only kind that can serve auto-discovery
  (account chain empty or `auto`) — a chain-scoped provider cannot enumerate an address's activity.
- Adding an ecosystem: implement `entity.WalletSyncer` in `internal/adapter/<name>/`, expose a
  `SupportedChains()`, register it in `cmd/eye/main.go`. Test pattern: golden fixtures of provider
  responses driven through an `httptest` server (see `internal/adapter/moralis`).
- Accounts entered by hand during a manual import become live wallets by updating `type`,
  `capabilities` and `data` in **one** call — a wallet may not keep `manual_positions`, so a
  split update is rejected by the merged capability validation.

**Account credential model**:
- Provider credentials live in `accounts.data` (encrypted at rest, ADR-005), keyed by a `provider`
  slug (`moralis`, `coingecko`, `binance`). Secret-looking keys are write-only over the API
  (`••••`+last4 mask; echoing the mask keeps the stored secret).
- `capabilities` (`portfolio_sync`, `trading`, `market_data`, `onchain_lookup`) declare what the
  credentials may do, validated against account type.
- `system_scopes` ⊆ capabilities marks user-agnostic capabilities (market_data, onchain_lookup) an
  admin shares system-wide. The resolver resolves clients user → system → env.

---

## 6. Runtime View

### 6.1 Execution Scenarios

#### Scenario 1: Track a New Asset Price

```text
API Client → MarketDataService → MarketDataStore → PostgreSQL
  1. POST /eye.v1.MarketDataService/CreateAsset (or CreatePrice)
  2. Handler validates request, maps proto → entity
  3. Store persists to PostgreSQL
  4. Response: created Asset or Price proto
```

#### Scenario 2: Fetch Prices from External Provider

```text
API Client → MarketDataService/FetchExternalPrices → resolver → adapter → CreatePrices
  1. POST /eye.v1.MarketDataService/FetchExternalPrices
  2. Handler resolves the price providers for the caller (credentials resolver:
     user account → system account → env fallback)
  3. Each provider (CoinGecko / Binance) fetches prices for the tracked assets
  4. Prices are bulk-inserted via CreatePrices
  5. Response: FetchExternalPricesResponse{prices_fetched, prices_stored, errors}
```

#### Scenario 2b: Sync Account Balances

```text
API Client → PortfolioService/SyncAccount → resolver → syncer → upsert holdings
  1. POST /eye.v1.PortfolioService/SyncAccount (owner or admin only)
  2. Branch on account type:
     - wallet   → WalletSyncer (Moralis) by address across chains
     - exchange → ExchangeSyncer (Binance) via the account's own API key
  3. Balances normalized, merged by symbol, assets ensured, holdings upserted
  4. Response: SyncAccountResponse{assets_upserted, holdings_upserted, errors}
```

#### Scenario 3: Execute Rebalancing Rule

```text
Cron / API Client → AutomationService/ExecuteRule
  1. Validate rule_id, check rule.status == active
  2. CreateRuleExecution(status=in_progress)
  3. Run rule logic (calculate rebalancing trades)
  4. Create Transaction records via PortfolioService
  5. UpdateRuleExecution(status=completed, completed_at=now)
  Note: dry_run=true returns simulated execution without DB writes
```

### 6.2 Critical Scenarios

**Error Handling:**
- Graceful degradation when external APIs are unavailable
- Retry with exponential backoff for temporary failures
- Transaction rollback for critical errors

**Load Scenarios:**
- Database connection pooling under high load
- Rate limiting to prevent exceeding external API limits
- Circuit breaker pattern for protection against cascading failures

---

## 7. Deployment View

### 7.1 Infrastructure

```
[Production Environment]
├── Reverse Proxy (Traefik)
│   ├── SSL Termination
│   ├── h2c upstream (HTTP/2 cleartext for Connect-RPC)
│   └── Path routing: Host($EYE_DOMAIN) && PathPrefix(/eye)
├── Application Container
│   ├── Connect-RPC h2c Server (:8080)
│   │   ├── MarketDataService
│   │   ├── PortfolioService
│   │   ├── AutomationService
│   │   └── AnalyticsService
│   └── Health: GET /eye/health
├── Database Container
│   ├── PostgreSQL 17+
│   ├── Automated Backups
│   └── pgxpool Connection Pooling
└── Monitoring Stack
    ├── Health Checks
    ├── Sentry (error tracking)
    └── Structured JSON logging
```

### 7.2 Component Mapping to Infrastructure

**Production Deployment:**
- **Single Container**: All gRPC services + HTTP Gateway in one Go binary
- **Database**: Separate PostgreSQL container with persistent volumes
- **Configuration**: Environment variables for API keys and settings
- **Scaling**: Horizontal scaling through load balancer + multiple app instances

**Development Environment:**
- **Docker Compose**: Local deployment of entire stack
- **Live Reload**: Air for automatic reloading on changes
- **Debug Mode**: Delve integration for debugging

---

## 8. Crosscutting Concepts

### 8.1 Security

**Authentication and Authorization:**
- **Delegated auth (psina)**: authentication is handled by the external psina service
  behind a Traefik `forwardAuth`. It validates the session/token and injects
  `X-User-Id`, `X-User-Email`, and `X-User-Roles` headers (ADR-004).
- **User provisioning**: `middleware.UserProvisioningInterceptor` reads those headers, lazily
  provisions the user, and binds roles to the request context (roles never persisted).
- **Ownership enforcement**: every by-ID RPC loads the entity and requires the caller to be its
  owner or an admin (`middleware.EnsureOwner`), returning `NotFound` for foreign IDs so their
  existence isn't leaked. Holdings/transactions inherit ownership through their account.
  `user_id` list overrides are admin-only.
- **Admin-gated mutations**: sharing account credentials system-wide (`system_scopes`) requires the
  admin role and an explicit update mask.

**Data Protection:**
- **Encryption at Rest**: `accounts.data` (provider API keys) is encrypted with AES-256-GCM +
  per-record HKDF keys in the store layer (ADR-005); the master key
  (`EYE_SECURITY_MASTERKEY`) never reaches the DB or logs.
- **Write-only secrets**: the API never returns credential values — only a `••••`+last4 mask.
- **Encryption in Transit**: TLS for all external connections (Traefik terminates public TLS).

### 8.2 Performance

**Caching:**
- **Price Data**: Caching current prices with TTL
- **Portfolio Calculations**: Caching portfolio calculations
- **Session Data**: In-memory caching of Telegram Bot sessions

**Query Optimization:**
- **Database Indexing**: Indexes on foreign keys and timestamp fields
- **Connection Pooling**: Efficient use of database connections
- **Batch Operations**: Group operations for multiple records

### 8.3 Data Management

**Data Model:**
- **Universal Asset Support**: Unified model for all asset types
- **Flexible Configuration**: JSON fields for rules and settings
- **Audit Trail**: Complete history of all operations

**Schema Management:**
- **Atlas Declarative**: Schema defined in `schema.hcl` (HCL format)
- **Automated Migrations**: `atlas schema apply` for dev environment
- **Backward Compatibility**: Support for backward compatibility
- **Data Integrity**: ACID transactions for critical operations

**Testing:**
- **Testcontainers**: Ephemeral PostgreSQL for integration tests
- **Schema Apply**: Atlas CLI applies schema to test containers
- **Isolation**: Each test run gets clean database

### 8.4 Operational Concepts

**Monitoring and Alerting:**
- **Health Checks**: /health endpoint for all services
- **Metrics Collection**: Prometheus-compatible metrics
- **Error Tracking**: Structured logging with correlation IDs
- **SLA Monitoring**: Response time SLA tracking

**Logging:**
- **Structured Logging**: JSON format with logging levels
- **Request Tracing**: Unique IDs for request tracking
- **Security Events**: Logging of all authentication events
- **Configuration**: TEXT format for development, JSON for production
- **Log Levels**: DEBUG, INFO, WARN, ERROR, FATAL

**Configuration Management:**
- **Environment Variables**: All sensitive data via env vars
- **Config Files**: YAML format for non-sensitive settings
- **Money Precision**: Decimal amounts stored as `amount / 10^precision`
- **External APIs**: Encrypted API keys with rotation support

**Background Scheduler (`internal/scheduler`):**
- Single cron scheduler (robfig/cron/v3) inside the `eye` binary, gated by `scheduler.enabled`
- Consumers: periodic automation rules (`RuleSchedule.CronExpression` + `Timezone`) and external price fetching (`scheduler.priceFetchCron`, default every 15 min)
- Active rule schedules are fully reloaded every minute — rule CRUD needs no hooks, mutations take effect within a minute
- Missed fires during downtime are **skipped, never caught up**: executing a stale trade plan is worse than skipping it
- Rule jobs call `ExecuteRule` in-process on behalf of the rule owner, so executions are recorded identically to the RPC path
- Single-instance only: on multi-instance deployments enable the scheduler on exactly one node via config; `pg_try_advisory_lock`-based leader election is a future option if needed

---

## 9. Architecture Decisions

### ADR-001: Modular Monolith vs Microservices
- **Status**: accepted
- **Context**: Choice between microservice architecture and monolith for MVP
- **Decision**: Modular monolith with microservices transition capability
- **Consequences**:
  - ➕ Fast MVP development, deployment simplicity
  - ➕ Clear service boundaries through gRPC interfaces
  - ➖ Need for caution when changing shared components

### ADR-002: Connect-RPC over h2c instead of gRPC-Gateway
- **Status**: accepted (supersedes original gRPC + gRPC-Gateway decision)
- **Context**: Original plan used gRPC internally + gRPC-Gateway for HTTP. Connect-RPC handles both natively.
- **Decision**: Connect-RPC with h2c transport. Single server accepts both Connect (JSON/protobuf) and gRPC clients.
- **Consequences**:
  - ➕ No separate gateway process, single port, simpler deployment
  - ➕ Browser-friendly Connect protocol without grpc-web proxy
  - ➕ Type safety via generated code, same proto definitions
  - ➖ Requires h2c-aware reverse proxy (Traefik `server.scheme=h2c`)

### ADR-003: 3-service domain API instead of 8 services
- **Status**: accepted
- **Context**: Initial design had 8 gRPC services (Storage, User, Asset, Portfolio, Price, Rule,
  Auth, Messenger). This caused excessive indirection and coupling.
- **Decision**: Consolidate into 3 domain services: MarketDataService (assets + prices),
  PortfolioService (portfolios, accounts, holdings, transactions), AutomationService (rules + executions).
- **Consequences**:
  - ➕ Simpler handler structure, fewer inter-service calls
  - ➕ Each service owns its store directly (no StorageService middleman)
  - ➖ Larger individual service surface area
  - ➖ Cross-domain operations (e.g. calculate portfolio value using prices) require service-to-service calls in the future

### ADR-004: User management delegated to external service (psina)
- **Status**: accepted
- **Context**: Managing users, auth, and sessions is significant scope separate from portfolio domain.
- **Decision**: Delegate user management and authentication to external `psina` service.
  Greedy Eye stores user references (user_id) but does not manage user lifecycle.
- **Consequences**:
  - ➕ Greedy Eye stays focused on financial domain
  - ➕ Auth reused across multiple services
  - ➖ Dependency on external service availability

### ADR-005: Application-level encryption for accounts.data
- **Status**: accepted
- **Context**: Provider credentials (API keys/secrets) move from env config into `accounts.data`
  (jsonb). A database dump must not leak them. Nothing queries `data` content via SQL — the store
  reads/writes the map only as a whole.
- **Decision**: AES-256-GCM in the Go store layer (not pgcrypto). The whole JSON-serialized map is
  encrypted as one blob and stored as `{"enc": "v1:<base64(nonce || ciphertext)>"}` — the column
  stays jsonb, no migration. Per-record keys via HKDF-SHA256 from a 32-byte master key
  (`EYE_SECURITY_MASTERKEY`, base64), `info` = account ID, so ciphertext is bound to its row.
  Read path: a map with the single `enc` key is decrypted; any other shape is a legacy plaintext
  row, returned as is and re-encrypted on its next update. Empty key = plaintext mode with a
  startup warning (dev); production must set the key.
- **Consequences**:
  - ➕ Key never reaches the DB process, SQL statements, or server logs; unit-testable
  - ➕ No schema migration; legacy rows stay readable; format versioned (`v1:`) for rotation
  - ➕ Encrypted values can't be swapped between rows (fails GCM authentication)
  - ➖ Wallet addresses no longer visible in raw SQL
  - ➖ Losing the master key loses all encrypted `accounts.data`; key rotation and legacy backfill
    are follow-ups
- **Rejected**: pgcrypto (key surfaces in `pg_stat_statements`/server logs, PG-coupled);
  per-field encryption (needs a secret-field classification, mixed-plaintext states);
  single static key without HKDF (swappable ciphertexts, shared nonce space)

### ADR-006: Composite asset identity (symbol, market, type)
- **Status**: accepted
- **Context**: `assets` had `UNIQUE(symbol)`, which physically blocks multi-market coverage:
  AAPL on NASDAQ vs an AAPL token, SBER on MOEX, same ticker with different semantics per venue.
  Manual/broker import and non-crypto assets are blocked on this.
- **Decision**: Identity is the composite `UNIQUE(symbol, market, type)`. `market` is the
  listing market/venue — `crypto` is a single global market for all crypto assets — **not** the
  price source: mapping to provider-native identifiers (CoinGecko coin id, T-Invest FIGI) lives
  in the provider adapters (hardcoded map for major coins, `contract:` tags for tokens); when a
  source with a dynamic instrument universe lands (broker import), that mapping becomes a
  dedicated `asset_external_refs(asset_id, source, ref)` table — one asset has refs in many
  sources, so it is not an asset column. `quote` holds
  the quote currency where applicable. On create, `market` defaults by type
  (cryptocurrency → `crypto`, forex → `forex`) and is required for exchange-listed types.
  Symbol-only lookups (`GetAssetBySymbol`) return the unique match or fail with
  InvalidArgument when the symbol exists on several markets — no silent picking.
- **Consequences**:
  - ➕ Multi-market catalog possible (stocks, bonds, funds) without symbol collisions
  - ➕ One asset row per crypto asset regardless of price provider (no BTC duplication)
  - ➕ Existing rows backfilled via column default `'crypto'`; no data migration needed
  - ➖ Symbol-only lookups become ambiguous once a ticker exists on two markets; callers
    must then resolve by ID or market
- **Rejected**: market = price source (duplicates every asset per provider, fragments
  holdings); an `external_ref` column on `assets` (one asset maps to many provider IDs —
  1:N belongs in a mapping table, and a 1:1 column would die on the second source);
  prefixing symbol with venue in one field (loses clean symbol for search/display)

---

## 10. Quality Requirements

### 10.1 Quality Tree

```
System Quality
├── Performance
│   ├── HTTP API response time < 200ms (95th percentile)
│   ├── gRPC response time < 50ms (95th percentile)
│   ├── Throughput > 1000 RPS
│   └── Voice processing < 3s
├── Availability
│   ├── 99.9% uptime
│   ├── Graceful degradation on external API failures
│   └── Health checks for monitoring
├── Security
│   ├── Authentication (JWT + API keys)
│   ├── Authorization (user-scoped operations)
│   ├── Encryption (TLS + database encryption)
│   └── Audit logging
├── Scalability
│   ├── Horizontal scaling readiness
│   ├── Database connection pooling
│   └── Stateless services design
└── Usability
    ├── Intuitive Telegram commands
    ├── Voice control
    └── Automatic notifications
```

### 10.2 Quality Scenarios

**Performance:**
- **Scenario**: Peak load of 100 concurrent Telegram Bot users
- **Metric**: 95th percentile response time < 500ms for portfolio commands
- **Current State**: Architecture ready, load testing required

**Security:**
- **Scenario**: Unauthorized portfolio access attempt
- **Metric**: 100% blocking of unauthorized requests
- **Current State**: JWT + API key mechanisms implemented

**Reliability:**
- **Scenario**: External price data API unavailable for 30 minutes
- **Metric**: System continues working with cached data
- **Current State**: Graceful degradation implementation required

---

## 11. Risks and Technical Debt

### 11.1 Identified Risks

| Risk | Probability | Impact | Mitigation |
| --- | --- | --- | --- |
| External API rate limit exceeded | High | Medium | Circuit breaker + caching + multiple providers |
| Data inaccuracy from price providers | Medium | High | Data validation + multiple sources + alerts |
| Monolith scaling issues | Low | High | Ready architecture for microservice split |
| API key security breach | Medium | Critical | Encryption + rotation + audit + least privilege |

### 11.2 Technical Debt

#### Debt 1: Partial business logic implementation

- Description: CRUD, portfolio valuation, external price fetch, and account sync (wallet +
  exchange) are implemented. Remaining stubs: GetPortfolioPerformance, EnrichAssetData,
  FindSimilarAssets, and the automation execution engines (DCA / rebalancing / stop-loss).
  ExecuteRule runs a minimal synchronous flow, not real trading logic.
- Impact: Data management, valuation, and sync work; automated strategy execution does not yet
  place real trades.
- Resolution Plan: rule-engine package → DCA/rebalancing/stop-loss engines → cron scheduler →
  performance metrics.

#### Debt 2: Lack of comprehensive monitoring

- Description: Basic health checks without detailed metrics
- Impact: Difficulty diagnosing production issues
- Resolution Plan: Prometheus + Grafana dashboards integration

#### Debt 3: Insufficient integration tests

- Description: Unit tests exist, but few end-to-end tests
- Impact: Regression risk during changes
- Resolution Plan: Automated integration tests in CI/CD

---

## 12. Glossary

| Term | Definition |
| --- | --- |
| **Asset** | Financial instrument: cryptocurrency, stock, bond, derivatives |
| **Holding** | Current position of a specific asset within an Account |
| **Account** | User's connection to an exchange, wallet, or broker |
| **Rule** | Portfolio automation rule (DCA, rebalancing, stop-loss, withdrawal) |
| **RuleExecution** | Single run of a Rule, with status and transaction references |
| **Connect-RPC** | Protocol from Buf that serves gRPC and browser-compatible HTTP API from one server |
| **h2c** | HTTP/2 cleartext — HTTP/2 without TLS, used for Connect-RPC behind a TLS-terminating proxy |
| **psina** | External service responsible for user management and authentication |
| **Price Provider** | External service providing price data (CoinGecko, Binance, etc.) |
| **Graceful Degradation** | Continued operation with limited functionality during failures |
| **Circuit Breaker** | Pattern protecting against cascading failures when external services are unavailable |

---

**Document Version**: 1.4
**Last Updated**: 2026-07-06
**Owner**: foxcool
**Status**: Active
