# SEC Monitor

本地优先的美股研究与 SEC 情报工作台。它把公告、监控标的、IPO、宏观数据、小盘候选、机构持仓和手动 AI 研判沉淀到本地 SQLite，便于持续复核；不构成投资建议，也不自动交易。

> **安全边界：local-first、无内置认证、不要公网裸露部署。** Docker 默认仅绑定 `127.0.0.1:9090`。如需远程访问，请通过 VPN 或已配置登录、TLS 与访问控制的反向代理，不要直接暴露应用端口。

> 项目包含 AI 辅助生成的代码。用于真实资金或对外生产环境前，请自行完成安全、合规和数据质量审查。

## 能做什么

- **监控标的与 SEC 公告**：管理股票/ETF，增量同步 EDGAR 文件，查看重大事件、内幕交易、财报预告与发布，并按条件发送通知。
- **IPO 监控**：跟踪 S-1/F-1、EFFECT、424B4、RW 等生命周期文件；结合 SEC 映射和 Longbridge 二次核验上市状态，支持关注公司及进展通知。
- **小盘研究与策略观察池**：基于本地财务、价格、技术、流动性与交易纪律快照筛选候选，保留评分、变化原因、交易计划和历史效果。
- **标的评估**：输入股票或 ETF 后复用已有研究逻辑，输出基本面、短线复核、趋势/动量、量价及交易纪律，并保存历史快照。
- **宏观与市场研究**：查看大盘、行业 ETF、期货、宏观日历（含非农、CPI、PPI、PCE、FOMC 等）及机构 13F 持仓变化。
- **研究补充**：保存 Longbridge 分析师共识、估值、期权研究、机构持仓与公司资料；详情页仅读取本地快照，不会因浏览页面额外调用第三方接口。
- **AI 研判**：支持多个 OpenAI 兼容提供商（含 DeepSeek）、可配置提示词模板、手动异步执行、结果/提示词/耗时审计和站内完成通知。不会自动调用第三方 AI。
- **运营与通知**：统一管理站内消息、Telegram、去重、重试、死信、任务日志、系统健康、SQLite 备份和恢复演练。

## 数据与边界

| 场景 | 主要来源 | 说明 |
| --- | --- | --- |
| 事实、公告、IPO、13F | SEC EDGAR | 业务事实的主来源 |
| 行情、公司资料、共识、估值、期权 | Longbridge，可配置 Tiingo / Twelve Data / Yahoo 回退 | 结果先写入本地快照 |
| 宏观日历 | BEA、BLS、FRED、Fed、Treasury、Census、DOL、EIA | BLS 暂不可用时可由 FRED 的 BLS 镜像回填；页面会标记为“数据期” |
| AI 研判 | 用户配置的 OpenAI 兼容 API | 仅在页面手动触发时调用 |

系统不会把商业平台的预测、评级或“利多/利空”标签当作 SEC 官方事实；数据缺失会明确显示，而不会填造结论。

## 快速开始

### Docker（推荐）

前置条件：Docker 与 Docker Compose v2。

```bash
# 生成并写入本地 .env；不要提交此文件
openssl rand -base64 32

# 启动（会构建前后端）
make docker-up
```

访问：<http://127.0.0.1:9090>

健康检查：<http://127.0.0.1:9090/healthz>

首次打开后，在“系统配置”完成：

1. 配置明确的 SEC User-Agent（例如 `SEC Monitor your-email@example.com`）。
2. 配置 `CONFIG_ENCRYPTION_KEY`，用于加密 Telegram、Longbridge、价格源和 AI 密钥。
3. 按需填写 Longbridge / Tiingo / Twelve Data / AI Provider 凭据。
4. 添加监控标的，并在“调度任务”检查启用的任务与运行时间。

`.env` 示例：

```env
CONFIG_ENCRYPTION_KEY=<openssl rand -base64 32 的输出>
SEC_USER_AGENT=SEC Monitor your-email@example.com
```

不要丢失或随意轮换已在使用的加密密钥，否则历史加密配置无法解密。

### 本地开发

前置条件：Go 1.24+、Node.js 20+、npm。

```bash
make start      # 后端 :8080，前端 :5173
make status
make logs
make stop
```

## 常用命令

```bash
# Docker
make docker-up
make docker-logs
make docker-down

# 小盘研究（Docker）
make docker-discovery-sync
make docker-discovery-incremental-sync
make docker-discovery-market-sync

# 测试与构建
go test ./...
cd web && npm run build
```

`make docker-up` 会停止本地开发服务，再启动 Docker 容器。`docker compose down` 保留数据库卷；`docker compose down -v` 会删除所有 Docker 数据，操作前请确认已备份。

## 使用建议

1. 先添加少量监控标的，完成一次 SEC 同步并核对结果。
2. 配置行情源后运行小盘研究或标的评估；缺少的价格/基本面证据会保留为待补齐状态。
3. 在“调度任务”按北京时间配置自动刷新；每项任务独立运行，不会因为某项失败阻塞其他数据源。
4. 在“系统健康”“同步历史”“通知日志”处理失败、重试与备份容量告警；避免频繁手动重跑额度敏感的行情或 AI 任务。
5. AI 仅作为研究辅助：先在详情页选择模型和模板手动执行，再结合原始 SEC 文件与本地证据判断。

## 运行与数据存储

- 默认数据库：`data/sec_monitor.db`；小盘研究使用独立 SQLite 库。
- Docker 数据保存在命名卷 `sec_monitor_sec-monitor-data`，容器内路径为 `/app/data`。
- 本地运行日志默认位于 `logs/YYYY-MM-DD/`；Docker 日志通过 `make docker-logs` 查看。
- SQLite 备份、恢复演练、数据库整理和历史清理由系统配置与调度任务统一管理。清理前会预览，备份任务会校验完整备份组。
- 同步、通知和 AI 调用均保留本地状态与脱敏错误，便于审计和排障。

## 安全

- 不要提交 `.env`、数据库、备份、日志、Token 或 API Key。
- 配置接口只返回脱敏密钥；敏感配置在数据库中加密保存。
- AI、Longbridge、Telegram 和价格源请求均可能产生费用或额度消耗，请使用预算、调度和手动触发控制。
- 备份默认仍在本机/本地 Docker 卷；如需灾备，请自行配置受控的异地备份流程。

## 项目结构

```text
cmd/                服务与研究同步入口
internal/api/       Gin 路由与处理器
internal/service/   业务、调度、通知与研究逻辑
internal/sec/       SEC EDGAR 客户端与解析
internal/model/     GORM 模型
web/                Vue 3 前端
docs/               详细设计、API 与运维文档
```

详细资料请查阅 [docs](docs/)；备份与恢复边界见 [docs/operations/backup-and-recovery.md](docs/operations/backup-and-recovery.md)。

## 许可证

[MIT License](LICENSE)
