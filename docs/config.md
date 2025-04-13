# Configuration and execution parameters

Get help with parameters and defaults

    eye --help
    eye --version

Start with config file

    eye -c [config file path]


## Example Configuration (`config.yaml`)

```yaml
# Logging settings
logging:
  output: "STDOUT" # Can be "STDOUT" or a file path like "/var/log/eye.log"
  level: "INFO"    # DEBUG, INFO, WARN, ERROR, FATAL
  format: "JSON"   # TEXT or JSON

# Telegram Bot settings (if Telegram service is enabled)
telegram:
  token: "YOUR_TELEGRAM_BOT_TOKEN" # Bot token from BotFather
  chatIDs: # List of chat IDs to send notifications to
    - "-1001234567890" # Example group chat ID
    # - "987654321"      # Example private chat ID

# Database connection (using environment variable is often preferred for secrets)
# database:
#   url: "postgresql://user:password@host:port/dbname?sslmode=disable"

# Enabled services (can also be set via EYE_SERVICES env var)
# services:
#   - asset
#   - portfolio
#   - price
#   - user
#   - storage # If storage runs as a separate service instance
#   - telegram # If telegram bot service is enabled

# Sentry integration (optional)
# sentry:
#   dsn: "YOUR_SENTRY_DSN"
#   environment: "production" # e.g., development, staging, production
```


## parameters

### logging.output

Service logging output

- type: string (STDOUT or file path)
- ENV: EYE_LOGGING_OUTPUT
- default: STDOUT

### logging.level

Service logging level

- type: string (DEBUG, INFO, WARN, ERROR, FATAL)
- ENV: EYE_LOGGING_LEVEL
- default: INFO

### logging.format

Service logging format

- type: string (TEXT, JSON)
- ENV: EYE_LOGGING_FORMAT
- default: TEXT


### telegram.ChatIDs

Telegram chat IDs

- type: string
- ENV: EYE_TELEGRAM_CHATIDS
- default: ""

### telegram.token

Telegram token

- type: string
- ENV: EYE_TELEGRAM_TOKEN
- default: ""
