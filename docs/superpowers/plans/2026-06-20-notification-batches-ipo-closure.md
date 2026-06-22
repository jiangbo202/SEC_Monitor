# Notification Batches And IPO Closure Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [x]`) syntax for tracking.

**Goal:** Suppress baseline/history notifications, send one explainable Telegram summary per sync, and enrich IPO companies with SEC-backed listing and offering data plus manual overrides.

**Architecture:** Add normalized notification batch and item models, then route both filing sync services through a shared batch service after database ingestion. Add an optional SEC market-enrichment client and conservative 424B4 parser; persist automatic IPO market data separately from manual overrides and merge them in the existing IPO company response.

**Tech Stack:** Go 1.24, Gin, GORM, SQLite, Vue 3, TypeScript, Element Plus

---

### Task 1: Persistence Models And Migration

**Files:**
- Create: `internal/model/notification_batch.go`
- Create: `internal/model/notification_batch_test.go`
- Create: `internal/model/ipo_company_market_data.go`
- Create: `internal/model/ipo_company_market_data_test.go`
- Modify: `internal/model/filing.go`
- Modify: `internal/model/ipo_company_override.go`
- Modify: `internal/model/sync_run.go`
- Modify: `internal/database/database.go`

- [x] **Step 1: Write failing table-name and migration tests**

Assert `NotificationBatch.TableName() == "notification_batches"`, `NotificationBatchItem.TableName() == "notification_batch_items"`, and `IPOCompanyMarketData.TableName() == "ipo_company_market_data"`. Extend database migration tests to create all new tables and columns.

- [x] **Step 2: Run model and database tests to verify RED**

Run: `GOCACHE=$(pwd)/.cache/go-build go test ./internal/model ./internal/database`

Expected: build failure because the new model types do not exist.

- [x] **Step 3: Add the models and fields**

Define `NotificationBatch` with sync-run/source/trigger/channel/target/status/count/retry/suppression/error/sent/timestamp fields and `NotificationBatchItem` with batch/entity/filing/company/form/title/url/event/status/reason fields. Define `IPOCompanyMarketData` with CIK, ticker, exchange, offer price, shares, listed verification, source, confidence, and timestamps. Add `NotifiedAt` to `Filing`, `WarningMessage` to `SyncRun`, and exchange/offer/shares/listing-date fields to `IPOCompanyOverride`.

- [x] **Step 4: Register AutoMigrate models and verify GREEN**

Run: `gofmt -w internal/model internal/database && GOCACHE=$(pwd)/.cache/go-build go test ./internal/model ./internal/database`

Expected: PASS.

### Task 2: Notification Batch Service And API

**Files:**
- Create: `internal/service/notification_batch.go`
- Modify: `internal/service/notification.go`
- Modify: `internal/service/service_test.go`
- Modify: `internal/api/handler/app.go`
- Modify: `internal/api/handler/app_test.go`
- Modify: `internal/api/router/router.go`
- Modify: `internal/api/router/router_test.go`

- [x] **Step 1: Write failing table-driven service tests**

Cover summary grouping, ten-item truncation, sent state, suppressed-only state, failed delivery, item pagination, source/status/trigger filters, and marking regular/IPO filings notified only after successful delivery.

- [x] **Step 2: Verify RED**

Run: `GOCACHE=$(pwd)/.cache/go-build go test ./internal/service -run 'TestNotificationBatch'`

Expected: build failure because `NotificationBatchService` does not exist.

- [x] **Step 3: Implement the batch service**

Add these public contracts:

```go
type NotificationCandidate struct {
    EntityKind, FilingID, Ticker, CIK, CompanyName string
    FilingType, Title, FilingURL, Status, Reason string
    EventAt time.Time
}

type NotificationBatchInput struct {
    SyncRunID uint
    Source, Trigger string
    Candidates []NotificationCandidate
}

func (s *NotificationBatchService) Deliver(ctx context.Context, input NotificationBatchInput) (model.NotificationBatch, error)
func (s *NotificationBatchService) List(ctx context.Context, filter NotificationBatchFilter) (PageResult[model.NotificationBatch], error)
func (s *NotificationBatchService) ListItems(ctx context.Context, batchID uint, page, pageSize int) (PageResult[model.NotificationBatchItem], error)
```

Render one Telegram summary, send with existing retry behavior, persist all item reasons, and update notification timestamps in one database transaction after successful delivery.

