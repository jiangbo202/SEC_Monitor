# Small-Cap Data Foundation Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build the isolated small-cap SQLite data foundation that ingests listed-security metadata, validates public daily prices, resolves outstanding shares, and publishes an explainable 30M–1B USD pre-screen universe without producing A/B candidates.

**Architecture:** Keep SEC Monitor as one product and server process while opening a second `small_cap.db`. Add an isolated `internal/discovery` package with source adapters, deterministic classification, calendar, snapshots, and an atomic batch coordinator. Reuse the main database only for task configuration and audit/run summaries; expose read-only discovery APIs plus an audited CSV price-import endpoint.

**Tech Stack:** Go 1.24, Gin, GORM, SQLite, robfig/cron v3, standard-library HTTP/CSV/ZIP/XML/JSON; Vue work is intentionally deferred to the workflow subproject.

---

## File Structure

Create focused files under `internal/discovery`:

- `models.go`: GORM records and status constants.
- `database.go`: discovery database open/migration only.
- `download.go`: bounded HTTP download, cache metadata, hashing, and ZIP safety.
- `nasdaq.go`: Nasdaq Trader directory parsing.
- `sec_bulk.go`: SEC ticker/submissions/companyfacts parsing contracts.
- `classification.go`: deterministic v1 include/exclude rules.
- `calendar.go`: New York timezone and versioned NYSE holiday lookup.
- `market.go`: normalized price DTO, CSV/Stooq parser, validation state.
- `shares.go`: latest reliable outstanding-share selection.
- `universe.go`: batch orchestration and atomic snapshot publication.
- `query.go`: paginated universe, batch, and provider-status reads.
- `internal/api/handler/discovery.go`: discovery HTTP handlers, keeping HTTP parsing inside the existing API package.

The main database keeps `TaskConfig`, `SyncRun`, audit, and notifications. Discovery tables live only in `small_cap.db`; cross-database references are string batch IDs/event keys.

### Task 1: Dual-Database Configuration and Bootstrap

**Files:**
- Modify: `internal/config/config.go`
- Modify: `internal/config/config_test.go`
- Create: `internal/discovery/database.go`
- Create: `internal/discovery/database_test.go`
- Modify: `cmd/server/main.go`
- Modify: `cmd/server/main_test.go`
- Modify: `internal/api/router/router.go`
- Modify: `internal/api/router/router_test.go`

- [ ] **Step 1: Write failing configuration and bootstrap tests**

Add table-driven assertions for the default `data/small_cap.db`, `SMALL_CAP_DATABASE_DSN`, comma-separated `SMALL_CAP_STOOQ_URLS`, timeout, and dated-storage path. Add a `run` test proving startup fails when discovery open/migration fails and succeeds with two in-memory handles.

```go
func TestLoadDiscoveryConfig(t *testing.T) {
    t.Setenv("SMALL_CAP_DATABASE_DSN", "data/research.db")
    t.Setenv("SMALL_CAP_STOOQ_URLS", "https://example.test/a.zip, https://example.test/b.csv")
    cfg := Load()
    if cfg.Discovery.Database.DSN != "data/research.db" { t.Fatal(cfg.Discovery.Database.DSN) }
    if diff := strings.Join(cfg.Discovery.StooqURLs, ","); diff != "https://example.test/a.zip,https://example.test/b.csv" { t.Fatal(diff) }
}
```

- [ ] **Step 2: Run tests and verify the new fields/functions are missing**

Run: `go test ./internal/config ./internal/discovery ./cmd/server ./internal/api/router`

Expected: FAIL because `Config.Discovery`, `discovery.Open`, and the router discovery dependency do not exist.

- [ ] **Step 3: Implement the configuration and second database handle**

Use these public contracts:

```go
type DiscoveryConfig struct {
    Database DatabaseConfig
    CacheDir string
    NasdaqListedURL, NasdaqOtherListedURL string
    SECTickerExchangeURL, SECSubmissionsURL, SECCompanyFactsURL string
    StooqURLs []string
    TaskTimeoutMin int
}

type Config struct {
    Server ServerConfig; Database DatabaseConfig; SEC SECConfig; System SystemConfig
    Discovery DiscoveryConfig
}

func OpenDatabase(cfg config.DatabaseConfig) (*gorm.DB, error)
func Migrate(db *gorm.DB) error
```

