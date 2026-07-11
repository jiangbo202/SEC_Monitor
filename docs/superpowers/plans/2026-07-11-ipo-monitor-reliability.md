# IPO Monitor Reliability Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Secure sensitive configuration and make IPO lifecycle detection, notification delivery, health reporting, and operator workflows reliable without adding non-SEC market-data dependencies.

**Architecture:** Add a configuration cryptography boundary in `internal/service`, then make notification batches durable retry jobs. Extend the IPO service with explicit lifecycle polling and a distinct `listing_pending` state. Expose a single IPO health summary to the Vue views, retaining GORM AutoMigrate and the current Gin API conventions.

**Tech Stack:** Go 1.24, Gin, GORM/SQLite, AES-256-GCM from the Go standard library, Vue 3, TypeScript, Element Plus, Vitest type-check script, Docker Compose.

## Global Constraints

- Keep SQLite as the default database and use GORM AutoMigrate; do not delete business history.
- Use only SEC public endpoints for IPO lifecycle and listing data.
- Sensitive values must never be returned by APIs, stored in notification error text, or logged unmasked.
- Keep manual IPO overrides higher priority than automated signals.
- Add Go tests before production code for every behavior change; run `gofmt` on changed Go files.
- Before each commit run `go test ./...`, `npm run build` from `web`, and `git status --short`.

---

## File Structure

- `internal/config/config.go`: parse and validate `CONFIG_ENCRYPTION_KEY`.
- `internal/service/config.go`: encrypt/decrypt system configuration, migrate legacy secrets, report key health.
- `internal/service/sensitive.go`: redact URL and header secrets before persistence or API delivery.
- `internal/model/notification_batch.go`: durable retry timestamps and dead-letter state fields.
- `internal/service/notification_batch.go`: retry scheduling, manual requeue, error sanitization.
- `internal/scheduler/scheduler.go`: independent per-task execution guards and retry task dispatch.
- `internal/model/ipo_company_market_data.go`: lifecycle check timestamp and listing evidence fields.
- `internal/model/ipo_offering_event.go`: parser diagnostic message.
- `internal/service/ipo_radar.go`: lifecycle polling, `listing_pending`, parser diagnostics, IPO health summary.
- `internal/api/handler/app.go` and `internal/api/router/router.go`: health and retry endpoints.
- `web/src/api/types.ts`, `web/src/views/IPORadarView.vue`, `web/src/views/NotificationLogsView.vue`, `web/src/views/DashboardView.vue`, `web/src/i18n/index.ts`: operator-facing status, retry, health, and responsive table updates.
- `docker-compose.yml`, `README.md`, `docs/config/README.md`: encryption key configuration and task operational guide.
- `internal/service/service_test.go`, `internal/service/notification_batch_test.go`, `internal/sec/ipo_market_test.go`, `internal/api/handler/app_test.go`, `internal/scheduler/scheduler_test.go`, `internal/config/config_test.go`: regression coverage.

### Task 1: Encrypt system secrets and redact notification errors

**Files:**
- Create: `internal/service/sensitive.go`
- Modify: `internal/config/config.go`, `internal/config/config_test.go`
- Modify: `internal/service/config.go`, `internal/service/service_test.go`
- Modify: `internal/service/notification_batch.go`, `internal/service/filing.go`
- Modify: `internal/api/handler/app.go`, `docker-compose.yml`, `README.md`, `docs/config/README.md`

**Interfaces:**
- Produces `config.SystemConfig.EncryptionKey []byte`, `EncryptionKeyError string`, and `ConfigService.EncryptionHealth() service.EncryptionHealth` without changing the existing `config.Load() Config` signature.
- Produces `SanitizeSensitiveError(string) string`, used before notification error persistence and response serialization.
- Produces `ConfigService.MigrateEncryptedValues(context.Context) error`, called during application startup after defaults are available.

- [ ] **Step 1: Write failing config/service tests**

```go
func TestConfigLoadReportsInvalidEncryptionKey(t *testing.T) {
    t.Setenv("CONFIG_ENCRYPTION_KEY", "invalid")
    if Load().System.EncryptionKeyError == "" { t.Fatal("expected invalid key error") }
}

func TestConfigServiceEncryptsMigratesAndMasksSecrets(t *testing.T) {
    // Seed plaintext encrypted=true telegram token; migrate with a 32-byte key.
    // Assert DB value starts with "enc:v1:", GetValue returns plaintext,
    // and List(..., true) returns the masked marker.
}

func TestSanitizeSensitiveErrorMasksTelegramURL(t *testing.T) {
    got := SanitizeSensitiveError(`Post "https://api.telegram.org/bot123:secret/sendMessage": timeout`)
    if strings.Contains(got, "123:secret") { t.Fatal("token leaked") }
}
```

