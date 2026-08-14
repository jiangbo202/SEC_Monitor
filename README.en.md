# SEC Monitor

<p align="center">
  <a href="https://github.com/jiangbo202/SEC_Monitor/blob/main/LICENSE"><img src="https://img.shields.io/github/license/jiangbo202/SEC_Monitor?style=flat" alt="License"></a>
  <a href="https://github.com/jiangbo202/SEC_Monitor/stargazers"><img src="https://img.shields.io/github/stars/jiangbo202/SEC_Monitor?style=flat&logo=github" alt="GitHub Stars"></a>
  <a href="https://github.com/jiangbo202/SEC_Monitor/releases"><img src="https://img.shields.io/github/v/release/jiangbo202/SEC_Monitor?display_name=tag&style=flat" alt="Latest release"></a>
  <img src="https://img.shields.io/badge/Go-1.24-00ADD8?style=flat&logo=go" alt="Go 1.24">
  <img src="https://img.shields.io/badge/Vue-3-42B883?style=flat&logo=vuedotjs" alt="Vue 3">
</p>

<p align="center">
  <a href="./README.md">简体中文</a> ·
  <a href="./README.en.md">English</a> ·
  <a href="https://github.com/jiangbo202/SEC_Monitor/releases">Releases</a> ·
  <a href="https://github.com/jiangbo202/SEC_Monitor/issues">Issues</a> ·
  <a href="https://github.com/jiangbo202/SEC_Monitor/pulls">Pull Requests</a>
</p>

A local-first US equity research and SEC intelligence workspace. It keeps
filings, watch targets, IPOs, macro data, small-cap research, institutional
holdings and manual AI research in local SQLite databases for review. It is not
investment advice and does not place trades.

> **Security boundary: local-first, no built-in authentication, and never
> expose the application directly to the public internet.** Docker binds to
> `127.0.0.1:9090` by default. For remote access, use a VPN or a reverse proxy
> with TLS, authentication and access control.

> This project contains AI-assisted code. Review security, compliance and data
> quality before using it with real capital or in a production environment.

## Capabilities

- **Watch targets and SEC filings**: manage stocks/ETFs, incrementally ingest
  EDGAR filings, and review major events, insider trades and earnings events.
- **IPO monitor**: follow S-1/F-1, EFFECT, 424B4 and RW lifecycle filings;
  reconcile listing status using SEC mappings and optional Longbridge checks.
- **Small-cap research and strategy pool**: preserve explainable fundamentals,
  price, technical, liquidity and trade-discipline snapshots and changes.
- **Ticker evaluation**: apply the existing research logic to a stock or ETF
  and retain historical results.
- **Macro and market research**: market trends, sector ETFs, US futures, macro
  releases (including payrolls, CPI, PPI, PCE and FOMC) and 13F holdings.
- **Research enrichment**: local snapshots for company profiles, analyst
  consensus, valuation, options research and institutional holdings.
- **Manual AI research**: multiple OpenAI-compatible providers (including
  DeepSeek), configurable templates, durable prompts/results and completion
  notifications. The application never calls third-party AI automatically.
- **Operations**: in-app and Telegram notifications, deduplication, retries,
  dead letters, task logs, health checks, SQLite backups and restore drills.

## Data sources and boundaries

| Area | Primary sources | Notes |
| --- | --- | --- |
| Filings, IPOs, 13F | SEC EDGAR | Source of record for filing facts |
| Prices and research enrichment | Longbridge, with configurable Tiingo / Twelve Data / Yahoo fallback | Results are stored as local snapshots |
| Macro calendar | BEA, BLS, FRED, Fed, Treasury, Census, DOL and EIA | FRED can mirror BLS data when BLS is unavailable; the UI marks these as data periods |
| AI research | User-configured OpenAI-compatible API | Called only after an explicit UI action |

The application does not present commercial forecasts, ratings or bullish/bearish
labels as SEC facts. Missing data remains explicit rather than being invented.

## Quick start

