# IPO 监控安全与可靠性设计

## 目标

将现有 IPO 监控从“可浏览的研究型 MVP”提升为可持续运行的本地优先监控：敏感配置不再以明文留在数据库或日志中；上市、定价、撤回等生命周期信号可解释且不遗漏；Telegram 暂时不可达时不会丢失通知。

本设计只使用 SEC 公开数据，不接入商业上市数据源，也不做投资建议或自动交易。

## 已确认的现状

- IPO 当前流默认只查询 `S-1,S-1/A,F-1,F-1/A,S-1MEF`。`EFFECT`、`424B4`、`RW` 仅在某个近期注册文件触发公司全历史回补时才会被发现。
- SEC 的 `company_tickers_exchange.json` 可能返回有效 Ticker 但没有交易所。当前规则把这种情况归为“更新中”，即使已经有强上市线索。
- 失败通知仅在同一次调用中尝试三次，失败批次没有后续调度重试。
- `Scheduler` 使用一个全局运行锁；小盘任务较长时会静默跳过其他任务。
- Telegram 请求 URL 中的 token 会出现在 HTTP 错误文本中；历史错误和本地 SQLite 曾保存该文本。

## 范围与阶段

### 阶段 1：敏感配置与错误脱敏

1. 增加环境变量 `CONFIG_ENCRYPTION_KEY`。它必须是 Base64 编码的 32 字节随机值，例如：

   ```bash
   openssl rand -base64 32
   ```

2. `internal/config.Config` 读取并校验该值。Docker Compose 透传该环境变量，README 与部署文档说明如何在 `.env` 中设置它。
3. `ConfigService` 对所有 `encrypted=true` 的配置值使用 AES-256-GCM：
   - 存储格式固定为 `enc:v1:<base64(nonce|ciphertext)>`；
   - API 返回仍使用现有掩码，不返回密文或明文；
   - 启动时在事务中迁移现存的明文敏感配置；已是 `enc:v1` 的值不重复加密；
   - 未配置或非法 key 时，已有旧值仍可读取以避免启动即丢配置，但系统健康状态为严重，并拒绝保存新的非空敏感值。这样不会把“encrypted”标签误当成真实加密。
4. 新增统一的错误脱敏函数，至少屏蔽 Telegram URL 中的 bot token、Bearer token 和常见 `token=` 参数；所有通知错误在写库前和 API 返回前都经过该函数。
5. 服务启动时幂等清理 `notification_batches.error_message` 与 `notification_logs.error_message` 中的历史敏感片段，保留非敏感的网络错误上下文。

### 阶段 2：IPO 生命周期与上市确认

1. 保留 `listed` 的严格定义：SEC 映射同时给出 Ticker、交易所和确认时间。
2. 新增 `listing_pending` 状态：SEC 映射有高置信度 Ticker，但交易所为空。状态原因明确为“SEC 已发现 Ticker，等待交易所字段确认”，置信度为 `medium`；它不等同于正式上市。
3. 状态优先级固定为：人工状态覆盖 > `withdrawn` > `listed` > `listing_pending` > `priced` > `effective` > `stale` > `updating` > `new`。这避免撤回文件被陈旧映射覆盖。
4. `IPOCompanyMarketData` 新增生命周期检查时间。每次 IPO 同步除注册文件外，还查询当前流的 `EFFECT`、`424B4`、`RW`；并按最久未检查优先，轮换回补活跃 IPO CIK 的完整 submissions 历史。
5. 新配置：
   - `ipo.lifecycle_sweep_enabled=true`
   - `ipo.lifecycle_max_ciks=50`
   - `ipo.lifecycle_recheck_hours=12`

   “活跃”指 `new`、`updating`、`effective`、`priced` 或 `listing_pending`，且近 180 天有 IPO 生命周期文件。每轮最多检查配置数，避免对 SEC 造成突发请求。
6. 详情页继续允许人工覆盖状态、Ticker、交易所和上市日期；覆盖始终高于自动判断，并显示更新时间与备注。

### 阶段 3：通知投递可靠性与调度