- [ ] **Step 2: Run tests to verify RED**

Run: `go test ./internal/config ./internal/service -run 'Test(ConfigLoadReportsInvalidEncryptionKey|ConfigServiceEncryptsMigratesAndMasksSecrets|SanitizeSensitiveErrorMasksTelegramURL)' -count=1`

Expected: FAIL because the encryption key, migration, and sanitizer APIs do not exist.

- [ ] **Step 3: Implement minimum cryptography and redaction boundary**

```go
// enc:v1:<base64(nonce|ciphertext)>, AES-256-GCM only.
func (s *ConfigService) encryptSecret(plain string) (string, error)
func (s *ConfigService) decryptSecret(stored string) (string, error)
func SanitizeSensitiveError(value string) string
```

Parse `CONFIG_ENCRYPTION_KEY` as Base64 32 bytes. Preserve legacy reads when no key is configured, but make `EncryptionHealth` critical and reject writes of new non-empty `Encrypted` values. Migrate existing encrypted rows transactionally when a valid key exists. Sanitize notification batch and legacy notification log errors before database writes and when list APIs return rows. Add Compose passthrough and documented `openssl rand -base64 32` setup.

- [ ] **Step 4: Run focused tests to verify GREEN**

Run: `go test ./internal/config ./internal/service -run 'Test(ConfigLoadReportsInvalidEncryptionKey|ConfigServiceEncryptsMigratesAndMasksSecrets|SanitizeSensitiveErrorMasksTelegramURL)' -count=1`

Expected: PASS.

- [ ] **Step 5: Commit security boundary**

```bash
git add internal/config internal/service internal/api/handler/app.go docker-compose.yml README.md docs/config/README.md
git commit -m "fix: protect system configuration secrets"
```

### Task 2: Persist notification retries and expose manual requeue

**Files:**
- Modify: `internal/model/notification_batch.go`, `internal/database/database.go`
- Modify: `internal/service/notification_batch.go`, `internal/service/notification_batch_test.go`
- Modify: `internal/api/handler/app.go`, `internal/api/router/router.go`, `internal/api/handler/app_test.go`
- Modify: `web/src/api/types.ts`, `web/src/views/NotificationLogsView.vue`, `web/src/i18n/index.ts`

**Interfaces:**
- Produces `NotificationBatch.NextRetryAt`, `LastAttemptAt`, and status `dead_letter`.
- Produces `NotificationBatchService.RetryDue(ctx, now) (NotificationRetryResult, error)` and `Requeue(ctx, batchID, now) (model.NotificationBatch, error)`.
- Produces `POST /notification-batches/:id/retry`.

- [ ] **Step 1: Write failing retry tests**

```go
func TestNotificationBatchFailureSchedulesExponentialRetry(t *testing.T) {
    // Failed first delivery must set status=failed, retry_count=1,
    // next_retry_at=now+5m, and a sanitized error.
}

func TestRetryDueSendsAndDeadLettersAfterFiveRounds(t *testing.T) {
    // Fake notifier fails five rounds; due jobs eventually become dead_letter.
}

func TestRequeueOnlyAcceptsFailedOrDeadLetter(t *testing.T) {
    // sent and suppressed return ErrValidation; failed resets next_retry_at to now.
}
```

- [ ] **Step 2: Run RED tests**

Run: `go test ./internal/service -run 'Test(NotificationBatchFailureSchedulesExponentialRetry|RetryDueSendsAndDeadLettersAfterFiveRounds|RequeueOnlyAcceptsFailedOrDeadLetter)' -count=1`

Expected: FAIL because durable retry state and methods do not exist.

- [ ] **Step 3: Implement retry state and API**

Use delays `[5m, 15m, 45m, 2h, 6h]`; `RetryCount` counts delivery rounds, not low-level HTTP attempts. The initial delivery creates `failed` plus `NextRetryAt` after its transient attempts fail. `RetryDue` locks each selected batch transactionally, sends the original eligible items, and transitions it to `sent`, next `failed`, or `dead_letter`. Add the handler and response type; page exposes dead-letter filter, next retry time, retry count, and a confirmation-free “重新投递” action for failed/dead-letter rows.

- [ ] **Step 4: Run focused API and service tests**

