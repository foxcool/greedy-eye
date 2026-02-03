# 🦅 Greedy Eye

Personal investment intelligence platform. Aggregate data from multiple sources, track your portfolio, and make informed financial decisions.

![Go Version](https://img.shields.io/badge/go-1.25+-00ADD8?logo=go)
![License](https://img.shields.io/badge/license-MIT-green)
![Status](https://img.shields.io/badge/status-alpha-orange)

---

## What It Does

Think of it as a personal Bloomberg Terminal on a budget — aggregate financial data from exchanges, brokers, and price feeds into one place. Track all your investments (crypto, stocks, bonds) and get insights to make better decisions.

Built as a learning project to demonstrate production-ready Go architecture for financial applications.

---

## Core Capabilities

**Data Aggregation**
- Pull data from exchanges (Binance), price feeds (CoinGecko), and brokerages
- Unified view of crypto, stocks, bonds, and derivatives
- Historical price tracking and performance analytics

**Portfolio Intelligence**
- Multi-account position tracking across platforms
- P&L calculations and cost basis tracking
- Asset allocation analysis

**Extensible Architecture**
- Modular gRPC services for different data sources
- Easy to add new adapters for exchanges or price feeds
- REST API via gRPC-Gateway for external integrations

---

## Technology Stack

**Backend:**
- Go 1.25+ with gRPC and Protocol Buffers
- PostgreSQL 17+ with pgx
- Connect-RPC for HTTP API

**Infrastructure:**
- Docker & Docker Compose
- Atlas declarative migrations (schema.hcl)
- Testcontainers for integration tests
- Structured logging (slog, Sentry)

**Integrations:**
- CoinGecko, Binance APIs
- Telegram Bot (optional interface)
- OpenAPI 3.0 spec

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

# Servers:
# HTTP API: http://localhost:8080
# gRPC:     localhost:50051
```

### Health Check

```bash
curl http://localhost:8080/health
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
├── api/v1/                # Protocol Buffer definitions (domain-based)
│   ├── marketdata.proto   # Asset + Price management
│   ├── portfolio.proto    # Portfolio + Holding + Account + Transaction
│   └── automation.proto   # Rule + RuleExecution
├── cmd/eye/               # Main application
├── internal/
│   ├── adapter/           # External API clients (Binance, CoinGecko, Telegram)
│   ├── api/               # Generated gRPC/HTTP code
│   ├── entity/            # Domain entities
│   ├── service/           # Business logic services
│   ├── store/             # Data persistence layer
│   │   └── postgres/      # PostgreSQL implementation
│   └── testutil/          # Test utilities (testcontainers)
├── schema.hcl             # Database schema (Atlas)
├── atlas.hcl              # Atlas configuration
├── docs/                  # Architecture documentation
└── deploy/                # Docker configs
```

---

## Documentation

- **[Architecture Guide](docs/architecture.md)** — System design, C4 diagrams, service structure
- **[Development Guide](docs/development.md)** — Setup, workflow, roadmap
- **[OpenAPI Spec](docs/openapi.yaml)** — HTTP API reference

---

## Development Status

**Phase 1-3:** ✅ Complete — Foundation, services, API gateway
**Phase 4:** 🔄 In Progress — Core business logic implementation
**Phase 5:** 📋 Planned — Production hardening

Current implementation:
- ✅ gRPC service architecture with 3 domain services
- ✅ Database layer with pgx
- ✅ External API adapters (CoinGecko, Binance)
- ✅ Integration test coverage
- 🔄 Portfolio analytics and insights

---

## Key Features

### Multi-Source Data
- Aggregate price data from exchanges and feeds
- Support for crypto, stocks, bonds, derivatives
- Extensible adapter pattern for new sources

### Portfolio Tracking
- Multi-account position aggregation
- Performance analytics and P&L
- Historical tracking

### Architecture
- Modular monolith (microservice-ready)
- Clean separation: services, adapters, domain
- Comprehensive testing

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

**Docs:** [Architecture](docs/architecture.md) • [Development](docs/development.md) • [API](docs/openapi.yaml)
