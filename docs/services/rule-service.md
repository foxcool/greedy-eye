# Rule Service - Greedy Eye

## Overview

**Type**: Core Business Logic Service  
**Priority**: HIGH  
**Architecture**: gRPC Service with Rule Engine + Alert System  

The Rule Service provides rule-based universal portfolio automation and alert management across all asset types. It handles business logic for multi-asset portfolio rebalancing, DCA strategies, stop-loss execution, and integrated alert system for crypto, securities, derivatives, and alternative assets.

## Current Implementation Status

### ✅ Phase 1: Architecture Foundation (COMPLETED)
1. **Proto Definitions**: ✅ RuleService gRPC interface defined
2. **Service Stubs**: ✅ All 9 gRPC methods implemented as stubs
3. **Testing**: ✅ Comprehensive tests for all methods
4. **Integration**: ✅ Service integrated into main.go with proper dependencies

### 🔄 Phase 2: Business Logic Implementation (NEXT)
1. **Rule Engine**: Implement rule execution logic
2. **Alert Integration**: Rule actions that trigger notifications
3. **Portfolio Automation**: DCA, rebalancing, stop-loss strategies
4. **Validation**: Rule configuration validation
5. **Scheduling**: Cron-based rule execution

## Architecture Design

### Service Structure
```go
type Service struct {
    log *zap.Logger  // Current stub implementation
}

// Future full implementation structure:
type RuleService struct {
    log               *zap.Logger
    storageClient     services.StorageServiceClient
    userClient        services.UserServiceClient
    portfolioClient   services.PortfolioServiceClient
    assetClient       services.AssetServiceClient
    priceClient       services.PriceServiceClient
    ruleEngine        *RuleEngine
    alertManager      *AlertManager
    scheduler         *RuleScheduler
}
```

### Core Components

#### 1. Rule Engine
- **Rule Execution**: Process different rule types across all asset classes (rebalancing, DCA, alerts)
- **Strategy Pattern**: Pluggable execution strategies for crypto, securities, and derivatives
- **State Management**: Track rule execution state and history across all platforms
- **Error Handling**: Graceful handling of execution failures across different asset types

#### 2. Alert Manager (Integrated)
- **Alert Actions**: Execute SEND_ALERT actions from rules
- **Channel Discovery**: Find user notification channels via UserService
- **Message Formatting**: Generate alert messages from templates
- **Rate Limiting**: Prevent alert spam with frequency controls

#### 3. Rule Scheduler
- **Cron Integration**: Schedule rule execution based on cron expressions
- **One-time Execution**: Support for delayed one-time rules
- **Timezone Handling**: Proper timezone support for scheduling
- **Concurrency Control**: Manage concurrent rule executions

## Functional Requirements

### 1. Rule Execution
- **ExecuteRule**: Synchronous rule execution with results
- **ExecuteRuleAsync**: Asynchronous execution with execution tracking
- **CancelRuleExecution**: Cancel running rule execution
- **Rule Types**: Support for rebalancing, DCA, stop-loss, alert rules

### 2. Rule Management
- **ValidateRule**: Validate rule configuration before saving
- **SimulateRule**: Dry-run rule execution with projected results
- **EnableRule/DisableRule**: Control rule active state
- **PauseRule/ResumeRule**: Temporary rule suspension

### 3. Alert System Integration
- **Alert Actions**: Rules can have action=SEND_ALERT
- **Channel Routing**: Find user's notification channels (telegram, email)
- **Message Templates**: Configurable alert message templates
- **Frequency Control**: Rate limiting and batching of alerts

## API Methods

### Rule Execution Operations
```protobuf
rpc ExecuteRule(ExecuteRuleRequest) returns (ExecuteRuleResponse);
rpc ExecuteRuleAsync(ExecuteRuleAsyncRequest) returns (ExecuteRuleAsyncResponse);
rpc CancelRuleExecution(CancelRuleExecutionRequest) returns (google.protobuf.Empty);
```

### Rule Validation and Simulation
```protobuf
rpc ValidateRule(ValidateRuleRequest) returns (ValidateRuleResponse);
rpc SimulateRule(SimulateRuleRequest) returns (SimulateRuleResponse);
```

### Rule Status Management
```protobuf
rpc EnableRule(EnableRuleRequest) returns (models.Rule);
rpc DisableRule(DisableRuleRequest) returns (models.Rule);
rpc PauseRule(PauseRuleRequest) returns (models.Rule);
rpc ResumeRule(ResumeRuleRequest) returns (models.Rule);
```

## Rule Types and Actions

### Supported Rule Types
1. **target_allocation**: Portfolio rebalancing to target percentages
2. **monthly_withdrawal**: Automated portfolio withdrawals
3. **stop_loss**: Portfolio protection with automatic selling
4. **dca**: Dollar-cost averaging for regular purchases
5. **price_alert**: Price monitoring with notifications
6. **portfolio_alert**: Portfolio value/change monitoring

