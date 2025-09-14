# Telegram Bot Service - Greedy Eye

## Overview

**Type**: Level 2 (Core Feature)  
**Priority**: HIGH  
**Estimated Duration**: 4-5 weeks  
**Architecture**: gRPC Service + Telegram Bot API + Speech Integration

The Telegram Bot Service provides users with a conversational interface to interact with their Greedy Eye portfolio management system. Users can check balances, view performance, execute trades, and manage their financial data through natural language commands via Telegram.

## Architecture Decisions

### Core Technologies

1. **Telegram Bot API** - Primary communication channel with users
2. **gRPC Service Integration** - Seamless integration with existing services
3. **Speech-to-Text/Text-to-Speech** - Voice message support with multiple providers
4. **Session Management** - Stateful conversations with context preservation
5. **Multi-language Support** - Russian and English command processing

### Technology Stack

- **Bot Framework**: gopkg.in/tucnak/telebot.v2 (already in project)
- **Speech Services**: Google Cloud Speech, OpenAI Whisper, Yandex SpeechKit
- **Session Storage**: In-memory with Redis fallback option
- **Message Processing**: Natural Language Processing for command parsing
- **Integration**: Full gRPC client integration with existing services

## Functional Requirements

### 1. Inline Commands Support
- `/start` - User registration and onboarding
- `/portfolio` - Portfolio overview with current values
- `/balance` - Current balances across all accounts
- `/prices` - Current market prices for tracked assets
- `/performance` - Portfolio performance analytics
- `/transactions` - Recent transaction history
- `/alerts` - Manage price and portfolio alerts
- `/settings` - User preferences and configurations
- `/help` - Complete command reference

### 2. Text Message Processing
- **Natural Language Queries**: "How much BTC do I have?", "Show me ETH price"
- **Context-Aware Responses**: Multi-turn conversations with state management
- **Command Parsing**: Intelligent parsing of user intents from free text
- **Error Handling**: Graceful handling of unclear or invalid requests

### 3. Voice Message Support
- **Speech-to-Text**: Convert voice messages to text commands
- **Multiple Providers**: Google, OpenAI Whisper, Yandex with fallback
- **Language Detection**: Automatic Russian/English detection
- **Voice Responses**: Optional Text-to-Speech for responses

### 4. Data Integration
- **User Context**: Seamless mapping between Telegram ID and system User ID
- **Portfolio Data**: Real-time access to user portfolios and holdings
- **Price Data**: Live market data integration
- **Transaction History**: Complete transaction tracking and reporting
- **Account Management**: Multi-account and multi-exchange support

## Technical Architecture

### Service Structure
```go
type TelegramBotService struct {
    log               *zap.Logger
    bot               *telebot.Bot
    storageClient     services.StorageServiceClient
    userClient        services.UserServiceClient
    portfolioClient   services.PortfolioServiceClient
    assetClient       services.AssetServiceClient
    priceClient       services.PriceServiceClient
    authClient        services.AuthServiceClient
    sessionManager    *SessionManager
    speechProvider    SpeechProvider
    commandHandler    *CommandHandler
    messageHandler    *MessageHandler
}
```

### Component Architecture

#### 1. Command Handler
- **Inline Commands**: Fast execution of predefined commands
- **Permission Validation**: User authentication and authorization
- **Rate Limiting**: Prevent spam and abuse
- **Response Formatting**: Rich text formatting with charts and tables

#### 2. Message Handler
- **Intent Recognition**: Parse user intentions from natural language
- **Context Management**: Maintain conversation state across messages
- **Entity Extraction**: Extract asset names, amounts, dates from text
- **Fallback Handling**: Handle unclear requests gracefully

#### 3. Voice Handler
- **Audio Processing**: Download and format voice messages for STT
- **Provider Selection**: Choose optimal STT provider based on language/quality
- **Error Recovery**: Handle STT failures with user-friendly messages
- **Response Generation**: Optional TTS for voice responses

#### 4. Session Manager
- **User Context**: Track user state, preferences, and current operations
- **Conversation Flow**: Manage multi-step operations like trade execution
- **Timeout Handling**: Clean up inactive sessions
- **Persistence**: Optional Redis backing for session data

### Speech-to-Text Integration

#### Provider Architecture
```go
type SpeechProvider interface {
    SpeechToText(audioData []byte, language string) (string, error)
    TextToSpeech(text string, language string) ([]byte, error)
    GetSupportedLanguages() []string
}
```

