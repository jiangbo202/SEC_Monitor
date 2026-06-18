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

Backend config also accepts:

| Variable | Default |
|---|---:|
| `SEC_BASE_URL` | `https://data.sec.gov` |
| `SEC_USER_AGENT` | `sec-monitor/0.1 contact@example.com` |
| `SEC_TIMEOUT_MS` | `10000` |
| `LOG_LEVEL` | `info` |
| `DATA_RETENTION_DAYS` | `30` |
| `STORAGE_BY_DAY` | `false` |
