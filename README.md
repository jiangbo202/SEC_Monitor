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
- IPO监控：扫描 SEC 当前申报流中的 S-1、F-1、S-1MEF 等 IPO/融资相关申请；发现公司后按 CIK 补齐 S-1/F-1、修订稿、EFFECT、424B、撤回类文件；支持申报列表和公司视图，并按公司聚合标注新申报、更新中、已生效、已定价、已上市、撤回和长期无更新等状态。
- Insider Trading：聚合 Form 3/4/5 内幕人持股变动披露。
- 同步历史与调度：内置 `sec_filing_sync` 和 `ipo_radar_sync` 两类周期任务，可立即执行、启停和调整 Cron。
- 总览页面：分区展示标的监控和 IPO监控 KPI，包含同步健康度、最近公告、IPO进行中公司数、IPO 状态分布和通知状态。
- Telegram：通知配置、测试发送、重试、通知日志。
- 系统配置：SEC 拉取策略、通知规则、数据保留、默认语言。
- 中英文切换：顶部可切换当前浏览器语言，系统配置可设置默认语言。
- 首次启动向导：引导设置 SEC User-Agent、添加标的、配置通知和首次同步。
- 系统健康页：检查 User-Agent、数据库、同步、通知和数据规模。
- 导出与备份：导出公告 CSV、标的 CSV、配置 JSON 和完整备份 JSON。
- 数据清理：按保留天数预览并确认清理。
- 审计日志：记录关键变更操作。

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
- `ipo.keywords`：按公司名或标题过滤，逗号分隔；留空表示不过滤。

IPO 页面说明：

- `公司视图`：按 CIK/公司聚合 IPO 项目，状态由系统根据本地已入库文件推断，不是 SEC 官方字段。
- `公司视图`展开后，文件按 `SEC 接收时间` 从旧到新展示，便于查看 IPO 流程。
- `申报列表`：按同步入库时间和 SEC 接收时间从新到旧展示，便于查看最新发现。
- `IPO进行中`统计不包含已定价、已上市、撤回/终止项目。

环境变量：

```bash
APP_ADDR=127.0.0.1:8080
DB_TYPE=sqlite
DB_DSN=data/sec_monitor.db
SEC_BASE_URL=https://data.sec.gov
SEC_USER_AGENT="sec-monitor/0.1 your-email@example.com"
SEC_TIMEOUT_MS=10000
```

SEC 要求请求方设置明确的 User-Agent。正式使用前请设置 `SEC_USER_AGENT`。

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
