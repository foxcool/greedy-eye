# HTTP API with gRPC-Gateway + Rule-Based Portfolio System

## Task Overview

**Type**: Level 3 (Intermediate Feature)
**Priority**: URGENT  
**Estimated Duration**: 3 weeks
**Architecture**: gRPC-Gateway + Rule-Based Portfolio Management

## FINAL ARCHITECTURE (Post-Creative Phases)

### Core Architecture Decisions

1. **gRPC-Gateway** - Автоматическая генерация HTTP API из proto файлов
2. **Rule-Based Portfolios** - Портфели = холдинги, Правила = отдельные сущности
3. **Zero Trust Security** - API keys + JWT, scope-based permissions
4. **Flexible Configuration** - Protobuf.Struct для гибкости стратегий

### Technology Stack

- **Framework**: gRPC-Gateway
- **Authentication**: API Keys + JWT hybrid
- **Build Tool**: buf + Go modules + Makefile
- **Language**: Go 1.24  
- **Storage**: PostgreSQL (existing)
- **Container**: Docker (existing)

## CREATIVE PHASES COMPLETED ✅

- [x] 🏗️ **Architecture Design**: gRPC-Gateway + межсервисные зависимости
- [x] 🔐 **Security Design**: Zero Trust + API key encryption + rate limiting
- [x] 🎨 **API Design**: Rule-based portfolio management + flexible strategies
- [x] 🔄 **Service Interaction**: Explicit service dependencies mapped

## IMPLEMENTATION PLAN (Updated)

### Phase 1: gRPC-Gateway Foundation (Week 1) ✅ COMPLETED

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

**Next Steps**: Proceed to Phase 2 (Security + Rule Services)

### Phase 2: Security + Rule Services (Week 2)

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

### Phase 3: Integration Services + Testing (Week 3)

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

## KEY ARCHITECTURAL COMPONENTS

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

- **Target Allocation**: Rebalancing по процентам (как в R скрипте)
- **Monthly Withdrawal**: Регулярные выводы на жизнь
- **Stop Loss**: Защитные стопы
- **DCA**: Регулярное накопление

## TECHNOLOGY VALIDATION CHECKPOINTS

- [x] gRPC-Gateway HTTP endpoints auto-generated
- [x] buf generate working for all proto files
- [x] Dual-server startup functional
- [x] Project builds successfully
- [x] Health endpoint available
- [ ] Zero Trust authentication flow validated
- [ ] Rule execution engine functional
- [ ] Inter-service gRPC communication working
- [ ] External API integrations responding

## STATUS

- [x] Initialization complete
- [x] Planning complete
- [x] All creative phases complete ✅
- [x] **Phase 1: gRPC-Gateway Foundation** ✅ COMPLETED
- [ ] **Phase 2: Security + Rule Services** ← Next phase
- [ ] Phase 3 pending

## PHASE 1 ACHIEVEMENTS 🎉

✅ **gRPC-Gateway Architecture**: HTTP API автоматически генерируется из proto файлов  
✅ **Dual-Server Setup**: gRPC (50051) + HTTP (8080) в одном процессе  
✅ **Proto Enhancement**: Добавлены HTTP аннотации к всем сервисам  
✅ **New Services Design**: AuthService и RuleService proto файлы созданы  
✅ **Build System**: buf + gRPC-Gateway полностью настроены  
✅ **Health Endpoint**: `/health` для мониторинга доступности  

## SUCCESS CRITERIA (Updated)

- [ ] HTTP API auto-generated from proto files
- [ ] Zero Trust authentication with API keys + JWT
- [ ] Rule-based portfolio management functional
- [ ] Target allocation rebalancing working (R script logic)
- [ ] Multi-source holdings aggregation (exchanges + wallets)
- [ ] External API integrations (Binance, Gate.io, CoinGecko, Etherscan)
- [ ] Rule execution history and impact analysis
- [ ] < 100ms response time for cached data
- [ ] Handle 100+ concurrent requests

## IMMEDIATE NEXT STEPS

1. **✅ Phase 1 COMPLETED**: gRPC-Gateway foundation готова
2. **Start Phase 2.1**: Implement AuthService с API key + JWT hybrid
3. **Zero Trust Security**: Настроить middleware для HTTP и gRPC
4. **External API Keys**: Encrypted storage для Binance, Gate.io keys
5. **Rate Limiting**: По scope и API ключам

## BUILD PHASE COMPLETED ✅

**Phase 1 Results**:

