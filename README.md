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
- SEC 公告同步：去重、重试、首次拉取天数限制、最大拉取数量、可选完整历史归档。
- SEC 公告列表：筛选、分页、Filing Date、发布时间、同步时间、Ticker、公告类型排序。
- 保存筛选视图：常用公告筛选条件可保存在浏览器本地。
- 重大事件雷达：聚合 8-K、S-1、S-3、424B、13D 等高关注公告。
- IPO监控：扫描 SEC 当前申报流并按 CIK 补齐生命周期文件；覆盖 EFFECT、424B4 和 RW 等关键节点，区分“上市待确认”与正式上市；从 424B4 提取发行信息并展示解析失败原因，支持状态和市场字段手动校准。
- Insider Trading：聚合 Form 3/4/5 内幕人持股变动披露。
- 小盘候选研究：基于 SEC 公司事实、财务指标、资本事件、Form 4 内幕交易和价格数据，筛选 A/B 级小盘候选，并提供变化解释、研究工作台、事件化摘要与历史 cohort 效果评估。
- 同步历史与调度：内置 SEC、IPO、小盘研究和通知重试任务；同一任务防重，不同任务可并行，可立即执行、启停和调整 Cron。
- 总览页面：分区展示标的监控和 IPO监控 KPI，包含同步健康度、最近公告、IPO进行中公司数、IPO 状态分布和通知状态。
- Telegram：通知配置、测试发送和持久化重试；首次同步及历史/生命周期补齐默认静默入库，每次同步最多发送一条分组摘要，通知批次可展开查看发送、抑制、待重试或死信原因，并支持手动重新投递。
- 系统配置：SEC 拉取策略、通知规则、数据保留、默认语言。
- 中英文切换：顶部可切换当前浏览器语言，系统配置可设置默认语言。
- 首次启动向导：引导设置 SEC User-Agent、添加标的、配置通知和首次同步。
- 系统健康页：检查 User-Agent、数据库、同步、通知和数据规模。
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

