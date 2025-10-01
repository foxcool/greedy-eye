# 🦅 Greedy Eye

**Universal portfolio management platform** with automated DCA strategies, rebalancing, and multi-asset tracking.

Track crypto, stocks, bonds, and derivatives in one place. Automate your investment strategy through Telegram bot with optional voice commands.

![Build Status](https://img.shields.io/badge/build-passing-brightgreen)
![Go Version](https://img.shields.io/badge/go-1.23+-00ADD8?logo=go)
![License](https://img.shields.io/badge/license-MIT-green)
![Status](https://img.shields.io/badge/status-alpha-orange)

---

## What Makes It Different

⚡ **Automation First** — Set DCA strategies, rebalancing rules, stop-loss alerts and let the system work
🌐 **Universal Assets** — Crypto, stocks, bonds, derivatives tracked in one unified portfolio
🏗️ **Production Ready Architecture** — Modular monolith with gRPC services, horizontal scaling ready
🤖 **Conversational Interface** — Manage everything through Telegram bot (optional voice support)

---

## Get Started by Role

<table>
<tr>
<td width="33%" valign="top">

### 👨‍💻 Developer

**[→ Setup Guide](docs/development.md)**

```bash
git clone https://github.com/foxcool/greedy-eye.git
cd greedy-eye
docker-compose up -d
```

**Health:** `localhost:8080/health`
**gRPC:** `localhost:50051`

</td>
<td width="33%" valign="top">

### 🏗️ Architect

**[→ Architecture](docs/architecture.md)**

**Stack:**
- Modular monolith
- 8 gRPC services
- PostgreSQL + Ent ORM
- gRPC-Gateway

**Design:** C4 diagrams, ADRs, service topology

</td>
<td width="33%" valign="top">

### 📱 User

**[→ Try Production](https://eye.darkfox.info)**

**Commands:**
`/portfolio` — View balance
`/dca` — Setup automated buying
`/alerts` — Price notifications
`/rebalance` — Portfolio optimization

</td>
</tr>
</table>

---

## Key Features

### Portfolio Tracking
- **Multi-asset support:** Cryptocurrencies, stocks, bonds, derivatives
- **Real-time prices:** CoinGecko, Binance, T-Bank Invest integrations
- **Performance analytics:** P&L, ROI, cost basis tracking
- **Multi-account:** Aggregate positions across exchanges and brokerages

### Investment Automation
- **DCA Strategies:** Schedule periodic purchases with custom rules
- **Rebalancing Rules:** Maintain target asset allocation automatically
- **Price Alerts:** Get notified when assets hit target prices
- **Stop-Loss Orders:** Automatic risk management rules

### Integration Ecosystem
- **Exchanges:** Binance, more coming
- **Brokerages:** T-Bank Invest, more coming
- **Price Data:** CoinGecko, exchange APIs
- **Messaging:** Telegram bot with Russian/English support

---

## Technology Stack

**Core:**
Go 1.23+ • gRPC • Protocol Buffers • PostgreSQL • Ent ORM

**APIs:**
gRPC-Gateway (auto-generated HTTP) • REST • OpenAPI 3.0

**Deployment:**
Docker • Single binary or microservices • Horizontal scaling ready

**Integrations:**
CoinGecko • Binance • T-Bank Invest • Telegram Bot API

→ [Full Architecture Details](docs/architecture.md) | [API Reference](docs/openapi.yaml)

---

## Current Status

**Phase 1-3:** Foundation & Services ✅ Complete
**Phase 4:** Business Logic Implementation 🔄 In Progress
**Phase 5:** Production Hardening 📋 Planned

**Recent Milestones:**
- ✅ Core services: User, Asset, Portfolio, Price with full business logic
- ✅ External API integration: CoinGecko, Binance price feeds
- ✅ Integration tests for all services
- ✅ Rule engine foundation for automation

→ [Detailed Roadmap](docs/development.md#roadmap-overview)

---

## Quick Start Examples

### Start Local Development
```bash
docker-compose up -d postgres
make dev
# Server starts on :8080 (HTTP) and :50051 (gRPC)
```

### Check System Health
```bash
curl http://localhost:8080/health
```

### Run Tests
```bash
make test                    # All tests
make test-integration       # Integration tests only
```

### Generate API Code
```bash
make buf-gen                # Regenerate protobuf and gRPC code
```

---

## Contributing

Found a bug? Have an idea? **PRs welcome!**

- 🐛 [Report Issues](https://github.com/foxcool/greedy-eye/issues)
- 💡 [Suggest Features](https://github.com/foxcool/greedy-eye/discussions)
- 📖 [Development Guide](docs/development.md)

**Quick Wins for Contributors:**
- Add new price data providers
- Implement exchange connectors (Kraken, Coinbase, etc.)
- Improve Telegram bot commands
- Write integration tests

---

## License

MIT License — use it, fork it, commercialize it.

---

**Production:** https://eye.darkfox.info
**Documentation:** [Architecture](docs/architecture.md) • [Development](docs/development.md)
**API:** [OpenAPI Spec](docs/openapi.yaml)
