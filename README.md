# SEC Monitor

简体中文 | [English](./README.en.md)

SEC Monitor 是一个本地优先的 SEC 情报监控系统，用于跟踪美股和 ETF 公告、IPO 生命周期、重大事件、内幕交易披露和 Telegram 通知。

> AI 生成 / AI 辅助项目：本仓库由 AI 编程代理协助构建，并由人工反复审查和迭代。请把它视为开源工具，不构成投资建议，也不是生产级合规系统。

## 技术栈

- 后端：Go 1.24、Gin、GORM
- 数据库：默认 SQLite
- 调度：robfig/cron
- 前端：Vue 3、Vite、TypeScript、Element Plus

## 功能

- 监控标的管理：Ticker 自动带出、分组、启用/停用、单标的同步状态。
- SEC 公告同步：去重、按失败类型限次重试、单标的部分完成、首次拉取天数限制、最大拉取数量、可选完整历史归档。
- SEC 公告列表：筛选、分页、Filing Date、发布时间、同步时间、Ticker、公告类型排序。
- 保存筛选视图：常用公告筛选条件可保存在浏览器本地。
- 重大事件雷达：聚合 8-K、S-1、S-3、424B、13D 等高关注公告。
- IPO监控：扫描 SEC 当前申报流并按 CIK 补齐生命周期文件；覆盖 EFFECT、424B4 和 RW 等关键节点，区分“上市待确认”与正式上市；从 424B4 提取发行信息并展示解析失败原因，支持状态和市场字段手动校准，并将待处理事项直接指向项目筛选或通知重试队列。
- Insider Trading：聚合 Form 3/4/5 内幕人持股变动披露。
- 小盘候选研究：基于 SEC 公司事实、财务指标、资本事件、Form 4 内幕交易和价格数据，筛选 A/B 级小盘候选，并提供变化解释、研究工作台、事件化摘要与历史 cohort 效果评估。
- 宏观日历：独立菜单将“经济数据发布”与“利率 / 流动性”分开查看。经济侧覆盖 BEA 的个人收入与支出/核心 PCE、GDP/实际消费，BLS 的就业报告（非农、失业率、平均时薪）、CPI/核心 CPI、PPI/核心 PPI、JOLTS，DOL 的初请失业金，Census 的零售销售、耐用品订单、新屋开工、新屋销售、国际贸易，EIA 的周度商业原油/汽油/馏分油库存及变化；利率侧覆盖美国财政部每日名义收益率曲线（3M、2Y、5Y、10Y、30Y、10Y-2Y 与 10Y-3M 利差）及实际收益率曲线（TIPS 5Y、10Y、30Y）。同时列出 Census 未来日历和美联储 FOMC 会议日历。全部记录保留官方原文链接与本地前期值，不抓取或混用商业平台的预测、星级或“利多/利空”标签。
- 同步历史与调度：内置 SEC、IPO、小盘研究、通知重试和 SQLite 校验备份任务；同一任务防重，不同任务可并行，可立即执行、启停和调整 Cron。SEC 同步会在可恢复的单标的异常后进行一次自动补偿，并将未解决情况标为“部分完成”；进程重启后会自动清除遗留的任务运行状态，并将超时且无心跳的小盘工作流标记为失败，避免页面永久显示“运行中”。
- 总览页面：分区展示标的监控和 IPO监控 KPI，包含同步健康度、最近公告、IPO进行中公司数、IPO 状态分布和通知状态。
- Telegram：通知配置、测试发送和持久化重试；首次同步及历史/生命周期补齐默认静默入库，每次同步最多发送一条分组摘要，通知批次可展开查看发送、抑制、待重试或死信原因，并支持手动重新投递。
- 系统配置：SEC 拉取策略、通知规则、公告/运行历史保留、SQLite 备份与恢复演练、默认语言。
- 中英文切换：顶部可切换当前浏览器语言，系统配置可设置默认语言。
- 首次启动向导：引导设置 SEC User-Agent、添加标的、配置通知和首次同步。
- 系统健康页：检查 User-Agent、数据库、同步、通知、数据规模，以及 SEC EDGAR 与行情 Provider 的最近运行状态、覆盖率、连续失败和可执行处理入口。
- 导出与备份：导出公告 CSV、标的 CSV、配置 JSON 和完整备份 JSON。
- 数据清理：按保留天数预览并确认清理。
- 审计日志：记录关键变更操作。

### ETF 精确监控

ETF 不能只按 Trust 的 CIK 监控。系统首先读取 SEC `company_tickers_mf.json`：只要其中存在该 Ticker，系统就直接使用该映射的唯一完整身份或返回映射中的完整候选供用户确认，不会再走搜索回退。只有该映射没有此 Ticker 记录时，才使用 SEC 全文搜索和 filing index 解析 `CIK + Series ID + Class ID`。只有完整身份才会保存为新的 ETF 标的，避免将同一 Trust 下其他基金的文件混入结果。