- [x] **Step 4: Add API handlers and routes**

Add `GET /api/notification-batches` and `GET /api/notification-batches/:id/items` with standard envelopes, filters, and pagination.

- [x] **Step 5: Verify GREEN**

Run: `gofmt -w internal/service internal/api && GOCACHE=$(pwd)/.cache/go-build go test ./internal/service ./internal/api/...`

Expected: PASS.

### Task 3: Filing Sync Suppression And Aggregation

**Files:**
- Modify: `internal/service/filing.go`
- Modify: `internal/service/service_test.go`
- Modify: `internal/api/router/router.go`

- [x] **Step 1: Write failing table-driven sync tests**

Cover first target sync as `initial_sync`, older publication time as `history_backfill`, same-day filing without publication time as eligible, notification-rule filtering, quiet-hours filtering, and one batch delivery after all targets finish.

- [x] **Step 2: Verify RED**

Run: `GOCACHE=$(pwd)/.cache/go-build go test ./internal/service -run 'TestFiling.*NotificationBatch|TestClassifyFilingNotification'`

Expected: FAIL because filing refresh still sends each filing immediately.

- [x] **Step 3: Implement classification and delayed delivery**

Replace `notifyNewFiling` calls with candidate collection. Classify against the target's previous `LastSyncAt`, then call the batch service once after all targets finish. Preserve SEC ingestion success when Telegram delivery fails and expose the failed batch independently.

- [x] **Step 4: Verify GREEN and regressions**

Run: `gofmt -w internal/service/filing.go internal/service/service_test.go && GOCACHE=$(pwd)/.cache/go-build go test ./internal/service`

Expected: PASS.

### Task 4: IPO Suppression And Batch Delivery

**Files:**
- Modify: `internal/service/ipo_radar.go`
- Modify: `internal/service/service_test.go`

- [x] **Step 1: Write failing table-driven IPO tests**

Cover first empty-database scan suppression, subsequent current-feed eligibility, lifecycle backfill suppression, IPO form-type filtering, one batch delivery, and failed delivery not setting `notified_at`.

- [x] **Step 2: Verify RED**

Run: `GOCACHE=$(pwd)/.cache/go-build go test ./internal/service -run 'TestIPORadar.*NotificationBatch'`

Expected: FAIL because IPO current filings still notify one by one and backfill reasons are not persisted.

- [x] **Step 3: Collect IPO candidates and deliver once**

Determine baseline state before scanning, collect current-feed and lifecycle candidates with explicit reasons, remove per-filing Telegram sends, and call the batch service once after ingestion.

- [x] **Step 4: Verify GREEN**

Run: `gofmt -w internal/service/ipo_radar.go internal/service/service_test.go && GOCACHE=$(pwd)/.cache/go-build go test ./internal/service`

Expected: PASS.

### Task 5: SEC IPO Market Enrichment

**Files:**
- Modify: `internal/sec/client.go`
- Modify: `internal/sec/client_test.go`
- Create: `internal/sec/ipo_market.go`
- Create: `internal/sec/ipo_market_test.go`
- Modify: `internal/service/ipo_radar.go`
- Modify: `internal/service/service_test.go`

- [x] **Step 1: Write failing SEC client and parser tests**

Use custom HTTP transports to cover the SEC ticker/exchange dataset, normalized CIK lookup, HTTP failures, valid 424B4 price/share text, ambiguous price text, and unsupported text.

- [x] **Step 2: Verify RED**

Run: `GOCACHE=$(pwd)/.cache/go-build go test ./internal/sec -run 'Test.*IPO|Test.*ListedCompany|Test.*424B4'`

Expected: build failure because market client contracts and parser do not exist.

- [x] **Step 3: Implement SEC market contracts**

Add:

```go
type ListedCompany struct { CIK, Name, Ticker, Exchange string }
type IPOOffering struct { OfferPrice string; SharesOffered int64; Source string; Confidence string }

func (c *HTTPClient) ListListedCompanies(ctx context.Context) ([]ListedCompany, error)
func (c *HTTPClient) FetchFilingDocument(ctx context.Context, filingURL string) (string, error)
func Parse424B4Offering(text string) (IPOOffering, bool)
```

Use the configured User-Agent and timeout. Parse only unambiguous positive values.

- [x] **Step 4: Integrate non-fatal enrichment**