Default `TaskTimeoutMin` to 60 and `CacheDir` to `.cache/discovery`. Use these defaults: `https://www.nasdaqtrader.com/dynamic/SymDir/nasdaqlisted.txt`, `https://www.nasdaqtrader.com/dynamic/SymDir/otherlisted.txt`, `https://www.sec.gov/files/company_tickers_exchange.json`, `https://www.sec.gov/Archives/edgar/daily-index/bulkdata/submissions.zip`, and `https://www.sec.gov/Archives/edgar/daily-index/xbrl/companyfacts.zip`. When `SMALL_CAP_DATABASE_DSN` is unset, derive `small_cap.db` from `filepath.Dir(DB_DSN)`; this automatically follows the existing dated main-DB path without duplicating date logic. Extend `router.Dependencies` with `DiscoveryDB *gorm.DB`; do not put discovery models in `internal/database.Migrate`.

- [ ] **Step 4: Run focused tests**

Run: `gofmt -w internal/config internal/discovery cmd/server internal/api/router && go test ./internal/config ./internal/discovery ./cmd/server ./internal/api/router`

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/config internal/discovery/database.go internal/discovery/database_test.go cmd/server internal/api/router
git commit -m "feat: bootstrap isolated discovery database"
```

### Task 2: Discovery Schema and Atomic Batch Records

**Files:**
- Create: `internal/discovery/models.go`
- Modify: `internal/discovery/database.go`
- Modify: `internal/discovery/database_test.go`

- [ ] **Step 1: Write failing migration tests**

Assert migration creates every discovery table and composite unique index, and that deleting a draft batch does not delete a previously published batch.

```go
models := []any{
    &Security{}, &Listing{}, &ClassificationSnapshot{}, &ProviderRun{},
    &MarketHoliday{}, &PriceSnapshot{}, &ShareSnapshot{},
    &UniverseBatch{}, &UniverseSnapshot{}, &ManualSecurityOverride{},
}
for _, m := range models {
    if !db.Migrator().HasTable(m) { t.Fatalf("missing %T", m) }
}
```

- [ ] **Step 2: Run the migration test and verify failure**

Run: `go test ./internal/discovery -run 'TestMigrate' -v`

Expected: FAIL because the models are undefined.

- [ ] **Step 3: Implement records with explicit status constants**

Use these identities and constraints:

```go
type Security struct { ID uint; CIK string `gorm:"size:10;uniqueIndex"`; CompanyName string; SIC int; StateOfIncorporation string; LatestAnnualForm string; CreatedAt, UpdatedAt time.Time }
type Listing struct { ID uint; SecurityID uint `gorm:"uniqueIndex:ux_listing_period"`; Ticker string `gorm:"uniqueIndex:ux_listing_period"`; ProviderTicker string; Exchange string; ValidFrom time.Time `gorm:"uniqueIndex:ux_listing_period"`; ValidTo *time.Time; Source string; MappingStatus string }
type ClassificationSnapshot struct { ID uint; BatchID string `gorm:"uniqueIndex:ux_classification_security"`; SecurityID uint `gorm:"uniqueIndex:ux_classification_security"`; Included bool; Status, Confidence, ReasonCode, RuleVersion string; EvidenceJSON string `gorm:"type:text"`; CreatedAt time.Time }
type ProviderRun struct { ID uint; BatchID, Provider, Status, SourceVersion, SHA256 string; EffectiveDate time.Time; RecordCount, ExpectedCount int; CoveragePct, ValidationErrorPct float64; Timely bool; ErrorMessage string; CreatedAt time.Time }
type MarketHoliday struct { Date string `gorm:"primaryKey;size:10"`; Name, CalendarVersion, SourceURL, ReviewedBy string; CompleteYear bool; ReviewedAt time.Time }
type PriceSnapshot struct { ID uint; Source, SourceVersion, Symbol string `gorm:"uniqueIndex:ux_price_source_symbol_day"`; TradeDate time.Time `gorm:"uniqueIndex:ux_price_source_symbol_day"`; CloseMicros, Volume int64; Currency string; Adjusted bool; QualityStatus string; CreatedAt time.Time }
type ShareSnapshot struct { ID uint; SecurityID uint `gorm:"uniqueIndex:ux_share_security_instant_accession"`; Instant time.Time `gorm:"uniqueIndex:ux_share_security_instant_accession"`; Accession string `gorm:"uniqueIndex:ux_share_security_instant_accession"`; Concept, Form, SourceURL, QualityStatus string; Shares int64; FiledAt time.Time; CreatedAt time.Time }
type UniverseBatch struct { BatchID string `gorm:"primaryKey;size:64"`; Status string; UniverseSourceVersion, PriceSourceVersion, ShareSourceVersion string; StartedAt time.Time; CompletedAt *time.Time; ErrorMessage string }
type UniverseSnapshot struct { ID uint; BatchID string `gorm:"uniqueIndex:ux_universe_security"`; SecurityID uint `gorm:"uniqueIndex:ux_universe_security"`; Ticker string; MarketCapUSD int64; Included bool; Status, ReasonCode, QualityStatus string; PriceSnapshotID, ShareSnapshotID uint; CreatedAt time.Time }
type ManualSecurityOverride struct { ID uint; SecurityID uint `gorm:"index"`; EffectiveStatus, Reason, SourceURL, Operator string; Active bool; CreatedAt, UpdatedAt time.Time }
type Evidence struct { Field, Value, Source string }
type SourceVersion struct { Source, Version, SHA256 string; EffectiveAt time.Time }
```

Price uses integer micro-dollars (`CloseMicros int64`) and shares use `int64`; compute market cap with checked integer arithmetic before converting to whole USD. Store evidence as a validated JSON string (`EvidenceJSON string` with `gorm:"type:text"`) to avoid a new dependency.

- [ ] **Step 4: Run discovery model tests**

Run: `gofmt -w internal/discovery && go test ./internal/discovery -run 'TestMigrate' -v`

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/discovery/models.go internal/discovery/database.go internal/discovery/database_test.go
git commit -m "feat: add discovery snapshot schema"
```

