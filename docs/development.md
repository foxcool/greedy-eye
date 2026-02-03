# Development Guide - Greedy Eye

Complete guide for developing and maintaining the Greedy Eye universal portfolio management system supporting diverse
asset types including cryptocurrencies, securities, derivatives, and alternative assets.

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
- gRPC server on port 50051
- HTTP API on port 8080
- Health check at http://localhost:8080/health

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

## Current Development Status

### Implementation Progress
- **Phase 1**: Infrastructure Foundation ✅ **COMPLETED**
- **Phase 2**: Core Architecture ✅ **COMPLETED**  
- **Phase 3**: Services & API Gateway ✅ **STUB PHASE COMPLETED**
- **Current**: Business Logic Implementation 🔄

### Service Status

| Service | Status | Implementation | Tests | Integration |
|---------|--------|---------------|-------|-------------|
| MarketDataStore | ✅ Complete | pgx + raw SQL | ✅ | ✅ |
| PortfolioStore | 🔄 In Progress | pgx + raw SQL | ❌ | ❌ |
| SettingsStore | 🔄 In Progress | pgx + raw SQL | ❌ | ❌ |
| UserService | ✅ Implemented | Full business logic | ✅ | ✅ |
| AssetService | ✅ Implemented | Full business logic | ✅ | ✅ |
| PortfolioService | 🔄 Stubs | API complete | ✅ | ❌ |
| PriceService | ✅ Implemented | External API integration | ✅ | ✅ |
| RuleService | 🔄 Stubs | API complete | ✅ | ❌ |
| **MessengerService** | 🔄 Stubs | Multi-platform architecture | ✅ | ❌ |
| AuthService | 🔄 Proto | Proto only | ❌ | ❌ |

### External Adapters Status

Adapter pattern isolates external API dependencies from core logic.
All adapters use gRPC status codes and interface-based design.

| Adapter | Provider | Status | Tests | Coverage |
|---------|----------|--------|-------|----------|
| Messenger | Telegram | ⚠️ Stubs | ✅ | 45.5% |
| Price Data | CoinGecko | ⚠️ Stubs | ✅ | 64.3% |
| Exchange | Binance | ⚠️ Stubs | ✅ | 57.1% |
| Blockchain | Moralis | ⚠️ Stubs | ✅ | 66.7% |

**Legend**: ⚠️ Stubs = Stub implementation with unimplemented methods, tests verify error handling

### Recent Achievements

#### v0.0.4-alpha - Adapter Pattern & MessengerService Refactoring

- ✅ **Adapter Architecture**: Implemented adapter pattern for external integrations (Messenger, Price Data, Exchange, Blockchain)
- ✅ **MessengerService**: Renamed TelegramBotService → MessengerService for multi-platform support
- ✅ **Stub Implementations**: All 4 adapter categories with comprehensive test coverage (45-67%)
- ✅ **Proto Simplification**: Simplified Account model to use flexible data maps for provider-specific parameters

#### v0.0.3-alpha - Core Services Implementation
- ✅ UserService, AssetService, PriceService - full business logic implementation
- ✅ External price data API integration with price fetching
- ✅ Integration tests for all core services
- ✅ Complete service architecture with dependency management
- ✅ HTTP API Gateway with gRPC-Gateway auto-generation
- ✅ Comprehensive test coverage (>90%) for all implemented services

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
│   ├── api/v1/             # Generated protobuf/connect code
│   ├── entity/             # Domain entities
│   ├── service/            # Business logic services
│   │   ├── asset/          # AssetService
│   │   ├── marketdata/     # MarketData gRPC handler
│   │   ├── portfolio/      # Portfolio gRPC handler
│   │   ├── price/          # PriceService
│   │   ├── rule/           # RuleService
│   │   ├── settings/       # SettingsStore
│   │   └── user/           # UserService
│   ├── store/              # Data persistence layer
│   │   └── postgres/       # PostgreSQL implementation (pgx)
│   └── testutil/           # Test utilities (testcontainers)
├── schema.hcl              # Database schema (Atlas HCL)
├── atlas.hcl               # Atlas configuration
├── docs/                   # Documentation
└── deploy/                 # Docker and deployment configs
```

## Configuration

### Environment Variables
```env
# Database
DB_URL=postgres://user:pass@localhost:5432/greedy_eye?sslmode=disable

# Server
GRPC_PORT=50051
HTTP_PORT=8080

# Logging
EYE_LOGGING_OUTPUT=STDOUT    # STDOUT or file path
EYE_LOGGING_LEVEL=INFO       # DEBUG, INFO, WARN, ERROR, FATAL
EYE_LOGGING_FORMAT=TEXT      # TEXT or JSON

# External APIs
BINANCE_API_KEY=your_key
COINGECKO_API_KEY=your_key
TBANK_INVEST_TOKEN=your_token

# Telegram Bot
EYE_TELEGRAM_TOKEN=your_token
EYE_TELEGRAM_CHATIDS="-1001234567890,987654321"

# Speech Services (for TelegramBotService)
OPENAI_API_KEY=your_key
GOOGLE_CREDENTIALS_PATH=./credentials.json
YANDEX_API_KEY=your_key
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

# Enabled services (optional)
services:
  - asset
  - portfolio
  - price
  - user
  - storage
  - telegram
