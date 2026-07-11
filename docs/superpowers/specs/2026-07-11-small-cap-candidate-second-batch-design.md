# 小盘股候选第二批设计

日期：2026-07-11
状态：已确认，实施中

## 目标

让候选评分可解释、把候选关注升级为研究工作台，并用首次进入 A/B 候选时的历史快照评估结果。系统继续只提供研究证据，不产生交易指令或收益承诺。

## 评分与变化

- 收入增长以最新可比季度同比为主；只有季度同比不可用时，才回退年度同比。
- 若季度同比为负、年度同比显著为正，保留季度结果并增加 `quarterly_growth_conflicts_with_annual` 质量标签，避免年度数据掩盖近期转弱。
- 审阅优先级归一为 0–100。分数由质量调整分、等级、变化、内幕买入、价格来源、流动性、市值及风险标签组成，并在 API 返回逐项加减分。
- 候选列表返回 `change_reasons`：首次入选、等级变化、总分变化、收入增长变化、现金 runway 变化和风险阻断变化。`change_status` 仍保留，确保现有筛选兼容。

## 研究工作台与提醒

- `CandidateWatch.Status` 继续表示 `active/archived` 生命周期。
- 新增独立研究状态：`inbox`、`researching`、`conviction`、`rejected`；归档不等于淘汰。
- 观察记录支持研究论点、风险、失效条件与下次复查日期。更新同一 ticker 时保持未提交字段，避免快速操作清空研究内容。
- 候选 Telegram 摘要只保留符合现有 A/B 和优先级设置的行动项，并在标题/原因中包含新增、改善、风险变更等变化信息；同批次日内去重规则保持不变。

## 效果评估

- 以证券第一次进入已发布 A/B 批次时关联的有效收盘价为基准，计算 1/5/20 日 cohort 表现。
- 报告按 A、B 和合计展示候选数、已成熟样本数、平均收益、胜率和最大回撤；数据不足时用 `null` 明确表示。
- IWM 仅作为可选基准：当本地 `price_snapshots` 已有同一窗口的 IWM 价格时返回超额收益；不存在时报告 `benchmark_available=false`，不伪造结果。后续可在独立行情任务中补齐基准数据。

## API

| 方法 | 路径 | 变更 |
|---|---|---|
| `GET` | `/api/discovery/candidates` | 增加 `change_reasons`，优先级范围改为 0–100 |
| `GET` | `/api/discovery/candidate-watches` | 返回研究状态、论点、风险、失效条件、下次复查日期 |
| `POST` | `/api/discovery/candidate-watches` | 接收并校验研究字段 |
| `GET` | `/api/discovery/candidates/effectiveness` | 返回 1/5/20 日 cohort 指标和可选 IWM 对比 |

## 非目标

- 不自动抓取或创建 IWM 历史数据。
- 不将研究状态自动变更为买卖意见。
- 不回填或修改历史候选快照。
