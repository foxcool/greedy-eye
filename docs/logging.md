# Logging

Greedy Eye uses structured logging. You can configure the output format (TEXT or JSON) and level (DEBUG, INFO, WARN, ERROR, FATAL) via configuration file or environment variables.

## Configuration

See `docs/config.md` for configuration options (`logging.output`, `logging.level`, `logging.format`).

## Log Formats

### JSON Format (Recommended for production/processing)

When `logging.format` is set to `JSON`.

```json
{
  "level": "info",
  "time": "2024-04-16T10:30:15.123Z",
  "service": "PortfolioService",
  "message": "Portfolio value calculated successfully",
  "portfolio_id": "pf_abc123xyz",
  "quote_asset_id": "USD",
  "total_value": 12345.67,
  "calculation_duration_ms": 45
}
```
```json
{
  "level": "error",
  "time": "2024-04-16T10:35:01.456Z",
  "service": "PriceService",
  "message": "Failed to fetch prices from external source",
  "source_id": "some_exchange_api",
  "error": "request timeout after 10s",
  "asset_ids": ["btc-usd", "eth-usd"]
}
```

### TEXT Format (Human-readable, good for development console)

When `logging.format` is set to `TEXT`. The exact format might vary slightly depending on the logging library used.

```text
INFO [2024-04-16T10:30:15Z] PortfolioService: Portfolio value calculated successfully portfolio_id=pf_abc123xyz quote_asset_id=USD total_value=12345.67 calculation_duration_ms=45
ERROR[2024-04-16T10:35:01Z] PriceService: Failed to fetch prices from external source source_id=some_exchange_api error="request timeout after 10s" asset_ids="[btc-usd eth-usd]"
```

## Fields

Common fields include:
*   `level`: Log level (e.g., "info", "error").
*   `time`: Timestamp in RFC3339 format.
*   `service`: Name of the service/component emitting the log.
*   `message`: The main log message.
*   Additional contextual fields (e.g., `portfolio_id`, `error`, `duration_ms`).
