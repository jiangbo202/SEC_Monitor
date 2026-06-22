# 小盘股研究与风险评分引擎设计

日期：2026-06-22
状态：待用户评审
依赖：[小盘股数据基础](./2026-06-22-small-cap-data-foundation-design.md)
上位规格：[小盘股发现与研究通知系统](./2026-06-22-small-cap-discovery-design.md)

## 1. 目标

对预筛池完成财务归一化、Form 4、融资工具和重大风险解析，生成可重放的数据质量分、基本面评分及正式 A/B 等级。未完成本子项目前不得产生 A/B。

## 2. 数据范围

- 最近 3 个完整财年和 8 个可计算季度。
- 融资工具、权证、反向拆股和退市风险至少回溯 3 年。
- 其他事件正文回溯 12 个月。
- Form 4 至少回溯 180 天。

优先使用 SEC `companyfacts.zip` 和 `submissions.zip`，单公司 API 只做增量补齐。每家公司保存覆盖起止日期和完整度；关键覆盖不足时不得进入 A 级。

## 3. XBRL 事实选择

归一化事实必须执行统一算法：

1. 校验指标单位。
2. 同期间优先最新 accepted time 的修订文件。
3. 期间结束日与 period of report 容差不超过 7 天。
4. duration 75～105 天视为季度、330～400 天视为年度，52/53 周财年按报告元数据校准。
5. 标准 taxonomy 优先；自定义标签必须通过 linkbase 或人工映射。
6. 同优先级冲突标记 `fact_conflict`，不自动猜测。
7. 保存 concept、unit、期间、form、filed、frame、accession 和解析器版本。

累计半年/九个月值通过同口径前期累计值相减还原季度；第四季度通过全年减前三季度。任一输入冲突时该季度不可计算。

### 3.1 v1 概念字典

概念字典作为版本化配置随解析器发布。每个指标按顺序选择一个不冲突的 aggregate fact，不得把语义重叠的 fallback 标签相加：

| 指标 | 标准概念优先级 | 归一化规则 |
|---|---|---|
| 收入 | `RevenueFromContractWithCustomerExcludingAssessedTax`、`Revenues`、`SalesRevenueNet` | USD duration；连续经营口径优先 |
| 现金 | `CashAndCashEquivalentsAtCarryingValue`、`Cash` | USD instant；不采用包含受限现金但无法拆分的总额 |
| 短期投资 | `ShortTermInvestments`、`MarketableSecuritiesCurrent`、`AvailableForSaleSecuritiesCurrent` | USD instant；只选一个总额，披露包含现金等价物时不得重复计算 |
| 经营现金流 | `NetCashProvidedByUsedInOperatingActivitiesContinuingOperations`、`NetCashProvidedByUsedInOperatingActivities` | USD duration；流入为正、流出为负 |
| 资本开支 | `PaymentsToAcquirePropertyPlantAndEquipment`、`PaymentsForAdditionsToPropertyPlantAndEquipment` | USD duration；统一为正数现金支出；负号扩展标签需显式 sign rule |
| 毛利 | `GrossProfit` | USD duration；与收入减营业成本交叉校验 |
| 营业成本 | `CostOfRevenue`、`CostOfGoodsAndServicesSold`、`CostOfGoodsSold` | USD duration；统一为正数成本，只选一个总额 |
| 普通股净利润 | `NetIncomeLossAvailableToCommonStockholdersBasic`、`NetIncomeLoss` | USD duration；使用 `NetIncomeLoss` 时只扣除 `PreferredStockDividendsAndOtherAdjustments` 或 `PreferredStockDividendsIncomeStatementImpact`，无法识别时降低置信度 |
| 流通股 | `dei:EntityCommonStockSharesOutstanding`、`us-gaap:CommonStockSharesOutstanding` | shares instant；不得使用加权平均股数 |
| 一年内长期债务 | `LongTermDebtCurrent`、`CurrentPortionOfLongTermDebt` | USD instant；语义重叠时只选一个 |
| 短期借款 | `ShortTermBorrowings`、`ShortTermDebtCurrent` | USD instant；与一年内长期债务分开后求和 |