### Task 3: Bounded Downloader and Source Cache

**Files:**
- Create: `internal/discovery/download.go`
- Create: `internal/discovery/download_test.go`

- [ ] **Step 1: Write table-driven downloader tests**

Cover 200, conditional 304, non-2xx, timeout, maximum-byte rejection, SHA-256, atomic cache replacement, and ZIP path traversal rejection. Construct ZIP bytes in the test with `archive/zip`; do not check in opaque binary fixtures.

```go
type DownloadResult struct {
    Path, FinalURL, ETag, LastModified, SHA256, ContentType string
    Size int64
    NotModified bool
}
```

- [ ] **Step 2: Verify the tests fail**

Run: `go test ./internal/discovery -run 'TestDownloader|TestSafeZIP' -v`

Expected: FAIL because `Downloader` and `OpenSafeZIP` do not exist.

- [ ] **Step 3: Implement bounded downloads**

Implement `Download(ctx, url, cacheKey, priorMetadata)` with an injected `http.Client`, `io.LimitReader(maxBytes+1)`, temp-file write, `Sync`, SHA-256, and atomic rename. `OpenSafeZIP` must reject absolute paths and entries whose cleaned path escapes the extraction root. Do not solve Stooq browser challenges or execute JavaScript.

- [ ] **Step 4: Run downloader tests**

