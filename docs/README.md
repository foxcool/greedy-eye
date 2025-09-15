# Documentation - Greedy Eye

Universal portfolio management system supporting diverse asset types including cryptocurrencies, securities, derivatives, and alternative assets - featuring conversational interface and automation capabilities.

## Quick Navigation

### 📋 Essential Documents
- **[ARCHITECTURE.md](ARCHITECTURE.md)** - Complete system architecture, technology stack, and design decisions
- **[DEVELOPMENT.md](DEVELOPMENT.md)** - Development guide, setup instructions, and current status

### 🔧 Service Documentation
- **[services/telegram-bot.md](services/telegram-bot.md)** - Telegram bot with voice support (STT/TTS)
- **[services/telegram-commands.md](services/telegram-commands.md)** - Complete command reference
- **[services/rule-service.md](services/rule-service.md)** - Rule engine and alert system

### 📁 Archive
- **[archive/](archive/)** - Historical documentation and deprecated files

## Project Status

### Current Phase: Business Logic Implementation 🔄

**Architecture Status**: ✅ **COMPLETE**
- All service stubs implemented with comprehensive APIs
- HTTP API Gateway functional with gRPC-Gateway
- Service integration and dependency management working
- 90%+ test coverage across all services

**Next Steps**:
1. Implement business logic in service stubs
2. External API integrations (crypto exchanges, market data providers, securities brokerages)
3. Real Telegram Bot API integration
4. Production deployment preparation

### Service Implementation Status

| Service | API | Implementation | Tests | Integration |
|---------|-----|---------------|-------|-------------|
| StorageService | ✅ | ✅ Complete | ✅ | ✅ |
| UserService | ✅ | 🔄 Stubs | ✅ | ✅ |
| AssetService | ✅ | 🔄 Stubs | ✅ | ✅ |
| PortfolioService | ✅ | 🔄 Stubs | ✅ | ✅ |
| PriceService | ✅ | 🔄 Stubs | ✅ | ✅ |
| RuleService | ✅ | 🔄 Stubs | ✅ | ✅ |
| TelegramBotService | ✅ | 🔄 Stubs | ✅ | ✅ |

## Key Architecture Decisions

### Alert System Design
**Decision**: Alerts integrated into RuleService (not separate AlertService)  
**Rationale**: YAGNI principle - alerts are rule actions, not separate domain  
**Implementation**: Rule with action=SEND_ALERT → finds user communication channels → routes to transport services  

### Service Separation  
**Decision**: TelegramBotService handles only transport, RuleService handles business logic  
**Rationale**: Clear separation of concerns, testable architecture  
**Benefit**: Easy to add EmailService, SMSService later  

### Communication Channels
**Decision**: Store telegram_id, email in UserAccounts as Account entities  
**Rationale**: Extensible for multiple communication channels (SMS, push, webhooks)  
**Flow**: RuleService → UserService (find channels) → TelegramBotService (transport)  

## Technology Stack

- **Language**: Go 1.23+
- **Internal API**: gRPC with Protocol Buffers
- **External API**: HTTP REST API with JSON (via gRPC-Gateway)
- **Database**: PostgreSQL 13+ with Ent ORM
- **Containerization**: Docker & Docker Compose
- **Build Tools**: buf (protobuf), Go modules, Make

## Quick Start

```bash
# Clone and setup
git clone https://github.com/foxcool/greedy-eye.git
cd greedy-eye
go mod download

# Start database
docker-compose up -d postgres

# Generate protobuf code
make buf-gen

# Run with live reload
make dev
```

Application starts:
- gRPC server on port 50051
- HTTP API on port 8080
- Health check at http://localhost:8080/health

## Documentation Philosophy

This documentation follows principles of **clarity and maintainability**:

- **Single Source of Truth**: Each topic covered in one place
- **Practical Focus**: Information developers actually need
- **Current Status**: Always reflects actual implementation state
- **YAGNI Applied**: No documentation for unimplemented features

### Documentation Structure

```
docs/
├── README.md              # This file - navigation and overview
├── ARCHITECTURE.md        # Complete system architecture
├── DEVELOPMENT.md         # Development guide and status
├── services/              # Individual service documentation
│   ├── telegram-bot.md    # Telegram bot service
│   ├── telegram-commands.md # Bot command reference
│   └── rule-service.md    # Rule engine and alerts
└── archive/               # Historical and deprecated docs
    ├── architecture/      # Old detailed architecture docs
    ├── development/       # Old development guides
    ├── product/          # Old roadmaps and vision docs
    └── features/         # Old feature specifications
```

## Contributing to Documentation

### When to Update Documentation
- **Architecture changes**: Update ARCHITECTURE.md
- **Development workflow changes**: Update DEVELOPMENT.md  
- **Service implementation progress**: Update service docs in services/
- **New services**: Create new service documentation

### Documentation Standards
- Use clear, concise language
- Include practical examples and code snippets
- Maintain current implementation status
- Follow existing structure and formatting
- Test all code examples before committing

## Getting Help

1. **Start with**: [DEVELOPMENT.md](DEVELOPMENT.md) for setup and common tasks
2. **Architecture questions**: [ARCHITECTURE.md](ARCHITECTURE.md) for system design
3. **Service-specific questions**: Check relevant file in [services/](services/)
4. **Historical context**: Search [archive/](archive/) for background information

For bugs or feature requests, create an issue on GitHub with:
- Clear description of the problem or requirement
- Steps to reproduce (for bugs)
- Expected vs actual behavior
- Relevant logs or error messages