Run: `go test ./internal/service ./internal/api/handler -run 'Test(NotificationBatchFailureSchedulesExponentialRetry|RetryDueSendsAndDeadLettersAfterFiveRounds|RequeueOnlyAcceptsFailedOrDeadLetter|TestAppHandler.*Notification)' -count=1`

Expected: PASS.

- [ ] **Step 5: Commit notification reliability**

```bash
git add internal/model internal/database internal/service/notification_batch.go internal/api web/src/api/types.ts web/src/views/NotificationLogsView.vue web/src/i18n/index.ts
git commit -m "feat: retry failed notification batches"
```

### Task 3: Allow independent scheduled tasks and run due notification retries

**Files:**
- Modify: `internal/service/task_config.go`, `internal/service/service_test.go`
- Modify: `internal/scheduler/scheduler.go`, `internal/scheduler/scheduler_test.go`
- Modify: `internal/api/router/router.go`

**Interfaces:**
- Produces task name `notification_retry_sync` with default cron `*/10 * * * *`.
- `Scheduler.RunTask(ctx, name)` prevents duplicate `name` runs but allows different task names concurrently.

- [ ] **Step 1: Write failing scheduler tests**

```go
func TestSchedulerAllowsDifferentTasksConcurrently(t *testing.T) {
    // Block discovery task; assert ipo task starts before discovery unblocks.
}

func TestSchedulerSuppressesDuplicateTaskRun(t *testing.T) {
    // Block first ipo run; second ipo run must not invoke the service twice.
}

func TestTaskConfigAddsNotificationRetryDefault(t *testing.T) {
    // Ensure default task name, enabled state and cron are persisted.
}
```

- [ ] **Step 2: Run RED tests**

Run: `go test ./internal/scheduler ./internal/service -run 'Test(SchedulerAllowsDifferentTasksConcurrently|SchedulerSuppressesDuplicateTaskRun|TaskConfigAddsNotificationRetryDefault)' -count=1`

Expected: FAIL because `Scheduler` has one global `running` flag and no retry task.

- [ ] **Step 3: Implement per-task guard and retry dispatch**

Replace `running bool` with `runningTasks map[string]bool` guarded by the existing mutex. Inject `NotificationBatchService` into the scheduler in the same style as other services, dispatch `RetryDue(ctx, time.Now().UTC())`, and update task setup/router wiring. A skipped duplicate returns nil without modifying the active run record; a different task has its own running key.

- [ ] **Step 4: Run scheduler tests**

Run: `go test ./internal/scheduler ./internal/service -run 'Test(SchedulerAllowsDifferentTasksConcurrently|SchedulerSuppressesDuplicateTaskRun|TaskConfigAddsNotificationRetryDefault)' -count=1`

Expected: PASS.

- [ ] **Step 5: Commit scheduling change**

```bash
git add internal/scheduler internal/service/task_config.go internal/service/service_test.go internal/api/router/router.go
git commit -m "fix: isolate scheduled task execution"
```

### Task 4: Add explicit IPO lifecycle polling and listing-pending status

**Files:**
- Modify: `internal/model/ipo_company_market_data.go`, `internal/service/ipo_radar.go`, `internal/service/config.go`
- Modify: `internal/service/service_test.go`, `internal/sec/client_test.go`
- Modify: `internal/api/handler/app.go`, `web/src/api/types.ts`, `web/src/views/IPORadarView.vue`, `web/src/i18n/index.ts`

**Interfaces:**
- Extends `IPORadarSettings` with `LifecycleSweepEnabled`, `LifecycleMaxCIKs`, `LifecycleRecheckHours`.
- Adds status `listing_pending` and `IPOCompanyItem.LifecycleCheckedAt`.
- Produces `RefreshWithTrigger` ingestion for current `EFFECT`, `424B4`, and `RW` plus deterministic active-CIK sweep.

- [ ] **Step 1: Write failing lifecycle/status tests**

```go
func TestIPORadarTickerWithoutExchangeIsListingPending(t *testing.T) {
    // SEC ticker with empty exchange becomes listing_pending and medium confidence.
}

func TestIPORadarWithdrawalOverridesListingMapping(t *testing.T) {
    // RW filing must return withdrawn even when market mapping looks listed.
}

func TestIPORadarLifecycleSweepRotatesOldestActiveCIKs(t *testing.T) {
    // Seed three active CIKs, max=2; verify oldest lifecycle checks are selected and timestamps advance.
}

func TestIPORadarCurrentLifecycleFormsAreIngested(t *testing.T) {
    // EFFECT/424B4/RW from current feed create lifecycle filings without a fresh S-1.
}
```

- [ ] **Step 2: Run RED tests**