After IPO ingestion, fetch the SEC mapping once, upsert automatic ticker/exchange matches, set first verification time, parse newly created 424B4 documents, and append optional failures to `SyncRun.WarningMessage`.

- [x] **Step 5: Verify GREEN**

Run: `gofmt -w internal/sec internal/service && GOCACHE=$(pwd)/.cache/go-build go test ./internal/sec ./internal/service`

Expected: PASS.

### Task 6: IPO Merge Rules, API, And Export

**Files:**
- Modify: `internal/service/ipo_radar.go`
- Modify: `internal/service/export.go`
- Modify: `internal/service/service_test.go`
- Modify: `internal/api/handler/app.go`
- Modify: `internal/api/handler/app_test.go`

- [x] **Step 1: Write failing precedence and validation tests**

Cover SEC-listed status over filing inference, manual status over SEC mapping, manual market fields over automatic fields, clearing nullable overrides, invalid price/share/date input, and CSV fields.

- [x] **Step 2: Verify RED**

Run: `GOCACHE=$(pwd)/.cache/go-build go test ./internal/service ./internal/api/handler -run 'TestIPO.*Market|Test.*IPO.*Export|Test.*Override'`

Expected: FAIL because market fields are absent from the response and override input.

- [x] **Step 3: Implement merged IPO company fields**

Load automatic market rows with overrides, apply manual precedence, expose source/confidence/update metadata, validate override input, and add fields to IPO CSV export.

- [x] **Step 4: Verify GREEN**

Run: `gofmt -w internal/service internal/api && GOCACHE=$(pwd)/.cache/go-build go test ./internal/service ./internal/api/...`

Expected: PASS.

### Task 7: Frontend Notification Batches

**Files:**
- Modify: `web/src/api/types.ts`
- Modify: `web/src/views/NotificationLogsView.vue`
- Modify: `web/src/views/DashboardView.vue`
- Modify: `web/src/i18n/index.ts`

- [x] **Step 1: Add batch API types and UI states**

Define `NotificationBatch` and `NotificationBatchItem`, then make the notification page default to batch rows with filters and expandable details while preserving legacy logs in a second tab.

- [x] **Step 2: Update dashboard summaries**

Load recent batches, show one row per run, and display sent/suppressed/failed counts instead of per-filing delivery rows.

- [x] **Step 3: Add Chinese and English labels**

Add source, trigger, suppression-reason, batch-status, count, and legacy-tab translations.

- [x] **Step 4: Verify frontend build**

Run: `npm run build` in `web`.

Expected: TypeScript and Vite build PASS.

### Task 8: Frontend IPO Closure

**Files:**
- Modify: `web/src/api/types.ts`
- Modify: `web/src/views/IPORadarView.vue`
- Modify: `web/src/i18n/index.ts`

- [x] **Step 1: Add effective market fields**

Add compact final-Ticker, exchange, and offer-price columns to company view. Extend the detail drawer with automatic/effective values, source, confidence, SEC verification time, manual listing date, shares, and editable overrides.

- [x] **Step 2: Add validation and localization**

Validate positive offer price/shares and ISO listing date before submitting. Add all Chinese and English labels and source descriptions.

- [x] **Step 3: Verify frontend build**

Run: `npm run build` in `web`.

Expected: PASS.

### Task 9: Documentation And Full Verification

**Files:**
- Modify: `README.md`
- Modify: `README.en.md`

- [x] **Step 1: Document notification and IPO behavior**

Explain silent baselines/history backfills, one-message summaries, batch logs, SEC-only IPO enrichment, best-effort 424B4 parsing, and manual precedence in both languages.

- [x] **Step 2: Run complete backend verification**

Run: `GOCACHE=$(pwd)/.cache/go-build go test ./... -coverprofile=/tmp/sec_monitor_cover.out`

Expected: all packages PASS.

- [x] **Step 3: Verify coverage**

Run: `GOCACHE=$(pwd)/.cache/go-build go tool cover -func=/tmp/sec_monitor_cover.out`

Expected: total statement coverage is at least 80%.

- [x] **Step 4: Run production frontend build**

Run: `npm run build` in `web`.

Expected: PASS.

- [ ] **Step 5: Browser verification**

Verify notification batch expansion, legacy logs, dashboard batch summary, IPO market columns, and manual override behavior on desktop. Confirm no framework overlay or relevant console errors.
