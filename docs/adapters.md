# Adapters Architecture

## Overview

Greedy Eye uses the **Adapter Pattern** to integrate with external services and platforms. This approach provides:

- **Abstraction**: Clean interfaces between core business logic and external integrations
- **Testability**: Easy mocking and testing without external dependencies
- **Flexibility**: Simple to swap implementations or add new providers
- **Maintainability**: Changes to external APIs don't affect core logic

## Adapter Categories

### 1. Messenger Adapters (`internal/adapters/messengers/`)

Integrate with messaging platforms for notifications and conversational interfaces.

#### Telegram Adapter (`messengers/telegram/`)

**Purpose**: Telegram Bot API integration for user notifications and bot interactions

**Key Methods**:
- `SendMessage()` - Send text messages
- `SendMessageWithKeyboard()` - Send messages with inline keyboards
- `SendPhoto()`, `SendDocument()` - Media sharing
- `EditMessage()`, `DeleteMessage()` - Message management
- `SetWebhook()` - Configure webhook for updates
- `GetMe()` - Bot information

**Configuration**:
```go
client := telegram.NewClient("bot_token")
```

**Status**: ⚠️ Stub implementation (Unimplemented)

**Future Adapters**: WhatsApp, Discord, Slack

---

### 2. Blockchain Adapters (`internal/adapters/blockchain/`)

Interface with blockchain networks for wallet tracking and on-chain data.

#### Moralis Adapter (`blockchain/moralis/`)

**Purpose**: Multi-chain blockchain data provider (Ethereum, BSC, Polygon, etc.)

**Key Methods**:
- `GetWalletBalance()` - Native token balance
- `GetWalletTokenBalances()` - ERC20/BEP20 token balances
- `GetWalletNFTs()` - NFT holdings
- `GetTransactionHistory()` - Transaction data
- `GetTokenPrice()` - Real-time token pricing
- `ValidateAddress()` - Address validation

**Configuration**:
```go
client := moralis.NewClient(moralis.Config{
    APIKey: "your_api_key",
})
```

**Supported Chains**: Ethereum, BSC, Polygon, Avalanche, Fantom, Cronos

**Status**: ⚠️ Stub implementation (Unimplemented)

**Future Adapters**: Etherscan, Blockcypher, Alchemy

---

### 3. Exchange Adapters (`internal/adapters/exchanges/`)

Connect to cryptocurrency exchanges for trading and balance tracking.

#### Binance Adapter (`exchanges/binance/`)

**Purpose**: Binance exchange API integration (spot, futures, margin)

**Key Methods**:
- `GetAccountBalances()` - All account balances
- `GetAssetBalance()` - Specific asset balance
- `PlaceOrder()` - Create new order
- `CancelOrder()` - Cancel existing order
- `GetOpenOrders()` - Active orders
- `GetOrderHistory()` - Historical orders
- `GetTradeHistory()` - Completed trades
- `GetSymbolPrice()` - Current market price
- `ValidateAccount()` - Verify credentials

**Configuration**:
```go
client := binance.NewClient(binance.Config{
    APIKey:    "your_api_key",
    APISecret: "your_api_secret",
    Sandbox:   false, // Use testnet if true
})
```

**Account Types Supported**:
- Spot trading
- Futures trading
- Margin trading
- Savings accounts

**Status**: ⚠️ Stub implementation (Unimplemented)

**Future Adapters**: Gate.io, Bybit, OKX, Coinbase

---

### 4. Price Data Adapters (`internal/adapters/pricedata/`)

Fetch market data and pricing information.

#### CoinGecko Adapter (`pricedata/coingecko/`)

**Purpose**: Comprehensive cryptocurrency market data aggregator

**Key Methods**:
- `GetCurrentPrice()` - Current asset price
- `GetMultiplePrices()` - Batch price fetching
- `GetHistoricalPrices()` - Historical price data
- `GetMarketChart()` - Price, volume, market cap charts
- `SearchAssets()` - Asset search by name/symbol
- `GetAssetDetails()` - Detailed asset metadata
- `GetSupportedCurrencies()` - List of vs currencies
- `Ping()` - API health check

**Configuration**:
```go
client := coingecko.NewClient(coingecko.Config{
    APIKey: "your_api_key", // Optional for free tier
    Pro:    false,          // Use Pro API if true
})
```

**Rate Limits**:
- Free tier: 10-30 calls/minute
- Pro tier: Higher limits with API key

**Status**: ⚠️ Stub implementation (Unimplemented)

**Note**: Legacy implementation exists at `internal/services/coingecko/` (will be migrated to adapter)

**Future Adapters**: CoinMarketCap, Cryptocompare, Messari

---

## Architecture Patterns

### Interface-Based Design

All adapters implement well-defined interfaces:

```go
// Example: Messenger Client Interface
type MessengerClient interface {
    SendMessage(ctx context.Context, chatID string, message string, options map[string]interface{}) error
    SendMessageWithKeyboard(ctx context.Context, chatID string, message string, keyboard interface{}) error
    // ... other methods
}

// Example: Exchange Client Interface
type ExchangeClient interface {
    GetAccountBalances(ctx context.Context, accountID string) ([]Balance, error)
    PlaceOrder(ctx context.Context, accountID string, order *Order) (*Order, error)
    // ... other methods
}
```

### Error Handling

Adapters use gRPC status codes for consistent error handling:

```go
return status.Error(codes.Unimplemented, "method not implemented")
return status.Error(codes.InvalidArgument, "invalid asset ID")
return status.Error(codes.Unavailable, "API temporarily unavailable")
```

### Configuration Management

Adapter configuration is stored in:
- **System-wide API keys**: `ExternalAPIKey` model with `is_system_key = true`
- **User-specific API keys**: `ExternalAPIKey` model linked to user account
- **Account metadata**: `Account` model with `external_api_key_id` reference

