# Small-Cap Candidate Trust First Batch Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make the small-cap candidate list internally consistent, distinguish missing data from absent signals, preserve a stable performance baseline, activate gross-margin scoring, and prevent low-coverage batches from replacing healthy results.

**Architecture:** Keep raw score snapshots for audit, but default candidate-facing queries to A/B grades and expose the excluded pool only through an explicit grade filter. Derive health and performance from versioned source batches and historical snapshots. Extend the existing financial summary pipeline for gross margin and add a relative coverage publication guard beside the configurable absolute threshold.

**Tech Stack:** Go 1.24, GORM, SQLite, Gin, Vue 3, TypeScript, Element Plus.

## Global Constraints

- Use table-driven Go tests where multiple cases share behavior.
- Preserve SQLite as the default database and use GORM migration for new columns.
- Do not make external SEC or market-data calls in tests.
- Run `gofmt`, `go test ./...`, and `npm run build` before handoff.

---

### Task 1: Candidate and excluded-pool query semantics

**Files:**
- Modify: `internal/discovery/query.go`
- Test: `internal/discovery/query_test.go`
- Modify: `web/src/views/DiscoveryCandidatesView.vue`

**Interfaces:**
- `ListCandidateScores(ctx, db, CandidateScoreQuery{})` returns only grades A/B.
- `CandidateScoreQuery{Grade: CandidateGradeExcluded}` returns only excluded rows.
- `candidateQualityTier` returns `excluded` for excluded rows.

- [x] Write failing query and overview tests with A, B, and excluded snapshots.
- [x] Run `go test ./internal/discovery -run 'TestCandidateScoreQuery|TestBuildCandidateOverview'` and confirm the excluded row is incorrectly included.
- [x] Add grade normalization and default `grade IN (A, B)` filtering; keep explicit excluded access.
- [x] Add the “排除池” grade option and excluded quality label in the Vue page.
- [x] Re-run the focused tests.

### Task 2: Candidate health semantics

**Files:**
- Modify: `internal/discovery/candidate_health.go`
- Test: `internal/discovery/candidate_health_test.go`
- Modify: `web/src/api/types.ts`
- Modify: `web/src/views/DiscoveryCandidatesView.vue`

**Interfaces:**
- `CandidateHealth` exposes `insider_data_status`, `qualified_insider_candidates`, and `no_qualified_insider_candidates`.
- A completed `insiders:sec-form4` source with zero qualified purchases is not a degraded data condition.
- A missing insider source is reported as `missing_insider_data`.

- [x] Write failing table-driven health tests for synced/no-event and missing-source cases.
- [x] Run the focused health tests and confirm current behavior degrades both cases.
- [x] Parse security-batch source versions and separate source availability from signal presence.
- [x] Update API types and health copy.
- [x] Re-run focused tests.

### Task 3: Stable candidate performance baseline

**Files:**
- Modify: `internal/discovery/query.go`
- Test: `internal/discovery/query_test.go`

**Interfaces:**
- Performance uses the first published batch in which the ticker was grade A/B.
- The baseline close comes from that batch's linked universe price snapshot.
- Later current batches do not reset `base_date` or `base_close`.

- [x] Write a failing test with an earlier candidate batch and a later current batch.
- [x] Confirm the test reports the later date as the baseline.
- [x] Add a baseline lookup by security/ticker and first A/B snapshot.
- [x] Re-run focused performance tests.

### Task 4: Gross-margin financial pipeline

**Files:**
- Modify: `internal/discovery/models.go`
- Modify: `internal/discovery/financials.go`
- Modify: `internal/discovery/universe.go`
- Test: `internal/discovery/financials_test.go`
- Test: `internal/discovery/universe_test.go`
- Test: `internal/discovery/database_test.go`

**Interfaces:**
- `FinancialSummary` and `FinancialMetricSnapshot` expose `GrossMarginAvailable` and `GrossMarginPct`.
- Gross margin is latest-quarter gross profit divided by aligned latest-quarter revenue, with cost-of-revenue fallback.
- Candidate scoring receives the persisted gross-margin percentage.

- [x] Add failing financial-summary, migration, and coordinator scoring tests.
- [x] Confirm gross margin is currently absent or score remains zero.
- [x] Compute and persist gross margin, then pass it into `DiscoveryScoreInput`.
- [x] Re-run focused financial and universe tests.

### Task 5: Coverage publication protection

**Files:**
- Modify: `internal/config/config.go`
- Modify: `internal/service/config.go`
- Modify: `internal/discovery/universe.go`
- Test: `internal/config/config_test.go`
- Test: `internal/service/service_test.go`
- Test: `internal/discovery/universe_test.go`
- Modify: `web/src/views/ConfigsView.vue`
- Modify: `docs/config/README.md`

**Interfaces:**
- New-install absolute research coverage default is 85%.
- A new batch also fails when coverage drops more than 15 percentage points below the latest published market batch.
- Failed batches retain diagnostics and do not replace the current pointer.

- [x] Write failing default-config and relative-coverage tests.
- [x] Confirm the current 20% default and lack of relative guard.
- [x] Raise defaults to 85 and add latest-published coverage lookup/gate.
- [x] Update settings fallback and documentation.
- [x] Re-run focused tests.

### Task 6: Full verification

**Files:**
- Modify only files required by failures discovered above.

- [x] Run `gofmt` on changed Go files.
- [x] Run `GOCACHE=/tmp/sec_monitor_go_cache go test ./...`.
- [x] Run `npm run build` in `web`.
- [x] Inspect `git diff --check` and `git status --short`.
- [x] Commit the completed first batch with a focused `fix:` message.
