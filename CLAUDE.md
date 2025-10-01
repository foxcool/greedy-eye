# Greedy Eye - Context for Claude

## Project Overview

Comprehensive portfolio management system for cryptocurrency investments built in Go.

## Current Architecture

- Modular monolith with gRPC services + HTTP API Gateway
- PostgreSQL database with Ent ORM
- gRPC-Gateway for HTTP API auto-generation
- Dual-server architecture (gRPC:50051 + HTTP:8080)

## Key Files

- `docs/architecture.md` - Architecture overview
- `docs/development.md` - Development progress
- `cmd/eye/main.go` - Main application entry point

## Current Status

- Phase 1: gRPC-Gateway foundation ✅ COMPLETED
- Phase 2: Security + Rule Services ✅ COMPLETED
- Phase 3: Integration Services + Testing ✅ COMPLETED
- Phase 4: Business Logic Implementation 🔄 IN PROGRESS

## Services Implemented

### Core Services
- **StorageService**: Data persistence and CRUD operations
- **AssetService**: Asset management and metadata
- **PortfolioService**: Portfolio operations and calculations
- **PriceService**: Price data fetching and storage
- **UserService**: User management and basic operations

### New Services (v0.0.3-alpha)
- **RuleService**: Rule execution and portfolio automation logic

### Data Models
- **Core**: Asset, Price, User, Account, Portfolio, Holding, Transaction
- **Rules**: Rule, RuleExecution, RuleSchedule  
- **Security**: ErrorDetails, APIKey, ExternalAPIKey

## Common Tasks

- `make buf-gen` - Generate protobuf code
- `make dev` - Run with live reload
- `make test` - Run tests
- `docker-compose up -d postgres` - Start database
- `markdownlint docs/` - Check markdown files

## Quick References

- gRPC port: 50051
- HTTP port: 8080
- Health check: http://localhost:8080/health
- Database: PostgreSQL on port 5432
- OpenAPI docs: docs/openapi.yaml

## Current Sprint Focus

- External API integrations (Binance, CoinGecko)
- Business logic implementation
- Integration testing and validation