#### Supported Providers

**1. Google Cloud Speech API**
- **Advantages**: Excellent Russian support, real-time processing
- **Use Case**: Primary provider for Russian users
- **Cost**: $0.006 per 15 seconds (STT), $4.00 per 1M chars (TTS)

**2. OpenAI Whisper API**
- **Advantages**: Best multilingual support, high accuracy
- **Use Case**: Fallback provider, mixed language detection
- **Cost**: $0.006 per minute (STT), $0.015 per 1K chars (TTS)

**3. Yandex SpeechKit**
- **Advantages**: Native Russian support, low latency
- **Use Case**: Russian-first users, cost optimization
- **Cost**: ~$0.15 per hour (STT), $0.40 per 1M chars (TTS)

#### Provider Selection Logic
```go
func (s *SpeechProvider) SelectProvider(userLang string, audioLength time.Duration) Provider {
    switch {
    case userLang == "ru" && audioLength < 30*time.Second:
        return s.yandexProvider
    case audioLength > 120*time.Second:
        return s.googleProvider  // Better for long audio
    default:
        return s.openaiProvider  // Best general purpose
    }
}
```

## Data Models

### Telegram User Model
```protobuf
message TelegramUser {
    string telegram_id = 1;
    string user_id = 2;  // Link to system User
    string username = 3;
    string first_name = 4;
    string last_name = 5;
    string language_code = 6;
    bool notifications_enabled = 7;
    repeated string subscribed_alerts = 8;
    google.protobuf.Timestamp created_at = 9;
    google.protobuf.Timestamp last_active = 10;
}
```

### Session Context
```protobuf
message SessionContext {
    string telegram_id = 1;
    string current_operation = 2;  // "trade", "portfolio_view", etc.
    map<string, string> context_data = 3;
    google.protobuf.Timestamp expires_at = 4;
    repeated string conversation_history = 5;
}
```

## API Endpoints

### gRPC Service Methods
```protobuf
service TelegramBotService {
    // Webhook endpoint for Telegram updates
    rpc ProcessTelegramUpdate(TelegramUpdateRequest) returns (TelegramUpdateResponse);
    
    // Send notification to user
    rpc SendNotification(SendNotificationRequest) returns (SendNotificationResponse);
    
    // Manage user subscriptions
    rpc ManageSubscriptions(ManageSubscriptionsRequest) returns (ManageSubscriptionsResponse);
    
    // Get user session info
    rpc GetUserSession(GetUserSessionRequest) returns (GetUserSessionResponse);
}
```

### HTTP Webhook Endpoints
- `POST /api/v1/telegram/webhook` - Telegram webhook for message processing
- `GET /api/v1/telegram/health` - Bot health check
- `POST /api/v1/telegram/notify` - Send notifications to users

## Security Implementation

### Authentication Flow
1. **User Registration**: Link Telegram ID with system User ID
2. **Session Validation**: Verify user permissions for each request  
3. **Rate Limiting**: Prevent spam (max 10 commands/minute per user)
4. **Data Encryption**: Encrypt sensitive data in session storage
5. **Audit Logging**: Log all user interactions for security monitoring

### Privacy Considerations
- **Data Minimization**: Store only necessary user data
- **Message Encryption**: E2E encryption for sensitive operations
- **Opt-out Support**: Easy unsubscribe from notifications
- **GDPR Compliance**: User data deletion and export support

## Implementation Plan

### Phase 1: Core Infrastructure (Week 1-2)
1. **Proto Definitions**: Define TelegramBotService gRPC interface
2. **Basic Service**: Implement service skeleton with logging
3. **Telegram Integration**: Connect to Telegram Bot API with basic commands
4. **User Mapping**: Create Telegram ID ↔ User ID mapping system
5. **Simple Commands**: Implement `/start`, `/help`, `/portfolio` commands

### Phase 2: Advanced Features (Week 3-4)
1. **Speech Integration**: Implement STT/TTS with provider selection
2. **Message Processing**: Add NLP for text message understanding
3. **Session Management**: Implement stateful conversations
4. **Rich Responses**: Add formatted responses with charts and tables
5. **All Commands**: Complete implementation of all inline commands