1. `NotificationBatch` 新增 `next_retry_at` 和 `last_attempt_at`。状态扩展为 `pending`、`sent`、`suppressed`、`failed`、`dead_letter`。
2. 首次投递仍可做短暂网络重试；若仍失败，则记录 `failed`，而不是丢弃候选。后续任务按到期时间重新发送同一批候选。
3. 重试采用指数退避：5 分钟、15 分钟、45 分钟、2 小时、6 小时；共最多 5 个投递轮次。超过次数变为 `dead_letter`，保留待人工处理。
4. 新增 `notification_retry_sync` 任务，默认 `*/10 * * * *`。新增 API `POST /notification-batches/:id/retry`，允许把失败或死信批次人工重新排队。
5. `Scheduler` 改为按任务名持有运行锁：同一个任务仍防并发；不同任务可以并行，不再被一次长时间小盘同步静默阻塞。

### 阶段 4：发行解析、健康和页面待办

1. 424B4 解析器升级版本，并记录 `parse_message`：`parsed`、`unsupported`、`invalid_value`、`fetch_failed` 等。解析先读取显式发行价/发行股数文本，再读取常见表格标签；只在价格和基础发行股数都确定时计算募资额，不把超额配售计入主发行数量。
2. 新增 IPO 健康汇总 API，返回：
   - 最近 IPO 同步与新文件数；
   - `listing_pending`、无市场映射、生命周期过期数量；
   - 424B4 解析失败数量；
   - 待重试、死信通知数量。
3. IPO 页面增加“待处理”筛选，可选择：上市待确认、解析失败、生命周期过期、通知失败。页面顶部展示可点击的健康提示。
4. 公司表固定状态、公司和操作列；窄屏保留横向滚动，但以渐隐提示和详情抽屉承载低频字段。所有新增状态、错误原因和健康标签提供中文文案。
5. Dashboard 把最近 IPO 通知失败列入健康提示，不能在失败投递时仍只显示“系统运行正常”。

## 数据迁移与兼容性

- 使用现有 GORM AutoMigrate 增加字段，不删除已有记录。
- 敏感配置的明文迁移只在合法 `CONFIG_ENCRYPTION_KEY` 存在时执行；迁移可重复执行。
- 历史通知错误脱敏会原地更新，不删除批次、数量、时间、状态或非敏感错误内容。
- 既有 `updating` 记录在刷新后可能变为 `listing_pending`；这是有意纠正，不影响人工覆盖。
- 既有失败批次默认进入可重试队列；已超过最大轮次的旧批次标为 `dead_letter`，不自动制造重复通知。

## API 与页面契约

- `GET /ipo-companies` 新增状态 `listing_pending` 与待处理筛选参数。
- `GET /ipo-health` 返回结构化健康摘要。
- `POST /notification-batches/:id/retry` 仅重新排队 `failed` 或 `dead_letter` 批次。
- `GET /notification-batches` 和 Dashboard 的错误文本均是脱敏结果。
- `NotificationBatch`、`IPOCompany`、`IPOOfferingEvent` 的前端 TypeScript 类型同步新增字段与状态。

## 验收标准

1. 含 Telegram token 的错误写入后，数据库/API/页面均只显示掩码；设置合法 key 后，数据库中的 Telegram token 为 `enc:v1:` 密文。
2. “有 SEC Ticker、无交易所”的公司显示 `listing_pending`，原因可读；有撤回文件时显示 `withdrawn`。
3. 近期仅出现 `424B4`、`EFFECT` 或 `RW` 的活跃公司可被当前流或生命周期扫查入库并重新判分。
4. 通知临时失败后会在下一次到期重试任务中重新发送；超过 5 轮变为死信，手工重发可重新投递。
5. 长时间小盘同步运行时，IPO 同步和通知重试仍可各自执行；重复触发同名任务不会并发。
6. 424B4 无法解析时，详情显示原因；健康面板准确统计待处理数量。
7. Go 单元测试覆盖上述状态优先级、加密迁移、错误脱敏、重试退避、调度并行和解析失败原因；前端构建通过。

## 非目标

- 不把 `listing_pending` 自动升级为正式 `listed`。
- 不接入付费或需 token 的第三方上市确认服务。
- 不清除文件、通知或人工覆盖的业务历史。
- 不改变小盘候选的评分规则。