Run: `gofmt -w internal/discovery && go test ./internal/discovery -run 'TestDownloader|TestSafeZIP' -v`

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/discovery/download.go internal/discovery/download_test.go
git commit -m "feat: add bounded discovery source downloads"
```

### Task 4: Nasdaq and SEC Security Metadata Parsers

**Files:**
- Create: `internal/discovery/nasdaq.go`
- Create: `internal/discovery/nasdaq_test.go`
- Create: `internal/discovery/sec_bulk.go`
- Create: `internal/discovery/sec_bulk_test.go`
- Create: `internal/discovery/testdata/nasdaq/nasdaqlisted.txt`
- Create: `internal/discovery/testdata/nasdaq/otherlisted.txt`
- Create: `internal/discovery/testdata/sec/company_tickers_exchange.json`
- Create: `internal/discovery/testdata/sec/submissions/CIK0000000001.json`
- Create: `internal/discovery/testdata/sec/companyfacts/CIK0000000001.json`

- [ ] **Step 1: Write parser tests before network clients**

Cover Nasdaq footer/timestamp rows, ETF/test flags, exchange mapping, punctuation-preserving symbols, zero-padded CIKs, domestic 10-K/10-Q metadata, F-issuer exclusion evidence, latest outstanding shares, malformed rows, and duplicate facts. Wrap the checked-in JSON fixtures into in-memory ZIPs during tests.

```go
type SecuritySourceRecord struct {
    CIK, Ticker, CompanyName, Exchange, SecurityName string
    TestIssue, ETF bool
    SIC int
    StateOfIncorporation, LatestAnnualForm string
    RecentForms []string
}
```

- [ ] **Step 2: Run and verify parser failures**

Run: `go test ./internal/discovery -run 'TestParseNasdaq|TestParseSECBulk' -v`

Expected: FAIL because the parsers are undefined.

- [ ] **Step 3: Implement streaming parsers and source interfaces**

```go
type SecurityMetadataSource interface { Load(ctx context.Context) ([]SecuritySourceRecord, SourceVersion, error) }
type ShareFactSource interface { LoadLatestShares(ctx context.Context, allowedCIKs map[string]struct{}) ([]ShareFact, SourceVersion, error) }
```

Stream ZIP entries rather than unpacking the entire SEC archives. Only retain company metadata and the two allowed outstanding-share concepts. Reject entries over configured per-file and total limits. Preserve raw source values in evidence.

- [ ] **Step 4: Run parser tests**

Run: `gofmt -w internal/discovery && go test ./internal/discovery -run 'TestParseNasdaq|TestParseSECBulk' -v`

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/discovery/nasdaq.go internal/discovery/nasdaq_test.go internal/discovery/sec_bulk.go internal/discovery/sec_bulk_test.go internal/discovery/testdata/nasdaq internal/discovery/testdata/sec
git commit -m "feat: parse listed securities and SEC bulk metadata"
```

### Task 5: Deterministic Security Classification

**Files:**
- Create: `internal/discovery/classification.go`
- Create: `internal/discovery/classification_test.go`
- Create: `internal/discovery/testdata/gold/security_classification.csv`

- [ ] **Step 1: Encode the specification as failing table-driven cases**

Cover every reason code: `test_issue`, `fund_or_etf`, `non_common_security`, `investment_company`, `spac`, `foreign_or_adr`, `financial_company`, `not_active_listed`, `domestic_operating_common`, `security_type_unresolved`, and `mapping_conflict`.

```go
type Classification struct { Included bool; Status, Confidence, ReasonCode string; Evidence []Evidence }
func ClassifySecurity(record SecuritySourceRecord, overrides []ManualSecurityOverride) Classification
```

- [ ] **Step 2: Verify classification tests fail**

Run: `go test ./internal/discovery -run 'TestClassifySecurity' -v`

Expected: FAIL because `ClassifySecurity` does not exist.

- [ ] **Step 3: Implement ordered rules exactly as the data-foundation specification**

Normalize security names only for matching; keep original evidence. Treat a de-SPAC as unresolved until Item 2.01 completion, non-6770 SIC, and updated listing mapping are all present. Manual overrides replace the effective classification but never delete automatic evidence.

- [ ] **Step 4: Add a fixture coverage assertion and run tests**

The fixture validator must require at least 120 approved rows and at least 10 rows per reason group before provider activation; smaller unit fixtures may run during development but activation remains blocked.

Run: `gofmt -w internal/discovery && go test ./internal/discovery -run 'TestClassifySecurity|TestClassificationGoldCoverage' -v`

Expected: PASS after the checked-in gold fixture meets coverage.

- [ ] **Step 5: Commit**

```bash
git add internal/discovery/classification.go internal/discovery/classification_test.go internal/discovery/testdata/gold/security_classification.csv
git commit -m "feat: classify discovery securities deterministically"
```

### Task 6: Versioned NYSE Trading Calendar

**Files:**
- Create: `internal/discovery/calendar.go`
- Create: `internal/discovery/calendar_test.go`
- Create: `internal/discovery/testdata/calendar/nyse_holidays_2026_2028.csv`

- [ ] **Step 1: Write calendar tests**

Test weekends, a normal weekday, every seeded holiday, an early-close day as a trading day, a manual exceptional closure, missing-year fail-closed behavior, and daylight-saving conversion.

```go
type MarketCalendar interface { IsTradingDay(ctx context.Context, day time.Time) (bool, error) }
var ErrCalendarYearMissing = errors.New("market calendar year missing")
```

- [ ] **Step 2: Verify missing implementation**

Run: `go test ./internal/discovery -run 'TestMarketCalendar' -v`

Expected: FAIL.

- [ ] **Step 3: Implement the database-backed calendar**