Run: `go test ./internal/service ./internal/sec -run 'TestIPORadar(TickerWithoutExchangeIsListingPending|WithdrawalOverridesListingMapping|LifecycleSweepRotatesOldestActiveCIKs|CurrentLifecycleFormsAreIngested)' -count=1`

Expected: FAIL because the pending state, sweep selection, and current lifecycle requests are absent.

- [ ] **Step 3: Implement lifecycle ingestion and deterministic status priority**

Add default system configurations and validation bounds (`max CIKs 1..200`, recheck hours `1..168`). Build the current query set by unioning configured registration forms with required lifecycle forms. Select active CIKs with the oldest null/expired `LifecycleCheckedAt`, limit them, backfill their submissions, and mark checks only after a successful fetch. Evaluate status in this exact order: manual override, withdrawal, confirmed listing, pending SEC ticker confirmation, priced, effective, stale, amendment, initial registration. Add Chinese/English labels, status reason, a status filter option, and show lifecycle check time in details.

- [ ] **Step 4: Run focused tests**

Run: `go test ./internal/service ./internal/sec -run 'TestIPORadar(TickerWithoutExchangeIsListingPending|WithdrawalOverridesListingMapping|LifecycleSweepRotatesOldestActiveCIKs|CurrentLifecycleFormsAreIngested)' -count=1`

Expected: PASS.

- [ ] **Step 5: Commit lifecycle reliability**

```bash
git add internal/model/ipo_company_market_data.go internal/service/ipo_radar.go internal/service/config.go internal/service/service_test.go internal/sec web/src/api/types.ts web/src/views/IPORadarView.vue web/src/i18n/index.ts
git commit -m "feat: monitor IPO lifecycle confirmations"
```

### Task 5: Improve 424B4 parsing and record diagnostics

**Files:**
- Modify: `internal/model/ipo_offering_event.go`, `internal/sec/ipo_market.go`, `internal/sec/ipo_market_test.go`
- Modify: `internal/service/ipo_radar.go`, `internal/service/service_test.go`
- Modify: `web/src/api/types.ts`, `web/src/views/IPORadarView.vue`, `web/src/i18n/index.ts`

**Interfaces:**
- Extends `IPOOffering` with `ParseMessage string` and `IPOOfferingEvent.ParseMessage string`.
- Raises `ipoOfferingParserVersion` by one.

- [ ] **Step 1: Write failing parser tests**

```go
func TestParse424B4OfferingReadsLabeledTableValues(t *testing.T) {
    // HTML table with "Public offering price" and "Shares offered" yields parsed values.
}

func TestParse424B4OfferingReportsMissingShareCount(t *testing.T) {
    // Price-only document returns ok=false and ParseMessage="shares_offered_not_found".
}
```

- [ ] **Step 2: Run RED tests**

Run: `go test ./internal/sec -run 'TestParse424B4Offering(ReadsLabeledTableValues|ReportsMissingShareCount)' -count=1`

Expected: FAIL because parser diagnostics and table extraction do not exist.

- [ ] **Step 3: Implement parser version and diagnostics**

Normalize table cell boundaries into labeled text before applying regexes. Accept only an explicit public offering price and base offered-share count; calculate gross proceeds from those two values. Store `parse_status=unsupported` plus the exact stable message for missing fields, malformed values, or fetch failure. Existing events with an older parser version become pending and are reprocessed. Display the message in the IPO detail offering table.

- [ ] **Step 4: Run parser and service tests**

Run: `go test ./internal/sec ./internal/service -run 'Test(Parse424B4Offering|IPORadar.*Offering)' -count=1`

Expected: PASS.

- [ ] **Step 5: Commit parser diagnostics**

```bash
git add internal/model/ipo_offering_event.go internal/sec/ipo_market.go internal/sec/ipo_market_test.go internal/service/ipo_radar.go internal/service/service_test.go web/src/api/types.ts web/src/views/IPORadarView.vue web/src/i18n/index.ts
git commit -m "feat: explain IPO offering parse results"
```

### Task 6: Publish IPO health and an operator attention queue

**Files:**
- Modify: `internal/service/ipo_radar.go`, `internal/service/notification_batch.go`, `internal/service/service_test.go`
- Modify: `internal/api/handler/app.go`, `internal/api/router/router.go`, `internal/api/handler/app_test.go`
- Modify: `web/src/api/types.ts`, `web/src/views/IPORadarView.vue`, `web/src/views/DashboardView.vue`, `web/src/i18n/index.ts`

