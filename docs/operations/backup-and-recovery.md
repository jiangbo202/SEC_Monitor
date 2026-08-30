# 备份与恢复边界

## 当前已自动完成的内容

- `sqlite_backup` 每天按调度页的全局时区在 `03:15` 创建一组 SQLite 一致性快照。
- 每组包含 `sec_monitor` 与 `small_cap` 两个文件；两者都通过 `integrity_check` 后才会发布为可恢复点。
- 配置 `system.backup_replica_dir` 后，本地完整快照组会以临时文件写入外部目录，完成同步与数据库校验后再原子发布；本地与外部目录不能相同。
- 默认保留最近 7 天完整快照组；过期的运行历史、任务执行记录及通知批次按“运行历史保留天数”清理。
- 系统健康页区分“副本文件齐全”与“副本恢复通过”。恢复演练分别选取本地和副本目录最近的完整双库快照，在隔离临时目录恢复，比较 SHA-256、执行 SQLite 完整性和应用必要表结构校验；不覆盖在线数据库。
- 两个来源的演练结果和失败原因独立持久化；旧演练没有来源标记时显示“尚未验证”。副本开启但恢复失败时，整体结果不会显示通过；本地备份丢失时仍会尝试验证副本。校验和用于检测暂存复制过程中的变化，不是对备份内容真实性的签名，也不证明双库跨业务事务原子性。
- 默认宿主机目录只能称为“备份副本”，应用不能证明它位于独立磁盘或异地主机。文件完整、恢复可用、异地容灾是三个不同保证。

## 配置异地副本

容器卷中的 `/app/data/backups` 与在线 SQLite 数据库位于同一个 Docker volume。它可以防止逻辑误操作和单文件损坏，但不能防范主机、磁盘或 volume 丢失。

当前 `docker-compose.yml` 已默认把宿主机 `./backups` 绑定到容器 `/app/backup-replica`，并通过 `SEC_MONITOR_BACKUP_REPLICA_DIR` 自动启用副本。这足以防范 Docker named volume 被误删，但不能防范整台主机或同一磁盘损坏。生产部署应通过 `SEC_MONITOR_BACKUP_REPLICA_HOST_DIR` 改为独立磁盘、NAS 或受控对象存储挂载目录，例如：

```yaml
services:
  sec-monitor:
    environment:
      SEC_MONITOR_BACKUP_REPLICA_DIR: /app/backup-replica
    volumes:
      - sec-monitor-data:/app/data
      - /srv/sec-monitor-replica:/app/backup-replica
```

也可以在 `.env` 中设置 `SEC_MONITOR_BACKUP_REPLICA_HOST_DIR=/srv/sec-monitor-replica`。如果“系统配置”里另行填写了 `system.backup_replica_dir`，该显式设置优先于环境变量。系统不会保存存储服务凭据，也不会直接连接云厂商接口；认证、传输加密和挂载可用性由部署层负责。

要形成完整灾备，还应确定：

1. 备份目标和加密方式；
2. 上传频率与保留天数；
3. 是否启用版本锁定或不可变保留；
4. 恢复到新主机的演练流程。

在这些信息确认前，应用不会自行把包含本地研究数据的备份上传到第三方。未配置异地目录时，系统健康会提示备份无法防范主机或 Docker volume 丢失；已配置但不可写、缺少完整快照组或超过 30 小时未更新时会触发健康告警。

## 容量与日常运维

- 系统健康页会在完整 SQLite 备份超过 50GB 时给出容量告警；这不会自动删除备份，仍由 `system.backup_retention_days` 控制保留周期。
- 小盘研究库、下载缓存和备份都使用同一持久化卷。建议至少预留“当前研究库大小 × 保留天数 + 50%”的空间，并在低峰期执行数据库压缩。
- 容器健康检查调用本地 `/healthz`；它只验证进程存活。数据源、任务、通知与恢复演练状态应以“系统健康”页为准。
- 如配置 `system.backup_dir`，目标目录必须是 Docker 容器可写的持久化挂载目录；否则仍使用 `/app/data/backups`。
- 如配置 `system.backup_replica_dir`，应使用与本地备份不同的独立挂载；系统会使用与本地相同的保留天数清理完整副本组，且不会自动删除目录内无法识别的其他文件。
