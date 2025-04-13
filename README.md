# Greedy-Eye

![Logo](docs/logo.png)

**Greedy-Eye** is a comprehensive portfolio management system with advanced trading features, analytics, notifications, and metrics, built using Go.

## ⚠️ Status

:warning: **Under development.**

Current focus:
- Building core service implementations (Asset, Portfolio, Price, User)
- Establishing database schema and migrations
- Implementing API integrations with exchanges

## 📜 Table of Contents

- [Overview](#overview)
- [Features](#features)
- [Supported Services](#supported-services)
- [Architecture](#architecture)
- [Deployment Options](#deployment-options)
- [Quickstart](#quickstart)
- [Development](#development)
- [Roadmap](#roadmap)
- [License](#license)

## Overview

Greedy-Eye is designed to help investment enthusiasts manage their portfolios efficiently. It integrates data fetching, analysis, trading, and real-time notifications to provide a seamless experience for tracking and optimizing your crypto investments.

```mermaid
C4Context
title System Context diagram for Greedy-Eye
Person(User, "User", "Greedy-Eye user.")

System_Ext(Exchange, "Exchange")
System_Ext(PricingService, "Pricing Service")
System_Ext(Messenger, "Messenger")
System_Ext(BrokerAPI, "Broker", "API of stock broker.")
System_Ext(PortfolioManager, "Portfolio Manager", "Another portfolio manager or wallet service.")

System_Boundary(b1, "Greedy-Eye Boundary", "The boundary of Greedy-Eye.") {
  System(Eye, "Eye instance", "An instance of Greedy-Eye.")
  SystemDb(DB, "Eye DB", "Database for Greedy-Eye.")
}

BiRel(User, Messenger, "Uses")
BiRel(Messenger, Eye, "Communicates with")
BiRel(Eye, DB, "Stores configuration and data")
BiRel(Eye, Exchange, "Sync balances and trades")
BiRel(Eye, PricingService, "Fetches prices")
BiRel(Eye, BrokerAPI, "Sync stock balances and trades")
BiRel(Eye, PortfolioManager, "Sync balances and trades")

UpdateLayoutConfig($c4ShapeInRow="3", $c4BoundaryInRow="2")
```

## Features

- **Portfolio Management**:
  - [x] Basic portfolio structure and holdings
  - [x] Multiple portfolios support
  - [ ] Advanced portfolio metrics and analytics
  - [ ] Rebalancing suggestions

- **Data Integration**:
  - [x] Basic price fetching from CoinGecko
  - [ ] Balance synchronization with exchanges
  - [ ] Transaction history import

- **Notifications**:
  - [x] Basic Telegram integration
  - [ ] Price alerts
  - [ ] Portfolio performance alerts

- **Trading Automation**:
  - [ ] Automated trading strategies
  - [ ] DCA and rebalancing automation
  - [ ] Trade execution on exchanges

*Note: Checked items [x] are implemented in the current version.*

## Supported Services

- **Exchanges**:
  - Binance (in progress)
  - GateIO (planned)

- **Notification Channels**:
  - Telegram (basic implementation)

- **Price Providers**:
  - CoinGecko (implemented)

## Architecture

Greedy-Eye follows a modular monolithic architecture with microservices deployment capability. Each component is responsible for a specific set of functionalities, and the system is designed to be extensible, allowing for easy integration of new services and features.

```mermaid
C4Component
title Layered Component Architecture for Greedy-Eye

Container_Boundary(b1, "Greedy-Eye Application") {

  ComponentDb(DB, "Database", "PostgreSQL / TimescaleDB", "Stores application data, including time-series price data")

  Boundary(storage, "Storage Layer") {
    Component(StorageService, "Storage Service", "Abstracts database interactions")
  }

  Boundary(domain, "Domain Layer") {
    Component(AssetService, "Asset Management Service", "Manages asset data and operations")
    Component(PortfolioService, "Portfolio Management Service", "Manages portfolios and holdings")
    Component(PriceService, "Price Management Service", "Manages price data and operations")
    Component(UserService, "Users and Accounts Service", "Manages user accounts")
    Component(TradingService, "Trading Service", "Handles trading operations")
  }


  Boundary(interface, "Interface Layer") {
    Component(APIGateway, "API Gateway", "Handles external requests")
    Component(TerminalService, "Terminal Service", "Handles user interactions")
  }
}

Rel(StorageService, DB, "Persists and retrieves data")

Rel(AssetService, StorageService, "Uses")
Rel(PortfolioService, StorageService, "Uses")
Rel(PriceService, StorageService, "Uses")
Rel(UserService, StorageService, "Uses")
Rel(TradingService, StorageService, "Uses")

Rel(APIGateway, AssetService, "Uses")
Rel(APIGateway, PortfolioService, "Uses")
Rel(APIGateway, PriceService, "Uses")
Rel(APIGateway, UserService, "Uses")
Rel(APIGateway, TradingService, "Uses")

Rel(TerminalService, AssetService, "Uses")
Rel(TerminalService, PortfolioService, "Uses")
Rel(TerminalService, PriceService, "Uses")
Rel(TerminalService, UserService, "Uses")
Rel(TerminalService, TradingService, "Uses")

UpdateLayoutConfig($c4ShapeInRow="3", $c4BoundaryInRow="3")
```

### Key Components:

- **Asset Service**: Manages information about financial assets (cryptocurrencies, stocks, etc.).
- **Portfolio Service**: Handles portfolio management, including balances, trades, and performance metrics.
- **Price Service**: Fetches and stores price data from external providers.
- **User Service**: Manages user accounts and authentication.
- **Trading Service**: Handles trade executions and strategy implementation.
- **Terminal Service**: Provides interfaces for user interaction and notifications.

### Service Layer Separation

Greedy-Eye follows a layered architecture pattern with clear separation of concerns:

#### Domain Services
Services like `AssetService`, `PortfolioService`, and `UserService` represent the business domain and implement domain-specific logic:
- Business rules validation
- Domain workflows and operations
- Cross-entity operations
- External integrations

#### Storage Service
`StorageService` acts as an abstraction layer for persistent storage:
- Low-level data CRUD operations
- Database transaction management
- Data versioning and history tracking
- Query optimization

Domain services depend on the Storage service for data persistence needs, never directly accessing the database.

## Deployment Options

Greedy-Eye offers flexible deployment options:

1. **Monolithic Mode**: Run all services in a single binary
2. **Microservices Mode**: Run each service separately and communicate via gRPC

Configure which services to run using either:
- Configuration file with `-c [config file path]`
- Environment variables (e.g., `EYE_SERVICES="asset,portfolio,price"`)

## Quickstart

### Docker

To run Greedy-Eye using Docker, follow these steps:

Create a docker-compose.yml file:

```yaml
version: '3.8'

services:
  greedy-eye:
    image: foxcool/greedy-eye:latest
    ports:
      - "8080:80"
    environment:
      - EYE_LOGGING_LEVEL=DEBUG
      - EYE_DATABASE_URL=postgresql://greedy-eye:password@db:5432/greedy-eye
      - EYE_SERVICES="asset,portfolio,price,user"
    depends_on:
      - db
    networks:
      - greedy-eye

  db:
    image: postgres:13
    environment:
      - POSTGRES_USER=greedy-eye
      - POSTGRES_PASSWORD=password
      - POSTGRES_DB=greedy-eye
    volumes:
      - postgres_data:/var/lib/postgresql/data
    networks:
      - greedy-eye

networks:
  greedy-eye:

volumes:
  postgres_data:
```

Run the following command:

```bash
docker-compose up -d
```

Greedy-Eye will be accessible at <http://localhost:8080>.

### Manual Build

To build and run Greedy-Eye manually:

1. Clone the repository:
```bash
git clone https://github.com/foxcool/greedy-eye.git
cd greedy-eye
```

2. Generate Go code from Protocol Buffers:
```bash
make protoc
```

3. Build the project:
```bash
make build
```

4. Run the application:
```bash
./bin/eye -c configs/config.yaml
```

## Development

### Prerequisites

- Go 1.23+
- Protocol Buffers compiler
- Docker and Docker Compose (for local development)

### Development Commands

```bash
# Generate protobuf files
make protoc

# Run linting
make lint

# Run tests
make test

# Start development environment
make up
```

## Roadmap

- [x] Core architecture and service structure
- [x] Basic gRPC interfaces
- [ ] Database schema and migrations
- [ ] Basic portfolio tracking
- [ ] Binance API integration
- [ ] Telegram notifications
- [ ] Portfolio analytics
- [ ] Trading automation
- [ ] Web interface

## License

This project is licensed under the MIT License.
