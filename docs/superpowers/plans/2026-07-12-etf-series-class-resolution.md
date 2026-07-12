# ETF Series/Class 精确解析 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 让新增 ETF 的 ticker 自动解析为 SEC Trust、Series、Class 身份，并在同步时只入库属于该基金份额的申报。

**Architecture:** 保留普通股票现有的 `company_tickers.json` 查询。为 ETF 在 `internal/sec` 增加基金身份解析与 filing-index 元数据解析：先读取 SEC `company_tickers_mf.json`，未命中时使用 SEC 全文检索和 filing index 取得候选；仅完整、唯一的身份可自动保存。WatchTarget 保存基金身份，FilingService 以 accession 缓存 index 结果并拒绝不匹配的 Trust 级文件。

**Tech Stack:** Go 1.24、Gin、GORM/SQLite、Vue 3、Element Plus、SEC EDGAR 公共 HTTP API。

## Global Constraints

- 只使用公开免费的 SEC 数据；行情供应商不得作为 CIK、Series 或 Class 的最终身份来源。
- ETF 精确模式必须同时保存非空 CIK、Series ID 与 Class ID；缺任一字段不得退化为自动监控整个 Trust。
- 普通股票、旧 ETF（未配置 Series/Class）保持当前同步兼容行为；UI 必须明确旧 ETF 未启用精确过滤。
- SEC 网络调用使用可替换 `http.RoundTripper`；单元测试不得访问外部 SEC。
- 运行 `gofmt`、`go test ./...` 和 `cd web && npm run build` 后才提交。

---

### Task 1: 定义并解析 SEC 基金身份

**Files:**
- Create: `internal/sec/fund_identity.go`
- Modify: `internal/sec/client.go:64-84`
- Modify: `internal/sec/client_test.go`

**Interfaces:**
- Produces `FundIdentity`, `FundResolution` 与 `FundIdentityClient`，供 API 和同步服务使用。
- `HTTPClient.ResolveFundTicker(ctx, ticker)` 先查 SEC 基金 ticker 清单，再返回候选或可自动采用的身份。

- [ ] **Step 1: 写失败测试，覆盖 SEC 基金清单解析和安全失败行为**

```go
func TestHTTPClientResolveFundTicker(t *testing.T) {
    client := newTestHTTPClient(t, map[string]string{
        "/company_tickers_mf.json": `{"fields":["cik","seriesId","classId","symbol"],"data":[[1976517,"S000102337","C000272806","DRAM"]]}`,
    })
    got, err := client.ResolveFundTicker(context.Background(), "dram")
    if err != nil || got.Identity == nil { t.Fatalf("resolution=%+v err=%v", got, err) }
    if *got.Identity != (FundIdentity{Ticker:"DRAM", CIK:"0001976517", SeriesID:"S000102337", ClassID:"C000272806", Source:"sec_company_tickers_mf"}) { t.Fatalf("identity=%+v", *got.Identity) }
}
```

另加 table cases：字段顺序变化、空 CIK、同 ticker 指向两个 Series/Class、HTTP 429、非法 JSON。重复或缺字段返回 `Identity:nil` 和可读 `Reason`，不返回部分可自动保存的身份。

- [ ] **Step 2: 运行测试，确认实现前失败**

Run: `go test ./internal/sec -run TestHTTPClientResolveFundTicker -count=1`

Expected: FAIL，`ResolveFundTicker` 尚未定义。

- [ ] **Step 3: 添加身份类型和主数据集解析**

```go
type FundIdentity struct {
    Ticker, CIK, SeriesID, ClassID, FundName, Source, EvidenceURL string
}
type FundResolution struct { Identity *FundIdentity; Candidates []FundIdentity; Reason string }
type FundIdentityClient interface {
    ResolveFundTicker(context.Context, string) (FundResolution, error)
    MatchFundFiling(context.Context, FundIdentity, FilingResult) (bool, string, error)
}
```

为 `HTTPClient` 增加 `CompanyTickersMFURL`，默认 `https://www.sec.gov/files/company_tickers_mf.json`。按 `fields` 名称定位列，而非硬编码列序号；将数值 CIK 标准化为十位字符串。只有单条、完整且 ticker 精确相等的记录才设置 `FundResolution.Identity`。

- [ ] **Step 4: 运行 SEC 包测试**

Run: `go test ./internal/sec -count=1`

Expected: PASS。

- [ ] **Step 5: 提交基金主数据解析**

```bash
git add internal/sec/fund_identity.go internal/sec/client.go internal/sec/client_test.go
git commit -m "feat: resolve ETF fund identities from SEC"
```