- `DRAM` 示例：Roundhill Memory ETF 的 Trust CIK 为 `0001976517`，精确身份为 `S000102337` / `C000272806`。
- 新增或编辑 ETF 时，自动匹配会直接填入完整身份；有多个候选时，必须在界面中选择正确基金份额才能保存。手工改动 CIK、Series ID 或 Class ID 会取消已验证状态，必须重新查询并使用已解析身份，或重新选择候选；仅恢复字段值不足以恢复验证状态。
- 系统不会自动退化为整个 Trust 的监控；不完整 ETF 身份会被后端拒绝。历史遗留的 Trust 级记录会在标的列表和详情中以黄色提示，其文件可能包含其他基金，应尽快补全身份。
- 同步时先下载 Trust 的 filings，再读取 SEC filing index 中的 Series/Class 元数据；匹配结果会按 accession 缓存，以减少重复 index 请求，并且只入库目标基金份额的文件。

## 快速开始

前置要求：

- Go 1.24+
- Node.js 20+
- npm

本地运行：

```bash
make start
make status
make logs
make restart
make stop
```

默认地址：

- 后端：http://127.0.0.1:8080
- 前端：http://127.0.0.1:5173
- 健康检查：http://127.0.0.1:8080/healthz

本地运行文件：

- PID 文件：`.runtime/`
- SQLite 数据库：`data/sec_monitor.db`
- 日志：`logs/YYYY-MM-DD/`

这些路径已被 Git 忽略。

## Docker 部署

Docker 镜像包含 Go API 服务和已构建的 Vue 前端。一个容器即可提供完整 Web UI 和 API。

当前 Compose 映射：

- 访问地址：http://127.0.0.1:9090
- 容器端口：`8080`
- `docker-compose.yml` 映射：`9090:8080`

前置要求：

- Docker
- Docker Compose v2

构建镜像：

```bash
make docker-build
```

使用 Docker Compose 启动：

```bash
make docker-up
```

`make docker-up` 会先停止本地 `make start` 服务，再启动 Docker 容器。若手动执行 `docker compose up`，请先运行 `make stop`，避免浏览器访问到旧的本地后端。

打开：

- Web UI：http://127.0.0.1:9090
- 健康检查：http://127.0.0.1:9090/healthz

常用 Docker 命令：

```bash
make docker-up       # 构建并启动
make docker-logs     # 查看容器日志
make docker-down     # 停止并移除容器，保留数据卷

docker compose ps
docker compose restart sec-monitor
docker compose logs -f sec-monitor
docker compose down
```

数据持久化：

- 容器内 SQLite 数据库：`/app/data/sec_monitor.db`
- Docker 命名卷：`sec_monitor_sec-monitor-data`
- `docker compose down` 会保留数据卷和数据库。
- `docker compose down -v` 会删除数据卷和数据库。

日志：

- Docker 容器日志输出到 stdout/stderr。
- 使用 `make docker-logs` 或 `docker compose logs -f sec-monitor` 查看。
- 本地开发的 `logs/` 目录不会被 Docker 容器使用。

修改 Docker 端口：

```yaml
ports:
  - "9090:8080"
```

把左侧改成你需要的宿主机端口，例如 `18080:8080`，然后执行：

```bash
make docker-up
```

正式使用前，请设置明确的 SEC User-Agent。推荐在 Web UI 的“系统配置 / SEC 拉取策略”填写类似 `SEC Monitor your-email@example.com` 的值，保存后执行 `make docker-up` 重启服务使其生效；也可以编辑 `docker-compose.yml` 中的 `SEC_USER_AGENT`，或启动时传入：

```bash
SEC_USER_AGENT="sec-monitor/0.1 your-email@example.com" docker compose up -d --build
```

同时必须为系统配置中的敏感值设置 32 字节 Base64 加密密钥。生成后保存到部署目录的 `.env`（不要提交该文件），然后重新执行 `make docker-up`：

```bash
openssl rand -base64 32
```

```env
CONFIG_ENCRYPTION_KEY=<output of openssl rand -base64 32>
```

不要轮换或丢失已在用的密钥，否则已有加密的 Telegram 与数据源配置将无法解密。未设置或密钥无效时，已有旧版明文敏感配置仍可读取用于恢复，但系统会报告严重健康问题，且不会保存新的非空敏感值。设置或更换 `.env` 后必须再次执行 `make docker-up`，再访问 `http://127.0.0.1:9090/api/ipo-health` 确认 IPO 运行状态。

升级或重建：

```bash
git pull
make docker-up
```

发布镜像示例：

```bash
docker build -t ghcr.io/<user>/sec-monitor:latest .
docker push ghcr.io/<user>/sec-monitor:latest
```

## 配置

运行时配置在 Web UI 的 `系统配置` 页面中管理。

SEC 拉取配置：

- `sec.sync_window_days`：每次同步只处理最近 N 天公告，`0` 表示不限制时间窗口。
- `sec.initial_fetch_days`：新标的首次同步只处理最近 N 天公告。
- `sec.max_fetch_count`：每个标的每次同步最多处理多少条公告，`0` 表示不限制。
- `sec.fetch_full_history`：是否启用 SEC 归档 submissions 文件拉取。

数据保留配置：

