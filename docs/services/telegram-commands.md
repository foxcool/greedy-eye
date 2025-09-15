# Telegram Bot Commands Reference - Greedy Eye

## Overview

This document provides a complete reference for all Telegram bot commands, their usage patterns, response formats, and integration with the Greedy Eye portfolio management system.

## Command Categories

### 1. Core Portfolio Commands
- `/start` - User registration and onboarding
- `/portfolio` - Portfolio overview and summary
- `/balance` - Current balances across all accounts
- `/performance` - Portfolio performance analytics

### 2. Market Data Commands
- `/prices` - Current market prices for tracked assets
- `/price [SYMBOL]` - Price for specific asset
- `/alerts` - Manage price and portfolio alerts

### 3. Transaction Commands
- `/transactions` - Recent transaction history
- `/tx [ID]` - Specific transaction details
- `/trade` - Interactive trading interface

### 4. Account Management
- `/accounts` - List connected accounts/exchanges
- `/sync` - Force synchronization with exchanges
- `/settings` - User preferences and configurations

### 5. Analytics Commands
- `/stats` - Portfolio statistics and insights
- `/compare [PERIOD]` - Compare performance periods
- `/export` - Export portfolio data

### 6. Utility Commands
- `/help` - Command reference and help
- `/support` - Contact support and feedback
- `/about` - Bot information and version

## Detailed Command Reference

### `/start` - User Registration & Onboarding

**Purpose**: Initialize user account and link Telegram ID with Greedy Eye system.

**Usage**:
```
/start
/start [referral_code]
```

**Flow**:
1. Check if user exists in system
2. If new user, create account and link Telegram ID
3. Show welcome message with quick setup guide
4. Offer to connect first exchange/wallet

**Response Example**:
```
🎉 Добро пожаловать в Greedy Eye!

Ваш аккаунт успешно создан.
Telegram ID: @username
User ID: uuid-here

Для начала работы:
1️⃣ Подключите биржу: /accounts
2️⃣ Синхронизируйте балансы: /sync  
3️⃣ Посмотрите портфолио: /portfolio

Нужна помощь? /help
```

**Error Handling**:
- User already registered: Show existing portfolio summary
- System error: Guide to contact support

---

### `/portfolio` - Portfolio Overview

**Purpose**: Display comprehensive portfolio overview with current values and allocations.

**Usage**:
```
/portfolio
/portfolio [CURRENCY]  # USD, EUR, BTC, etc.
```

**Response Format**:
```
💼 ПОРТФОЛИО | 12.03.2024 15:30

📊 Общая стоимость: $45,672.50 (+2.34% за день)

🪙 АКТИВЫ:
├─ BTC: 1.2456 ($42,180.20) - 92.4%
├─ ETH: 15.789 ($3,245.80) - 7.1% 
├─ USDT: 246.50 ($246.50) - 0.5%

📈 ПРОИЗВОДИТЕЛЬНОСТЬ:
├─ За день: +$1,045.30 (+2.34%)
├─ За неделю: +$2,890.15 (+6.75%)
├─ За месяц: +$8,450.80 (+22.70%)

🏦 ИСТОЧНИКИ:
├─ Binance: $38,420.15 (84.1%)
├─ Gate.io: $7,252.35 (15.9%)

Детали: /stats | Обновить: /sync
```

**Interactive Elements**:
- Inline keyboard with quick actions
- Currency conversion buttons
- Time period selection

**Data Sources**:
- PortfolioService.CalculatePortfolioValue()
- PriceService.GetLatestPrices()
- StorageService.ListHoldings()

---

### `/balance` - Current Balances

**Purpose**: Show current balances across all connected accounts.

**Usage**:
```
/balance
/balance [EXCHANGE]  # binance, gate, metamask
/balance [ASSET]     # BTC, ETH, etc.
```

**Response Format**:
```
💰 БАЛАНСЫ | Обновлено: 15:30

🔸 BINANCE
├─ BTC: 0.8456 ($36,120.40)
├─ ETH: 10.234 ($2,145.20) 
├─ USDT: 150.50 ($150.50)
└─ Всего: $38,416.10

🔸 GATE.IO  
├─ BTC: 0.4000 ($17,080.00)
├─ ETH: 5.555 ($1,165.80)
└─ Всего: $18,245.80

💳 ОБЩИЙ БАЛАНС: $56,661.90

Последнее обновление: 2 мин назад
Принудительное обновление: /sync
```

