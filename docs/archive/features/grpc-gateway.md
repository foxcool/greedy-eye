# HTTP API with gRPC-Gateway + Rule-Based Portfolio System

## Overview

**Type**: Level 3 (Intermediate Feature)
**Priority**: URGENT  
**Estimated Duration**: 3 weeks
**Architecture**: gRPC-Gateway + Rule-Based Portfolio Management

## Architecture Decisions

### Core Technologies

1. **gRPC-Gateway** - Automatic HTTP API generation from proto files
2. **Rule-Based Portfolios** - Portfolios = holdings, Rules = separate entities
3. **Zero Trust Security** - API keys + JWT, scope-based permissions
4. **Flexible Configuration** - Protobuf.Struct for strategy flexibility

### Technology Stack

- **Framework**: gRPC-Gateway
- **Authentication**: API Keys + JWT hybrid
- **Build Tool**: buf + Go modules + Makefile
- **Language**: Go 1.24  
- **Storage**: PostgreSQL (existing)
- **Container**: Docker (existing)

## Implementation Status

### Phase 1: gRPC-Gateway Foundation ✅ COMPLETED

**Summary**: Successfully implemented gRPC-Gateway foundation with dual-server architecture.

**Achievements**:

- ✅ Enhanced all existing proto files with HTTP annotations
- ✅ Created new proto files for AuthService and RuleService
- ✅ Set up buf toolchain for gRPC-Gateway generation
- ✅ Updated build system with buf commands
- ✅ Implemented dual-server (gRPC port 50051 + HTTP port 8080)
- ✅ Added health check endpoint at `/health`
- ✅ Configured gRPC-Gateway registration for all services

**Technical Validation**:

- [x] gRPC-Gateway HTTP endpoints auto-generated
- [x] buf generate working for all proto files
- [x] Dual-server startup functional
- [x] Project builds successfully
- [x] Health endpoint available

### Phase 2: Security + Rule Services (Week 2) - IN PROGRESS

#### 2.1 Security Implementation
- [ ] Implement AuthService with API key + JWT hybrid
- [ ] Create security middleware for HTTP and gRPC
- [ ] Set up encrypted external API key storage
- [ ] Implement rate limiting by scope

#### 2.2 Rule-Based Architecture
- [ ] Implement RuleService gRPC service
- [ ] Create rule execution engine with strategy pattern
- [ ] Implement basic rule types:
  - [ ] Target allocation rebalancing
  - [ ] Monthly withdrawal
  - [ ] DCA (Dollar Cost Averaging)
- [ ] Rule execution history tracking

#### 2.3 Service Dependencies
- [ ] Update PortfolioService for rule integration
- [ ] Enhance PriceService with aggregation
- [ ] Create inter-service client dependencies

### Phase 3: Integration Services + Testing (Week 3) - PLANNED

#### 3.1 Integration Services
- [ ] Refactor CoinGecko service to provider pattern
- [ ] Implement Binance provider (balances/trades)
- [ ] Implement Gate.io provider (balances/trades)  
- [ ] Implement Etherscan provider (wallet monitoring)

#### 3.2 Rule Execution Testing
- [ ] Test target allocation rule with R script logic
- [ ] Test multi-source portfolio rebalancing
- [ ] Test rule execution history and impact analysis

#### 3.3 End-to-End Validation
- [ ] Validate all HTTP endpoints via gRPC-Gateway
- [ ] Test Zero Trust security flows
- [ ] Performance testing (< 100ms for cached data)
- [ ] Integration testing with external APIs

## Key Architecture Components

### 1. Enhanced Proto Structure

```
api/
├── models/
│   ├── portfolio.proto (simplified - no strategies)
│   ├── rule.proto (new - flexible rule system)
│   └── rule_execution.proto (new - execution tracking)
├── services/
│   ├── portfolio_service.proto (enhanced with rule integration)
│   ├── rule_service.proto (new - rule CRUD + execution)
│   ├── auth_service.proto (new - Zero Trust security)
│   └── integration_service.proto (new - external API aggregation)
```

### 2. Service Dependencies Map

```
PortfolioService → RuleService, PriceService, AuthService
RuleService → PortfolioService, IntegrationService, PriceService  
PriceService → IntegrationService, AssetService
IntegrationService → External APIs (Binance, Gate.io, CoinGecko, Etherscan)
AuthService → StorageService (for API key storage)
```

### 3. Rule-Based Examples

- **Target Allocation**: Rebalancing by percentages (like R script)
- **Monthly Withdrawal**: Regular withdrawals for living expenses
- **Stop Loss**: Protective stops
- **DCA**: Regular accumulation

## HTTP API Gateway Design

### Key Features
- **Authentication**: API key-based authentication
- **Authorization**: Role-based access control
- **Rate Limiting**: Per-client request limits
- **Logging**: Comprehensive request/response logging
- **Error Handling**: Consistent error response format
- **Validation**: Request validation and sanitization

### API Endpoints Structure
```
/api/v1/users/{id}          - User profile management
/api/v1/portfolios/{id}     - Portfolio operations
/api/v1/assets/{symbol}     - Asset information
/api/v1/prices/{symbol}     - Price data
/api/v1/transactions/{id}   - Transaction history
/api/v1/accounts/{id}       - Account information
/api/v1/rules/{id}          - Rule management
/api/v1/rules/{id}/execute  - Rule execution
```

### Authentication Flow
1. External service provides API key in Authorization header
2. API Gateway validates key and identifies client
3. Request forwarded to appropriate gRPC service
4. Response transformed to JSON and returned

## Technology Validation Results

- [x] gRPC-Gateway HTTP endpoints auto-generated
- [x] buf generate working for all proto files
- [x] Dual-server startup functional
- [x] Project builds successfully
- [x] Health endpoint available
- [ ] Zero Trust authentication flow validated
- [ ] Rule execution engine functional
- [ ] Inter-service gRPC communication working
- [ ] External API integrations responding

## Success Criteria

- [ ] HTTP API auto-generated from proto files
- [ ] Zero Trust authentication with API keys + JWT
- [ ] Rule-based portfolio management functional
- [ ] Target allocation rebalancing working (R script logic)
- [ ] Multi-source holdings aggregation (exchanges + wallets)
- [ ] External API integrations (Binance, Gate.io, CoinGecko, Etherscan)
- [ ] Rule execution history and impact analysis
- [ ] < 100ms response time for cached data
- [ ] Handle 100+ concurrent requests

## Phase 1 Achievements 🎉

✅ **gRPC-Gateway Architecture**: HTTP API automatically generated from proto files  
✅ **Dual-Server Setup**: gRPC (50051) + HTTP (8080) in single process  
✅ **Proto Enhancement**: Added HTTP annotations to all services  
✅ **New Services Design**: AuthService and RuleService proto files created  
✅ **Build System**: buf + gRPC-Gateway fully configured  
✅ **Health Endpoint**: `/health` for monitoring availability  

## Next Steps

1. **Start Phase 2.1**: Implement AuthService with API key + JWT hybrid
2. **Zero Trust Security**: Setup middleware for HTTP and gRPC
3. **External API Keys**: Encrypted storage for Binance, Gate.io keys
4. **Rate Limiting**: By scope and API keys
5. **Rule Services**: Implement RuleService gRPC service

## Dependencies

- **Existing Services**: StorageService, UserService, PortfolioService, AssetService, PriceService
- **New Services**: AuthService, RuleService, IntegrationService
- **External APIs**: Binance, Gate.io, CoinGecko, Etherscan
- **Build Tools**: buf, protoc-gen-grpc-gateway, protoc-gen-openapiv2