- `system.data_retention_days`：按同步入库时间保留公告，过期公告可预览并清理。
- `system.operation_history_retention_days`：保留已完成的 SEC 同步运行/明细、小盘工作流步骤和运维告警去重记录，默认 90 天；同时会清理超过该期限、且已被当前批次替代的单标的行情修正完整快照，以避免反复补价放大 SQLite。不会删除公告、当前候选、收盘价历史、通知或研究结论。
- `system.storage_by_day`：预留的按天分目录存储开关。
- `system.backup_retention_days`：SQLite 快照备份保留天数，默认 7 天。
- `system.backup_dir`：SQLite 快照目录；留空时使用数据库目录下的 `backups/`。Docker 默认落在持久化卷中的 `/app/data/backups/`。
- `system.storage_warning_pct`：数据库所在磁盘的使用率告警阈值，默认 `80`。超过阈值会在系统健康页提示。

SQLite 备份与恢复：

- 调度任务 `sqlite_backup` 默认每天 `03:15`（按“调度任务”页面显示的时区）执行。它通过 SQLite `VACUUM INTO` 生成独立快照，先分别校验 `sec_monitor` 与 `small_cap` 的临时文件，再一起发布为完整备份组；任一阶段失败都会删除临时/半套文件。系统健康页的“恢复演练”会把最新完整备份对复制到临时隔离目录，以只读方式执行 SQLite `integrity_check` 并校验关键业务表；不会覆盖当前数据库或保留临时副本，结果会记录在本地。
- 系统健康页的“整理数据库”是低峰期手动维护操作：它会先创建并校验一组新的完整备份，再对 `sec_monitor` 与 `small_cap` 在线库顺序执行 SQLite `VACUUM`，释放已删除快照占用的物理空间。执行期间需要独占写入锁，因此请避免与 SEC/IPO/小盘同步并行；前后体积、耗时、状态和错误会记录在本地。此操作不请求 SEC 或行情 Provider，也不删除任何业务数据。
- 备份目录和保留天数可在“系统配置 / 数据保留与清理”调整；备份任务也可在“调度任务”页面手动执行、改期或停用。
- 调度任务 `operation_history_cleanup` 默认每周日 `03:45` 执行。它清理超过运行历史保留期的已完成工作流日志，以及已被当前批次替代的旧单标的行情修正完整快照；“系统配置 / 数据保留与清理”支持先预览后手动清理。运行中的任务、当前候选、行情历史和全部研究结论均会保留。
- 调度页面会保存最近状态、连续失败次数与脱敏后的错误原因；连续失败 3 次以上也会进入系统健康告警。
- “同步历史”会记录每个标的的失败类别、请求尝试次数和下次自动处理时间。超时、临时上游异常会在本轮执行中进行一次补偿；`429` 会等待下一轮调度，避免继续消耗额度；`404`、基金身份不完整和配置问题会暂缓 24 小时，期间不再自动请求 SEC，但可从同步历史手动重试。
- “系统健康 / 运行摘要与待办”提供不消耗外部接口额度的本地运行报告：聚合任务失败/部分完成/卡住、超时未运行、SEC 自动重试队列、暂缓标的、行情 Provider、备份过期/不完整及恢复演练异常，并附带直达处理入口。可手动发送运行摘要到 Telegram；调度任务 `operational_health_notification_sync` 默认关闭，启用后每天 `09:15` 仅在存在异常时推送，且相同异常在 12 小时内不会重复打扰。
- 运行摘要还会检查最近 48 小时的慢步骤：单个 SEC 标的同步超过 2 分钟，或小盘工作流阶段超过其阶段阈值（行情/SEC 全量 90 分钟、技术历史 30 分钟、公司资料 20 分钟等）会显示为待办；仍在运行且超过阈值的阶段标为严重。所有判断仅基于本地运行时间，不会额外调用外部接口。
- 收到 Docker 停止信号或本地 `Ctrl+C` 时，HTTP 服务会先停止接收新请求，并在最多 25 秒内完成正在处理的请求后退出。
- 恢复时先停止服务，保留当前 `sec_monitor.db` 和 `small_cap.db` 的副本，再用同一时间戳的两份快照替换数据库文件；如存在对应的 `-wal` / `-shm` 文件也一并移除，然后重新执行 `make docker-up`。恢复会回到快照时点，之后写入的数据不会自动合并。

数据源健康与降级：

- “系统健康 / 数据源健康”不会主动请求 SEC 或行情接口，因此不会额外消耗额度；它只汇总已落库的 SEC 调度状态和行情 Provider 运行记录。
- `warning` 表示最近失败、验证窗口未完成或行情源已降级；`critical` 表示连续失败达到 3 次或 Provider 已失败。每行“去处理”会带到调度任务或小盘发现日志，便于查看脱敏错误、覆盖率和回退来源。
- 多行情源链（例如 `longbridge,tiingo,twelvedata`）仍按既有顺序自动补齐剩余标的；健康页会显示实际发生降级的 Provider，而不是发起额外探测。市场批次的覆盖率、来源分布和 Provider 明细可在“小盘发现日志”继续查看。

界面配置：

- `ui.default_locale`：默认界面语言，支持 `zh-CN` 和 `en-US`。
- `ui.onboarding_completed`：是否已完成首次启动向导。
- 顶部语言切换会保存到当前浏览器，优先级高于系统默认语言。

通知规则配置：