### Task 2: 实现 SEC 文件回退与 filing-index 身份缓存

**Files:**
- Modify: `internal/sec/fund_identity.go`
- Modify: `internal/sec/client_test.go`
- Create: `internal/model/fund_filing_identity.go`
- Modify: `internal/database/database.go`

**Interfaces:**
- Consumes `FundIdentityClient.ResolveFundTicker` 和 `FilingResult.AccessionNumber`。
- Produces `model.FundFilingIdentity`，以 `(cik, accession_number)` 唯一保存 `series_ids_json`、`class_ids_json`、`parse_status`、`parse_message` 与 `checked_at`。

- [ ] **Step 1: 写失败测试，覆盖 DRAM 回退和 index 匹配**

```go
func TestHTTPClientResolveFundTickerFallsBackToSECSearch(t *testing.T) {
    client := newTestHTTPClient(t, fixtureRoutes{
        "/company_tickers_mf.json": `{"fields":["cik","seriesId","classId","symbol"],"data":[]}`,
        "/LATEST/search-index": searchHit("0001976517-26-005961", "0001976517"),
        "/Archives/edgar/data/1976517/000139834426005961/0001398344-26-005961-index.htm": fundIndex("Roundhill Memory ETF", "S000102337", "C000272806"),
    })
    got, err := client.ResolveFundTicker(context.Background(), "DRAM")
    if err != nil || got.Identity == nil || got.Identity.ClassID != "C000272806" { t.Fatalf("%+v %v", got, err) }
}
func TestHTTPClientMatchFundFiling(t *testing.T) {
    identity := FundIdentity{CIK:"0001976517", SeriesID:"S1", ClassID:"C1"}
    matched, reason, err := client.MatchFundFiling(ctx, identity, FilingResult{CIK: identity.CIK, AccessionNumber:"0001976517-26-000001"})
    if err != nil || !matched || reason != "matched_class" { t.Fatalf("%v %q %v", matched, reason, err) }
}
```

覆盖：全文搜索命中多个 CIK、index 有多个不匹配 Series、index 缺少 Series/Class、index HTTP 错误。前两类只返回候选而不设置自动身份；后两类返回 `matched=false` 及原因。

- [ ] **Step 2: 运行新测试，确认失败**

Run: `go test ./internal/sec -run 'TestHTTPClient(ResolveFundTickerFallsBackToSECSearch|MatchFundFiling)' -count=1`

Expected: FAIL。

- [ ] **Step 3: 实现回退和 index 解析**

使用 `https://efts.sec.gov/LATEST/search-index` 的 `adsh`、`ciks` 与 `display_names` 生成候选 accession/index URL。仅接受 index 的 Series 名称与候选文件正文中 ticker 同时精确匹配的候选。将 index 中的 Series 与 Class/Contract 行解析为 `FundIdentity`；不完整或多个不同候选保留在 `FundResolution.Candidates`。

为缓存模型添加：

```go
type FundFilingIdentity struct {
    ID uint `gorm:"primaryKey"`
    CIK string `gorm:"size:32;not null;uniqueIndex:idx_fund_filing_identity"`
    AccessionNumber string `gorm:"size:128;not null;uniqueIndex:idx_fund_filing_identity"`
    SeriesIDsJSON string `gorm:"type:text"`
    ClassIDsJSON string `gorm:"type:text"`
    ParseStatus string `gorm:"size:32;not null;index"`
    ParseMessage string `gorm:"type:text"`
    CheckedAt time.Time `gorm:"index"`
}
```

在 `database.Migrate` 注册模型。`MatchFundFiling` 只负责网络解析；服务层负责读取和写入缓存。

- [ ] **Step 4: 运行 SEC 与数据库测试**

Run: `go test ./internal/sec ./internal/database -count=1`

Expected: PASS。

- [ ] **Step 5: 提交回退和 index 解析**

```bash
git add internal/sec/fund_identity.go internal/sec/client_test.go internal/model/fund_filing_identity.go internal/database/database.go
git commit -m "feat: resolve ETF identities from SEC filings"
```

### Task 3: 保存 ETF 身份并在同步时精确过滤

**Files:**
- Modify: `internal/model/watch_target.go`
- Modify: `internal/model/sync_run_detail.go`
- Modify: `internal/service/watch_target.go`
- Modify: `internal/service/filing.go`
- Modify: `internal/service/service_test.go`
- Modify: `internal/database/database.go`

