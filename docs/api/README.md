# API Documentation

Base path: `/api`

Success response:

```json
{"code":0,"message":"ok","data":{}}
```

Error response:

```json
{"code":"validation_failed","message":"..."}
```

## Watch Targets

- `GET /watch-targets`
- `POST /watch-targets`
- `GET /watch-targets/:id`
- `PUT /watch-targets/:id`
- `DELETE /watch-targets/:id`
- `PATCH /watch-targets/:id/status`
- `POST /watch-targets/:id/sync`
- `GET /watch-targets/:id/sync-details`

## SEC Filings

- `GET /filings`
- `POST /filings/refresh`
- `GET /filings/:id`
- `GET /filings/cleanup-preview`
- `POST /filings/cleanup`

Common filing query params:

- `ticker`
- `company_name`
- `filing_type`
- `date_from`
- `date_to`
- `notification_status`
- `sort_by`
- `sort_order`
- `page`
- `page_size`

## Sync Runs

- `GET /sync-runs`
- `GET /sync-runs/:id/details`

## Scheduler

- `GET /task-configs`
- `PUT /task-configs/:id`
- `POST /task-configs/:id/run`

## Configuration

- `GET /system-configs`
- `PUT /system-configs`
- `POST /system-configs/reload`

Important config groups:

- `sec.*`
- `system.*`
- `ui.*`
- `notification.*`
- `telegram.*`

## Telegram

- `GET /telegram/config`
- `PUT /telegram/config`
- `POST /telegram/test`

## Logs

- `GET /operation-logs`
- `GET /notification-logs`

## System Health

- `GET /system-health`

Returns runtime status, health issues, target counts, filing counts, notification failures, Telegram status, SEC User-Agent, database metadata, and latest sync status.

## Export

These endpoints return raw downloadable files, not the standard API response wrapper:

- `GET /exports/filings.csv`
- `GET /exports/watch-targets.csv`
- `GET /exports/configs.json`
- `GET /exports/backup.json`