- `notification.important_only`：仅通知重点公告类型。
- `notification.filing_types`：只通知指定公告类型，使用逗号分隔，例如 `8-K,10-K,S-1`。
- `notification.keywords`：只通知标题或正文中包含指定关键词的公告，使用逗号分隔。
- `notification.quiet_hours_enabled`：是否启用静默时间。
- `notification.quiet_hours_start` / `notification.quiet_hours_end`：静默时间范围，格式 `HH:mm`。

IPO监控配置：

- `ipo.enabled`：是否启用 IPO监控。
- `ipo.form_types`：扫描的 SEC 表单类型，默认 `S-1,S-1/A,F-1,F-1/A,S-1MEF`。
- `ipo.lookback_days`：只保留最近 N 天的当前申报结果。
- `ipo.max_results`：每类表单最多拉取条数，SEC 当前申报接口上限按 100 处理。
- `ipo.notify_enabled`：IPO 申报入库后是否发送 Telegram 提醒。
- `ipo.notify_form_types`：只提醒指定 IPO 表单类型，例如 `EFFECT,424B4`；留空表示全部。
- `ipo.keywords`：按公司名或标题过滤，逗号分隔；留空表示不过滤。

IPO 页面说明：

- `公司视图`：按 CIK/公司聚合 IPO 项目，状态由系统根据本地已入库文件推断，不是 SEC 官方字段；默认仅显示进行中的项目。勾选“显示已结束项目”或直接按状态筛选，可查看已上市、撤回和过期历史。
- 状态列包含判断依据、置信度和来源；可在详情抽屉中手动覆盖状态、最终 Ticker 和备注。
- `公司视图`展开后，文件按 `SEC 接收时间` 从旧到新展示，便于查看 IPO 流程。
- `申报列表`：按同步入库时间和 SEC 接收时间从新到旧展示，便于查看最新发现。
- `IPO进行中`统计不包含已定价、已上市、撤回/终止项目。
- 支持导出 IPO 公司 CSV 和 IPO 文件 CSV。
- SEC 上市公司映射匹配后，系统记录最终 Ticker、交易所和首次确认上市时间；该时间不是实际首日交易日期。
- IPO 同步会先读取 SEC 的上市公司映射。只有同时具备 Ticker 与交易所的公司才视为已确认上市；其后出现的 S-1/F-1 注册文件不会再次进入 IPO 候选、回补多年历史文件或发送普通 IPO 通知。仅有 Ticker 的 `listing_pending` 公司仍会继续监控。
- 新发现的 424B4 会保守解析发行价、发行数量和预计募资总额，无法明确提取时保持为空且不影响同步。
- 每份 424B4 都记录为发行事件：最早可解析文件为首次定价，后续文件区分重复条款、定价修正和后续发行；只有首次定价和定价修正会更新 IPO 摘要并发送独立定价通知，后续发行不会覆盖原 IPO 发行价。
- `公司详情`中的`发行事件`表可查看事件分类、发行条款、SEC 文件和通知状态。
- 手动状态、Ticker、交易所、发行价、发行数量和实际上市日期优先于自动数据。

IPO 运营状态：

- `new`：发现初始 S-1/F-1 注册文件；`updating`：发现修订文件；`effective`：发现 EFFECT；`priced`：发现 424B4 定价文件。
- `listing_pending`：SEC 映射已有 Ticker、但尚未确认交易所；`listed`：Ticker 与交易所已确认；`withdrawn`：发现 RW；`stale`：超过 60 天没有新的 IPO 文件。
- `listing_pending`、`parse_failed`、`lifecycle_stale`、`notification_failed` 和 `missing_market_mapping` 都是公司列表的运营关注筛选。健康卡会显示对应数量、到期重试和死信数量；点击标签可直接筛选项目。
- 健康卡下方的“运营待办”按处理优先级列出死信通知、到期重试、待确认上市、缺少市场映射、过期生命周期核查和发行解析失败。每项均提供下一步说明及直达入口；通知类事项会带到“通知日志”的对应失败队列，其他事项会带到 IPO 公司筛选。加密密钥缺失或无效会显示为严重健康问题。

IPO 生命周期与通知运营：

- `ipo.lifecycle_sweep_enabled` 默认开启。每次 IPO 同步会按最久未检查优先补查仍在进行中的公司；仅检查近 180 天有 IPO 生命周期文件的项目。`ipo.lifecycle_max_ciks`（默认 `50`，范围 `1`–`200`）限制单次补查量，`ipo.lifecycle_recheck_hours`（默认 `12`，范围 `1`–`168`）定义过期检查窗口。已手动标记为 `listed` 或 `withdrawn` 的公司不会参加补查；生命周期补齐只入库，不发送通知。
- `notification_retry_sync` 默认启用、每 10 分钟运行一次。它只重投到期的失败批次；每个批次经历首次发送后，依次在 5 分钟、15 分钟、45 分钟、2 小时和 6 小时后重试。仍失败后进入 `dead_letter`（死信），不会丢失内容，可在“通知日志”筛选死信并点击“重新投递”。
- 打开“通知日志”的“通知批次”页查看 `pending`、`sent`、`suppressed`、`failed` 与 `dead_letter`。对 `failed` 或 `dead_letter` 批次点击“重新投递”，会重置重试周期并立即加入队列；正在重试的批次不能手动重新投递。

