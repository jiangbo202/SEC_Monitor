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

- [ ] Add default task `candidate_notification_sync` disabled by default with daily cron.
- [ ] Extend scheduler constructor to accept `*service.CandidateNotificationService`.
- [ ] Route `candidate_notification_sync` to `CandidateNotificationService.Send(ctx, Confirm: true)`.
- [ ] Tests: default task exists; disabled task does not run; manual run sends candidate notification.

### Task 2: Candidate detail evidence API

**Files:**
- Create: `internal/discovery/candidate_detail.go`
- Create: `internal/discovery/candidate_detail_test.go`
- Modify: `internal/api/handler/app.go`
- Modify: `internal/api/handler/app_test.go`
- Modify: `internal/api/router/router.go`

- [ ] Add `GetCandidateDetail(ctx, db, ticker)` for current batch.
- [ ] Return score snapshot plus latest financial metric, insider transactions, capital risks, data quality, and evidence summary.
- [ ] API: `GET /api/discovery/candidates/:ticker/detail`.
- [ ] Tests: returns current batch evidence and 404/empty behavior for unknown ticker.

### Task 3: Candidate detail drawer UI

**Files:**
- Modify: `web/src/api/types.ts`
- Modify: `web/src/api/types.typecheck.ts`
- Modify: `web/src/views/DiscoveryCandidatesView.vue`

- [ ] Add detail types.
- [ ] Add row action to open detail drawer.
- [ ] Display score breakdown, evidence, data quality, insider, capital risk, and social heat sections.
- [ ] Build validation via `npm run build`.

### Task 4: Data source health and missing-data visibility

**Files:**
- Create: `internal/discovery/candidate_health.go`
- Create: `internal/discovery/candidate_health_test.go`
- Modify: `internal/api/handler/app.go`
- Modify: `internal/api/router/router.go`
- Modify: `web/src/views/DiscoveryCandidatesView.vue`

- [ ] Add summary of missing/stale financial, insider, market cap, and capital risk evidence from current batch.
- [ ] API: `GET /api/discovery/candidates/health`.
- [ ] UI alert card above candidate table.

### Task 5: One-click discovery workflow

**Files:**
- Create: `internal/service/discovery_workflow.go`
- Create: `internal/service/discovery_workflow_test.go`
- Modify: `internal/api/handler/app.go`
- Modify: `internal/api/router/router.go`
- Modify: `web/src/views/DiscoveryCandidatesView.vue`

- [ ] Add workflow service that runs pre-screen scoring through existing discovery coordinator hooks where available, then returns current candidates and notification dry-run.
- [ ] API: `POST /api/discovery/candidates/refresh`.
- [ ] UI button “刷新候选工作流”.

### Task 6: Optional social heat scaffold

**Files:**
- Modify: `internal/discovery/models.go`
- Modify: `internal/discovery/database.go`
- Create: `internal/discovery/social_heat.go`
- Create: `internal/discovery/social_heat_test.go`
- Modify: `internal/service/config.go`
- Modify: `internal/service/service_test.go`

- [ ] Add `SocialHeatSnapshot` model with provider, mentions, baseline, z-score, status, evidence URL/count.
- [ ] Add disabled-by-default config keys for social heat.
- [ ] Add manual snapshot upsert/query helpers.
- [ ] Do not perform unauthenticated Reddit scraping.

### Task 7: Sector explainability

**Files:**
- Create: `internal/discovery/sector_explain.go`
- Create: `internal/discovery/sector_explain_test.go`
- Modify: `internal/discovery/candidate_detail.go`

- [ ] Add sector score explanation mapping using available reason/score fields and optional manual sector note.
- [ ] Expose in candidate detail.

### Task 8: Candidate daily report/archive

**Files:**
- Create: `internal/discovery/candidate_report.go`
- Create: `internal/discovery/candidate_report_test.go`
- Modify: `internal/api/handler/app.go`
- Modify: `internal/api/router/router.go`
- Modify: `web/src/views/DiscoveryCandidatesView.vue`

- [ ] Add `GET /api/discovery/candidates/report?date=YYYY-MM-DD`.
- [ ] Use current/latest batch if date not supplied.
- [ ] Return A/B counts, top candidates, message, data-quality summary.
- [ ] UI: report drawer or card.

### Task 9: Notification history UX polish

**Files:**
- Modify: `web/src/views/NotificationLogsView.vue`

- [ ] Add candidate-specific columns and copy text where needed.
- [ ] Keep SEC/IPO behavior unchanged.

### Task 10: Documentation and final verification

**Files:**
- Modify: `docs/superpowers/specs/2026-06-22-small-cap-discovery-design.md`
- Modify: `docs/superpowers/specs/2026-06-22-small-cap-social-heat-design.md`

- [ ] Document implemented endpoints, disabled-by-default social heat, scheduling, force resend, and remaining operational caveats.
- [ ] Run `go test ./... -count=1`.
- [ ] Run `npm run build`.
- [ ] Run coverage and confirm total `>= 80%`.
- [ ] Commit completed changes.