**Interfaces:**
- Consumes `WatchTarget.FundSeriesID`、`FundClassID` 与 `sec.FundIdentityClient.MatchFundFiling`。
- Produces只包含目标 Series/Class 的 `model.Filing`，并将未匹配原因写入 `SyncRunDetail.ErrorMessage`。

- [ ] **Step 1: 写失败测试，确保 Trust 不会泄漏其他 ETF 文件**

```go
func TestFilingServiceSyncFiltersFundClass(t *testing.T) {
    target := model.WatchTarget{Ticker:"DRAM", CIK:"0001976517", TargetType:"etf", FundSeriesID:"S000102337", FundClassID:"C000272806", Status:"enabled"}
    secClient := &fakeFundSECClient{filings: []sec.FilingResult{{AccessionNumber:"keep"}, {AccessionNumber:"drop"}}, matches: map[string]bool{"keep":true, "drop":false}}
    result, err := NewFilingService(db, secClient, notifier, configs).RefreshTargets(ctx, []model.WatchTarget{target})
    if err != nil || result.NewFilings != 1 { t.Fatalf("%+v %v", result, err) }
    assertStoredAccessions(t, db, []string{"keep"})
}
```

另加 tests：无 Series/Class 的旧 ETF 仍入库全部文件；index 网络失败使该 target 进入 `partial`、写明 `fund identity unavailable`，但其他 target 成功；Class 匹配优先于仅 Series 匹配；成功但过滤掉 Trust 其他基金文件时 `SyncRunDetail.WarningMessage` 写明过滤数量。

- [ ] **Step 2: 运行测试，确认失败**

Run: `go test ./internal/service -run TestFilingServiceSyncFiltersFundClass -count=1`

Expected: FAIL，WatchTarget 尚无基金身份字段。

- [ ] **Step 3: 增加持久字段、输入校验和同步过滤**

为 `WatchTarget`、`WatchTargetInput` 添加 `fund_series_id`、`fund_class_id`、`identity_source`、`identity_verified_at`、`identity_note`。`toModel` 要求两项基金 ID 要么同时为空、要么同时符合 `S[0-9]+` / `C[0-9]+`；`target_type != "etf"` 时拒绝这两项。

在 `model.SyncRunDetail` 增加 `WarningMessage string \`gorm:"type:text" json:"warning_message,omitempty"\``，并将 `finishSyncRunDetail` 改为接收 `errorMessage, warningMessage string`，更新所有现有调用以传入空 warning。`FilingService.RefreshTargets` 的 `applyFetchSettings` 后调用：

```go
filings, skipped, err := s.filterFundFilings(ctx, target, filings)
if err != nil { s.finishSyncRunDetail(ctx, detail.ID, "failed", 0, detailStartedAt, "fund identity unavailable: "+err.Error(), ""); continue }
warning := ""
if skipped > 0 { warning = fmt.Sprintf("fund identity filtered %d trust filings", skipped) }
```

将 `warning` 传给该 target 最终的 `finishSyncRunDetail` 调用。`filterFundFilings` 先从 `fund_filing_identities` 读缓存，未命中时调用 `FundIdentityClient.MatchFundFiling` 并持久化结果；只保留 `matched=true`。对于完整身份但 sec client 不支持接口，明确失败，不得入库全部 Trust 文件。

- [ ] **Step 4: 运行服务测试**

Run: `go test ./internal/service -count=1`

Expected: PASS。

- [ ] **Step 5: 提交精确同步过滤**

```bash
git add internal/model/watch_target.go internal/service/watch_target.go internal/service/filing.go internal/service/service_test.go internal/database/database.go
git commit -m "feat: filter ETF filings by series and class"
```

### Task 4: 提供统一的 ticker 解析 API 与安全候选确认

**Files:**
- Modify: `internal/api/handler/app.go`
- Modify: `internal/api/handler/app_test.go`
- Modify: `web/src/api/types.ts`

**Interfaces:**
- `GET /api/sec/tickers/:ticker?target_type=etf` 返回现有股票字段，或 `fund_identity` / `fund_candidates` / `resolution_reason`。
- `PUT/POST` 标的输入可携带完整基金身份；候选必须由用户提交选中的完整对象。

- [ ] **Step 1: 写失败 API 测试**

```go
func TestLookupTickerReturnsExactFundIdentity(t *testing.T) {
    rec := request(router, http.MethodGet, "/sec/tickers/DRAM?target_type=etf", nil)
    requireJSONContains(t, rec.Body.String(), `"fund_series_id":"S000102337"`)
    requireJSONContains(t, rec.Body.String(), `"fund_class_id":"C000272806"`)
}
```

