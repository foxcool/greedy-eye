# Greedy-Eye

![Logo](docs/logo.png)

**Greedy-Eye** is a comprehensive portfolio management system with advanced trading features, analytics, notifications, and metrics, built using Go.

## ⚠️ Status

:warning: **Under development.** Features are being actively added and improvements are ongoing.

## 📜 Table of Contents

- [Overview](#overview)
- [Features](#features)
- [Supported Services](#supported-services)
- [Architecture](#architecture)
- [Quickstart](#quickstart)
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

- Portfolio Management: Track and manage your cryptocurrency holdings across multiple wallets and exchanges.
- Automated Trading: Execute trades based on predefined strategies and market conditions.
- Analytics & Metrics: Gain insights into portfolio performance with detailed analytics.
- Real-Time Notifications: Receive instant alerts on trades, portfolio changes, and important market events.
- Extensible Architecture: Easily add new services and integrations as your needs grow.

## Supported Services

- Exchanges: Binance, GateIO.
- Notification Channels: Telegram.
- Price Providers: CoinGecko.

(Note: Currently in development. More services will be supported in upcoming releases.)

## Architecture

Greedy-Eye follows a modular monolithic architecture, with each component responsible for a specific set of functionalities. The system is designed to be extensible, allowing for easy integration of new services and features.

```mermaid
    C4Component
      title Component diagram for Greedy-Eye
 
      System_Boundary(b1, "Greedy-Eye Boundary", "The boundary of Greedy-Eye.") {
         Component(PortfolioService, "Portfolio Management Service")
         Component(TradingService, "Trading Service")
         Component(DataStorage, "Data Storage")
         Component(TerminalService, "Terminal Service")
         Component(PricingService, "Pricing Service")
      }

      Rel(PortfolioService, DataStorage, "Stores portfolio data")
      Rel(TradingService, DataStorage, "Stores trade data")
      Rel(TerminalService, DataStorage, "Stores user configurations")
      Rel(PricingService, DataStorage, "Stores price data")

      Rel(PortfolioService, TradingService, "Rebalances portfolios")
      Rel(TradingService, PricingService, "Fetches prices")
      Rel(TerminalService, PortfolioService, "Interacts with user portfolios")
      Rel(TerminalService, TradingService, "Executes trades")
      Rel(TerminalService, PricingService, "Fetches prices")
      Rel(PortfolioService, TerminalService, "Sends notifications")

      

      UpdateLayoutConfig($c4ShapeInRow="2", $c4BoundaryInRow="2")
```

Key Components:

- **Portfolio Management Service**: Handles the aggregation and management of user portfolios, including balances, trades, and performance metrics.
- **Trading Service**: Manages trade executions based on predefined strategies and market conditions.
- **Data Storage**: Central repository for storing all relevant data, including user configurations, balances, trades, and analytics.
- **Terminal Service**: Provides a control interface for users to interact with the system and receive real-time notifications.
- **Pricing Service**: Fetches and stores price data from external providers for use in portfolio analysis and trading.

## Quickstart

### Docker

To run Greedy-Eye using Docker, follow these steps:

Write a docker-compose.yml file:

```yaml
version: '3.8'

services:
   greedy-eye:
      image: fox/greedy-eye:latest
      ports:
         - "8080:80"
      environment:
         - GREEDY_EYE_DB_URL=postgresql://greedy-eye:password@db:5432/greedy-eye
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
      networks:
         - greedy-eye

networks:
   greedy-eye:
```

Run the following command:

```bash
docker-compose up -d
```

Greedy-Eye will be accessible at <http://localhost:8080>.

### Manual

To run Greedy-Eye manually, follow these steps:

Clone the repository:

```bash
git clone https://github.com/foxcool/greedy-eye.git
```

Build the project:

```bash
make build
```

Run the application:

```bash
./greedy-eye
```

Greedy-Eye will be accessible at <http://localhost:8080>.

## License

This project is licensed under the MIT License. See the [LICENSE](LICENSE) file for details.