**Interfaces:**
- Produces `IPORadarHealth` via `GET /ipo-health`.
- Extends `IPOCompanyFilter` with `Attention string` values: `listing_pending`, `parse_failed`, `lifecycle_stale`, `notification_failed`.

- [ ] **Step 1: Write failing health/filter tests**

```go
func TestIPORadarHealthCountsOperatorAttention(t *testing.T) {
    // Seed one pending listing, one stale lifecycle check, one unsupported offering,
    // and one dead-letter batch; assert each count and latest IPO run are returned.
}

func TestIPOCompanyAttentionFilterReturnsPendingListing(t *testing.T) {
    // listing_pending filter returns only the pending CIK.
}
```

- [ ] **Step 2: Run RED tests**

Run: `go test ./internal/service ./internal/api/handler -run 'Test(IPO(RadarHealthCountsOperatorAttention|CompanyAttentionFilterReturnsPendingListing)|AppHandler.*IPOHealth)' -count=1`

Expected: FAIL because health aggregation and attention filter do not exist.

- [ ] **Step 3: Implement health aggregation and UI**

Return counts for pending listing, missing market mapping, stale lifecycle checks, unsupported offering events, due retry batches, and dead letters; include latest IPO sync timestamp/status. The IPO page shows clickable health tags that set the attention filter. The dashboard adds an error alert when recent IPO notification delivery failed or dead letters exist. Fix table usability by pinning status/company/actions and keeping overflow fields in drawer details.

- [ ] **Step 4: Run focused tests and build web**

Run: `go test ./internal/service ./internal/api/handler -run 'Test(IPO(RadarHealthCountsOperatorAttention|CompanyAttentionFilterReturnsPendingListing)|AppHandler.*IPOHealth)' -count=1 && npm run build`

Workdir for build: `web`

Expected: PASS.

- [ ] **Step 5: Commit operator health workflow**

```bash
git add internal/service internal/api web/src/api/types.ts web/src/views/IPORadarView.vue web/src/views/DashboardView.vue web/src/i18n/index.ts
git commit -m "feat: surface IPO monitoring health"
```

### Task 7: Verify the integrated workflow and document operations

**Files:**
- Modify: `README.md`, `docs/config/README.md`, `docs/api/README.md`
- Modify: `internal/api/handler/app_test.go`, `internal/service/service_test.go`

**Interfaces:**
- Documents `CONFIG_ENCRYPTION_KEY`, `notification_retry_sync`, the lifecycle sweep settings, IPO health endpoint, manual retry endpoint, and all operator statuses.

- [ ] **Step 1: Add end-to-end regression tests**

```go
func TestIPOReliabilityWorkflow(t *testing.T) {
    // Encrypt a Telegram config, ingest a 424B4-only lifecycle event,
    // observe listing_pending, fail delivery once, run due retry successfully,
    // and assert IPO health returns no remaining failed notification.
}
```

- [ ] **Step 2: Run the integrated regression test**

Run: `go test ./internal/service ./internal/api/handler -run TestIPOReliabilityWorkflow -count=1`

Expected: PASS. This task adds no new production behavior; all behavior was test-driven in Tasks 1–6, and this test verifies their composition.

- [ ] **Step 3: Implement only missing integration glue and docs**

Document Docker setup:

```env
CONFIG_ENCRYPTION_KEY=<output of openssl rand -base64 32>
```

Document that users must restart `make docker-up` after setting the key, verify `/api/ipo-health`, and use Notification Logs to requeue dead letters. Do not add another data provider or change small-cap scoring.

- [ ] **Step 4: Run full verification**

Run:

```bash
go test ./...
cd web && npm run build
git status --short
```

Expected: all Go packages PASS, frontend build succeeds, and only intended source/docs changes remain.

- [ ] **Step 5: Commit documentation and integrated regression coverage**

```bash
git add README.md docs/config/README.md docs/api/README.md internal/api/handler/app_test.go internal/service/service_test.go
git commit -m "docs: operate reliable IPO monitoring"
```

## Plan Self-Review

- Spec coverage: Task 1 covers encryption and redaction; Tasks 2–3 cover persistent retry and scheduler isolation; Task 4 covers listing confirmation and lifecycle sweep; Task 5 covers 424B4 diagnostics; Task 6 covers health and operator UX; Task 7 covers integrated tests and operations documentation.
- Placeholder scan: no deferred implementation markers are used; every task has a concrete API, test name, commands, and commit boundary.
- Type consistency: `listing_pending`, `NotificationBatchService.RetryDue`, `NotificationBatchService.Requeue`, `IPORadarHealth`, and `notification_retry_sync` are defined once and used consistently in later tasks.
