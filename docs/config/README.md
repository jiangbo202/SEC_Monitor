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
| `CONFIG_ENCRYPTION_KEY` | required for new sensitive values | Base64-encoded 32-byte AES-256-GCM key used for encrypted system settings. Generate with `openssl rand -base64 32`; keep it only in your local environment or Docker `.env`. |
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
| `SMALL_CAP_MIN_PUBLISH_COVERAGE_PCT` | `85` | Minimum market price coverage required to publish a research candidate batch. A batch also cannot fall more than 15 percentage points below the previous published batch for the same provider. |
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

## Sensitive Configuration Encryption

Generate and retain one key before saving Telegram or other sensitive settings:

```bash
openssl rand -base64 32
```

For Docker Compose, add the output to a local `.env` file:

```env
CONFIG_ENCRYPTION_KEY=<output of openssl rand -base64 32>
```

After creating or changing `.env`, restart Docker with `make docker-up`; a plain container restart does not load a changed Compose environment reliably. Existing plaintext sensitive settings are migrated transactionally on startup. Do not rotate or discard the key while existing encrypted values must remain readable. Without a valid key, existing legacy plaintext values remain readable for recovery, but new non-empty sensitive values are rejected and health reports a critical configuration issue.

## IPO Lifecycle And Notification Operations

The following persisted system settings are available in the System Settings page:

| Key | Default | Operator effect |
|---|---:|---|
| `ipo.lifecycle_sweep_enabled` | `true` | Enables the lifecycle sweep during each IPO sync. |
| `ipo.lifecycle_max_ciks` | `25` | Maximum active CIKs swept per sync; valid range is 1–200. The oldest checks are selected first. |
| `ipo.lifecycle_recheck_hours` | `24` | A lifecycle check is stale after this many hours; valid range is 1–168. |

The sweep always includes required lifecycle forms (`EFFECT`, `424B4`, and `RW`) even when they are absent from `ipo.form_types`. It skips companies manually finalized as `listed` or `withdrawn`, and lifecycle backfills are stored without Telegram notifications.

`notification_retry_sync` is a default enabled scheduler task with cron `*/10 * * * *`. It sends only due `failed` notification batches. A failed initial delivery is retried after 5 minutes, then 15 minutes, 45 minutes, 2 hours, and 6 hours; a later failure becomes `dead_letter`. Keep this task enabled unless notification recovery is intentionally paused.

After deployment, verify `GET /api/ipo-health`. The endpoint reports pending listings, missing market mappings, stale lifecycle checks, unsupported offering parses, failed/due/dead-letter notification batches, and the latest IPO sync. Use the Notification Logs page to inspect a batch; `failed` and `dead_letter` batches can be manually requeued, which resets their retry cycle for immediate delivery. A batch with an active retry lease cannot be requeued.

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
