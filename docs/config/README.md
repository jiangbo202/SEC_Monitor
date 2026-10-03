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
| `SMALL_CAP_PRICE_PROVIDER` | empty | Stock price source: `longbridge`, `futu`, `stooq`, or `longbridge,futu`. |
| `SMALL_CAP_LONGBRIDGE_APP_KEY` | empty | Longbridge OpenAPI App Key. |
| `SMALL_CAP_LONGBRIDGE_APP_SECRET` | empty | Longbridge OpenAPI App Secret. |
| `SMALL_CAP_LONGBRIDGE_ACCESS_TOKEN` | empty | Longbridge OpenAPI Access Token. |
| `SMALL_CAP_LONGBRIDGE_ANALYST_RATING_ENABLED` | `true` | Whether to synchronise Longbridge analyst consensus snapshots after a small-cap workflow. |
| `SMALL_CAP_LONGBRIDGE_ANALYST_RATING_REQUEST_BUDGET` | `20` | Total Longbridge analyst-consensus requests per workflow, shared by current candidates and enabled stock watch targets. |
| `SMALL_CAP_LONGBRIDGE_ANALYST_RATING_TARGET_CHANGE_PCT` | `5` | Minimum percentage change in the consensus average target price that is considered a notification-worthy update. |
| `SMALL_CAP_LONGBRIDGE_FUNDAMENTAL_REQUEST_INTERVAL_MS` | `1100` | Shared minimum interval for Longbridge company-profile and analyst-rating requests; rate-limited calls are retried once after the next slot. |
| `SMALL_CAP_MIN_PUBLISH_COVERAGE_PCT` | `85` | Minimum market price coverage required to publish a research candidate batch. A batch also cannot fall more than 15 percentage points below the previous published batch for the same provider. |
| `SMALL_CAP_CACHE_RETENTION_DAYS` | `14` | Delete SEC download-cache files older than this period at the start of a small-cap sync. It never deletes SQLite research data. |
| `SMALL_CAP_STOOQ_URLS` | empty | Comma-separated Stooq CSV/ZIP URLs when using the Stooq provider. |

Data-source settings are managed on the Data Sources & API page. Stock routes support Longbridge / Futu (or a configured Stooq CSV). US continuous futures use the official Futu HTTP API and an independent module; they share Futu credentials, pause and daily budget. Enable `us_futures_sync` separately for scheduled updates.



| UI Field | Stored key | Runtime equivalent |
|---|---|---|
| Price Provider | `discovery.price_provider` | `SMALL_CAP_PRICE_PROVIDER` |
| Stooq URLs | `discovery.stooq_urls` | `SMALL_CAP_STOOQ_URLS` |
| Longbridge App Key | `discovery.longbridge_app_key` | `SMALL_CAP_LONGBRIDGE_APP_KEY` |
| Longbridge App Secret | `discovery.longbridge_app_secret` | `SMALL_CAP_LONGBRIDGE_APP_SECRET` |
| Longbridge Access Token | `discovery.longbridge_access_token` | `SMALL_CAP_LONGBRIDGE_ACCESS_TOKEN` |
| Longbridge Analyst Rating Enabled | `discovery.longbridge_analyst_rating_enabled` | `SMALL_CAP_LONGBRIDGE_ANALYST_RATING_ENABLED` |
| Longbridge Analyst Rating Request Budget | `discovery.longbridge_analyst_rating_request_budget` | `SMALL_CAP_LONGBRIDGE_ANALYST_RATING_REQUEST_BUDGET` |
| Longbridge Analyst Rating Target Change % | `discovery.longbridge_analyst_rating_target_change_pct` | `SMALL_CAP_LONGBRIDGE_ANALYST_RATING_TARGET_CHANGE_PCT` |
| Min Publish Coverage % | `discovery.min_publish_coverage_pct` | `SMALL_CAP_MIN_PUBLISH_COVERAGE_PCT` |


Backend config also accepts:

| Variable | Default |
|---|---:|
| `SEC_BASE_URL` | `https://data.sec.gov` |
| `SEC_USER_AGENT` | `sec-monitor/0.1 contact@example.com` |
| `SEC_TIMEOUT_MS` | `10000` |
| `SEC_REQUESTS_PER_SECOND` | `8` |
| `SEC_MAX_RETRIES` | `2` |
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
| `ipo.lifecycle_max_ciks` | `50` | Maximum active CIKs swept per sync; valid range is 1–200. The oldest checks are selected first. |
| `ipo.lifecycle_recheck_hours` | `12` | A lifecycle check is stale after this many hours; valid range is 1–168. |

The sweep always includes required lifecycle forms (`EFFECT`, `424B4`, and `RW`) even when they are absent from `ipo.form_types`. It selects only active companies with an IPO lifecycle filing in the last 180 days, skips companies manually finalized as `listed` or `withdrawn`, and stores lifecycle backfills without Telegram notifications.

`notification_retry_sync` is a default enabled scheduler task with cron `*/10 * * * *`. It sends only due `failed` notification batches. A failed initial delivery is retried after 5 minutes, then 15 minutes, 45 minutes, 2 hours, and 6 hours; a later failure becomes `dead_letter`. Keep this task enabled unless notification recovery is intentionally paused.

`sqlite_backup` creates a matching `sec_monitor` + `small_cap` snapshot pair. Both temporary snapshots must pass SQLite `integrity_check` before either is published. The System Health page can run a recovery drill: it copies the newest complete pair into an isolated temporary directory, validates integrity and required schemas in read-only mode, records the result locally, and then removes the temporary copies. Incomplete files are never counted as restore points.

`operation_history_cleanup` runs weekly by default and retains only a configurable window of completed diagnostic history. It removes SEC sync run records/details, small-cap workflow steps, and operational alert deduplication records; it never removes filings, candidates, published batches, market history, notifications, or research conclusions. Use the preview in System Settings before a manual cleanup.

After deployment, verify `GET /api/ipo-health`. The endpoint reports pending listings, missing market mappings, stale lifecycle checks, unsupported offering parses, failed/due/dead-letter notification batches, and the latest IPO sync. Use the Notification Logs page to inspect a batch; `failed` and `dead_letter` batches can be manually requeued, which resets their retry cycle for immediate delivery. A batch with an active retry lease cannot be requeued.


```bash
export SMALL_CAP_PRICE_PROVIDER=longbridge
export SEC_USER_AGENT="sec-monitor/0.1 your-email@example.com"
go run ./cmd/discovery-sync
```