公司扩展概念只有在 linkbase 映射到目标概念，或进入版本化 `concept_overrides` 后才能使用。覆盖需包含 CIK、扩展概念、目标指标、sign rule、有效 accession 范围、理由和审计信息。

`可用现金` 只允许“现金 + 一个不重叠短期投资总额”。`未来 12 个月债务` 只允许“一个长期债务 current 总额 + 一个不重叠短期借款总额”。无法证明不重叠时标记冲突，不进行加总。

## 4. 财务指标

### 4.1 收入

计算季度同比和年度同比。最新季度收入低于 5M、同比超过 200%、上年同期不为正或近期存在重大收购/剥离时产生增长质量标记。`comparability_risk` 阻止 A，但允许进入 B 级人工复核。

### 4.2 现金续航

同时计算 CFO 与 FCF 续航：

```text
可用现金 = 现金及现金等价物 + 可随时变现的短期投资
CFO burn = max(0, -TTM 经营现金流 / 12)
FCF burn = max(0, -(TTM 经营现金流 - TTM 资本开支) / 12)
```

A 级使用两种续航的较低值。两者均为正现金流时按最高档处理。未来 12 个月本金到期超过可用现金 25% 时产生债务到期风险并阻止 A。

### 4.3 毛利率与稀释

毛利率水平按本地行业样本百分位评分，样本少于 20 家时使用中性低置信度分；同比改善单独评分。股本稀释按拆并股调整后的一年变化率评分，同时展示三年累计稀释。

## 5. Form 4

合格买入要求 Form 4/4-A、非衍生表、transaction code `P`、acquired code `A`、交易价值大于 0，且角色为 CEO、CFO 或已确认创始人。排除授予、期权、行权、赠与、税务代扣和内部转换。

创始人默认需要人工确认；XML title 明确包含 Founder/Co-Founder 时只生成确认建议。修订表单必须关联原交易并去重。

内幕分为绝对金额 10 分和相对增持 10 分。相对增持使用买入股数除以买入前持股数；无法计算时相对部分为 0 并降低置信度。

## 6. 融资与风险状态机

融资工具状态：

```text
proposed → registered → active → partially_used → exhausted/expired/withdrawn
```

- 普通 shelf registration 不自动等于 ATM，也不单独阻止 A。
- ATM 必须由销售协议或 prospectus supplement 确认；实际销售从 10-Q/10-K、424B5 或 8-K 更新。
- 424B5 关联 registration statement，避免重复融资事件。
- 权证保存发行、剩余、行权价和到期日；潜在稀释超过基本股本 10% 时阻止 A。
- 已确认融资 180 天内阻止 A。
- Going Concern 和退市缺陷阻止 A/B。
- 反向拆股 365 天内阻止 A。

状态只有在结构化字段、明确金额/日期或人工确认后成为 `confirmed`。关键词只能生成 `suspected`。

## 7. 事件解析

识别重大合同、首次 GAAP 季度盈利和上调指引。重大合同高置信度必须识别合同方以及金额、期限、最低采购承诺之一；上调指引必须解析同一指标的新旧区间。

高置信度规则在固定标注样本上的 precision 必须 ≥90%，否则全部降级为人工待确认，不发送即时通知。

## 8. 评分与等级

权重保持：收入 30、现金 20、内幕买入 20、毛利率 10、稀释 10、赛道 10。详细分档使用上位规格第 11 节。

### 8.1 v1 行业体系

按 4 位 SIC 第一匹配规则映射，特殊范围优先于宽范围：

| 本地行业 ID | SIC 范围 | v1 赛道分 |
|---|---|---:|
| `biotech_pharma` | 2833～2836 | 7 |
| `medical_devices_services` | 3841～3851、8000～8099 | 7 |
| `software_it` | 7370～7379 | 8 |
| `semiconductor_electronics` | 3570～3579、3650～3679 | 8 |
| `communication_media` | 4800～4899、7812～7841 | 6 |
| `energy` | 1300～1399、2900～2999 | 5 |
| `utilities` | 4900～4999 | 4 |
| `consumer_staples` | 0100～0999、2000～2199、5400～5499 | 5 |
| `consumer_discretionary` | 2200～2399、2500～2599、3100～3199、3711～3799、5200～5399、5500～5999、7000～7099 | 5 |
| `materials` | 1000～1299、1400～1499、2400～2499、2600～2699、2800～2829、2840～2899、3000～3099、3200～3399 | 5 |
| `industrials` | 1500～1799、3400～3569、3580～3649、3680～3699、3700～3710、4000～4799 | 6 |
| `other` | 其他未排除 SIC | 5 |