小盘候选研究功能：

- 功能定位：仅用于研究与通知，不构成投资建议，也不自动交易。
- 数据来源：SEC 官方 companyfacts/submissions bulk 数据、SEC Form 4、SEC capital event 文件、本地财务解析结果，以及配置的日线价格源。
- 研究模式默认先做 SEC/财务初筛，再按价格源链补充价格和市值相关证据，避免对全市场逐只请求价格。
- A 级候选重点关注：市值小于 500M、收入高增长、现金 runway 充足、近期无融资风险、近 180 天有合格 Form 4 open-market buy。
- B 级候选重点关注：市值小于 1B、收入增长较好、资本风险可控，并保留质量状态和缺失原因。
- B 级候选会进一步分层为`强B`、`普通B`和`观察B`：低收入基数、极端增长、低流动性、财务缺失或活跃融资风险会被打上质量标签，帮助优先排查可信度。
- 候选列表默认打开`主推荐`视图：仅显示 A、强B和普通B中没有财务缺失、低收入基数、低流动性或活跃融资风险的标的；可一键切回完整研究池，观察B和风险标的不会被删除。
- 候选列表默认按`优先级`（0–100）排序，综合总分、质量层级、新增/改善状态、流动性、价格来源、市值和风险标签；悬浮可查看逐项加减分和字段变化依据。
- 候选详情会展示最多 20 条近期 SEC 公告的类型、日期、Item 和 EDGAR 链接。系统只保存公告索引，不复制 SEC 正文；新一次 security universe 同步后即可为当前候选补齐该证据链。
- 候选详情的“证据溯源”会集中展示当前评分批次依赖的评分、价格/市值、财务、Form 4、近期公告、融资风险及公司资料快照的来源、截至日期和覆盖状态。详情页只读本地数据；不会因查看页面额外调用 SEC、Longbridge 或其他行情接口。
- 候选详情的“评分历史与入选事件”保留最近 12 个已发布批次的等级、总分、分差、评分版本和核心变化原因，并展示不可变的首次进入 A/B 或质量升级事件及其当日价格基线。它用于复核候选变化，不构成收益或交易承诺。
- 候选详情的“建议下一步”会将证据状态转换为单一研究动作：例如补齐价格、市值、财务或 Form 4 覆盖，确认生物医药业务模型/稀释风险，或在证据完整后阅读近期文件并形成可证伪论点。它是研究流程提示，不构成买卖建议。
- 价格健康同时展示本批次有效交易日的当日价格、回退到前一交易日的价格、过期价格与缺失价格。列表“价格日期”标签可查看每只标的的具体新鲜度，批次覆盖率不再等同于所有价格都是当日收盘价。
- 健康栏会单独统计“内幕来源是否同步”“候选中实际有内幕记录的数量”“候选中实际有 SEC 公告索引的数量”和“有合格买入的数量”。当来源标记已同步但当前候选的 Form 4 或 SEC 公告覆盖为 `0/N` 时，健康状态会明确降级，而不会误报正常。
- `调整分`会对低收入基数、极端增长、低流动性、财务缺失和活跃融资风险设置保守上限，原始总分仍保留用于审计。
- 候选概览会展示等级分布、强B/观察B、新增/改善、主要赛道和质量标签分布，用于快速判断当天列表是否值得深入查看。
- 候选变化状态包括`新增`、`改善`、`转弱`和`延续`，由当前发布批次与上一已发布 market-prescreen 批次对比得出。
- 页面提供独立`候选关注列表`，可将小盘候选按待研究、研究中、重点关注或淘汰管理，并记录论点、风险、失效条件与下次复查日期；这不同于 SEC 监控标的，主要用于研究跟踪。
- 候选页会将已关注标的中逾期、当天及未来 7 天需要复查的研究卡聚合为“研究复查待办”；日期按系统配置的调度时区解释。即使标的已退出当前候选池，待办仍会保留，避免研究结论失去复核；待办仅读取本地研究卡和当前评分，不会触发 SEC 或行情请求。
- `效果评估`按首次进入 A/B 候选时的价格快照统计 1/5/20 日 cohort 收益、胜率和最大回撤；本地具备 IWM 数据时额外展示相对收益，缺失时明确标记为不可比。
- 市场质量使用本地日线快照派生平均成交额、波动率、20 日动量与最大回撤；异常候选会打上可复核标签，但不替代基础评分。
- 技术分析同样基于本地日线快照，独立识别“上穿 20 日均线”“突破前 20 日最高收盘价”和“放量突破”（量比至少 1.5 倍）。每只标的需至少 21 个有效交易日；历史不足时明确标记，不会生成虚假信号。技术信号仅用于研究筛选与详情解释，不改变基本面总分、等级或通知规则。详情图表额外显示 MA20、MA50 与 MA200，供短线/中长期趋势复核。
- 每次成功发布 market prescreen 后，系统默认自动预热当前 A/B 候选中历史不足 200 个交易日的标的；它只请求尚未补齐的新候选，并补齐近 320 个自然日（通常约 220 个交易日），沿用现有行情源、token 轮换、请求预算和回退顺序。预热失败（例如临时限流）只会记录 warning，不会撤销已发布的候选批次。需要立即重试时，也可在“小盘候选”页面执行一次`回填技术历史`；该操作不改基本面评分、不发送通知。
- 候选详情中的融资/稀释风险会区分当前活跃、近 180 日已失效与更早的历史记录；更早的已失效文件仅保留为审计证据，不会被标注为当前风险或评分阻断。
- 候选效果评估扩展至 60 个交易日；候选列表可按当前筛选条件导出 CSV。
- 候选通知会合并活跃关注候选近 24 小时的重大 SEC 文件（8-K、定期报告、注册/招股文件、EFFECT、RW、Form 4），仍受 Telegram 开关和日内去重保护。
- 收入增长评分优先使用最新可比季度同比；季度不可用才回退年度同比。季度转弱而年度仍偏高时会标注质量提示，不会用年度最大值掩盖近期变化。
- 风险事件识别包括 S-1、ATM Program、Reverse Split、Going Concern Warning、Warrants 等；相关事件会影响候选资格和得分。
- 候选得分维度包括收入增长、现金储备、内幕增持、毛利率、股本稀释历史和赛道空间；当前实现中缺失或无法可靠自动判断的维度会以质量状态保守处理。
- 同步过程按批次落库，保留 source version、provider run、provider health、候选快照和健康检查，便于审计和重跑。
- “小盘发现日志”额外保留每次工作流的步骤时间线：准备/缓存清理、SEC 增量财务、SEC 全量宇宙、行情预筛、技术历史、Longbridge 公司资料补充和摘要健康检查都会记录开始时间、结束时间、记录数和可脱敏的失败原因；未生成批次的失败也可追溯。
- “小盘发现日志 / 行情 Provider 配置与可观测性”只读取本地已落库的配置、最近市场批次、价格来源构成、Provider 健康记录和 NYSE 交易日历覆盖情况，不会触发行情请求。页面展示的 Tiingo/Twelve Data/Yahoo 请求数是本系统的单次运行保护预算（Tiingo 为每个已配置 token 的预算之和），并非供应商账户的剩余额度；实际额度请以供应商后台为准。
- `small_cap_discovery_sync` 作为每日增量任务：消费 SEC 监控新发现的 10-Q/10-K，仅向 SEC 请求受影响发行人的 Company Facts JSON；同时对比当天 Nasdaq/SEC 紧凑证券目录，发现全新的 CIK/Ticker 后只下载该发行人的 submissions 与 Company Facts JSON，合并进新的不可变证券池，再更新行情预筛。它不会下载全量 companyfacts/submissions archive。若 Ticker 复用、CIK 已存在的新上市线或身份冲突，为避免错误合并会留给每周全量校准处理。`small_cap_discovery_full_sync` 是独立、默认关闭的每周全量校准任务，用于重新发现标的并校验完整 SEC 宇宙。请在“调度任务”页面为全量校准选择低峰周末时间并启用。
- `watch_target_market_sync` 是独立的监控标的日线任务，默认启用。它每天在调度时区的周二至周六 `05:30` 运行（上海时区对应前一美股交易日收盘后，兼容夏令时/冬令时），只读取所有已启用监控标的并向已配置行情源请求最新已完成 NYSE 交易日的收盘价、累计成交量和估算成交额，再写入本地日线供详情图表和技术信号使用。它不请求 SEC、不改变小盘候选评分；休市或尚未收盘时会安全跳过。可在“调度任务”页面调整 cron、启停或手动运行。
- market 同步采用研究模式发布：即使价格 provider 仍在 validation/degraded 或当日价格数据部分缺失，也允许发布可审计的研究批次；生产级 provider 激活门槛仍保留在状态机中。
- 研究模式支持最低发布覆盖率保护；当本次价格覆盖率低于阈值时，系统保留诊断和价格证据但不发布新候选批次，避免用少量结果覆盖已有完整列表。
- 同一交易日可重复补跑 market sync。已失败的同内容批次会被重置后重试，已发布批次保持幂等。
- Tiingo 限流时会停止继续请求未缓存标的；若配置了后续价格源，系统会继续用后续来源补缺口；如果最终覆盖率低于最低发布阈值，则 market 阶段失败并等待下次补跑。
- “系统健康 / 运行摘要与待办”将 SEC 到期重试、暂缓标的、Longbridge 公司资料到期补偿、当前候选行情补偿和低覆盖率 Provider 汇总为可操作待办。Provider 错误会按限流、超时/上游、认证权限、资源不存在等类别给出恢复建议；该页面只读取本地状态，不会消耗外部接口额度。