正式使用前，请设置明确的 SEC User-Agent。可以编辑 `docker-compose.yml` 中的 `SEC_USER_AGENT`，也可以启动时传入：

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
- `system.storage_by_day`：预留的按天分目录存储开关。

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
- `listing_pending`、`parse_failed`、`lifecycle_stale` 和 `notification_failed` 是公司列表的运营关注筛选。健康卡还会单独显示 `missing_market_mapping`、到期重试和死信数量；加密密钥缺失或无效会显示为严重健康问题。

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
- 价格健康同时展示本批次有效交易日的当日价格、回退到前一交易日的价格、过期价格与缺失价格。列表“价格日期”标签可查看每只标的的具体新鲜度，批次覆盖率不再等同于所有价格都是当日收盘价。
- 健康栏会单独统计“内幕来源是否同步”“候选中实际有内幕记录的数量”“候选中实际有 SEC 公告索引的数量”和“有合格买入的数量”。当来源标记已同步但当前候选的 Form 4 或 SEC 公告覆盖为 `0/N` 时，健康状态会明确降级，而不会误报正常。
- `调整分`会对低收入基数、极端增长、低流动性、财务缺失和活跃融资风险设置保守上限，原始总分仍保留用于审计。
- 候选概览会展示等级分布、强B/观察B、新增/改善、主要赛道和质量标签分布，用于快速判断当天列表是否值得深入查看。
- 候选变化状态包括`新增`、`改善`、`转弱`和`延续`，由当前发布批次与上一已发布 market-prescreen 批次对比得出。
- 页面提供独立`候选关注列表`，可将小盘候选按待研究、研究中、重点关注或淘汰管理，并记录论点、风险、失效条件与下次复查日期；这不同于 SEC 监控标的，主要用于研究跟踪。
- `效果评估`按首次进入 A/B 候选时的价格快照统计 1/5/20 日 cohort 收益、胜率和最大回撤；本地具备 IWM 数据时额外展示相对收益，缺失时明确标记为不可比。
- 市场质量使用本地日线快照派生平均成交额、波动率、20 日动量与最大回撤；异常候选会打上可复核标签，但不替代基础评分。
- 技术分析同样基于本地日线快照，独立识别“上穿 20 日均线”“突破前 20 日最高收盘价”和“放量突破”（量比至少 1.5 倍）。每只标的需至少 21 个有效交易日；历史不足时明确标记，不会生成虚假信号。技术信号仅用于研究筛选与详情解释，不改变基本面总分、等级或通知规则。详情图表额外显示 MA20 与 MA50，供短线/中期趋势复核。
- 每次成功发布 market prescreen 后，系统默认自动预热当前 A/B 候选中历史不足 50 个交易日的标的；它只请求尚未补齐的候选，并沿用现有行情源、token 轮换、请求预算和回退顺序。预热失败（例如临时限流）只会记录 warning，不会撤销已发布的候选批次。需要立即重试时，也可在“小盘候选”页面执行一次`回填技术历史`；默认请求近 120 个自然日（通常约 80 个交易日），不改基本面评分、不发送通知。
- 候选详情中的融资/稀释风险会区分当前活跃、近 180 日已失效与更早的历史记录；更早的已失效文件仅保留为审计证据，不会被标注为当前风险或评分阻断。
- 候选效果评估扩展至 60 个交易日；候选列表可按当前筛选条件导出 CSV。
- 候选通知会合并活跃关注候选近 24 小时的重大 SEC 文件（8-K、定期报告、注册/招股文件、EFFECT、RW、Form 4），仍受 Telegram 开关和日内去重保护。
- 收入增长评分优先使用最新可比季度同比；季度不可用才回退年度同比。季度转弱而年度仍偏高时会标注质量提示，不会用年度最大值掩盖近期变化。
- 风险事件识别包括 S-1、ATM Program、Reverse Split、Going Concern Warning、Warrants 等；相关事件会影响候选资格和得分。
- 候选得分维度包括收入增长、现金储备、内幕增持、毛利率、股本稀释历史和赛道空间；当前实现中缺失或无法可靠自动判断的维度会以质量状态保守处理。
- 同步过程按批次落库，保留 source version、provider run、provider health、候选快照和健康检查，便于审计和重跑。
- market 同步采用研究模式发布：即使价格 provider 仍在 validation/degraded 或当日价格数据部分缺失，也允许发布可审计的研究批次；生产级 provider 激活门槛仍保留在状态机中。
- 研究模式支持最低发布覆盖率保护；当本次价格覆盖率低于阈值时，系统保留诊断和价格证据但不发布新候选批次，避免用少量结果覆盖已有完整列表。
- 同一交易日可重复补跑 market sync。已失败的同内容批次会被重置后重试，已发布批次保持幂等。
- Tiingo 限流时会停止继续请求未缓存标的；若配置了后续价格源，系统会继续用后续来源补缺口；如果最终覆盖率低于最低发布阈值，则 market 阶段失败并等待下次补跑。

小盘候选配置：

- `discovery.price_provider`：价格数据源。支持单源 `tiingo`、`twelvedata`、`yahoo`、`stooq`，也支持链式配置如 `tiingo,twelvedata,yahoo`、`tiingo,yahoo` 或 `stooq,tiingo,yahoo`。链式模式会按顺序请求，后续 provider 只补前面未覆盖的 ticker。
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

Docker 运行小盘候选：

```bash
make docker-up
make docker-discovery-sync          # 全流程：security universe + market prescreen
make docker-discovery-market-sync   # 仅补跑 market prescreen，适合 Tiingo 限流后继续补价格
```

本地 Go 运行：

```bash
go run ./cmd/discovery-sync
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
SMALL_CAP_PRICE_PROVIDER=tiingo,twelvedata,yahoo
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
```

SEC 要求请求方设置明确的 User-Agent。正式使用前请设置 `SEC_USER_AGENT`。
小盘候选的价格源、Tiingo token、token 组、Twelve Data API Key、请求预算、Yahoo 请求预算和最低发布覆盖率可以在页面“系统配置 / 小盘候选数据源”中填写，也可以通过环境变量注入；页面配置优先于环境变量。不要把真实 token 写入仓库文件或提交到 git。

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
