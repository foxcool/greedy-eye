# Product Roadmap - Greedy Eye

## Current Development Status

### Phase 1: Infrastructure Foundation ✅ COMPLETED
- [x] Project setup and Go modules
- [x] Docker containerization
- [x] PostgreSQL database setup
- [x] CI/CD pipeline (GitHub Actions)
- [x] Development environment with debugging

### Phase 2: Core Architecture ✅ COMPLETED
- [x] gRPC API definitions (Proto files)
- [x] Ent ORM schema definitions
- [x] Basic service structure
- [x] Database migrations
- [x] StorageService implementation

### Phase 3: HTTP API Gateway ✅ PHASE 1 COMPLETED
- [x] **gRPC-Gateway Foundation** (Week 1)
  - [x] Enhanced all existing proto files with HTTP annotations
  - [x] Created new proto files for AuthService and RuleService
  - [x] Set up buf toolchain for gRPC-Gateway generation
  - [x] Implemented dual-server (gRPC + HTTP) architecture
  - [x] Added health check endpoint
  - [x] Configured gRPC-Gateway registration

- [ ] **Security + Rule Services** (Week 2) - IN PROGRESS
  - [ ] Implement AuthService with API key + JWT hybrid
  - [ ] Create security middleware for HTTP and gRPC
  - [ ] Set up encrypted external API key storage
  - [ ] Implement rate limiting by scope
  - [ ] Implement RuleService gRPC service
  - [ ] Create rule execution engine

- [ ] **Integration Services + Testing** (Week 3) - PLANNED
  - [ ] External API integrations (Binance, Gate.io, CoinGecko)
  - [ ] Rule execution testing
  - [ ] End-to-end HTTP API validation
  - [ ] Performance testing

## Feature Roadmap

### Q1 2024: Crypto Foundation
- [x] MVP with basic crypto portfolio tracking
- [x] gRPC-Gateway HTTP API foundation
- [ ] Binance integration
- [ ] **Telegram Bot Service** - Conversational interface with voice support
- [ ] Basic arbitrage detection

### Q2 2024: Multi-Asset Expansion
- [ ] Multi-exchange crypto support
- [ ] Banking account integration
- [ ] Basic expense tracking
- [ ] Advanced analytics
- [ ] Rule-based portfolio management

### Q3 2024: Comprehensive Financial Management
- [ ] Real estate tracking
- [ ] Lending platform integration (JetLend)
- [ ] DeFi integration
- [ ] Tax optimization tools
- [ ] Advanced trading automation

### Q4 2024: Enterprise & Family Features
- [ ] Multi-user management
- [ ] Advanced reporting
- [ ] Machine learning insights
- [ ] API ecosystem for third-party integrations

### 2025: Advanced Features
- [ ] Business financial management
- [ ] Advanced compliance tools
- [ ] Community features
- [ ] Yield Interoperability Protocol

## Technical Milestones

### Current Sprint (Q1 2024)
- **Target**: Complete HTTP API Gateway with security + Telegram Bot Service
- **Progress**: Phase 1 complete, Phase 2 in progress, Telegram Bot starting
- **Key Features**: 
  - ✅ HTTP API auto-generation from proto files
  - ✅ Dual-server architecture
  - 🔄 Zero Trust authentication
  - 🔄 Rule-based portfolio management
  - 🔄 **Telegram Bot Service with Speech-to-Text integration**

### Next Sprint (Q2 2024)
- **Target**: External integrations and testing
- **Key Features**:
  - Multi-exchange support
  - Rule execution engine
  - Performance optimization
  - Comprehensive testing

## Architecture Evolution

### Current Architecture
- **Pattern**: Modular monolith with gRPC services
- **API**: Dual protocol (gRPC + HTTP via gRPC-Gateway)
- **Database**: PostgreSQL with Ent ORM
- **Build**: buf + Go modules

### Future Architecture (2025)
- **Pattern**: Microservices with service mesh
- **API**: GraphQL federation + gRPC
- **Database**: Multi-model (PostgreSQL + TimescaleDB + Redis)
- **Build**: Distributed builds with caching

## Risk Mitigation

### Technical Risks
- **API Integration Complexity**: Mitigated by adapter pattern
- **Rate Limiting**: Managed with provider-specific limits
- **Data Accuracy**: Cross-validation from multiple sources
- **Security**: Enhanced security for financial data

### Market Risks
- **Regulatory Changes**: Compliance-first design
- **Competition**: Focus on unique value propositions
- **User Adoption**: Prioritize user experience
- **Provider Relationships**: Diversified integration strategy

## Success Metrics

### Technical Metrics
- **API Performance**: < 100ms response time for cached data
- **Availability**: 99.9% uptime
- **Throughput**: 100+ concurrent requests
- **Error Rate**: < 0.1% for critical operations

### Business Metrics
- **User Retention**: 80% monthly retention
- **Portfolio Growth**: 10% average portfolio value increase
- **Feature Adoption**: 60% of users using advanced features
- **Integration Coverage**: 90% of user's financial accounts connected

## Dependencies

### External Services
- **Binance API**: Exchange integration
- **Gate.io API**: Multi-exchange support
- **CoinGecko API**: Price data
- **Telegram Bot API**: Notifications
- **Etherscan API**: On-chain data

### Internal Components
- **StorageService**: Data persistence
- **AuthService**: Authentication and authorization
- **RuleService**: Rule-based portfolio management
- **IntegrationService**: External API aggregation
- **PriceService**: Market data management
- **TelegramBotService**: Conversational interface with voice support

## Next Quarter Priorities

1. **Complete HTTP API Gateway** (Weeks 1-3)
   - Finish security implementation
   - Complete rule services
   - Comprehensive testing

2. **External Integrations** (Weeks 4-8)
   - Binance API integration
   - Gate.io support
   - CoinGecko price feeds

3. **Portfolio Management** (Weeks 9-12)
   - Rule execution engine
   - Target allocation rebalancing
   - Performance analytics

4. **Production Readiness** (Weeks 13-16)
   - Security hardening
   - Performance optimization
   - Monitoring and alerting