Load `America/New_York` with `time.LoadLocation`; never compare UTC dates directly. Seed 2026–2028 from the NYSE hours/calendar source with source URL, reviewed date, reviewer, version, and completeness flag. A year without a complete version returns `ErrCalendarYearMissing`.

- [ ] **Step 4: Run calendar tests**

Run: `gofmt -w internal/discovery && go test ./internal/discovery -run 'TestMarketCalendar' -v`

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/discovery/calendar.go internal/discovery/calendar_test.go internal/discovery/testdata/calendar
git commit -m "feat: add versioned NYSE market calendar"
```

### Task 7: Normalized Market Price Providers and CSV Import

**Files:**
- Create: `internal/discovery/market.go`
- Create: `internal/discovery/market_test.go`
- Create: `internal/discovery/testdata/market/stooq.csv`
- Create: `internal/discovery/testdata/gold/market_price_validation.csv`

- [ ] **Step 1: Write parsing and validation tests**

Cover normalized CSV, Stooq ASCII format, an in-memory ZIP containing the CSV, daily `PER`, punctuation mapping, adjusted-close rejection, non-USD rejection, duplicate symbol/date, negative volume, stale trade dates, coverage/timeliness state, SHA-256, and 304 reuse.

```go
type PriceRecord struct { Symbol string; TradeDate time.Time; OpenMicros, HighMicros, LowMicros, CloseMicros, Volume int64; Currency string; Adjusted bool; Source string }
type ProviderResult struct { Provider, Status, SourceVersion, SHA256 string; EffectiveDate time.Time; Records, Expected int; CoveragePct, ValidationErrorPct float64; Timely bool }
type PriceProvider interface { Load(ctx context.Context, expected []Listing) ([]PriceRecord, ProviderResult, error) }
```

- [ ] **Step 2: Run and verify failures**

Run: `go test ./internal/discovery -run 'TestParsePrices|TestPriceValidation' -v`

Expected: FAIL.

- [ ] **Step 3: Implement providers without hard-coded Stooq endpoints**

Parse configured HTTPS URLs through `Downloader`. Normalize prices to integer micro-dollars with decimal-string parsing; never pass through float64. Reject `is_adjusted=true`. Implement `ImportPriceCSV(ctx, reader, metadata)` using the same validator and persist no rows until the full file passes schema validation.

- [ ] **Step 4: Enforce activation metrics**

Compute coverage, next-day-12:00-ET timeliness, 100-row independent-source error, and consecutive degraded days. The provider transitions `validation → active` only after 20 trading days and all thresholds; `active → degraded` after three consecutive failures.

Run: `gofmt -w internal/discovery && go test ./internal/discovery -run 'TestParsePrices|TestPriceValidation|TestProviderState' -v`

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/discovery/market.go internal/discovery/market_test.go internal/discovery/testdata/market internal/discovery/testdata/gold/market_price_validation.csv
git commit -m "feat: validate discovery market price providers"
```

### Task 8: Share Selection and Checked Market-Cap Computation

**Files:**
- Create: `internal/discovery/shares.go`
- Create: `internal/discovery/shares_test.go`
- Create: `internal/discovery/market_cap.go`
- Create: `internal/discovery/market_cap_test.go`

- [ ] **Step 1: Write share and market-cap boundary tests**

Cover DEI priority, us-gaap fallback, 10-Q/A replacement, instant-date ordering, weighted-average rejection, >150-day staleness, post-report financing invalidation, multiple-class conflict, split mismatch, integer overflow, exactly 30M, exactly 1B, and price older than three trading days.

```go
type ShareFact struct { CIK, Concept, Unit, Form, Accession string; Instant, FiledAt time.Time; Shares int64; SourceURL string }
type CapitalEvent struct { CIK, Kind, Accession string; EffectiveAt time.Time; ChangesShares bool }
type ShareSelection struct { Fact *ShareFact; QualityStatus, ReasonCode string }
func SelectShareSnapshot(facts []ShareFact, events []CapitalEvent, asOf time.Time) ShareSelection
func ComputeMarketCapUSD(closeMicros, shares int64) (int64, error)
```

- [ ] **Step 2: Verify failures**

Run: `go test ./internal/discovery -run 'TestSelectShares|TestComputeMarketCap' -v`

Expected: FAIL.

- [ ] **Step 3: Implement deterministic selection and integer arithmetic**