- 🎯 **Dual-Server Architecture**: gRPC + HTTP working
- 🎯 **gRPC-Gateway Integration**: Auto HTTP API generation
- 🎯 **Proto Structure**: Enhanced with HTTP annotations
- 🎯 **Build System**: buf + gRPC-Gateway toolchain ready
- 🎯 **Foundation Services**: AuthService & RuleService designed

**Validation Completed**:

```bash
# Project builds successfully
go build ./cmd/eye/

# gRPC-Gateway files generated
find . -name "*.pb.gw.go" | wc -l  # 5 services

# Health endpoint ready
curl http://localhost:8080/health
```

Ready for **Phase 2: Security + Rule Services** implementation.

## DEPENDENCIES (Updated)

- **Existing Services**: StorageService, UserService, PortfolioService, AssetService, PriceService
- **New Services**: AuthService, RuleService, IntegrationService
- **External APIs**: Binance, Gate.io, CoinGecko, Etherscan
- **Build Tools**: buf, protoc-gen-grpc-gateway, protoc-gen-openapiv2


## REFLECTION COMPLETED ✅

### Multi-Expert Review Highlights

**What Went Well**:
- gRPC-Gateway подход избежал дублирования кода
- Buf toolchain упростил работу с protobuf
- Graceful shutdown реализован правильно
- Health endpoint простой и эффективный

**Critical Issues Found**:
- **Hardcoded HTTP port**: 8080 вместо config.HTTP.Port (TODO оставлен)
- **Incomplete HTTP annotations**: Только storage_service имеет полные аннотации
- **Missing tests**: Нет тестов для HTTP endpoints
- **Security gaps**: No authentication, no TLS, insecure credentials

**Lessons Learned**:
- Proto-first development экономит время
- HTTP аннотации должны быть полными до начала имплементации
- Config validation критична при старте

**Next Priority Fixes**:
1. Fix hardcoded HTTP port
2. Complete HTTP annotations для всех сервисов
3. Add HTTP request logging
4. Create integration tests

Reflection document: \`memory-bank/reflection/reflection-grpc-gateway-phase1.md\`


## POST-REFLECTION CREATIVE PHASES 🎨

Based on multi-expert review findings, the following creative phases are needed:

### 🏗️ Middleware Pipeline Architecture (FLAGGED)
- Design extensible HTTP middleware pipeline
- Request logging middleware architecture
- Authentication middleware hooks
- Rate limiting integration points
- CORS configuration strategy
- Prometheus metrics collection

### ⚙️ Error Handling Strategy (FLAGGED)
- Unified error handling across gRPC and HTTP
- Error code mapping (gRPC → HTTP status codes)
- Circuit breaker pattern for resilience
- Retry logic for transient failures
- Error response format standardization

### 🧪 Testing Framework Design (FLAGGED)
- HTTP endpoint testing architecture
- Integration test setup for dual-server
- Performance benchmarking framework
- Contract testing with OpenAPI specs
- Chaos testing approach

### 💚 Health Check Enhancement (FLAGGED)
- Separate readiness and liveness probes
- Dependency health aggregation
- Graceful degradation patterns
- Health check response format
- Service dependency mapping

**Status**: Ready for CREATIVE phase work

### 🏗️ Middleware Pipeline Architecture ✅ COMPLETE
- Design document: memory-bank/creative/creative-middleware-pipeline.md
- Decision: Unified gRPC interceptors + minimal HTTP layer
- Fixed JWT naming redundancy in proto files
- Ready for implementation


### ⚙️ Error Handling Strategy ✅ COMPLETE
- Design document: memory-bank/creative/creative-error-handling.md
- Decision: gRPC Status + Error Interceptor pattern
- Unified error handling for both protocols
- Circuit breaker pattern included


## CRITICAL FIXES COMPLETED ✅

### Configuration
- ✅ Default HTTP port changed from 80 to 8080
- ✅ HTTP port using config value (not hardcoded)

### HTTP Annotations
- ✅ AssetService - all methods annotated
- ✅ PortfolioService - all methods annotated
- ✅ PriceService - all methods annotated
- ✅ StorageService - Holdings, Users, Accounts, Transactions annotated

### Code Quality
- ✅ JWT naming fixed (JWTToken → JWT)
- ✅ error_details.proto created for structured errors
- ✅ Unified middleware approach via gRPC interceptors

### Ready for Implementation
- Middleware Pipeline: gRPC interceptors design complete
- Error Handling: Unified error interceptor pattern ready
- All proto files: HTTP annotations complete

