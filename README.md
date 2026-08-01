<p align="center"><img src="docs/logo.svg" width="128" alt="Greedy Eye logo"></p>

# Greedy Eye

Personal investment intelligence platform. Aggregate data from multiple sources, track your portfolio, and make informed financial decisions.

![Go Version](https://img.shields.io/badge/go-1.25+-00ADD8?logo=go)
![License](https://img.shields.io/badge/license-MIT-green)
![Status](https://img.shields.io/badge/status-alpha-orange)

---

## What It Does

Aggregates everything you own — on-chain wallets, exchange accounts, broker
positions, manually entered holdings — into one catalog, prices it, and shows
what the total actually consists of.

Built as a personal, self-hosted system, and as a demonstration of production-shaped
Go architecture for financial data.

---

## Core Capabilities

**Data Aggregation**
- On-chain balances across EVM chains, Substrate, TON, Solana, Bitcoin, Cosmos,
  Tezos and more — one `WalletSyncer` per ecosystem, routed by address shape
- Exchange balances (Binance spot) via the account's own signed API key
- Manual and LLM-assisted import of positions and transactions, with provenance
- Prices from external providers on a budgeted schedule

**Portfolio Truth**
- Multi-account position tracking with per-user ownership enforcement
- Portfolio valuation that **reports what it could not price** (`ValuationCoverage`)
  instead of silently dropping those positions
- Scam-filter verdicts on assets; flagged holdings are quarantined out of totals
  in the open, not subtracted quietly
- Composite asset identity `(symbol, market, type)` plus contract-level identity,
  so a fake token cannot inherit a real ticker's price

**Extensible Architecture**
- Adapter pattern for every external API; credentials live in the database,
  encrypted, resolved per account
- Connect-RPC serves browser-friendly JSON and gRPC from one port
- Modular monolith that can be split into services by configuration

---

## Technology Stack

**Backend:**
- Go 1.25+, Connect-RPC and Protocol Buffers (h2c on :8080)
- PostgreSQL 17+ with pgx (raw SQL, no ORM)
- Atlas declarative schema (`schema.hcl`)

**Infrastructure:**
- Docker & Docker Compose; Traefik with psina forwardAuth in front
- Testcontainers for integration tests
- Structured logging (slog), Sentry

**Integrations:**
- Prices: CoinGecko, Binance
- On-chain: Moralis (EVM), Subscan (Substrate), tonapi, Helius (Solana),
  Esplora (Bitcoin), Cosmos LCD, TzKT (Tezos), Blockchair
- Notifications: Telegram
- See [docs/providers.md](docs/providers.md) for what each one needs

---

## Quick Start

### Prerequisites
- Go 1.25+
- Docker (for testcontainers and dev environment)
- Atlas CLI (`curl -sSf https://atlasgo.sh | sh`)

### Run Locally

```bash
# Clone and start
git clone https://github.com/foxcool/greedy-eye.git
cd greedy-eye

# Start dev environment
make up
make schema-apply

# Server (Connect-RPC over h2c — REST + gRPC on one port):
# http://localhost:8080
```

### Health Check

```bash
curl http://localhost:8080/eye/health
```

### Run Tests

```bash
make test                    # All tests (unit + integration)
make test-unit               # Unit tests only
make test-integration        # Integration tests (uses testcontainers)
```

---

## Project Structure

```
greedy-eye/
├── api/v1/                # Protobuf definitions + generated code
│   ├── marketdata.proto   # Assets, prices, valuation coverage
│   ├── portfolio.proto    # Portfolios, accounts, holdings, transactions, import
│   ├── automation.proto   # Rules + executions
│   ├── analytics.proto    # Heatmaps
│   └── apiv1connect/      # Generated Connect handlers and clients
├── cmd/eye/               # Binary: wiring, config, provider registration
├── internal/
│   ├── adapter/           # External API clients, one package per provider
│   │                      # + ratelimit/ (shared rate and quota budget per key)
│   ├── entity/            # Domain entities and provider interfaces
│   ├── middleware/        # User provisioning, ownership enforcement
│   ├── scamfilter/        # Asset identity scoring and verdicts
│   ├── scheduler/         # Cron: rule schedules + price sweep
│   ├── service/           # Connect handlers: marketdata, portfolio,
│   │                      # automation, analytics, credentials
│   └── store/postgres/    # pgx implementation + integration tests
├── schema.hcl             # Database schema (Atlas, declarative)
├── docs/                  # Architecture, development, providers
└── deploy/                # Compose config
```

---

## Documentation

- **[Architecture Guide](docs/architecture.md)** — System design, C4 diagrams, service structure
- **[Development Guide](docs/development.md)** — Setup, workflow, roadmap
- **API reference** — the `api/v1/*.proto` files are the source of truth. Connect-RPC serves them as JSON and gRPC; there is no separate OpenAPI spec (Connect is not covered by the OpenAPI generator).

---

## Development Status

**Alpha, running in production by its author.** One instance, one user, a real
portfolio. The label is not modesty: the number the system reports is still being
made trustworthy, and that is the current focus.

Done:
- ✅ 4-service Connect-RPC monolith (MarketData, Portfolio, Automation, Analytics)
- ✅ pgx raw SQL store; account credentials encrypted at rest (ADR-005), resolved
  per account (user → system → env)
- ✅ Ownership enforced on every by-ID and list RPC
- ✅ Wallet sync across 8 ecosystems; Binance spot sync; manual and LLM import
- ✅ Scheduled price fetch, budgeted against each credential's remaining plan
- ✅ Scam filtering with contract-level asset identity
- ✅ Valuation coverage: the total says what it could not price

In progress / next:
- 🔄 Quote confidence — a price with no market volume behind it must not enter
  the sum as if it were real
- 🔄 Price coverage for non-crypto assets (MOEX, SPBEX, FX)
- 📋 Automation engines (DCA / rebalancing / stop-loss) — deliberately blocked
  until the portfolio total is trustworthy; rules on top of a wrong number are
  worse than no rules
- 📋 Metrics and monitoring beyond the health check

Beyond the feature work above, calling this beta would also need a documented
upgrade path for someone else's instance (the schema is applied declaratively,
with no versioned migrations), end-to-end tests in CI, and metrics. Known risks
and debt: [docs/architecture.md](docs/architecture.md) §11.

---

## Development

```bash
make up               # Start dev environment (docker compose)
make schema-apply     # Apply database schema
make test             # Run all tests
make test-unit        # Run unit tests only
make test-integration # Run integration tests (testcontainers)
make buf-gen          # Generate protobuf code
make schema-diff      # Show schema changes
```

See [Development Guide](docs/development.md) for details.

---

## License

MIT License

---

**Docs:** [Architecture](docs/architecture.md) • [Development](docs/development.md) • API: [`api/v1/*.proto`](api/v1/)