再加测试：模糊基金候选返回 HTTP 200 和 `fund_candidates`，而不是伪造 CIK；`target_type=stock` 继续使用原有查询；创建 ETF 时只传 Series 或 Class 返回 400。

- [ ] **Step 2: 运行 API 测试，确认失败**

Run: `go test ./internal/api/handler -run TestLookupTickerReturnsExactFundIdentity -count=1`

Expected: FAIL。

- [ ] **Step 3: 扩展 handler 和传输类型**

先保留 `LookupCIK` 作为股票主路径；当 `target_type=etf` 或股票主路径未命中时，断言 `h.SEC.(sec.FundIdentityClient)` 并返回基金 resolution。将完整身份复制到 `WatchTargetInput`，不从前端自由文本推断身份来源。

`web/src/api/types.ts` 添加：

```ts
export interface FundIdentity { ticker: string; cik: string; series_id: string; class_id: string; fund_name?: string; source: string; evidence_url?: string }
export interface TickerLookup { ticker: string; cik?: string; company_name?: string; target_type: string; fund_identity?: FundIdentity; fund_candidates?: FundIdentity[]; resolution_reason?: string }
```

- [ ] **Step 4: 运行 handler 测试**

Run: `go test ./internal/api/handler -count=1`

Expected: PASS。

- [ ] **Step 5: 提交解析 API**

```bash
git add internal/api/handler/app.go internal/api/handler/app_test.go web/src/api/types.ts
git commit -m "feat: expose ETF identity lookup"
```

### Task 5: 完成新增标的界面与运营可见性

**Files:**
- Modify: `web/src/views/TargetsView.vue`
- Modify: `web/src/i18n/index.ts`
- Modify: `README.md`

**Interfaces:**
- Consumes `TickerLookup.fund_identity` 与 `TickerLookup.fund_candidates`。
- Produces完整的 ETF 身份随 create/update 请求保存，并在列表/详情提示精确过滤状态。

- [ ] **Step 1: 写前端类型检查前的交互断言清单**

在 `TargetsView.vue` 的新增对话框实现以下可验证状态：

```ts
if (lookup.fund_identity) {
  Object.assign(form, { cik: lookup.fund_identity.cik, company_name: lookup.fund_identity.fund_name || lookup.ticker,
    fund_series_id: lookup.fund_identity.series_id, fund_class_id: lookup.fund_identity.class_id })
  identityState.value = 'exact'
}
```

候选大于一条时显示 Element Plus `el-select`，选项标签为 `fund_name || ticker + CIK + series_id + class_id`；未选择前禁用保存。无法确认时显示“不会自动监控整个 Trust”，保留手动字段但不设置精确身份。

- [ ] **Step 2: 运行类型检查，确认新增字段尚不存在**

Run: `cd web && npx vue-tsc --noEmit`

Expected: FAIL，新增字段和状态尚未声明。

- [ ] **Step 3: 实现表单、列表提示和双语文案**

在新增/编辑表单维护 `fund_series_id`、`fund_class_id`、`identity_source`。ETF 完整身份显示绿色“基金份额已精确匹配”；ETF 缺失身份显示黄色“Trust 级监控，可能包含其他基金文件”。普通股票不显示该提示。所有 API 错误继续用现有 `ElMessage` 流程。

- [ ] **Step 4: 构建前端**

Run: `cd web && npm run build`

Expected: PASS（允许现有 Rollup chunk-size warning）。

- [ ] **Step 5: 更新 README 并提交**

补充数据源优先级、DRAM 示例、Trust 级风险、候选确认和 index 缓存行为。

```bash
git add web/src/views/TargetsView.vue web/src/i18n/index.ts README.md
git commit -m "feat: guide precise ETF monitoring setup"
```

### Task 6: 完整回归验证

**Files:**
- Modify: `README.md`（仅在验证命令或操作说明需更正时）

- [ ] **Step 1: 运行 Go 全量测试**

Run: `env GOCACHE=$PWD/.cache/go-build GOMODCACHE=$PWD/.cache/go-mod go test ./...`

Expected: PASS。

- [ ] **Step 2: 运行前端生产构建**

Run: `cd web && npm run build`

Expected: PASS。

- [ ] **Step 3: 检查提交范围**

Run: `git diff --check && git status --short`

Expected: 无格式错误；不包含 `data/`、`logs/`、`.cache/`、`.runtime/` 或 `web/dist/`。

- [ ] **Step 4: 提交任何验证文档修订**

```bash
git add README.md
git commit -m "docs: verify precise ETF monitoring workflow"
```

仅当 README 在步骤 1–3 后确有修改时执行本步骤。
