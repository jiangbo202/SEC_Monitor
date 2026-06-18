# SEC Monitor

简体中文 | [English](./README.en.md)

SEC Monitor 是一个本地优先的 Web 应用，用于监控美股和 ETF 的 SEC EDGAR 公告。

> AI 生成 / AI 辅助项目：本仓库由 AI 编程代理协助构建，并由人工反复审查和迭代。请把它视为开源工具，不构成投资建议，也不是生产级合规系统。

## 技术栈

- 后端：Go 1.24、Gin、GORM
- 数据库：默认 SQLite
- 调度：robfig/cron
- 前端：Vue 3、Vite、TypeScript、Element Plus

## 功能

- 监控标的管理：Ticker 自动带出、启用/停用、单标的同步状态。
- SEC 公告同步：去重、重试、首次拉取天数限制、最大拉取数量、可选完整历史归档。
- SEC 公告列表：筛选、分页、Filing Date、发布时间、同步时间、Ticker、公告类型排序。
- 同步历史：状态、触发来源、检查标的数、新增公告数、失败标的数、错误信息。
- 总览页面：监控标的、最近公告、同步健康度、通知状态。
- Telegram：通知配置、测试发送、重试、通知日志。
- 系统配置：SEC 拉取策略、数据保留、默认语言。
- 中英文切换：顶部可切换当前浏览器语言，系统配置可设置默认语言。
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
- 顶部语言切换会保存到当前浏览器，优先级高于系统默认语言。

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
- `AGENTS.md` 已故意忽略，请勿把本地代理指令提交到公开仓库。
- 运行数据、日志、构建产物、依赖目录和缓存已被忽略。
- 不要提交 Telegram Bot Token、SQLite 数据库文件或本地环境文件。

## 许可证

MIT License。详见 [LICENSE](LICENSE)。