Use accession accepted time to order amendments, reject facts not sourced from 10-Q/10-K amendments, and return explicit `stale`, `conflict`, or `missing` quality. Use checked multiplication (`closeMicros > math.MaxInt64/shares`) before dividing by 1,000,000.

- [ ] **Step 4: Run focused tests**

Run: `gofmt -w internal/discovery && go test ./internal/discovery -run 'TestSelectShares|TestComputeMarketCap' -v`

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/discovery/shares.go internal/discovery/shares_test.go internal/discovery/market_cap.go internal/discovery/market_cap_test.go
git commit -m "feat: compute qualified small-cap market values"
```

### Task 9: Atomic Universe Coordinator

**Files:**
- Create: `internal/discovery/universe.go`
- Create: `internal/discovery/universe_test.go`
- Create: `internal/discovery/query.go`
- Create: `internal/discovery/query_test.go`

- [ ] **Step 1: Write orchestration failure and idempotency tests**

Test successful draft→published flow, metadata failure, price failure, missing calendar, inactive provider, duplicate input versions, 1,000-row transaction chunks, preserving the last published batch, inclusion boundaries, and pagination/filter ordering.

```go
type Coordinator struct { DB *gorm.DB; Metadata SecurityMetadataSource; Shares ShareFactSource; Prices PriceProvider; Calendar MarketCalendar; Clock func() time.Time }
func (c *Coordinator) SyncSecurityUniverse(ctx context.Context) (UniverseBatch, error)
func (c *Coordinator) SyncMarketPrices(ctx context.Context) (UniverseBatch, error)
```

- [ ] **Step 2: Verify coordinator tests fail**

Run: `go test ./internal/discovery -run 'TestCoordinator|TestUniverseQuery' -v`

Expected: FAIL.

- [ ] **Step 3: Implement stage and publish transactions**

Create deterministic batch IDs from input-version hashes plus effective date. Write draft records in chunks, validate counts and references, then publish with one short transaction that marks the new batch published and updates the current-batch pointer. Never mutate a published snapshot. Failed or partial batches retain diagnostics without changing the pointer.

- [ ] **Step 4: Run coordinator/query tests**

Run: `gofmt -w internal/discovery && go test ./internal/discovery -run 'TestCoordinator|TestUniverseQuery' -v`

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/discovery/universe.go internal/discovery/universe_test.go internal/discovery/query.go internal/discovery/query_test.go
git commit -m "feat: publish atomic small-cap universe batches"
```

### Task 10: Scheduler Integration and Main-Database Run Summaries

**Files:**
- Create: `internal/model/discovery_run.go`
- Modify: `internal/database/database.go`
- Modify: `internal/database/database_test.go`
- Create: `internal/service/discovery_run.go`
- Create: `internal/service/discovery_run_test.go`
- Modify: `internal/service/task_config.go`
- Modify: `internal/service/service_test.go`
- Modify: `internal/scheduler/scheduler.go`
- Modify: `internal/scheduler/scheduler_test.go`
- Modify: `internal/api/router/router.go`

- [ ] **Step 1: Write failing scheduler tests**

Require default `security_universe_sync` and `market_price_sync` tasks, `America/New_York` cron location, discovery task delegation, a 60-minute context timeout, global mutual exclusion, and main-db completion status even when a discovery batch fails.

```go
type DiscoveryRunner interface {
    SyncSecurityUniverse(context.Context) (discovery.UniverseBatch, error)
    SyncMarketPrices(context.Context) (discovery.UniverseBatch, error)
}

type DiscoveryRun struct {
    ID uint; TaskName, BatchID, Status string
    StartedAt time.Time; FinishedAt *time.Time
    Processed, Failed int; ErrorMessage string
    CreatedAt, UpdatedAt time.Time
}
```

- [ ] **Step 2: Verify failures**

Run: `go test ./internal/service ./internal/scheduler -run 'Test.*Task|TestScheduler' -v`

Expected: FAIL because discovery tasks and New York cron configuration are absent.

- [ ] **Step 3: Register discovery tasks without weakening existing task behavior**

Use `cron.New(cron.WithLocation(nyLocation))`. Seed `security_universe_sync` disabled by default at `30 4 * * *` and `market_price_sync` disabled by default at `30 12 * * 1-5`; operators enable them after source configuration. Keep one global running guard for now. Wrap discovery calls in `context.WithTimeout` from runtime config. Persist task name, discovery batch ID, status, counts, error, and timestamps through `DiscoveryRunService` in the main database; do not add cross-db foreign keys or reuse filing-specific `SyncRun` fields.

