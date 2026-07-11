# Small-Cap Candidate Third Batch Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add market-quality evidence, watched-candidate filing alerts, 60-day cohort analysis, and a filtered candidate CSV export.

**Architecture:** Derive market metrics at query time from valid local `price_snapshots`; preserve immutable score snapshots. Enrich the existing candidate notification preview with watch filings from the main DB, and deliver all eligible events through the existing notification-batch service. Reuse candidate filters for CSV rows.

**Tech Stack:** Go 1.24, Gin, GORM, SQLite, Vue 3, TypeScript, Element Plus.

## Global Constraints

- Tests are table-driven for classifications, filters and notification eligibility.
- No network calls in unit tests.
- SQLite remains the default; do not introduce a new provider or queue.
- Run `gofmt`, `go test ./...`, coverage check and `npm run build` before commit.

---

- [x] Add failing tests for derived market metrics and risk labels.
- [x] Implement market-quality hydration, quality-tag/priority effects, list UI and API types.
- [x] Add failing notification tests for watched major filings and no-candidate event batches.
- [x] Implement candidate-watch filing discovery, preview/send integration and UI event display.
- [x] Extend cohort tests and implementation from 20 to 60 trading days.
- [x] Add a tested candidate CSV export that honors existing candidate filters.
- [x] Update README/API types, run full verification and commit.