See `api/models/api_key.proto` and `api/models/account.proto` for details.

---

## Testing Strategy

### Stub Tests

All adapters include comprehensive stub tests that verify:
- Method signatures are correct
- Unimplemented errors are returned properly
- Configuration is initialized correctly

Example:
```go
func TestBinanceClient_GetAccountBalances(t *testing.T) {
    client := NewClient(Config{
        APIKey:    "test-api-key",
        APISecret: "test-api-secret",
        Sandbox:   true,
    })

    t.Run("should return unimplemented error", func(t *testing.T) {
        balances, err := client.GetAccountBalances(context.Background(), "test-account")

        assert.Nil(t, balances)
        assert.Error(t, err)
        assert.Equal(t, codes.Unimplemented, status.Code(err))
    })
}
```

### Integration Tests

Future implementation will include:
- **Mock servers**: Test against fake API responses
- **Sandbox environments**: Use testnet/sandbox APIs for real integration testing
- **Contract tests**: Verify adapter behavior matches external API contracts

---

## Implementation Roadmap

### Phase 1: Foundation ✅ (Completed)

- [x] Design adapter interfaces
- [x] Create directory structure
- [x] Implement stub methods with unimplemented errors
- [x] Write comprehensive stub tests
- [x] Verify all tests pass

### Phase 2: Core Implementations (Planned)

**Priority 1 - Essential for MVP**:
- [ ] CoinGecko adapter (price data)
- [ ] Telegram adapter (notifications)

**Priority 2 - Exchange Integration**:
- [ ] Binance adapter (trading and balances)

**Priority 3 - Blockchain Support**:
- [ ] Moralis adapter (wallet tracking)

### Phase 3: Advanced Features (Future)

- [ ] Rate limiting and retry logic
- [ ] Response caching
- [ ] Circuit breakers for fault tolerance
- [ ] Metrics and monitoring
- [ ] Webhook handlers
- [ ] WebSocket support for real-time data

### Phase 4: Additional Providers (Future)

- [ ] Gate.io exchange adapter
- [ ] Etherscan blockchain adapter
- [ ] WhatsApp messenger adapter
- [ ] CoinMarketCap price data adapter

---

## Usage Examples

### Fetching Price Data

```go
// Initialize CoinGecko adapter
cgClient := coingecko.NewClient(coingecko.Config{
    APIKey: systemAPIKey,
    Pro:    false,
})

// Get current Bitcoin price
priceData, err := cgClient.GetCurrentPrice(ctx, "bitcoin", "usd")
if err != nil {
    return err
}

fmt.Printf("Bitcoin price: $%.2f\n", priceData.Price)
```

### Checking Exchange Balance

```go
// Initialize Binance adapter
binanceClient := binance.NewClient(binance.Config{
    APIKey:    userAPIKey,
    APISecret: userAPISecret,
    Sandbox:   false,
})

// Get account balances
balances, err := binanceClient.GetAccountBalances(ctx, accountID)
if err != nil {
    return err
}

for _, balance := range balances {
    fmt.Printf("%s: %.8f (locked: %.8f)\n",
        balance.Asset, balance.Free, balance.Locked)
}
```

### Tracking Wallet Holdings

```go
// Initialize Moralis adapter
moralisClient := moralis.NewClient(moralis.Config{
    APIKey: systemAPIKey,
})

// Get all token balances for wallet
balances, err := moralisClient.GetWalletTokenBalances(ctx, "eth", walletAddress)
if err != nil {
    return err
}

for _, token := range balances {
    fmt.Printf("%s (%s): %s\n", token.Name, token.Symbol, token.Balance)
}
```

### Sending Telegram Notification

```go
// Initialize Telegram adapter
tgClient := telegram.NewClient(botToken)

// Send price alert
err := tgClient.SendMessage(ctx, userChatID,
    "🚨 Price Alert: Bitcoin reached $50,000!",
    map[string]interface{}{
        "parse_mode": "Markdown",
    })
if err != nil {
    return err
}
```

---

## Security Considerations

### API Key Management

1. **Encryption**: All API keys are encrypted at rest using `encrypted_api_key`, `encrypted_api_secret` fields
2. **System Keys**: Admin-configured keys (`is_system_key = true`) shared across users with permission control
3. **User Keys**: User-specific keys linked to individual accounts
4. **Key Rotation**: Support for key expiration and rotation (`expires_at`, `status` fields)

### Access Control

- **Permissions**: Fine-grained permissions per key (`permissions` array)
- **Whitelisting**: System keys can be restricted to specific users (`allowed_user_ids`)
- **Audit Trail**: Track key usage (`last_used_at`, `usage_count`)

### Best Practices

1. Never log API keys or secrets
2. Use sandbox/testnet for development
3. Implement rate limiting to prevent abuse
4. Validate all input parameters
5. Handle API errors gracefully
6. Use HTTPS for all external API calls
7. Rotate keys regularly

---

## Related Documentation

- [Architecture Overview](architecture.md)
- [Development Guide](development.md)
- [API Models - ExternalAPIKey](../api/models/api_key.proto)
- [API Models - Account](../api/models/account.proto)
- [MessengerService Proto](../api/services/messenger_service.proto)

---

## Contributing

When adding a new adapter:

1. Create directory: `internal/adapters/{category}/{provider}/`
2. Implement client: `client.go` with interface methods
3. Add configuration: Config struct with API credentials
4. Write tests: `client_test.go` with comprehensive coverage
5. Document: Add section to this file with usage examples
6. Update: `architecture.md` and `development.md` as needed

**Test Coverage Requirements**: Minimum 50% for stubs, 80% for full implementations