**Features**:
- Real-time balance updates
- Multi-exchange aggregation
- Asset filtering options
- Manual sync trigger

---

### `/prices` - Market Prices

**Purpose**: Display current market prices for tracked or requested assets.

**Usage**:
```
/prices
/prices [SYMBOLS]    # BTC,ETH,BNB
/price [SYMBOL]      # Single asset detailed price
```

**Response Format**:
```
📊 ЦЕНЫ | 12.03.2024 15:30 UTC

🟡 BTC/USDT: $67,850.30 
   ├─ 24ч: +2.34% (+$1,550.80)
   ├─ Мин/Макс: $65,420 / $68,200
   └─ Объем: $1.2B

🔵 ETH/USDT: $3,420.80
   ├─ 24ч: +1.85% (+$62.10) 
   ├─ Мин/Макс: $3,350 / $3,480
   └─ Объем: $890M

🟠 BNB/USDT: $590.40
   ├─ 24ч: -0.45% (-$2.70)
   ├─ Мин/Макс: $585 / $598  
   └─ Объем: $145M

Источник: CoinGecko + Binance
Алерты: /alerts | Обновить: /sync
```

**Interactive Elements**:
- Asset selection buttons
- Price alert setup
- Chart view links

---

### `/performance` - Performance Analytics

**Purpose**: Detailed portfolio performance analysis with charts and metrics.

**Usage**:
```
/performance
/performance [PERIOD]  # 1d, 1w, 1m, 3m, 1y
/performance vs [BENCHMARK]  # vs BTC, vs SPY
```

**Response Format**:
```
📈 АНАЛИТИКА ПОРТФОЛИО

🎯 ДОХОДНОСТЬ (30 дней):
├─ Абсолютная: +$8,450.80 (+22.70%)
├─ vs Bitcoin: +5.20% (outperformed)
├─ vs USD: +22.70%
└─ Аннуализировано: ~95.40%

📊 РИСК-МЕТРИКИ:
├─ Волатильность: 18.5%
├─ Коэф. Шарпа: 1.85
├─ Макс. просадка: -12.3%
└─ VaR (5%): -$2,845

🎢 ДИНАМИКА:
├─ Лучший день: +8.9% ($3,420)
├─ Худший день: -5.2% (-$1,890) 
├─ Выигрышных дней: 67%
└─ Средний дневной доход: +0.75%

📍 БЕНЧМАРКИ:
├─ S&P 500: +12.40% (outperformed +10.3%)
├─ Bitcoin: +17.50% (outperformed +5.2%)
└─ Gold: +8.90% (outperformed +13.8%)

График: [Посмотреть в браузере]
Детали: /stats | Экспорт: /export
```

---

### `/alerts` - Alert Management

**Purpose**: Manage price alerts and portfolio notifications.

**Usage**:
```
/alerts
/alerts add [ASSET] [PRICE]     # /alerts add BTC 70000
/alerts remove [ID]             # /alerts remove 123
/alerts list                    # List all alerts
```

**Response Format**:
```
🔔 АЛЕРТЫ И УВЕДОМЛЕНИЯ

⚡ АКТИВНЫЕ АЛЕРТЫ:
├─ BTC > $70,000 (осталось +3.2%)
├─ ETH < $3,000 (осталось -12.3%)  
├─ Портфолио > $50,000 ✅ (достигнут)
└─ Потеря > -10% за день

📱 НАСТРОЙКИ УВЕДОМЛЕНИЙ:
├─ Ценовые алерты: ✅ включены
├─ Изменения портфолио > 5%: ✅
├─ Еженедельный отчет: ✅
└─ Аварийные уведомления: ✅

➕ Добавить алерт: 
   /alerts add BTC 75000
   /alerts add portfolio_loss 15%
   
🔧 Управление: 
   /settings notifications
```

---

### `/transactions` - Transaction History

**Purpose**: View transaction history with filtering and search.

**Usage**:
```
/transactions
/transactions [LIMIT]          # /transactions 20
/transactions [EXCHANGE]       # /transactions binance  
/transactions [ASSET]          # /transactions BTC
/tx [ID]                      # /tx 12345
```

