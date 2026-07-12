# ETF Series/Class 精确解析设计

## 目标

让“新增标的”的 Ticker 自动带出同时适用于普通股票和 ETF，并且 ETF 同步只保留目标基金份额的 SEC 文件，避免将同一 Trust 下其他基金的申报混入目标标的。

## 范围

- 新增/编辑标的时的 Ticker 自动解析。
- ETF 的 CIK、Series ID、Class ID 绑定与后续 SEC 文件过滤。
- 仅使用公开免费 SEC 数据；行情数据源不能作为 CIK 或基金份额身份的最终依据。

不包含 ETF 净值、行情、持仓或跨 Trust 的人工合并。

## 解析链路

1. 普通股票继续使用 SEC `company_tickers.json`，获得 CIK 和公司名。
2. ETF/基金优先查询 SEC `company_tickers_mf.json`，获取 `CIK`、`seriesId`、`classId` 和 ticker。
3. 若基金清单没有该 ticker，调用 SEC 全文检索定位近期、精确包含目标 ticker 与基金名称的基金文件；读取该文件的 SEC filing index，提取其 Series/Class 标识。
4. 只有取得唯一且完整的 `CIK + Series ID + Class ID` 时才自动保存。多个候选或证据不足时返回候选和原因，要求用户选择；不会退化为自动监控整个 Trust。

`DRAM` 是回归样例：Roundhill ETF Trust (`0001976517`) 的 Roundhill Memory ETF 应解析到 `S000102337` 与 `C000272806`。

## 数据与同步

- 为 WatchTarget 增加可选的 `series_id`、`class_id`、`identity_source`、`identity_verified_at` 与 `identity_note`。
- `target_type=etf` 且含 Series/Class 时启用基金份额级过滤；股票和未完成身份确认的旧 ETF 保持既有行为，UI 明确显示“未精确过滤”。
- 同步下载 Trust 的 filings 后，对每一份候选文件读取/缓存 SEC filing index 的 Series/Class 元数据。
- 仅当 index 中存在目标 Series 或 Class 时入库；缺失、冲突或无法读取的文件不自动归属，并写入同步详情原因。
- 以 accession number 缓存 index 解析结果，避免每次同步重复请求 SEC。

## UI 与错误处理

- 自动带出成功时显示身份来源和“已精确匹配基金份额”。
- 有候选但不唯一时，在新增标的弹窗显示候选基金名称、CIK、Series/Class，用户确认后保存。
- 完全无法确认时保留现有手动填写入口，并说明手动 Trust CIK 会产生非精确监控风险。

## 验收与测试

- 普通股票的现有 Ticker→CIK 行为不变。
- `company_tickers_mf.json` 命中时可保存完整基金身份。
- DRAM 类“基金清单未收录”的文件回退可解析完整身份。
- 同一 Trust 的两个 ETF 文件中，只入库目标 Series/Class 的文件。
- 缺失或多个 Series/Class 候选时不自动入库，且给出可读原因。
- 网络失败、非法 JSON 和 index 缺失均不让整次同步失败。
