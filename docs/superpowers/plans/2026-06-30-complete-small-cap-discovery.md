# Complete Small-Cap Discovery Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Finish the remaining small-cap discovery product surface: automatic candidate notification, task configuration, candidate drill-down evidence, data quality visibility, workflow orchestration, optional social heat scaffolding, sector explainability, and report/archive views.

**Architecture:** Keep discovery data in the discovery database and operational notification/task state in the main database. Add read-only evidence/detail/report APIs and frontend views first, then wire scheduler execution through existing `TaskConfig` and `Scheduler.RunTask`. Social heat is implemented as a compliant optional provider scaffold and manual snapshot model; it remains disabled unless explicitly configured.

**Tech Stack:** Go 1.24, Gin, GORM, SQLite, robfig/cron, Vue 3, TypeScript, Element Plus.

---

### Task 1: Candidate notification scheduler

**Files:**
- Modify: `internal/service/task_config.go`
- Modify: `internal/scheduler/scheduler.go`
- Modify: `internal/scheduler/scheduler_test.go`
- Modify: `internal/api/router/router.go`

- [x] Add default task `candidate_notification_sync` disabled by default with daily cron.
- [x] Extend scheduler constructor to accept `*service.CandidateNotificationService`.
- [x] Route `candidate_notification_sync` to `CandidateNotificationService.Send(ctx, Confirm: true)`.
- [x] Tests: default task exists; disabled task does not run; manual run sends candidate notification.

### Task 2: Candidate detail evidence API

**Files:**
- Create: `internal/discovery/candidate_detail.go`
- Create: `internal/discovery/candidate_detail_test.go`
- Modify: `internal/api/handler/app.go`
- Modify: `internal/api/handler/app_test.go`
- Modify: `internal/api/router/router.go`

- [x] Add `GetCandidateDetail(ctx, db, ticker)` for current batch.
- [x] Return score snapshot plus latest financial metric, insider transactions, capital risks, data quality, and evidence summary.
- [x] API: `GET /api/discovery/candidates/:ticker/detail`.
- [x] Tests: returns current batch evidence and unknown ticker behavior.

### Task 3: Candidate detail drawer UI

**Files:**
- Modify: `web/src/api/types.ts`
- Modify: `web/src/api/types.typecheck.ts`
- Modify: `web/src/views/DiscoveryCandidatesView.vue`

- [x] Add detail types.
- [x] Add row action to open detail drawer.
- [x] Display score breakdown, evidence, data quality, insider, capital risk, and sector explanation sections.
- [x] Build validation via `npm run build`.

### Task 4: Data source health and missing-data visibility

**Files:**
- Create: `internal/discovery/candidate_health.go`
- Create: `internal/discovery/candidate_health_test.go`
- Modify: `internal/api/handler/app.go`
- Modify: `internal/api/router/router.go`
- Modify: `web/src/views/DiscoveryCandidatesView.vue`

- [x] Add summary of missing/stale financial, insider, market cap, and capital risk evidence from current batch.
- [x] API: `GET /api/discovery/candidates/health`.
- [x] UI alert card above candidate table.

### Task 5: One-click discovery workflow

**Files:**
- Create: `internal/service/discovery_workflow.go`
- Create: `internal/service/discovery_workflow_test.go`
- Modify: `internal/api/handler/app.go`
- Modify: `internal/api/router/router.go`
- Modify: `web/src/views/DiscoveryCandidatesView.vue`

- [x] Add workflow service that returns current candidates and health status without external network calls.
- [x] API: `POST /api/discovery/candidates/refresh`.
- [x] UI button “刷新候选工作流”.

### Task 6: Optional social heat scaffold

**Files:**
- Modify: `internal/discovery/models.go`
- Modify: `internal/discovery/database.go`
- Create: `internal/discovery/social_heat.go`
- Create: `internal/discovery/social_heat_test.go`
- Modify: `internal/service/config.go`
- Modify: `internal/service/service_test.go`

- [x] Add `SocialHeatSnapshot` model with provider, mentions, baseline, score, status, and evidence URL.
- [x] Add disabled-by-default config keys for social heat.
- [x] Add manual snapshot upsert/query helpers.
- [x] Do not perform unauthenticated Reddit scraping.

### Task 7: Sector explainability

**Files:**
- Create: `internal/discovery/sector_explain.go`
- Create: `internal/discovery/sector_explain_test.go`
- Modify: `internal/discovery/candidate_detail.go`

- [x] Add sector score explanation mapping using SIC and score fields.
- [x] Expose in candidate detail.

### Task 8: Candidate daily report/archive

**Files:**
- Create: `internal/discovery/candidate_report.go`
- Create: `internal/discovery/candidate_report_test.go`
- Modify: `internal/api/handler/app.go`
- Modify: `internal/api/router/router.go`
- Modify: `web/src/views/DiscoveryCandidatesView.vue`

- [x] Add `GET /api/discovery/candidates/report?date=YYYY-MM-DD`.
- [x] Use current/latest batch if date not supplied.
- [x] Return A/B counts, top candidates, message, data-quality summary.
- [x] UI: report dialog.

### Task 9: Notification history UX polish

**Files:**
- Modify: `web/src/views/NotificationLogsView.vue`

- [x] Add candidate-specific source/status support in notification history from previous commit.
- [x] Keep SEC/IPO behavior unchanged.

### Task 10: Documentation and final verification

**Files:**
- Modify: `docs/superpowers/specs/2026-06-22-small-cap-discovery-design.md`
- Modify: `docs/superpowers/specs/2026-06-22-small-cap-social-heat-design.md`

- [x] Document implemented endpoints, disabled-by-default social heat, scheduling, force resend, and remaining operational caveats.
- [x] Run `go test ./... -count=1`.
- [x] Run `npm run build`.
- [x] Run coverage and confirm total `>= 80%`.
- [x] Commit completed changes.