**Response Format**:
```
📝 ТРАНЗАКЦИИ | Последние 10

🔄 2024-03-12 15:24 | Покупка
├─ BTC: +0.1456 за $9,850.40
├─ Комиссия: $4.92 (0.05%)
├─ Binance | ID: #789123
└─ P&L: +$145.20 (текущий)

💰 2024-03-11 09:15 | Продажа  
├─ ETH: -2.5000 за $8,550.00
├─ Комиссия: $8.55 (0.1%)
├─ Gate.io | ID: #654789
└─ P&L: +$420.50 (закрытый)

🔄 2024-03-10 20:45 | Покупка
├─ USDT: +5,000.00 за $5,000.00
├─ Комиссия: $0.00 (0%)
├─ Binance | ID: #456123  
└─ Депозит с банковской карты

Показать еще: /transactions 20
Детали: /tx [ID] | Экспорт: /export
```

---

### `/trade` - Interactive Trading

**Purpose**: Execute trades through conversational interface.

**Usage**:
```
/trade
/trade buy [AMOUNT] [ASSET]    # /trade buy 100 USDT of BTC
/trade sell [AMOUNT] [ASSET]   # /trade sell 0.5 BTC
```

**Interactive Flow**:
```
💱 ТОРГОВЛЯ

Что вы хотите сделать?
[Купить] [Продать] [Обменять]

>>> Пользователь: Купить
>>> Бот: На какую сумму?
>>> Пользователь: $1000
>>> Бот: Какой актив купить?
>>> Пользователь: BTC

🎯 ПОДТВЕРЖДЕНИЕ СДЕЛКИ:
├─ Операция: Покупка BTC
├─ Сумма: $1,000.00
├─ Цена: $67,850.30 (~0.01474 BTC)
├─ Биржа: Binance (лучшая цена)
├─ Комиссия: ~$0.75 (0.075%)
└─ Итого к получению: ~0.01473 BTC

⚠️ Подтвердить сделку?
[✅ Подтвердить] [❌ Отменить] [🔄 Изменить]
```

**Risk Management**:
- Portfolio percentage limits
- Daily trading limits  
- Confirmation for large trades
- Market impact warnings

---

### `/settings` - User Settings

**Purpose**: Configure user preferences and bot behavior.

**Usage**:
```
/settings
/settings currency [CODE]      # /settings currency EUR
/settings language [LANG]      # /settings language en
/settings notifications        # Notification preferences
```

**Settings Menu**:
```
⚙️ НАСТРОЙКИ

💰 ВАЛЮТА ОТОБРАЖЕНИЯ:
├─ Текущая: USD 🇺🇸
├─ Доступные: EUR, RUB, BTC, ETH
└─ Изменить: /settings currency [CODE]

🌍 ЯЗЫК ИНТЕРФЕЙСА:  
├─ Текущий: Русский 🇷🇺
├─ Доступные: English, Русский
└─ Изменить: /settings language [LANG]

🔔 УВЕДОМЛЕНИЯ:
├─ Ценовые алерты: ✅ включены
├─ Изменения портфолио: ✅ > 5%
├─ Еженедельные отчеты: ✅ по Пн
├─ Аварийные: ✅ всегда
└─ Настроить: /settings notifications

🔐 БЕЗОПАСНОСТЬ:
├─ 2FA для торговли: ❌ отключена  
├─ Лимит торговли: $1,000/день
├─ Подтверждение сделок: ✅
└─ Настроить: /settings security

📊 ОТОБРАЖЕНИЕ:
├─ Формат времени: 24ч
├─ Число знаков после запятой: 4
├─ Группировка активов: по бирже
└─ Настроить: /settings display
```

## Voice Command Processing

### Supported Voice Commands

**Russian Commands**:
- "Покажи портфолио" → `/portfolio`
- "Сколько у меня биткоина" → `/balance BTC`
- "Какая цена эфира" → `/price ETH`  
- "Купи биткоин на тысячу долларов" → `/trade buy 1000 USD of BTC`