小盘候选配置：

- `discovery.price_provider`：价格数据源。支持单源 `longbridge`、`tiingo`、`twelvedata`、`yahoo`、`stooq`，也支持链式配置如 `longbridge,tiingo,twelvedata,yahoo`。链式模式会按顺序请求，后续 provider 只补前面未覆盖的 ticker。
- `discovery.longbridge_app_key`、`discovery.longbridge_app_secret`、`discovery.longbridge_access_token`：Longbridge OpenAPI 凭据。系统通过官方 Go SDK 批量请求美股常规时段 Quote（每请求最多 500 个代码），只在美股收盘后写入当日正式收盘价与累计成交量；盘中不会把即时成交价当作日线收盘。三个凭据均加密保存并在页面脱敏显示。
- `discovery.longbridge_company_profile_enabled`、`discovery.longbridge_company_profile_request_budget`、`discovery.longbridge_company_profile_ttl_days`：控制公司资料补充。每次小盘同步发布后，仅为当前候选中缺失或过期的公司按预算增量请求 Longbridge `company overview`；成功结果缓存到本地，详情页不会请求外部接口。候选和监控标的详情均可手动“刷新公司资料”强制更新一个标的，并明确展示 Longbridge 来源和更新时间。
- Longbridge 公司资料的单标的临时失败会保留本地重试状态（最近错误、失败次数、下次补偿时间）。后续小盘同步优先补偿已到期失败项，并避免在退避窗口内反复消耗请求；“小盘发现日志 / 公司资料补偿队列”可查看脱敏错误并立即重试某一家，不会重跑 SEC 或行情同步。
- `discovery.longbridge_analyst_rating_enabled`、`discovery.longbridge_analyst_rating_request_budget`、`discovery.longbridge_analyst_rating_target_change_pct`：控制 Longbridge 的机构评级聚合共识同步。每次候选工作流会在不影响候选发布的前提下，以同一个总预算轮转当前候选和已启用的股票监控标的；详情页可手动“刷新分析师评级”，只请求当前 ticker，不会重跑 SEC、价格同步或候选评分。系统只保存提供方的评级分布、覆盖数与目标价聚合结果，不虚构个人分析师或机构名单。
- `analyst_rating.notify_enabled`：开启后，评级方向、覆盖数量或平均目标价达到阈值的变化会沿用 Telegram 通知批次发送；首次建立快照不推送。变化先本地持久化，进程重启或短暂 Telegram 故障后会在下一次候选工作流恢复投递。
- “小盘发现日志 / 行情补偿队列”只使用已落库的当前 A/B 候选、价格快照和本地日线诊断，列出缺价、过期价、日期异常或本地回退价。单标的“补齐并重算”仅请求该标的的历史行情，随后复制当前市场批次并只替换该标的的价格、市值、资格和分数，发布新的可追溯市场修正批次；不会下载 SEC、不会扫描全量标的，也不会修改历史批次。若本地报价已经属于更新的交易日，则需要运行正常市场同步，以保证所有标的采用同一有效日期。
- `discovery.stooq_urls`：Stooq 格式 CSV/ZIP 下载地址，多个用逗号分隔。必须是后端可直接下载的数据文件 URL，普通网页地址不可用；当前未内置可靠官方默认地址。
- `discovery.tiingo_api_token`：单个 Tiingo token。兼容旧配置。
- `discovery.tiingo_api_tokens`：多个 Tiingo token，逗号或换行分隔；系统会按顺序轮换使用。
- `discovery.tiingo_request_budget`：单次 market sync 每个 Tiingo token 最多发起的 HTTP 请求数，用于控制免费额度消耗；`0` 表示不限制。
- `discovery.twelve_data_api_key`：Twelve Data API Key。
- `discovery.twelve_data_request_budget`：单次 market sync 最多发起的 Twelve Data 请求数，默认 `700`。
- `discovery.twelve_data_request_interval_ms`：Twelve Data 请求间隔，默认 `8000ms`，用于控制免费层 `8 API credits/minute` 的分钟额度。
- `discovery.yahoo_request_budget`：单次 market sync 最多发起的 Yahoo chart 请求数；Yahoo 不需要 token，但仍建议控制请求量。
- `discovery.min_publish_coverage_pct`：研究模式最低发布覆盖率，默认 `85`。低于该值，或相对上一已发布批次下降超过 15 个百分点时，不会覆盖当前候选列表。
- `discovery.auto_technical_history_warmup`：是否在成功发布候选后自动补齐新候选的技术历史，默认 `true`。关闭后仍可在页面手动回填。
- `discovery.cache_dir`：小盘候选 SEC bulk/cache 目录。Docker 推荐使用持久化路径 `/app/data/.cache/discovery`。