- [ ] **Step 4: Run service and scheduler tests**

Run: `gofmt -w internal/service internal/scheduler internal/api/router && go test ./internal/service ./internal/scheduler ./internal/api/router`

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/model/discovery_run.go internal/database internal/service/discovery_run.go internal/service/discovery_run_test.go internal/service/task_config.go internal/service/service_test.go internal/scheduler internal/api/router/router.go
git commit -m "feat: schedule discovery data foundation jobs"
```

### Task 11: Discovery APIs and Audited Price Import

**Files:**
- Create: `internal/api/handler/discovery.go`
- Create: `internal/api/handler/discovery_test.go`
- Modify: `internal/api/router/router.go`
- Modify: `internal/api/router/router_test.go`
- Modify: `internal/api/handler/app.go`

- [ ] **Step 1: Write failing API tests**

Cover success envelopes, pagination, ticker/status/reason filters, newest-first batches, provider diagnostics, malformed CSV, oversized upload, adjusted-price rejection, audited import, and nil-discovery dependency returning a controlled 503 rather than panic.

Routes:

```text
GET  /api/discovery/universe
GET  /api/discovery/batches
GET  /api/discovery/providers
POST /api/discovery/prices/import
```

- [ ] **Step 2: Verify API tests fail**

Run: `go test ./internal/discovery ./internal/api/router -run 'TestDiscovery|Test.*Router' -v`

Expected: FAIL with missing handlers/routes.

- [ ] **Step 3: Implement narrow handlers**

Use the existing `{code:0,message:"ok",data:...}` envelope and service pagination conventions. Limit upload bodies before multipart parsing. The import handler records an `import` operation in the main audit service with file hash, row count, source, and effective date, never raw file content.

- [ ] **Step 4: Run handler/router tests**

Run: `gofmt -w internal/discovery internal/api && go test ./internal/discovery ./internal/api/router ./internal/api/handler`

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/api/handler/discovery.go internal/api/handler/discovery_test.go internal/api/router internal/api/handler/app.go
git commit -m "feat: expose discovery data foundation APIs"
```

### Task 12: Retention, Performance Fixtures, Documentation, and Full Verification

**Files:**
- Create: `internal/discovery/retention.go`
- Create: `internal/discovery/retention_test.go`
- Create: `internal/discovery/performance_test.go`
- Modify: `README.md`
- Modify: `README.en.md`
- Modify: `docs/config/README.md`
- Modify: `.gitignore`

- [ ] **Step 1: Write retention and benchmark-scale tests**

Test 90-day daily retention, month-end compaction, preservation of published/current evidence, provider cache cleanup, and a fixed generator producing 8,000 securities, 7,000 prices, 1,500 share facts, and 90 days of history.

```go
type RetentionPolicy struct { DailyDays int; KeepMonthEnd bool; CacheDays int }
func ApplyRetention(ctx context.Context, db *gorm.DB, now time.Time, policy RetentionPolicy) (RetentionResult, error)
```

- [ ] **Step 2: Verify retention tests fail**

Run: `go test ./internal/discovery -run 'TestRetention|TestPerformanceDataset' -v`

Expected: FAIL.

- [ ] **Step 3: Implement retention and document operations**

Never delete the current batch, source versions referenced by it, manual overrides, or provider validation history needed for activation. Document both DB paths, dated storage, Stooq URL configuration, CSV schema, disabled-by-default tasks, NYSE calendar review, 20-day validation, backup/restore of both databases, and the fact that this phase outputs no A/B candidates. Ignore `data/small_cap.db` and discovery download cache.

- [ ] **Step 4: Run the complete verification suite**

Run:

```bash
gofmt -w internal cmd
go test ./...
go test ./... -coverprofile=/tmp/sec_monitor_cover.out
go tool cover -func=/tmp/sec_monitor_cover.out
cd web && npm run build
```

Expected: all tests and frontend build PASS; total Go statement coverage is at least 80%. Record the reference-machine performance separately; do not make normal unit tests depend on wall-clock network performance.

- [ ] **Step 5: Review scope and commit**

Confirm no A/B scoring, Form 4 analysis, financing classification, Reddit calls, new frontend page, or automatic WatchTarget creation entered this subproject.

```bash
git add internal/discovery README.md README.en.md docs/config/README.md .gitignore
git commit -m "feat: complete small-cap data foundation"
```