### Rule Actions
1. **EXECUTE_TRADE**: Execute buy/sell orders
2. **REBALANCE_PORTFOLIO**: Rebalance to target allocation
3. **SEND_ALERT**: Send notification to user
4. **LOG_EVENT**: Log rule execution event
5. **WEBHOOK_CALL**: Call external webhook (future)

## Alert System Design

### Alert Flow
```
Rule triggers with action=SEND_ALERT
↓
RuleService.executeAlertAction()
↓
UserService.getUserNotificationChannels(user_id)
↓
For each channel (telegram, email):
  → TelegramBotService.sendNotification() 
  → EmailService.sendEmail() (future)
```

### Alert Configuration
```json
{
  "rule_type": "price_alert",
  "conditions": {
    "asset_symbol": "BTC",  // or "AAPL", "SBER", etc.
    "condition": "GREATER_THAN",
    "threshold_value": 50000.0
  },
  "actions": [
    {
      "type": "SEND_ALERT",
      "template": "🚨 {{.asset_symbol}} price crossed ${{.threshold_value}}: ${{.current_price}}",
      "channels": ["telegram"]
    }
  ]
}
```

## Data Models

### Rule Configuration
Rules use flexible `google.protobuf.Struct` configuration for different rule types:

```protobuf
message Rule {
  string id = 1;
  string rule_type = 4;            // "price_alert", "target_allocation", etc.
  google.protobuf.Struct configuration = 8;  // Rule-specific config
  RuleSchedule schedule = 9;       // When to execute
  RuleStatus status = 7;           // ACTIVE, PAUSED, DISABLED
}
```

### Rule Execution Tracking
```protobuf
message RuleExecution {
  string id = 1;
  string rule_id = 2;
  ExecutionStatus status = 7;      // PENDING, IN_PROGRESS, COMPLETED, FAILED
  repeated string created_transaction_ids = 9;
  google.protobuf.Struct execution_summary = 12;
}
```

## Security and Performance

### Security Considerations
- **Input Validation**: Strict validation of rule configurations
- **User Authorization**: Verify user owns rules being executed
- **Rate Limiting**: Prevent rule execution abuse
- **Audit Logging**: Log all rule executions and changes

### Performance Requirements
- **Rule Execution**: < 5s for simple rules, < 30s for complex portfolio operations
- **Alert Processing**: < 1s for alert rule evaluation
- **Concurrent Rules**: Support 50+ concurrent rule executions
- **Schedule Accuracy**: ±1 minute accuracy for scheduled executions

### Error Handling
- **Graceful Degradation**: Continue with partial rule execution on non-critical errors
- **Retry Logic**: Automatic retry for transient failures
- **Dead Letter Queue**: Queue failed rules for manual review
- **Rollback Support**: Undo capabilities for failed executions

## Dependencies

### Internal Services
- **StorageService**: Rule and execution persistence
- **UserService**: User validation and notification channel discovery
- **PortfolioService**: Portfolio calculations and modifications
- **AssetService**: Asset information and validation
- **PriceService**: Current price data for rule evaluation

### External Integrations
- **Exchange APIs**: Crypto trade execution (Binance, etc.) - implemented
- **Brokerage APIs**: Securities trade execution (T-Bank Invest) - planned
- **Scheduling System**: Cron-based rule execution - planned
- **Notification Services**: Alert delivery via multiple channels - implemented

## Implementation Plan

### Phase 1: Core Rule Engine (Week 1-2)
1. **Rule Engine**: Basic rule execution framework
2. **Simple Rules**: Implement price alert and basic portfolio rules
3. **Alert Integration**: Connect rules to TelegramBotService
4. **Validation**: Rule configuration validation logic

### Phase 2: Advanced Features (Week 3-4)  
1. **Complex Rules**: Target allocation and DCA implementations
2. **Scheduling**: Cron-based rule execution
3. **Simulation**: Dry-run capabilities with cost estimation
4. **Management**: Enable/disable/pause functionality

### Phase 3: Production Features (Week 5-6)
1. **Performance**: Optimization for concurrent execution
2. **Monitoring**: Rule execution metrics and alerting
3. **Error Handling**: Comprehensive error recovery
4. **Testing**: Load testing and integration validation

## Success Criteria

### Technical Success
- [ ] All rule types functional with proper execution
- [ ] Alert system integrated with notification channels
- [ ] Rule scheduling working with cron expressions
- [ ] Comprehensive error handling and recovery
- [ ] Performance targets met (5s simple rules, 30s complex)

### Business Logic Success
- [ ] Portfolio rebalancing rules functional
- [ ] Price alert rules working end-to-end
- [ ] DCA rules executing scheduled purchases
- [ ] Stop-loss rules providing portfolio protection
- [ ] Alert rate limiting preventing spam

### Integration Success
- [ ] Seamless integration with all dependent services
- [ ] Proper user authorization and validation
- [ ] Alert delivery through TelegramBotService
- [ ] Rule execution history and audit logging
- [ ] Production-ready monitoring and metrics