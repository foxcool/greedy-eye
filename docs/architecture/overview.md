# Architecture Overview - Greedy Eye

## Architecture Pattern
**Pattern**: Modular Monolith with gRPC Services + HTTP API Gateway
**Communication**: Internal gRPC + External HTTP API
**Deployment**: Docker containers with configurable services
**Integration**: External services via HTTP API

## Technology Stack

### Core Technologies
- **Language**: Go 1.23+
- **Internal API**: gRPC with Protocol Buffers
- **External API**: HTTP REST API with JSON (via gRPC-Gateway)
- **Database**: PostgreSQL 13+
- **ORM**: Ent (Facebook's Go ORM)
- **Containerization**: Docker & Docker Compose
- **Build Tools**: buf (protobuf), Go modules, Make

### Development Tools
- **Live Reload**: Air
- **Debugging**: Delve
- **CI/CD**: GitHub Actions
- **IDE**: Cursor with Go support
- **Version Control**: Git

### External Integrations
- **Cryptocurrency Exchange**: Binance API (planned)
- **Price Data**: CoinGecko API (planned)
- **Notifications**: Telegram Bot API (basic implementation)
- **External Services**: HTTP API Gateway for data access
- **Authentication**: API Key and JWT-based authentication
- **Monitoring**: Sentry (error tracking, planned)

## Service Architecture

### Core Services (Internal gRPC)
1. **StorageService**: Data persistence and retrieval
2. **AssetService**: Asset management and metadata  
3. **PortfolioService**: Portfolio operations and calculations
4. **PriceService**: Price data fetching and storage
5. **UserService**: User management and basic operations
6. **AuthService**: Authentication, API keys, JWT tokens, external integrations
7. **RuleService**: Rule-based portfolio automation (DCA, rebalancing, stop-loss)

### HTTP API Gateway
8. **HTTP API Gateway**: External HTTP API wrapper
   - RESTful endpoints for external integrations
   - Authentication and authorization
   - Rate limiting and security
   - Request/response transformation via gRPC-Gateway
   - Logging and monitoring

## Data Flow Architecture

### Internal Communication
- **Service-to-Service**: gRPC with Protocol Buffers
- **Database Access**: Ent ORM with PostgreSQL
- **Authentication**: Internal service authentication

### External Communication
- **External Services**: HTTP API Gateway → gRPC Services
- **Data Access**: HTTP endpoints → JSON responses (auto-generated)
- **Authentication**: API key-based access control

## Security Architecture

### API Security
- **API Key Management**: Secure key generation and rotation
- **Rate Limiting**: Prevent API abuse and DoS attacks
- **Input Validation**: Strict request validation
- **Access Control**: Role-based endpoint access
- **Audit Logging**: Complete API access logging

### Network Security
- **Internal gRPC**: Private network communication
- **HTTP API**: Public exposure with authentication
- **TLS/HTTPS**: Encrypted communication
- **Network Segmentation**: Service isolation

## Performance Considerations

### HTTP API Gateway
- **Connection Pooling**: Efficient gRPC connection management
- **Caching**: Response caching for frequently accessed data
- **Compression**: HTTP response compression
- **Async Processing**: Non-blocking request handling

### Database Optimization
- **Connection Pooling**: PostgreSQL connection management
- **Query Optimization**: Efficient database queries
- **Indexing**: Proper database indexing
- **Caching**: Database query result caching

## Monitoring and Observability

### Logging Strategy
- **Request Logging**: All HTTP API requests
- **Error Logging**: Detailed error information
- **Performance Metrics**: Response times and throughput
- **Security Events**: Authentication failures and suspicious activity

### Metrics Collection
- **API Performance**: Response times and success rates
- **Usage Patterns**: API endpoint usage statistics
- **Error Rates**: Error frequency and patterns
- **Security Metrics**: Authentication and access patterns

## Deployment Architecture

### Container Strategy
- **API Gateway**: Dual-server (gRPC + HTTP) in single container
- **Database**: PostgreSQL container
- **Reverse Proxy**: Nginx for SSL termination (optional)

### Configuration Management
- **Environment Variables**: Service configuration
- **API Keys**: Secure key management
- **Feature Flags**: Controllable feature activation
- **Logging Configuration**: Structured logging setup