### Phase 3: Production Features (Week 5)
1. **Notifications**: Push notifications for price alerts and portfolio changes
2. **Performance Optimization**: Caching, connection pooling, response optimization
3. **Monitoring**: Metrics, alerting, and health checks
4. **Testing**: Comprehensive integration and load testing
5. **Documentation**: Complete API documentation and user guides

## Performance Requirements

### Response Times
- **Simple Commands**: < 500ms (portfolio, balance)
- **Complex Queries**: < 2s (performance analytics)
- **Speech Processing**: < 3s (including STT conversion)
- **Voice Response**: < 5s (including TTS generation)

### Throughput
- **Concurrent Users**: 100+ simultaneous conversations
- **Message Rate**: 1000+ messages/minute
- **Voice Processing**: 50+ voice messages/minute
- **Notification Delivery**: 10000+ notifications/hour

### Reliability
- **Uptime**: 99.9% availability
- **Error Rate**: < 0.1% for critical operations
- **Recovery Time**: < 5 minutes for service restart
- **Data Consistency**: 100% message delivery guarantee

## Monitoring and Analytics

### Key Metrics
- **User Engagement**: DAU, command usage frequency
- **Response Quality**: User satisfaction, error rates
- **Performance**: Response times, throughput, error rates
- **Speech Quality**: STT accuracy, TTS usage rates

### Alerting
- **Service Health**: Downtime, high error rates
- **Performance Degradation**: Slow responses, queue buildup
- **Security Events**: Rate limit violations, suspicious activity
- **External Dependencies**: Telegram API issues, STT provider failures

## Configuration

### Environment Variables
```bash
# Telegram Configuration
TELEGRAM_BOT_TOKEN=your_bot_token_here
TELEGRAM_WEBHOOK_URL=https://your-domain.com/api/v1/telegram/webhook
TELEGRAM_WEBHOOK_SECRET=your_webhook_secret

# Speech Configuration  
SPEECH_PROVIDER=openai  # google, openai, yandex
SPEECH_CACHE_TTL=24h
OPENAI_API_KEY=your_openai_key
GOOGLE_CREDENTIALS_PATH=/path/to/google-credentials.json
YANDEX_API_KEY=your_yandex_key

# Session Configuration
SESSION_TIMEOUT=30m
SESSION_STORAGE=memory  # memory, redis
REDIS_URL=redis://localhost:6379

# Rate Limiting
RATE_LIMIT_COMMANDS=10/minute
RATE_LIMIT_MESSAGES=30/minute
RATE_LIMIT_VOICE=5/minute
```

## Dependencies

### External Services
- **Telegram Bot API**: Core messaging functionality
- **Google Cloud Speech**: STT/TTS provider
- **OpenAI API**: Whisper STT and TTS
- **Yandex SpeechKit**: Russian-optimized STT/TTS
- **Redis** (optional): Session persistence

### Internal Services
- **StorageService**: User and portfolio data
- **UserService**: User management and preferences
- **PortfolioService**: Portfolio calculations and analytics
- **AssetService**: Asset information and metadata
- **PriceService**: Market data and price history
- **AuthService**: Authentication and authorization

## Risk Mitigation

### Technical Risks
- **Telegram API Limits**: Rate limiting, fallback messaging
- **Speech Provider Outages**: Multi-provider fallback system
- **High Latency**: Caching, async processing, user feedback
- **Storage Failures**: Graceful degradation, session recovery

### Business Risks
- **User Privacy**: End-to-end encryption, data minimization
- **Spam Prevention**: Rate limiting, user validation, reporting
- **Compliance**: GDPR compliance, audit logging, data retention
- **Cost Control**: Usage monitoring, provider cost optimization

## Success Criteria

### Technical Success
- [ ] All inline commands functional with <500ms response time
- [ ] Speech-to-text working with 95%+ accuracy for Russian/English
- [ ] 99.9% uptime with graceful error handling
- [ ] Complete integration with all existing gRPC services
- [ ] Comprehensive test coverage (>90%)

### User Experience Success  
- [ ] Intuitive command interface requiring minimal learning
- [ ] Natural language processing for common queries
- [ ] Rich response formatting with charts and tables
- [ ] Reliable voice message processing
- [ ] Proactive notifications for important portfolio events

### Business Success
- [ ] 80%+ user adoption rate among existing platform users
- [ ] 60%+ monthly retention rate for bot users
- [ ] Average session length > 5 minutes
- [ ] User satisfaction score > 4.5/5
- [ ] <$0.10 per user per month operational cost