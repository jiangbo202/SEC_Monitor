# Notification Batches And IPO Closure Design

## Goal

Prevent initial/history imports from flooding Telegram, aggregate each synchronization into one explainable notification batch, and complete the IPO workflow with SEC-backed market status plus manual correction.

## Scope

This design covers:

- Watch-target filing synchronization notifications.
- IPO current-feed and lifecycle-backfill notifications.
- Notification batch persistence, API, and UI.
- SEC-backed IPO ticker and exchange verification.
- Best-effort 424B4 offer-price and offered-share extraction.
- Manual IPO market-field correction.

It does not add third-party market data, background message queues, AI summaries, multi-user inbox assignment, or automatic retry workers.

## Notification Architecture

### Data Model

Add `notification_batches`:

- `id`
- `sync_run_id`, indexed
- `source`: `filing` or `ipo`
- `trigger`
- `channel`
- `target`
- `status`: `sent`, `suppressed`, or `failed`
- `item_count`
- `sent_count`
- `suppressed_count`
- `failed_count`
- `retry_count`
- `suppression_summary`
- `error_message`
- `sent_at`
- timestamps

Add `notified_at` to regular `filings`, matching the field already present on `ipo_filings`. This provides a direct successful-delivery timestamp while batch items retain the audit trail.

Add `warning_message` to `sync_runs` so optional enrichment failures can be shown without incorrectly changing successful SEC ingestion to a failed run.

Add `notification_batch_items`:

- `id`
- `batch_id`, indexed
- `entity_kind`: `filing` or `ipo_filing`
- `filing_id`, indexed
- `ticker`
- `cik`
- `company_name`
- `filing_type`
- `title`
- `filing_url`
- `event_at`
- `status`: `sent`, `suppressed`, or `failed`
- `reason`: `eligible`, `initial_sync`, `history_backfill`, `lifecycle_backfill`, `rule_filtered`, `quiet_hours`, or `delivery_failed`
- timestamps

Keep `notification_logs` unchanged for historical compatibility. New batch sends are represented by the batch tables, not duplicated into the legacy table.

### Suppression Rules

Watch-target filings:

1. If `WatchTarget.LastSyncAt` is nil at the start of the run, all newly stored filings for that target are `initial_sync` and are not sent.
2. On later runs, compare the filing publication timestamp to the target's previous successful sync time.
3. A filing whose publication timestamp is older than the previous successful sync is `history_backfill` and is not sent.
4. If publication time is absent, a filing date before the previous successful sync date is historical; the same date remains eligible to avoid suppressing a genuinely new same-day filing.
5. Existing form, keyword, importance, and quiet-hour rules still apply after history classification.

IPO filings:

1. If no IPO filings exist when a scan starts, the scan establishes an `initial_sync` baseline and sends nothing.
2. New items from later SEC current-feed scans can be sent.
3. Files found by company lifecycle backfill are always `lifecycle_backfill` and are never sent.
4. Existing IPO form-type notification filters still apply.

Suppressed and filtered items are persisted as batch items so the user can see why no alert was sent.

### Batch Delivery

Each sync run creates at most one notification batch per source.

After all database writes finish:

1. Collect eligible items.
2. Group them by ticker for watched filings or company/CIK for IPO filings.
3. Render one Telegram message with run type, total count, grouped counts, and up to 10 filing lines.
4. If more than 10 items are eligible, show the remaining count without adding more lines.
5. Keep the message below Telegram's message-size limit.
6. Send with the existing three-attempt retry behavior.
7. On success, mark eligible batch items `sent`, set the batch `sent`, and update filing `notified_at` fields.
8. On failure, mark eligible items and the batch `failed`; do not update filing notification timestamps.
9. If a run has only suppressed items, persist a `suppressed` batch without making a Telegram request.
10. If a run has no new items, no batch is required.

The synchronization itself succeeds even when Telegram delivery fails. Delivery failure is visible through the batch status and system health rather than changing successful SEC ingestion into a failed ingestion run.

## Notification API And UI

Add:

- `GET /api/notification-batches`
  - Filters: `source`, `status`, `trigger`, `date_from`, `date_to`.
  - Pagination: standard `page`, `page_size`, `total`, `items`.
  - Default order: `created_at DESC, id DESC`.
- `GET /api/notification-batches/:id/items`
  - Default order: `event_at DESC, id DESC`.

The notification page becomes two tabs:

1. `Notification Batches` is the default. It shows run time, source, trigger, total, sent, suppressed, status, and error. Rows expand to item details and suppression reasons.
2. `Legacy Logs` keeps the current notification-log view unchanged.

The dashboard recent-notification panel uses batches and displays one row per synchronization rather than one row per filing.

## IPO Closure Architecture

### Data Model

Add `ipo_company_market_data`:

