# Data Models - Greedy Eye

## Core Models

### Asset Management
- **Asset**: Financial instruments (cryptocurrencies, stocks, etc.)
- **Price**: Historical and current pricing data with timestamps

### Portfolio Management  
- **User**: User accounts and profiles
- **Account**: User financial accounts (exchange accounts, wallets)
- **Portfolio**: User investment portfolios
- **Holding**: Current asset positions within portfolios
- **Transaction**: Historical trades and transfers

### Rule-Based Automation
- **Rule**: Portfolio automation rules (DCA, rebalancing, stop-loss)
- **RuleExecution**: Execution tracking with detailed steps and results
- **RuleSchedule**: Timing configuration for rule execution

### Security & Authentication
- **ErrorDetails**: Structured error handling with metadata
- **APIKey**: Authentication tokens with scopes and rate limiting
- **JWT**: JSON Web Tokens for session management
- **ExternalAPIKey**: Third-party service integration keys

## Data Relationships

### User-Centric Design
```
User
├── Portfolio (1:n)
│   ├── Holding (1:n)
│   ├── Transaction (1:n)
│   └── Rule (1:n)
├── Account (1:n)
└── APIKey (1:n)
```

### Asset-Price Relationship
```
Asset (1:n) ← Price
- Supports multiple exchanges
- Historical price tracking
- Real-time price updates
```

### Rule Execution Flow
```
Rule → RuleExecution → ExecutionStep[]
- Configurable automation
- Detailed execution tracking  
- Error handling and recovery
```

## Data Storage Strategy

### PostgreSQL Schema
- **Primary Database**: PostgreSQL for ACID compliance
- **Time-Series Data**: Price data with timestamp indexing
- **JSON Configuration**: Flexible rule configuration storage
- **Audit Trail**: Complete transaction and execution history

### Performance Optimization
- **Indexing**: Strategic indexes on foreign keys and timestamps
- **Partitioning**: Time-based partitioning for price data
- **Connection Pooling**: Efficient database connection management
- **Query Optimization**: Optimized queries for frequent operations