Longbridge 同时支持候选的日 K 历史回填；历史 OHLCV 中的每日累计成交量会保存到本地价格快照，并在候选详情的日线图中以成交量柱状展示。为控制历史数据的唯一标的额度，自动预热与手动回填只处理当前候选，不对全部 SEC 标的无差别拉取历史。

“监控标的”详情的本地日线与候选详情共用同一份价格快照库：首次可通过“回填/刷新价格历史”补齐 MA20/MA50/MA200 所需历史；之后由 `watch_target_market_sync` 每日仅追加/校验最新已完成收盘，不会每天重新下载完整历史窗口。

Docker 运行小盘候选：

```bash
make docker-up
make docker-discovery-sync          # 全流程：security universe + market prescreen
make docker-discovery-market-sync   # 仅补跑 market prescreen，适合 Tiingo 限流后继续补价格
```

本地 Go 运行：

```bash
go run ./cmd/discovery-sync          # 手动全量校准
DISCOVERY_SYNC_PHASE=incremental go run ./cmd/discovery-sync # 手动执行每日轻量增量（含新增上市标的发现）
DISCOVERY_SYNC_PHASE=market go run ./cmd/discovery-sync
```

首次执行会下载 SEC bulk 数据，体积通常为数 GB，耗时较长；后续会复用缓存。Docker 模式下请使用 `make docker-discovery-sync`，避免把缓存和数据库写到本地进程的不同路径。