**English Commands**:
- "Show portfolio" → `/portfolio`
- "Bitcoin balance" → `/balance BTC`
- "Ethereum price" → `/price ETH`
- "Buy one thousand dollars of Bitcoin" → `/trade buy 1000 USD of BTC`

### Voice Response Format

```
🎙️ Распознано: "Покажи портфолио"
🔄 Выполняю команду /portfolio...

[Standard portfolio response]

🔊 Хотите голосовой ответ?
[🔈 Озвучить] [📝 Только текст]
```

## Natural Language Processing

### Supported Query Patterns

**Balance Queries**:
- "How much BTC do I have?" → Balance for BTC
- "What's my total portfolio value?" → Portfolio summary
- "Show me Binance balances" → Filtered balance view

**Price Queries**:
- "Bitcoin price" → Current BTC price
- "Is ETH going up?" → ETH price with trend analysis
- "What's the best performing coin today?" → Top gainers

**Performance Queries**:
- "How am I doing this month?" → Monthly performance
- "Am I beating Bitcoin?" → Portfolio vs BTC comparison
- "Show me my worst performing asset" → Asset performance ranking

### Context-Aware Responses

```
User: "Buy more"
Bot: "Buy more of what? Your recent transactions:
     - BTC purchase 2 days ago
     - ETH purchase 1 week ago
     [BTC] [ETH] [Other]"

User: Selects [BTC]  
Bot: "How much BTC would you like to buy?
     Your current BTC: 1.2456 ($84,320)
     Available balance: $2,450 USDT"
```

## Error Handling & Recovery

### Common Error Scenarios

**1. Service Unavailable**:
```
⚠️ Сервис временно недоступен

Возможные причины:
├─ Техническое обслуживание
├─ Проблемы с биржей  
└─ Высокая нагрузка

Попробуйте через несколько минут.
Статус системы: /status
```

**2. Invalid Command**:
```
❓ Команда не распознана: "/portfollio"

Возможно, вы имели в виду:
├─ /portfolio - посмотреть портфолио
├─ /prices - текущие цены
└─ /help - список всех команд

Голосовые сообщения также поддерживаются! 🎤
```

**3. Insufficient Data**:
```
📊 Недостаточно данных для анализа

Для расчета производительности необходимо:
├─ Подключить хотя бы одну биржу ❌
├─ Синхронизировать транзакции ❌  
└─ Подождать 24 часа для расчетов

Начать настройку: /accounts
```

## Rate Limiting & Fair Usage

### Usage Limits
- **Commands**: 10 per minute per user
- **Voice Messages**: 5 per minute per user  
- **Text Messages**: 30 per minute per user
- **Trading Operations**: 3 per minute per user

### Rate Limit Response
```
⏱️ Слишком много запросов

Вы превысили лимит команд (10/мин).
Попробуйте через 45 секунд.

Лимиты существуют для:
├─ Защиты от спама
├─ Стабильной работы
└─ Справедливого использования

Ваши лимиты: /limits
```

## Troubleshooting

### Common Issues & Solutions

**Bot Not Responding**:
1. Check bot status: @GreedyEyeBot
2. Restart conversation: /start
3. Check system status: /status

**Incorrect Balances**:  
1. Force sync: /sync
2. Check exchange connections: /accounts
3. Contact support: /support

**Voice Not Working**:
1. Try shorter messages (< 60 seconds)
2. Speak clearly in Russian or English  
3. Use text fallback for complex requests

**Trading Errors**:
1. Check available balances: /balance
2. Verify trading limits: /settings security
3. Ensure exchange API permissions

## Support & Feedback

### Getting Help
- `/help` - Command reference
- `/support` - Contact support team  
- `/feedback [MESSAGE]` - Send feedback
- `/bug [DESCRIPTION]` - Report bug

### Support Response Format
```
🆘 ПОДДЕРЖКА

Ваше сообщение отправлено команде поддержки.
Тема: Проблема с синхронизацией Binance
ID обращения: #SUP-789123

Ожидаемое время ответа:
├─ Обычные вопросы: 2-4 часа  
├─ Технические проблемы: 30 минут
└─ Срочные проблемы: 10 минут

Статус обращения: /support status 789123
База знаний: /help
```

This comprehensive command reference ensures users can effectively interact with the Greedy Eye Telegram bot while maintaining consistent user experience and clear expectations.