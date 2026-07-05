# Configuration

## Local Runtime

The local control script reads environment variables before starting services.

| Variable | Default | Description |
|---|---:|---|
| `APP_ADDR` | `127.0.0.1:8080` | Backend listen address. |
| `FRONTEND_HOST` | `127.0.0.1` | Vite frontend host. |
| `FRONTEND_PORT` | `5173` | Vite frontend port. |
| `LOCAL_RETENTION_DAYS` | `14` | Number of dated log/data directories to keep. |
| `LOCAL_START_TIMEOUT` | `60` | Seconds to wait for backend/frontend health checks during startup. |
| `LOCAL_LOGS_BY_DAY` | `1` | Store local logs under `logs/YYYY-MM-DD/`. |
| `LOCAL_DATA_BY_DAY` | `0` | Store SQLite DB under `data/YYYY-MM-DD/`; disabled by default to keep one continuous local DB. |
| `LOCAL_DATE` | current date | Override runtime date, useful for testing retention. |
| `DB_DSN` | derived | SQLite database path. Defaults to `data/sec_monitor.db` or `data/YYYY-MM-DD/sec_monitor.db` when `LOCAL_DATA_BY_DAY=1`. |
| `SMALL_CAP_PRICE_PROVIDER` | empty | Small-cap price provider. Supports `tiingo`, `twelvedata`, `yahoo`, `stooq`, or ordered chains such as `tiingo,twelvedata,yahoo`, `tiingo,yahoo`, and `stooq,tiingo,yahoo`. |
| `TIINGO_API_TOKEN` | empty | Tiingo API token for the real small-cap price source. Keep it in your shell/profile or local process environment; do not commit it. |
| `TIINGO_API_TOKENS` | empty | Comma-separated Tiingo tokens. Request budget is applied per token. |
| `SMALL_CAP_TIINGO_BASE_URL` | `https://api.tiingo.com` | Tiingo API base URL. |
| `SMALL_CAP_TIINGO_REQUEST_BUDGET` | `45` | Max Tiingo requests per token for one market sync. |
| `TWELVE_DATA_API_KEY` | empty | Twelve Data API key. |
| `SMALL_CAP_TWELVE_DATA_BASE_URL` | `https://api.twelvedata.com` | Twelve Data API base URL. |
| `SMALL_CAP_TWELVE_DATA_REQUEST_BUDGET` | `700` | Max Twelve Data requests for one market sync. |
| `SMALL_CAP_TWELVE_DATA_REQUEST_INTERVAL_MS` | `8000` | Delay between Twelve Data requests. Keeps the free tier near 8 API credits/minute. |
| `SMALL_CAP_YAHOO_BASE_URL` | `https://query1.finance.yahoo.com` | Yahoo chart API base URL. |
| `SMALL_CAP_YAHOO_REQUEST_BUDGET` | `45` | Max Yahoo chart requests for one market sync. |
| `SMALL_CAP_MIN_PUBLISH_COVERAGE_PCT` | `20` | Minimum market price coverage required to publish a research candidate batch. Lower coverage keeps the previous published list. |
| `SMALL_CAP_STOOQ_URLS` | empty | Comma-separated Stooq CSV/ZIP URLs when using the Stooq provider. |

The same small-cap data-source settings can be managed from the System Settings page:

| UI Field | Stored key | Runtime equivalent |
|---|---|---|
| Price Provider | `discovery.price_provider` | `SMALL_CAP_PRICE_PROVIDER` |
| Stooq URLs | `discovery.stooq_urls` | `SMALL_CAP_STOOQ_URLS` |
| Tiingo API Token | `discovery.tiingo_api_token` | `TIINGO_API_TOKEN` |
| Tiingo API Tokens | `discovery.tiingo_api_tokens` | `TIINGO_API_TOKENS` |
| Tiingo Request Budget | `discovery.tiingo_request_budget` | `SMALL_CAP_TIINGO_REQUEST_BUDGET` |
| Tiingo Base URL | `discovery.tiingo_base_url` | `SMALL_CAP_TIINGO_BASE_URL` |
| Twelve Data API Key | `discovery.twelve_data_api_key` | `TWELVE_DATA_API_KEY` |
| Twelve Data Request Budget | `discovery.twelve_data_request_budget` | `SMALL_CAP_TWELVE_DATA_REQUEST_BUDGET` |
| Twelve Data Request Interval ms | `discovery.twelve_data_request_interval_ms` | `SMALL_CAP_TWELVE_DATA_REQUEST_INTERVAL_MS` |
| Twelve Data Base URL | `discovery.twelve_data_base_url` | `SMALL_CAP_TWELVE_DATA_BASE_URL` |
| Yahoo Request Budget | `discovery.yahoo_request_budget` | `SMALL_CAP_YAHOO_REQUEST_BUDGET` |
| Yahoo Base URL | `discovery.yahoo_base_url` | `SMALL_CAP_YAHOO_BASE_URL` |
| Min Publish Coverage % | `discovery.min_publish_coverage_pct` | `SMALL_CAP_MIN_PUBLISH_COVERAGE_PCT` |

Stored system settings take precedence over environment variables. The Tiingo token is returned to the browser only as a masked value. Saving the masked value keeps the existing token; clearing the field removes it.

Backend config also accepts:

| Variable | Default |
|---|---:|
| `SEC_BASE_URL` | `https://data.sec.gov` |
| `SEC_USER_AGENT` | `sec-monitor/0.1 contact@example.com` |
| `SEC_TIMEOUT_MS` | `10000` |
| `LOG_LEVEL` | `info` |
| `DATA_RETENTION_DAYS` | `30` |
| `STORAGE_BY_DAY` | `false` |

Example Tiingo local run:

```bash
export SEC_USER_AGENT="sec-monitor/0.1 your-email@example.com"
export SMALL_CAP_PRICE_PROVIDER=tiingo,twelvedata,yahoo
export TIINGO_API_TOKEN="your-tiingo-token"
export SMALL_CAP_TIINGO_REQUEST_BUDGET=45
export TWELVE_DATA_API_KEY="your-twelve-data-key"
export SMALL_CAP_TWELVE_DATA_REQUEST_BUDGET=700
export SMALL_CAP_TWELVE_DATA_REQUEST_INTERVAL_MS=8000
export SMALL_CAP_YAHOO_REQUEST_BUDGET=200
go run ./cmd/discovery-sync
```