SIC 6000～6799 已在证券池排除，不参与映射。范围重叠时按表格顺序取第一个；公司级人工覆盖优先于 SIC，并保存理由。v1 分数是可审计研究假设，不代表行业投资建议。

正式启用 B 级前行业映射覆盖初筛池 ≥90%。未映射公司只进入 `watch`。

行业毛利率基准使用具备有效可比数据的美国经营性普通股，按本地行业分组，每季度生成不可变版本；样本少于 20 家时使用低置信度中性分。

A 级必须满足市值、季度收入增长、现金续航、180 天无确认融资、90 天无 ATM 风险、关键角色 Form 4 买入、无阻断风险、基本面分 ≥70、数据质量 ≥85。

B 级必须满足市值、季度收入增长 >20%、行业分 ≥7、基本面分 ≥50、数据质量 ≥70，且无 Going Concern 或退市缺陷。

## 9. 批次一致性

每次计算生成 `evaluation_batch_id`，绑定证券池、价格、SEC 数据、解析器和配置版本。依赖过期或不一致时批次为 `partial`，保持旧等级并禁止新晋级。

配置必须跨字段校验、生成不可变版本和哈希，并支持激活前数量影响预览。

## 10. 校准门槛

评分 v1 是启发式模型。正式发送普通 A/B 晋级通知前，对至少 200 家分层样本执行两年历史重放，并运行至少 20 个交易日 shadow mode。检查事实正确率、行业偏差、候选数量、风险漏判和人工误判；不得为了达到目标数量调整阈值。

### 10.1 金标准数据集

实现计划必须创建以下离线 fixtures；单元测试不访问真实外网：

- `testdata/security_classification_matrix.csv`：至少 120 个合成契约矩阵案例，覆盖普通股及每种排除原因，每类至少 10 个；它不属于独立来源的 gold evidence。分类激活仍需 Task 12/连续 20 个交易日验证阶段策划真实、独立来源的 gold cases。
- `testdata/gold/xbrl_periods.json`：至少 50 个公司/期间案例，覆盖季度累计还原、第四季度推导、修订、53 周财年、扩展标签、单位冲突和不可比期间。
- `testdata/gold/form4/`：至少 200 份 ownership XML，其中至少 60 份 code P 买入，并覆盖 A/M/F/G、衍生表、多人申报和 4/A。
- `testdata/gold/financing_events.json`：至少 200 个事件，覆盖 shelf、ATM 建立/使用、424B5、PIPE、权证、反向拆股、Going Concern 和解除状态。
- `testdata/gold/contracts_guidance.json`：重大合同和指引各至少 100 个正例、100 个负例。

每个标签保存 accession number、预期标签、事件身份、理由、标注人、复核人、复核状态和源文件 SHA-256。修订表单与原表单按一个事件身份计算，避免重复放大样本。

指标定义：`precision=TP/(TP+FP)`，`recall=TP/(TP+FN)`。只有 `review_status=approved` 的样本进入分母；更新解析规则前后使用相同冻结数据集。新增失败案例只能追加，不能删除使指标下降的样本。

## 11. 验收

- 抽样季度收入还原人工核验一致率 ≥98%。
- Form 4 `P` 解析 precision/recall ≥99%。
- 已确认融资和阻断风险 precision ≥95%、recall ≥90%。
- 高置信度重大合同/指引 precision ≥90%。
- 相同输入和配置得到完全相同评分。
- 关键覆盖不足、冲突或过期时不会产生 A 级。
- 参考环境为 Apple Silicon 8 核、16GB RAM、本地 SSD；输入文件已下载时处理与写库不超过 40 分钟，峰值 RSS 不超过 4GB。
- 性能数据集至少包含 1,500 家预筛公司、每家 8 个季度/3 个财年事实、3 年 submissions 索引及对应事件引用；不足时使用固定合成数据补足并分别报告。
