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

Query params:

- `ticker`
- `status`
- `target_type`
- `group`
- `page`
- `page_size`

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

Default tasks include `notification_retry_sync` (`*/10 * * * *`, enabled), which retries due failed notification batches. Do not disable it unless notification recovery is intentionally paused.

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
- `GET /notification-batches?source=&status=&trigger=&date_from=&date_to=&page=&page_size=`
- `GET /notification-batches/:id/items?page=&page_size=`
- `POST /notification-batches/:id/retry`

Notification batch statuses are `pending`, `sent`, `suppressed`, `failed`, and `dead_letter`. The retry endpoint accepts only `failed` or `dead_letter` batches, resets their retry cycle, and makes them immediately due. It returns a validation error while a retry lease is active.

## IPO Monitoring

- `GET /ipo-health`
- `GET /ipo-companies?company_name=&cik=&status=&attention=&sort_by=&sort_order=&page=&page_size=`
- `GET /ipo-companies/:cik/offerings?page=&page_size=`
- `PUT /ipo-companies/:cik/override`
- `GET /ipo-filings?company_name=&cik=&filing_type=&notified=&sort=&page=&page_size=`
- `POST /ipo-filings/refresh`

`GET /ipo-health` returns `pending_listing`, `missing_market_mapping`, `stale_lifecycle_checks`, `unsupported_offering_events`, `failed_notification_batches`, `due_retry_batches`, `dead_letter_batches`, and `latest_sync`. Operators should use these counts after setup or a restart to verify the IPO monitor.

Company `status` values are `new`, `updating`, `effective`, `priced`, `listing_pending`, `listed`, `withdrawn`, and `stale`. Valid company `attention` filters are `listing_pending`, `parse_failed`, `lifecycle_stale`, and `notification_failed`.

`listing_pending` means SEC has supplied a ticker but exchange confirmation is still missing. `parse_failed` selects companies with unsupported 424B4 offering events; `lifecycle_stale` selects active companies overdue under `ipo.lifecycle_recheck_hours`; `notification_failed` selects companies with failed or dead-letter IPO notification batches.

## System Health

- `GET /system-health`

Returns runtime status, health issues, target counts, filing counts, notification failures, Telegram status, SEC User-Agent, database metadata, and latest sync status.

## Export

These endpoints return raw downloadable files, not the standard API response wrapper:

- `GET /exports/filings.csv`
- `GET /exports/watch-targets.csv`
- `GET /exports/configs.json`
- `GET /exports/backup.json`