```

### Money Precision and Decimal Handling
All monetary amounts use decimal precision to avoid floating-point errors:
```
real_value = amount / 10^precision
```
This applies to transaction amounts, prices, holdings, and other financial values.

## Service Architecture

### Dependency Graph
```
Store Layer (PostgreSQL + pgx)
├── MarketDataStore (assets, prices)
├── PortfolioStore (portfolios, holdings, accounts, transactions)
└── SettingsStore (user preferences)

Service Layer
├── UserService
├── AssetService → MarketDataStore
├── PriceService → MarketDataStore, AssetService
├── PortfolioService → PortfolioStore, AssetService
├── RuleService → UserService, PortfolioService, AssetService, PriceService
└── MessengerService → All services
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

### Roadmap Overview

```mermaid
%%{init: {'theme':'base', 'themeVariables': { 'fontSize':'14px'}}}%%
graph LR
    subgraph Foundation["🏗️ Foundation"]
        direction TB
        F1[Infrastructure<br/>✅ Done]
        F2[Database Schema<br/>✅ Done]
        F3[gRPC Services<br/>✅ Done]
        F4[HTTP Gateway<br/>✅ Done]
    end

    subgraph Core["⚙️ Core Services"]
        direction TB
        C1[StorageService<br/>✅ Done]
        C2[UserService<br/>✅ Done]
        C3[AssetService<br/>✅ Done]
        C4[PriceService<br/>✅ Done]
        C5[Price Data API<br/>✅ Done]
    end

    subgraph Portfolio["📊 Portfolio"]
        direction TB
        P1[PortfolioService<br/>🔄 In Progress]
        P2[Holdings Logic<br/>📋 Planned]
        P3[Transactions<br/>📋 Planned]
        P4[Analytics<br/>📋 Planned]
    end

    subgraph Automation["🤖 Automation"]
        direction TB
        A1[RuleService<br/>📋 Planned]
        A2[DCA Engine<br/>📋 Planned]
        A3[Rebalancing<br/>📋 Planned]
        A4[Alerts<br/>📋 Planned]
    end

    subgraph Integration["🔌 Integration"]
        direction TB
        I1[Trading APIs<br/>📋 Planned]
        I2[MessengerBot<br/>📋 Planned]
        I3[Voice Support<br/>🎯 Future]
        I4[Additional APIs<br/>🎯 Future]
    end

    subgraph Production["🚀 Production"]
        direction TB
        PR1[AuthService<br/>📋 Planned]
        PR2[Security<br/>🎯 Future]
        PR3[Monitoring<br/>🎯 Future]
        PR4[Deploy<br/>🎯 Future]
    end

    Foundation --> Core
    Core --> Portfolio
    Portfolio --> Automation
    Core --> Integration
    Automation --> Production
    Integration --> Production

    classDef done fill:#10b981,stroke:#065f46,color:#fff,stroke-width:2px
    classDef progress fill:#f59e0b,stroke:#92400e,color:#fff,stroke-width:2px
    classDef planned fill:#6b7280,stroke:#374151,color:#fff,stroke-width:2px
    classDef future fill:#3b82f6,stroke:#1e40af,color:#fff,stroke-width:2px

    class F1,F2,F3,F4,C1,C2,C3,C4,C5 done
    class P1 progress
    class P2,P3,P4,A1,A2,A3,A4,I1,I2,PR1 planned
    class I3,I4,PR2,PR3,PR4 future
```

### Service Implementation Status

```mermaid
%%{init: {'theme':'base'}}%%
mindmap
  root((Greedy Eye<br/>Services))
    Foundation ✅
      StorageService ✅
        Ent ORM
        Migrations
        CRUD Operations
      Database ✅
        PostgreSQL
        Connection Pool
    Core Services
      UserService ✅
        User Management
        External API Keys
      AssetService ✅
        Asset Metadata
        Multi-type Support
      PriceService ✅
        Price Data APIs ✅
        Price History
        Trading APIs 📋
    Business Logic
      PortfolioService 🔄
        Holdings 📋
        Transactions 📋
        Analytics 📋
      RuleService 📋
        Rule Engine 📋
        DCA 📋
        Rebalancing 📋
        Alerts 📋
    Integration
      MessengerBot 📋
        Commands 📋
        Voice 🎯
        Notifications 📋
      External APIs
        Price Data ✅
        Trading 📋
        Additional 🎯
    Security 🎯
      AuthService 📋
        JWT Tokens 📋
        API Keys 📋
      Monitoring 🎯
        Metrics 🎯
        Logging 🎯
```

### Current Sprint Goals

#### ✅ Completed

- StorageService with full Ent ORM implementation
- UserService with business logic and preference management
- AssetService with multi-asset type support
- PriceService with external price data API integration
- Integration tests for all core services

#### 🔄 In Progress

- PortfolioService business logic implementation
- Additional external API integrations

#### 📋 Next Up

1. **PortfolioService** - Complete portfolio calculations and analytics
2. **RuleService** - Implement rule engine and automation strategies
3. **MessengerBot** - Basic command handling and notifications
4. **AuthService** - JWT authentication and authorization

#### 🎯 Future Milestones

- Advanced analytics and risk management
- Multi-provider price aggregation
- Voice interface for messenger bot
- Additional trading platform integrations
- Production deployment with monitoring

## Common Development Tasks

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
curl http://localhost:8080/health

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
1. **Port conflicts**: Kill processes on 50051/8080
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

