# Changelog

AI Coding Remote Relay Server 的重要变更记录在此文件中。

格式参考 [Keep a Changelog](https://keepachangelog.com/en/1.1.0/)。正式发布后遵循 [Semantic Versioning](https://semver.org/spec/v2.0.0.html)。

> Release status: **0.0.1**
>
> `0.x` 阶段继续快速迭代。后续变更按实际影响决定是否兼容，并记录在 `Changed` 或 `Removed`；`0.0.1` 不构成兼容性冻结。

## [Unreleased]

## [0.0.1] - 2026-08-13

### Added

- 提供 `/healthz`、`/status`、`/ws/app` 和 `/ws/agent` 端点。
- 支持 Project、Thread、Turn 消息方向校验与透明转发。
- 提供可直接用于 Apifox 调试的 `project.list`、`project.snapshot`、`thread.list` 和 `thread.snapshot` 契约示例。
- 支持 `thread.read -> thread.detail` 方向校验、Relay CLI 查询、JSON fixtures 和 Apifox Body 契约。
- Thread 快照新增 `latest_message_preview`，明确区分最新可见消息与 Codex 原始 `preview`。
- 提供 `/ws/app` 与 `/ws/agent` 完整的发送 Body、接收 Body 和字段规范，并支持独立同步及远端回读校验。
- 为 Apifox `/ws/app` 提供可直接发送的 `project.list` 默认 JSON Message。
- 支持 Agent 离线拒绝、App 重连快照、Ping/Pong、有界发送队列和优雅关闭。
- 支持转发并缓存 `agent.capabilities`，让 App 在提交任务前获知远程执行的沙箱和宿主能力限制。
- 支持 `execution.profile.list/snapshot` 和 `turn.start.permission_profile_id`，让 iPhone 使用 App Server 对当前项目允许的执行档位。
- 提供 `relayctl` 项目查询、会话查询、Turn 启动、事件观察和中断命令。
- `relayctl` 新增项目 permission profile 查询和 Turn `--profile` 参数，便于真实链路验收。
- 提供 Docker、Docker Compose、本地 E2E 和 Apifox 同步能力。
- 提供隔离 `debug`（`18765`）、`simulator`（`18767`）和 `iphone`（`18768`）服务的 `./run` 本地重启脚本。
- 支持 `turn.acknowledged`，并在 `/status` 暴露最近确认的 Turn 终态与当前 App 连接代次，供延迟真机刷新可靠等待最终输出送达。

### Changed

- Wire Protocol 升级为 `spec_version: "2.0"`，以 Project、Thread、Turn 作为唯一执行模型。
- Relay 保持无状态，不保存 Prompt、日志、Diff、Codex Thread 或业务 Task。
- 文档明确 Admin Platform、Diagnostics API、用户/行为管理、SLS 和 CoreDevice 独立于 Relay；Relay 只生产可采集的结构化服务日志。
- 本地、Docker、Relay CLI 和 Apifox 的默认端口统一为 `18765`。
- Apifox WebSocket 接口改为相对路径，由所选环境提供 Relay 前置 URL。

### Removed

- 删除 `spec_version: "1.0"` 的 `run.*` 路由、Schema 和示例。

### Fixed

- 对齐 Relay 与 Mac Agent 的 WebSocket 单消息上限。
- iPhone 重连后可恢复最近的 Agent Hello 和状态快照。
- Agent 产生的 `turn.rejected` 可携带 `execution_context`、所需能力和恢复动作，旧 payload 字段保持兼容。
