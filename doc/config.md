# Configuration and execution parameters

Get help with parameters and defaults

    eye --help
    eye --version

Start with config file

    eye -c [config file path]

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