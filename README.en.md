# SEC Monitor

[简体中文](./README.md) | English

SEC Monitor is a local-first Web app for monitoring SEC EDGAR filings for US stocks and ETFs.

> AI-generated / AI-assisted project: this repository was built with help from AI coding agents and reviewed iteratively by a human operator. Treat it as an open-source utility, not financial advice or a production compliance system.

## Stack

- Backend: Go 1.24, Gin, GORM
- Database: SQLite by default
- Scheduler: robfig/cron
- Frontend: Vue 3, Vite, TypeScript, Element Plus

## Features

- Watch target management with ticker lookup, groups, enable/disable, and per-target sync status.
- SEC filing refresh with deduplication, retry, initial fetch day limit, max fetch count, and optional full-history archive fetch.
- SEC filing list with filters, pagination, sortable filing date, publish time, sync time, ticker, and filing type.
- Saved filing views stored locally in the browser.
- Major Event Radar for 8-K, S-1, S-3, 424B, 13D, and other high-signal filings.
- IPO Radar scans SEC current filings for S-1, F-1, 424B, and other IPO/offering-related submissions, with optional Telegram alerts.
- Insider Trading page for Form 3/4/5 ownership-change disclosures.
- Sync history and scheduling with built-in `sec_filing_sync` and `ipo_radar_sync` jobs, manual run, enable/disable, and cron editing.
- Dashboard overview for monitored targets, recent filings, sync health, and notification status.
- Telegram notification settings, test sending, retries, and notification logs.
- Structured system configuration for SEC fetch policy, notification rules, data retention, and default language.
- Chinese/English UI switching: the top bar controls the current browser preference, and System Settings controls the default language.
- First-run setup guide for SEC User-Agent, first target, notifications, and initial sync.
- System Health page for User-Agent, database, sync, notification, and data-size checks.
- Export and backup for filings CSV, watch targets CSV, configs JSON, and full backup JSON.
- Data cleanup preview and confirmed cleanup based on retention days.
- Operation audit logs for key changes.

## Quick Start

Prerequisites:

- Go 1.24+
- Node.js 20+
- npm

Run locally:

```bash
make start
make status
make logs
make restart
make stop
```

Default URLs:

- Backend: http://127.0.0.1:8080
- Frontend: http://127.0.0.1:5173
- Health: http://127.0.0.1:8080/healthz

Local runtime files:

- PID files: `.runtime/`
- SQLite database: `data/sec_monitor.db`
- Logs: `logs/YYYY-MM-DD/`

These paths are intentionally ignored by Git.

## Docker Deployment

The Docker image contains both the Go API server and the built Vue frontend. One container serves the full Web UI and API.

Current Compose mapping:

- Host URL: http://127.0.0.1:9090
- Container port: `8080`
- Mapping in `docker-compose.yml`: `9090:8080`

Prerequisites:

- Docker
- Docker Compose v2

Build the image:

```bash
make docker-build
```

Run with Docker Compose:

```bash
make docker-up
```

`make docker-up` stops the local `make start` services first, then starts the Docker container. If you run `docker compose up` manually, run `make stop` first so the browser does not hit a stale local backend.

Open:

- Web UI: http://127.0.0.1:9090
- Health: http://127.0.0.1:9090/healthz

Common Docker operations:

```bash
make docker-up       # build and start
make docker-logs     # follow container logs
make docker-down     # stop and remove container, keep data volume

docker compose ps
docker compose restart sec-monitor
docker compose logs -f sec-monitor
docker compose down
```

Data persistence:

- SQLite database inside container: `/app/data/sec_monitor.db`
- Docker named volume: `sec_monitor_sec-monitor-data`
- `docker compose down` keeps the volume and data.
- `docker compose down -v` removes the volume and deletes the database.

Logs:

- Container logs are written to Docker stdout/stderr.
- View them with `make docker-logs` or `docker compose logs -f sec-monitor`.
- The local development `logs/` directory is not used by the Docker container.

Change Docker port:

```yaml
ports:
  - "9090:8080"
```

Change the left side to the host port you want, for example `18080:8080`, then run:

```bash
make docker-up
```

Before serious use, set a descriptive SEC User-Agent. Edit `SEC_USER_AGENT` in `docker-compose.yml` or pass it at runtime:

```bash
SEC_USER_AGENT="sec-monitor/0.1 your-email@example.com" docker compose up -d --build
```

Upgrade/rebuild:

```bash
git pull
make docker-up
```

Publish example:

```bash
docker build -t ghcr.io/<user>/sec-monitor:latest .
docker push ghcr.io/<user>/sec-monitor:latest
```

## Configuration

Runtime configuration is available in the Web UI under `System Settings`.

SEC fetch settings:

- `sec.sync_window_days`: limits every sync to filings from recent N days. `0` means no date window.
- `sec.initial_fetch_days`: limits first sync for a target to recent N days.
- `sec.max_fetch_count`: limits filings processed per target per sync. `0` means no limit.
- `sec.fetch_full_history`: enables SEC archived submissions file fetching.

Data retention settings:

- `system.data_retention_days`: filings older than this by sync time can be previewed and cleaned.
- `system.storage_by_day`: reserved for day-based local storage layout.

Interface settings:

- `ui.default_locale`: default UI language, supports `zh-CN` and `en-US`.
- `ui.onboarding_completed`: whether the first-run setup guide has been completed.
- The top language switch is stored in the current browser and takes precedence over the system default.

Notification rule settings:

- `notification.important_only`: only notify important filing types.
- `notification.filing_types`: only notify selected filing types, comma-separated, for example `8-K,10-K,S-1`.
- `notification.keywords`: only notify filings whose title or content contains selected keywords, comma-separated.
- `notification.quiet_hours_enabled`: enables quiet hours.
- `notification.quiet_hours_start` / `notification.quiet_hours_end`: quiet-hour window in `HH:mm` format.

IPO Radar settings:

- `ipo.enabled`: enables IPO Radar.
- `ipo.form_types`: SEC form types to scan. Default is `S-1,S-1/A,F-1,F-1/A,S-1MEF`.
- `ipo.lookback_days`: keeps only current filing results from recent N days.
- `ipo.max_results`: max rows per form type. The SEC current-filing endpoint is capped at 100 here.
- `ipo.notify_enabled`: sends Telegram alerts for newly stored IPO Radar filings.
- `ipo.keywords`: comma-separated company/title keyword filter. Empty means no keyword filter.

Environment variables:

```bash
APP_ADDR=127.0.0.1:8080
DB_TYPE=sqlite
DB_DSN=data/sec_monitor.db
SEC_BASE_URL=https://data.sec.gov
SEC_USER_AGENT="sec-monitor/0.1 your-email@example.com"
SEC_TIMEOUT_MS=10000
```

SEC requires a descriptive User-Agent. Set `SEC_USER_AGENT` before serious use.

## Development

Backend tests:

```bash
GOCACHE=$(pwd)/.cache/go-build GOMODCACHE=$(pwd)/.cache/go-mod go test ./...
```

Frontend build:

```bash
cd web
npm run build
```

Coverage:

```bash
GOCACHE=$(pwd)/.cache/go-build GOMODCACHE=$(pwd)/.cache/go-mod go test ./... -coverprofile=/tmp/sec_monitor_cover.out
go tool cover -func=/tmp/sec_monitor_cover.out
```

## Repository Notes

- This is an AI-generated / AI-assisted codebase. Review changes before deploying or relying on alerts.
- Runtime data, logs, build output, dependency folders, and caches are ignored.
- Do not commit Telegram bot tokens, SQLite data files, or local environment files.

## License

MIT License. See [LICENSE](LICENSE).