Tiingo 免费额度注意事项：

- 免费额度通常不足以每天对全部美股逐只请求价格；推荐设置较小的 `discovery.tiingo_request_budget`，并依靠缓存分批补齐。
- 推荐价格源使用 `tiingo,twelvedata,yahoo`：Tiingo 先请求有限预算内的标的，Twelve Data 再补 Tiingo 未覆盖的缺口，Yahoo 最后兜底。
- Twelve Data 免费额度接近一次全量 market sync，且有 `8 API credits/minute` 限制；默认每 8 秒请求一次。补 700 个 ticker 约需 90 分钟，适合每天定时跑一次，不要频繁手动重跑。
- 多 token 只能缓解单 token 限流，不能改变 Tiingo 对账号/网络/服务端策略的限制。
- 页面 API usage 出现 hourly/daily 429 后，需要等待 Tiingo 额度恢复；恢复后运行 `make docker-discovery-market-sync` 继续补齐。

通知批次说明：

- 新标的首次同步只建立数据基线，不发送历史公告通知。
- 后续完整历史归档中早于上次成功同步时间的公告只入库，不通知。
- IPO 首次扫描建立静默基线，公司生命周期补齐始终不通知。
- 小盘候选通知会做降噪：A 级正常纳入；B 级仅纳入`强B`，或`普通B`且状态为新增/改善的候选；`观察B`、低质量和融资风险候选只在页面和日报中展示，不主动推送。
- 每次同步最多发送一条 Telegram 摘要；通知日志默认按批次展示，展开后可查看每条公告的发送或抑制原因。

环境变量：

```bash
APP_ADDR=127.0.0.1:8080
DB_TYPE=sqlite
DB_DSN=data/sec_monitor.db
SEC_BASE_URL=https://data.sec.gov
SEC_USER_AGENT="sec-monitor/0.1 your-email@example.com"
SEC_TIMEOUT_MS=10000
SEC_REQUESTS_PER_SECOND=8
SEC_MAX_RETRIES=2
SMALL_CAP_PRICE_PROVIDER=longbridge,tiingo,twelvedata,yahoo
SMALL_CAP_LONGBRIDGE_APP_KEY="your-longbridge-app-key"
SMALL_CAP_LONGBRIDGE_APP_SECRET="your-longbridge-app-secret"
SMALL_CAP_LONGBRIDGE_ACCESS_TOKEN="your-longbridge-access-token"
TIINGO_API_TOKEN="your-tiingo-token"
TIINGO_API_TOKENS="token-a,token-b"
SMALL_CAP_TIINGO_REQUEST_BUDGET=45
TWELVE_DATA_API_KEY="your-twelve-data-key"
SMALL_CAP_TWELVE_DATA_REQUEST_BUDGET=700
SMALL_CAP_TWELVE_DATA_REQUEST_INTERVAL_MS=8000
SMALL_CAP_YAHOO_REQUEST_BUDGET=200
SMALL_CAP_AUTO_TECHNICAL_HISTORY_WARMUP=true
SMALL_CAP_MIN_PUBLISH_COVERAGE_PCT=85
SMALL_CAP_CACHE_DIR=/app/data/.cache/discovery
SMALL_CAP_CACHE_RETENTION_DAYS=14
```

SEC 要求请求方设置明确的 User-Agent。正式使用前请设置 `SEC_USER_AGENT`。
小盘候选的价格源、Tiingo token、token 组、Twelve Data API Key、请求预算、Yahoo 请求预算和最低发布覆盖率可以在页面“系统配置 / 小盘候选数据源”中填写，也可以通过环境变量注入；页面配置优先于环境变量。不要把真实 token 写入仓库文件或提交到 git。

小盘研究库继续使用 SQLite，并启用 WAL、5 秒写锁等待与小连接池，降低后台同步与页面读取并发时的锁冲突。“小盘候选”页会显示研究库和 SEC 下载缓存的实际占用；缓存超过保留期后会在同步开始时自动清理，也可在页面确认后手动清理。此操作只删除可重新下载的缓存文件，不会删除候选、评分、公告、通知或研究记录。

## 开发

后端测试：

```bash
GOCACHE=$(pwd)/.cache/go-build GOMODCACHE=$(pwd)/.cache/go-mod go test ./...
```

前端构建：

```bash
cd web
npm run build
```

覆盖率：

```bash
GOCACHE=$(pwd)/.cache/go-build GOMODCACHE=$(pwd)/.cache/go-mod go test ./... -coverprofile=/tmp/sec_monitor_cover.out
go tool cover -func=/tmp/sec_monitor_cover.out
```

## 仓库说明

- 本项目是 AI 生成 / AI 辅助代码库。部署或依赖告警前请自行审查。
- 运行数据、日志、构建产物、依赖目录和缓存已被忽略。
- 不要提交 Telegram Bot Token、SQLite 数据库文件或本地环境文件。

## 许可证

MIT License。详见 [LICENSE](LICENSE)。
