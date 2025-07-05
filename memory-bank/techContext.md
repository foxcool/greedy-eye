# Technical Context - Greedy Eye

## Architecture Overview
**Pattern**: Modular Monolith with gRPC Services + HTTP API Gateway
**Communication**: Internal gRPC + External HTTP API
**Deployment**: Docker containers with configurable services
**Integration**: External services via HTTP API

## Technology Stack

### Core Technologies
- **Language**: Go 1.24
- **Internal API**: gRPC with Protocol Buffers
- **External API**: HTTP REST API with JSON
- **Database**: PostgreSQL 17
- **ORM**: Ent (Facebook's Go ORM)
- **Containerization**: Docker & Docker Compose
- **HTTP Router**: TBD (Gin, Echo, or Chi)

### Development Tools
- **Live Reload**: Air
- **Debugging**: Delve
- **CI/CD**: GitHub Actions
- **IDE**: Cursor with Go support
- **Version Control**: Git

### External Integrations
- **Cryptocurrency Exchange**: Binance API
- **Price Data**: CoinGecko API
- **Notifications**: Telegram Bot API
- **External Services**: HTTP API Gateway for data access
- **Monitoring**: Sentry (error tracking)

## Service Architecture

### Core Services (Internal gRPC)
1. **StorageService**: Data persistence and retrieval
2. **AssetService**: Asset management and metadata
3. **PortfolioService**: Portfolio operations and calculations
4. **PriceService**: Price data fetching and storage
5. **UserService**: User management and authentication
6. **TradingService**: Trading operations (planned)
7. **TerminalService**: User interface and notifications (planned)

### New Components
8. **HTTP API Gateway**: External HTTP API wrapper
   - RESTful endpoints for external integrations
   - Authentication and authorization
   - Rate limiting and security
   - Request/response transformation
   - Logging and monitoring

## Data Flow Architecture

### Internal Communication
- **Service-to-Service**: gRPC with Protocol Buffers
- **Database Access**: Ent ORM with PostgreSQL
- **Authentication**: Internal service authentication

### External Communication
- **External Services**: HTTP API Gateway → gRPC Services
- **Data Access**: HTTP endpoints → JSON responses
- **Authentication**: API key-based access control

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
```

### Authentication Flow
1. External service provides API key in Authorization header
2. API Gateway validates key and identifies client
3. Request forwarded to appropriate gRPC service
4. Response transformed to JSON and returned

## Security Considerations

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

## Development Patterns

### Code Organization
- **cmd/gateway/**: HTTP API Gateway entry point
- **internal/gateway/**: HTTP API Gateway implementation
- **internal/middleware/**: Authentication, logging, rate limiting
- **internal/handlers/**: HTTP request handlers
- **internal/transform/**: Request/response transformation

### API Design Principles
- **RESTful Design**: Standard HTTP methods and status codes
- **JSON Format**: Consistent JSON request/response format
- **Error Handling**: Standardized error response structure
- **Versioning**: API versioning strategy (/api/v1/)
- **Documentation**: OpenAPI/Swagger documentation

## Integration Architecture

### External Service Integration
- **Webhook Support**: HTTP endpoints for external workflows
- **Authentication**: API key-based authentication
- **Data Format**: JSON request/response
- **Error Handling**: Consistent error reporting

### Data Service Focus
- **Read Operations**: Portfolio, asset, price data access
- **Write Operations**: User preferences, portfolio updates
- **Real-time Data**: Price updates and portfolio changes
- **Historical Data**: Transaction history and analytics

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

## Future Technical Roadmap

### Phase 1: HTTP API Gateway (Current)
- Basic HTTP API Gateway implementation
- Authentication and rate limiting
- Core endpoints for external integration

### Phase 2: Enhanced Integration
- Advanced data access endpoints
- Webhook support for real-time updates
- Enhanced security features

### Phase 3: Scale and Optimize
- Performance optimization
- Advanced caching strategies
- Load balancing and scaling

## Deployment Architecture

### Container Strategy
- **API Gateway**: Separate container for HTTP API Gateway
- **gRPC Services**: Existing service containers
- **Database**: PostgreSQL container
- **Reverse Proxy**: Nginx for SSL termination (optional)

### Configuration Management
- **Environment Variables**: Service configuration
- **API Keys**: Secure key management
- **Feature Flags**: Controllable feature activation
- **Logging Configuration**: Structured logging setup
