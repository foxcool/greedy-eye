# Getting Started - Greedy Eye

## Overview

Greedy Eye is a comprehensive portfolio management system for cryptocurrency investments built in Go. The system uses a modular monolith architecture with gRPC services and HTTP API Gateway for external integrations.

## Prerequisites

- Go 1.24 or later
- Docker and Docker Compose
- PostgreSQL 17 (or Docker container)
- buf CLI (for Protocol Buffers)
- Make (for build automation)

## Quick Start

### 1. Clone the Repository

```bash
git clone https://github.com/foxcool/greedy-eye.git
cd greedy-eye
```

### 2. Set up Development Environment

```bash
# Install dependencies
go mod download

# Set up pre-commit hooks (optional)
pre-commit install

# Start PostgreSQL database
docker-compose up -d postgres

# Run database migrations
go run cmd/eye/main.go migrate
```

### 3. Configure Environment

Copy the example environment file and configure:

```bash
cp .env.example .env
# Edit .env with your configuration
```

Required configuration:
- Database connection string
- API keys for external services (Binance, CoinGecko)
- Telegram bot token (if using notifications)

### 4. Build and Run

```bash
# Build the application
make build

# Run with live reload (development)
make dev

# Or run directly
go run cmd/eye/main.go
```

The application will start:
- gRPC server on port 50051
- HTTP API on port 8080
- Health check at http://localhost:8080/health

## Development Workflow

### Protocol Buffers

The project uses buf for Protocol Buffer management:

```bash
# Generate gRPC code
make buf-gen

# Generate HTTP gateway code
make buf-gateway

# Generate OpenAPI documentation
make buf-docs
```

### Database Operations

```bash
# Run migrations
go run cmd/eye/main.go migrate

# Create new migration
go run cmd/eye/main.go migrate create <name>

# Check migration status
go run cmd/eye/main.go migrate status
```

### Testing

```bash
# Run all tests
make test

# Run tests with coverage
make test-coverage

# Run integration tests
make test-integration
```

### Code Quality

```bash
# Format code
make fmt

# Run linter
make lint

# Run static analysis
make vet
```

## Project Structure

```
greedy-eye/
├── api/                    # Protocol Buffer definitions
│   ├── models/            # Data models
│   └── services/          # Service definitions
├── cmd/                   # Application entrypoints
│   └── eye/              # Main application
├── internal/              # Private application code
│   ├── api/              # Generated protobuf code
│   ├── config/           # Configuration management
│   ├── database/         # Database operations
│   ├── handlers/         # HTTP handlers
│   ├── middleware/       # Middleware components
│   ├── services/         # Business logic services
│   └── utils/            # Utility functions
├── docs/                  # Documentation
├── deployments/          # Deployment configurations
├── scripts/              # Build and deployment scripts
└── tests/                # Test files
```

## Configuration

The application uses environment variables for configuration:

### Database
```env
DB_HOST=localhost
DB_PORT=5432
DB_USER=postgres
DB_PASSWORD=password
DB_NAME=greedy_eye
DB_SSL_MODE=disable
```

### Server
```env
GRPC_PORT=50051
HTTP_PORT=8080
LOG_LEVEL=info
```

### External APIs
```env
BINANCE_API_KEY=your_binance_api_key
BINANCE_SECRET_KEY=your_binance_secret_key
COINGECKO_API_KEY=your_coingecko_api_key
TELEGRAM_BOT_TOKEN=your_telegram_bot_token
```

## API Usage

### gRPC API

Connect to the gRPC server on port 50051:

```go
conn, err := grpc.Dial("localhost:50051", grpc.WithInsecure())
if err != nil {
    log.Fatal(err)
}
defer conn.Close()

client := pb.NewAssetServiceClient(conn)
```

### HTTP API

The HTTP API is available on port 8080:

```bash
# Health check
curl http://localhost:8080/health

# Get assets
curl http://localhost:8080/api/v1/assets

# Get portfolio
curl http://localhost:8080/api/v1/portfolios/{id}
```

## Development Tips

### Live Reload

Use Air for live reload during development:

```bash
# Install Air
go install github.com/cosmtrek/air@latest

# Run with live reload
air
```

### Debugging

Use Delve for debugging:

```bash
# Install Delve
go install github.com/go-delve/delve/cmd/dlv@latest

# Debug the application
dlv debug cmd/eye/main.go
```

### Database Inspection

Use the built-in database tools:

```bash
# Connect to database
make db-connect

# View database schema
make db-schema

# Reset database (development only)
make db-reset
```

## Common Issues

### Port Already in Use

If ports 50051 or 8080 are already in use:

```bash
# Kill processes using the ports
lsof -ti:50051 | xargs kill -9
lsof -ti:8080 | xargs kill -9
```

### Database Connection Issues

1. Ensure PostgreSQL is running
2. Check database credentials in .env
3. Verify database exists and migrations are applied

### Protocol Buffer Generation Issues

1. Ensure buf is installed and up to date
2. Check buf.gen.yaml configuration
3. Verify proto files syntax

## Next Steps

1. **Explore the API**: Use the generated OpenAPI documentation
2. **Add Integration**: Connect your exchange accounts
3. **Create Rules**: Set up portfolio rebalancing rules
4. **Monitor**: Use the health endpoints and metrics
5. **Extend**: Add custom handlers and middleware

## Getting Help

- Check the documentation in `docs/`
- Review example configurations in `examples/`
- Look at test files for usage patterns
- Create an issue on GitHub for bugs or feature requests