- `id`
- `cik`, unique
- `ticker`
- `exchange`
- `offer_price`, decimal-compatible string
- `shares_offered`, integer
- `listed_verified_at`
- `ticker_source`
- `offering_source`
- `ticker_confidence`
- `offering_confidence`
- timestamps

Extend `ipo_company_overrides` with nullable manual fields:

- `exchange`
- `offer_price`
- `shares_offered`
- `listing_date`

The existing manual `final_ticker`, status, and note fields remain.

Automatic data and manual corrections remain separate. API responses merge them with manual values taking precedence.

### SEC Company Mapping

Extend the SEC client with a CIK-indexed listed-company mapping sourced from an official SEC ticker/exchange dataset.

During each IPO scan:

1. Fetch the mapping once, not once per company.
2. Normalize CIK values before matching.
3. For every stored IPO company found in the mapping, upsert ticker and exchange.
4. Set `listed_verified_at` only on the first successful match.
5. Mark ticker source as SEC and confidence as high.
6. Never overwrite manual override values.

Failure to fetch or parse this optional mapping records a scan warning and leaves previous market data unchanged. Current filing ingestion continues.

### 424B4 Extraction

When a newly discovered filing type starts with `424B4`:

1. Fetch the filing document through the SEC client using the configured User-Agent and timeout.
2. Convert visible document text to normalized whitespace.
3. Apply conservative extraction patterns for an explicit per-share offer price and offered-share count.
4. Save only unambiguous positive values.
5. Mark source as the filing URL and confidence as medium.
6. Do not overwrite an existing manual override.
7. Parsing or document-fetch failure is non-fatal and does not prevent filing storage.

This is best-effort extraction, not a guarantee that every prospectus format is supported.

### IPO Status Resolution

Merged status precedence:

1. Manual status override.
2. SEC listed-company mapping match: `listed`, high confidence.
3. Existing filing-based inference: withdrawn, priced, effective, updating, new, or stale.

`listed_verified_at` means the first time SEC's mapping confirmed the company. It is not labeled as the actual first trading date. The optional manual `listing_date` is displayed separately as the actual/confirmed date entered by the user.

## IPO API And UI

Extend IPO company responses with:

- `final_ticker`
- `exchange`
- `offer_price`
- `shares_offered`
- `listed_verified_at`
- `listing_date`
- `market_data_source`
- `market_data_confidence`
- `market_data_updated_at`

Extend `PUT /api/ipo-companies/:cik/override` with exchange, offer price, shares offered, and listing date. Empty fields clear the corresponding manual override.

Company view:

- Add compact final-Ticker, exchange, and offer-price columns.
- Keep the current latest-update default sort.
- Preserve responsive horizontal scrolling.

Company detail drawer:

- Show automatic value, effective value, source, confidence, and update time.
- Allow manual editing of status, final ticker, exchange, offer price, shares offered, listing date, and note.
- Clearly label `SEC first verified` separately from `Listing date`.

IPO company CSV export includes all effective market fields and their source/update metadata.

## Error Handling

- SEC filing ingestion and Telegram delivery are separate outcomes.
- Database errors remain fatal for the active operation.
- Telegram failures persist a failed batch but do not roll back stored filings.
- SEC market-mapping and 424B4 extraction failures populate the sync run warning and preserve previous IPO market data.
- Invalid offer price, share count, listing date, status, or CIK returns `validation_failed`.
- Secrets and Telegram targets remain masked where existing APIs require masking.

## Testing

Use table-driven Go tests for:

- Initial watch-target sync suppression.
- Later historical backfill suppression.
- Same-day fallback eligibility without publication time.
- IPO initial-baseline and lifecycle-backfill suppression.
- Notification rule and quiet-hour reasons.
- One batch per source per sync run.
- Telegram summary truncation and grouping.
- Successful and failed batch state transitions.
- SEC CIK mapping normalization and non-fatal failures.
- 424B4 price/share extraction across supported and unsupported text.
- Automatic versus manual market-data precedence.
- IPO listed-status precedence.
- API pagination, filters, batch item details, and override validation.

Frontend verification covers:

- Batch and legacy tabs.
- Expandable batch items and suppression labels.
- Dashboard batch summary.
- IPO market columns and detail editing.
- Chinese and English labels.

Required verification remains:

- `go test ./... -coverprofile=/tmp/sec_monitor_cover.out`
- Total Go statement coverage at least 80%.
- `npm run build` in `web`.
- Browser checks for notification batches and IPO company details.

## Migration And Compatibility

- GORM AutoMigrate creates the two batch tables and IPO market table, and adds override columns.
- Existing filings, IPO filings, overrides, notification logs, and sync runs remain unchanged.
- Existing notification-log API remains available.
- No historical notification-log backfill into batches is attempted.
