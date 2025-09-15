# Development Guide - Greedy Eye

Complete guide for developing and maintaining the Greedy Eye universal portfolio management system supporting diverse asset types including cryptocurrencies, securities, derivatives, and alternative assets.

## Quick Start

### Prerequisites
- Go 1.23+
- Docker and Docker Compose
- PostgreSQL (or Docker container)
- buf CLI (for Protocol Buffers)
- Make (for build automation)

### Setup
```bash
git clone https://github.com/foxcool/greedy-eye.git
cd greedy-eye

# Install dependencies
go mod download

# Start database
docker-compose up -d postgres

# Generate protobuf code
make buf-gen

# Run with live reload
make dev
```

The application starts:
- gRPC server on port 50051
- HTTP API on port 8080
- Health check at http://localhost:8080/health

## Current Development Status

### Implementation Progress
- **Phase 1**: Infrastructure Foundation ✅ **COMPLETED**
- **Phase 2**: Core Architecture ✅ **COMPLETED**  
- **Phase 3**: Services & API Gateway ✅ **STUB PHASE COMPLETED**
- **Current**: Business Logic Implementation 🔄

### Service Status
| Service | Status | Implementation | Tests | Integration |
|---------|--------|---------------|-------|-------------|
| StorageService | ✅ Complete | Full Ent ORM | ✅ | ✅ |
| UserService | ✅ Stubs | API complete | ✅ | ✅ |
| AssetService | ✅ Stubs | API complete | ✅ | ✅ |
| PortfolioService | ✅ Stubs | API complete | ✅ | ✅ |
| PriceService | ✅ Stubs | API complete | ✅ | ✅ |
| RuleService | ✅ Stubs | API complete | ✅ | ✅ |
| TelegramBotService | ✅ Stubs | Full architecture | ✅ | ✅ |
| AuthService | ✅ Proto | Proto only | ❌ | ❌ |

### Recent Achievements
- ✅ Complete service architecture with dependency management
- ✅ TelegramBotService with voice support (STT/TTS) architecture
- ✅ RuleService with integrated alert system
- ✅ HTTP API Gateway with gRPC-Gateway auto-generation
- ✅ Comprehensive test coverage (>90%) for all implemented services
- ✅ Clean architecture without over-engineering (YAGNI applied)

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
# Run migrations
go run cmd/eye/main.go migrate

# Reset database (dev only)
make db-reset

# Connect to database
make db-connect
```

### Testing
```bash
# Run all tests
make test

# Run with coverage
make test-coverage

# Run specific service tests
go test ./internal/services/telegram/...
```

### Code Quality
```bash
# Format code
make fmt

# Run linter
make lint

# Static analysis
make vet
```

## Project Structure

```
greedy-eye/
├── api/                    # Protocol Buffer definitions
│   ├── models/            # Data models (user.proto, asset.proto, etc.)
│   └── services/          # Service definitions (user_service.proto, etc.)
├── cmd/eye/               # Main application entry point
├── internal/
│   ├── api/              # Generated protobuf code
│   ├── services/         # Business logic services
│   │   ├── storage/      # StorageService (full implementation)
│   │   ├── user/         # UserService (stubs)
│   │   ├── asset/        # AssetService (stubs)
│   │   ├── portfolio/    # PortfolioService (stubs)
│   │   ├── price/        # PriceService (stubs)
│   │   ├── rule/         # RuleService (stubs)
│   │   └── telegram/     # TelegramBotService (stubs + architecture)
│   └── adapters/         # External service adapters
├── docs/                 # Documentation (simplified structure)
└── deploy/              # Docker and deployment configs
```

## Configuration

### Environment Variables
```env
# Database
DB_URL=postgres://user:pass@localhost:5432/greedy_eye?sslmode=disable

# Server
GRPC_PORT=50051
HTTP_PORT=8080
LOG_LEVEL=info

# External APIs
BINANCE_API_KEY=your_key
COINGECKO_API_KEY=your_key
TBANK_INVEST_TOKEN=your_token
TELEGRAM_BOT_TOKEN=your_token

# Speech Services (for TelegramBotService)
OPENAI_API_KEY=your_key
GOOGLE_CREDENTIALS_PATH=./credentials.json
YANDEX_API_KEY=your_key
```

## Service Architecture

### Dependency Graph
```
StorageService (base)
├── UserService
├── AssetService  
├── PriceService → AssetService
├── PortfolioService → AssetService, StorageService
├── RuleService → UserService, PortfolioService, AssetService, PriceService
└── TelegramBotService → All services
```

### Service Communication
- **Internal**: gRPC with Protocol Buffers
- **External**: HTTP API via gRPC-Gateway
- **Database**: PostgreSQL with Ent ORM
- **External APIs**: HTTP clients (future implementation)

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
// Use structured logging with zap
s.log.Info("method called", 
    zap.String("user_id", req.UserId),
    zap.String("operation", "create_portfolio"))

s.log.Error("operation failed",
    zap.Error(err),
    zap.String("context", "database_query"))
```

### Testing Pattern
```go
func TestService_Method(t *testing.T) {
    log := zaptest.NewLogger(t)
    service := NewService(log)
    ctx := context.Background()

    req := &services.MethodRequest{
        Field: "test_value",
    }

    resp, err := service.Method(ctx, req)

    // For stubs: expect Unimplemented
    assert.Nil(t, resp)
    assert.Error(t, err)
    
    st, ok := status.FromError(err)
    require.True(t, ok)
    assert.Equal(t, codes.Unimplemented, st.Code())
}
```

## Next Development Steps

### Immediate (Current Sprint)
1. **Business Logic Implementation**
   - Start with UserService (user management)
   - Implement AssetService (universal asset metadata - crypto, securities, derivatives)
   - Add PriceService (CoinGecko integration for crypto, T-Bank Invest for securities)

2. **External Integrations**
   - CoinGecko API for cryptocurrency price data
   - T-Bank Invest API for Russian securities market (stocks, bonds, ETFs)
   - Basic Telegram Bot API integration

### Medium Term
1. **Universal Portfolio Logic**
   - Multi-asset portfolio calculations and analytics (crypto + securities)
   - Rule execution engine for all asset types
   - Alert system implementation across all platforms

2. **Production Readiness**
   - Security hardening
   - Performance optimization
   - Monitoring and metrics

### Architecture Evolution
- **Current**: Stubs with complete APIs ✅
- **Next**: Business logic implementation 🔄
- **Future**: External integrations and optimization

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
LOG_LEVEL=debug go run cmd/eye/main.go

# Use delve debugger
dlv debug cmd/eye/main.go

# Check service health
curl http://localhost:8080/health

# Test gRPC methods with grpcurl
grpcurl -plaintext localhost:50051 list
grpcurl -plaintext localhost:50051 services.StorageService/CreateUser
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

### Build Issues
```bash
# Clean build cache
go clean -cache -modcache

# Regenerate everything
make clean && make buf-gen && make build

# Check dependencies
go mod why -m module_name
```

This guide covers everything needed for effective development on the Greedy Eye project. The focus is on maintaining clean architecture while implementing business logic incrementally.