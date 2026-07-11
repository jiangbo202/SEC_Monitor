# Small-Cap Candidate Second Batch Plan

**Goal:** Add explainable score changes, a structured candidate research workspace, event-aware candidate summaries, and historical cohort effectiveness metrics.

**Constraints:** Go table-driven tests; SQLite/GORM migration; no external network in tests; preserve existing A/B filtering and notification dedupe.

## Tasks

- [x] Add failing scoring tests for quarterly-first growth and normalized priority scoring.
- [x] Implement quarterly-first selection, growth conflict tag, 0–100 priority, and field-level `change_reasons`.
- [x] Add failing watch lifecycle tests for research fields and partial updates.
- [x] Migrate and implement structured research states/fields, then expose them through the existing watch API and UI dialog.
- [x] Add notification summary tests for change context and update the candidate summary rendering.
- [x] Add failing cohort-effectiveness tests for 1/5/20 day return, win rate, drawdown, and optional IWM benchmark.
- [x] Add effectiveness API and a compact dashboard section on the candidate page.
- [x] Update API types and README documentation.
- [x] Run `gofmt`, `go test ./...`, coverage check, `npm run build`, and commit a focused change.
