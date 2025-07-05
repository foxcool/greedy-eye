# HTTP API Wrapper + Basic Integrations MVP

## Task Overview

**Type**: Level 3 (Intermediate Feature) - back to focused scope
**Priority**: URGENT  
**Estimated Duration**: 3 weeks
**Architecture**: HTTP wrapper + 4 core integrations

## MVP SCOPE - FOCUSED APPROACH

### Core Integrations (MVP)

1. **Binance API** - Exchange balances/trades
2. **Gate.io API** - Exchange balances/trades  
3. **CoinGecko API** - Market prices (existing, refactor)
4. **Etherscan API** - Ethereum wallet monitoring

### Architecture Decision Points

#### 1. HTTP API Interface Design

## COMPREHENSIVE INTEGRATION ARCHITECTURE

### Core Service

- **gRPC Server**: Port 50051 (existing)
- **HTTP Server**: Port 8080 (new, same process)
- **Direct Integration**: HTTP handlers call gRPC services

### External Integration Services (Comprehensive)

#### 1. Traditional Finance Integrations

- **T-Invest API**: Тинькофф Инвестиции (акции, облигации, ETF)
- **Open Banking APIs**: Банковские счета (будущее)
- **Lending Platforms**: JetLend, P2P (будущее)

#### 2. DeFi & Blockchain Integrations

- **Zapper API**: DeFi positions, protocols, yields
- **Blockchain Clients**: Bitcoin, Ethereum, Polygon
- **Tonkeeper API**: TON blockchain integration
- **Etherscan API**: Ethereum transactions & monitoring

#### 3. Price & Market Data

- **CoinGecko API**: Crypto prices (existing, refactor)
- **CoinMarketCap API**: Alternative price source
- **Exchange APIs**: Binance, GateIO price feeds

#### 4. Portfolio & Account Data

- **Exchange APIs**: Binance, GateIO, Coinbase (balances, trades)
- **Wallet Monitoring**: Address tracking for whales analysis
- **DeFi Position Tracking**: Through Zapper integration

### Unified Integration Architecture

## Technology Stack Validation

- Framework: Gin HTTP framework
- Build Tool: Go modules + Makefile
- Language: Go 1.24  
- Storage: PostgreSQL (existing)
- Container: Docker (existing)

## Technology Validation Checkpoints

- [ ] Gin HTTP server integrated in cmd/eye/main.go
- [ ] Direct gRPC service calls from HTTP handlers
- [ ] Basic JSON transformation implemented
- [ ] Dual-server startup (HTTP + gRPC) functional
- [ ] Configuration updated for HTTP port

## Status

- [x] Initialization complete
- [x] Planning complete (revised architecture)
- [ ] Technology validation in progress
- [ ] Creative phase (Architecture) pending
- [ ] Creative phase (Security) pending
- [ ] Creative phase (API Design) pending
- [ ] Implementation pending

## Implementation Plan

### Phase 1: Foundation (Week 1)

1. [ ] Service Integration
   - [ ] Add HTTP server to cmd/eye/main.go
   - [ ] Update Config struct with HTTP port
   - [ ] Parallel HTTP + gRPC server startup

2. [ ] Technology Validation
   - [ ] Gin hello world on port 8080
   - [ ] Direct gRPC service access test
   - [ ] Basic JSON response test

3. [ ] Project Structure
   - [ ] Create internal/http/ directory
   - [ ] HTTP handlers framework
   - [ ] Middleware system

### Phase 2: Core HTTP Wrapper (Week 2)

1. [ ] Middleware Stack
   - [ ] Authentication middleware
   - [ ] Rate limiting middleware
   - [ ] Logging middleware
   - [ ] CORS middleware

2. [ ] Handler Framework  
   - [ ] Base handler structure
   - [ ] Error handling system
   - [ ] Request validation framework

3. [ ] Direct gRPC Integration
   - [ ] Service injection into handlers
   - [ ] Response transformation utilities
   - [ ] Error mapping HTTP<->gRPC

### Phase 3: API Endpoints (Week 3)

1. [ ] User Endpoints
   - [ ] GET /api/v1/users/{id}
   - [ ] PUT /api/v1/users/{id}
   - [ ] User preferences API

2. [ ] Portfolio Endpoints
   - [ ] GET /api/v1/portfolios/{id}
   - [ ] GET /api/v1/portfolios/{id}/holdings
   - [ ] Portfolio analytics API

3. [ ] Asset & Price Endpoints
   - [ ] GET /api/v1/assets/{symbol}
   - [ ] GET /api/v1/prices/{symbol}
   - [ ] Market data API

## Creative Phases Required

- [x] 🏗️ Architecture Design: HTTP wrapper integration and middleware pipeline
- [x] 🔐 Security Design: API key management and rate limiting strategy  
- [x] 🎨 API Design: RESTful endpoints and JSON schema design

## SIMPLIFIED ARCHITECTURE BENEFITS

- ✅ Single container deployment
- ✅ Direct gRPC service access (no network calls)
- ✅ Shared configuration and logging
- ✅ Simplified testing and development
- ✅ Lower resource overhead
- ✅ Easier error handling and debugging

## Dependencies

- StorageService (existing gRPC, direct access)
- UserService (existing gRPC, direct access)
- PortfolioService (existing gRPC, direct access)
- AssetService (existing gRPC, direct access)
- PriceService (existing gRPC, direct access)
- PostgreSQL database (existing)
- Docker environment (existing)

## Risk Mitigation

- Performance: Direct service access, no network overhead
- Security: Comprehensive security review and testing
- Integration: Versioned API with backward compatibility

## Success Criteria  

- [ ] HTTP API responds to all planned endpoints
- [ ] API key authentication fully functional
- [ ] Rate limiting enforced per client
- [ ] < 50ms response time for simple queries (faster due to direct access)
- [ ] Handle 100+ concurrent requests
- [ ] Complete audit logging implemented

## Next Steps

1. Proceed to CREATIVE mode for Architecture Design
2. Technology validation implementation
3. Begin Phase 1 foundation development