### Docker (recommended)

Prerequisites: Docker and Docker Compose v2.

```bash
# Generate a value and put it in a local .env file. Never commit .env.
openssl rand -base64 32

# Build and start the full application.
make docker-up
```

Open <http://127.0.0.1:9090>. Health endpoint:
<http://127.0.0.1:9090/healthz>.

On first use, configure the following in **System Settings**:

1. A descriptive SEC User-Agent, such as `SEC Monitor your-email@example.com`.
2. `CONFIG_ENCRYPTION_KEY`, used to encrypt Telegram, Longbridge, market-data
   and AI credentials.
3. The providers you intend to use.
4. Watch targets and scheduler settings.

Example `.env`:

```env
CONFIG_ENCRYPTION_KEY=<output of openssl rand -base64 32>
SEC_USER_AGENT=SEC Monitor your-email@example.com
```

Do not lose or casually rotate an active encryption key: existing encrypted
configuration will no longer be readable.

### Local development

Prerequisites: Go 1.24+, Node.js 20+ and npm.

```bash
make start      # API :8080, frontend :5173
make status
make logs
make stop
```

## Common commands

```bash
# Docker
make docker-up
make docker-logs
make docker-down

# Small-cap research in Docker
make docker-discovery-sync
make docker-discovery-incremental-sync
make docker-discovery-market-sync

# Validation
go test ./...
cd web && npm run build
```

`make docker-up` stops the local development services before starting Docker.
`docker compose down` keeps the data volume; `docker compose down -v` deletes
all Docker data, so make sure backups are available first.

## Operational guidance

1. Start with a small set of watch targets and verify one SEC sync.
2. Configure a price provider before running small-cap research or a ticker
   evaluation. Missing evidence is retained as a gap instead of being guessed.
3. Review individual schedules and their timezone in **Scheduler**. Jobs are
   independent, so one failed source does not block other jobs.
4. Resolve failures, retries and storage warnings from **System Health**,
   **Sync History** and **Notification Logs**. Avoid repeatedly forcing
   quota-sensitive market-data or AI jobs.
5. Treat AI output as research assistance: review the underlying SEC filings
   and local evidence before acting.

## Storage and security

- Main data defaults to `data/sec_monitor.db`; small-cap research has a
  separate SQLite database.
- Docker persists `/app/data` in the `sec_monitor_sec-monitor-data` volume.
- Local logs are written under `logs/YYYY-MM-DD/`; Docker logs are available
  through `make docker-logs`.
- Backups, restore drills, database compaction and retention cleanup are
  managed through settings and scheduled jobs.
- Never commit `.env`, databases, backups, logs, browser traces, API keys,
  Telegram tokens or exported research data.
- External AI, market-data and notification providers may incur fees and
  receive the data sent by a user-triggered request. Use budgets and manual
  controls accordingly.

## Project layout

```text
cmd/                service and research-sync entry points
internal/api/       Gin routes and handlers
internal/service/   business, scheduling, notification and research services
internal/sec/       SEC EDGAR client and parsers
internal/model/     GORM models
web/                Vue 3 frontend
docs/               design, API and operations documentation
```

See [docs](docs/) for detailed material and
[docs/operations/backup-and-recovery.md](docs/operations/backup-and-recovery.md)
for backup/restore boundaries. See [SECURITY.md](SECURITY.md) for the security
policy and deployment boundary.

## Feedback and contributions

- Report bugs or propose features through
  [Issues](https://github.com/jiangbo202/SEC_Monitor/issues).
- Pull requests are welcome. Please read [CONTRIBUTING.md](CONTRIBUTING.md)
  first.
- Do not report vulnerabilities in a public issue; follow
  [SECURITY.md](SECURITY.md) instead.

## License

[MIT License](LICENSE)

## Star History

[![Star History Chart](https://api.star-history.com/svg?repos=jiangbo202/SEC_Monitor&type=Date)](https://star-history.com/#jiangbo202/SEC_Monitor&Date)
