# IPO Company Sorting Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Show the company with the newest SEC filing activity first by default and support server-side status-column sorting.

**Architecture:** Extend the IPO company filter with an allowlisted sort field and direction. Keep aggregation in `IPORadarService`, sort the complete aggregate result before pagination, and have the Element Plus table send custom sort changes to the API.

**Tech Stack:** Go 1.24, Gin, GORM, Vue 3, TypeScript, Element Plus

---

### Task 1: Service sorting contract

**Files:**
- Modify: `internal/service/ipo_radar.go`
- Test: `internal/service/service_test.go`

- [ ] Add table-driven failing tests for default latest accepted time, filing-date fallback, status order in both directions, and deterministic ties.
- [ ] Run `go test ./internal/service -run TestIPORadarServiceCompanySortingTableDriven` and confirm the new expectations fail.
- [ ] Add `SortBy` and `SortOrder` to `IPOCompanyFilter`, then implement allowlisted aggregate sorting before pagination.
- [ ] Run the focused service test and confirm it passes.

### Task 2: API and table interaction

**Files:**
- Modify: `internal/api/handler/app.go`
- Modify: `web/src/views/IPORadarView.vue`

- [ ] Parse `sort_by` and `sort_order` in `ListIPOCompanies`.
- [ ] Mark the status and latest-update columns as Element Plus custom-sort columns.
- [ ] Initialize the frontend sort state to `latest_update DESC`, reset pagination on sort changes, and reload from the API.
- [ ] Run `npm run build` in `web` and confirm the TypeScript production build passes.

### Task 3: Full verification

**Files:**
- No additional files.

- [ ] Run `gofmt` on changed Go files.
- [ ] Run `go test ./... -coverprofile=/tmp/sec_monitor_cover.out` and confirm all tests pass.
- [ ] Run `go tool cover -func=/tmp/sec_monitor_cover.out` and confirm total coverage remains at least 80%.
- [ ] Run `npm run build` in `web` and confirm the production build passes.
