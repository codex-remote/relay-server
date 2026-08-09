# Changelog

AI Coding Remote Relay Server 的重要变更记录在此文件中。

格式参考 [Keep a Changelog](https://keepachangelog.com/en/1.1.0/)。正式发布后遵循 [Semantic Versioning](https://semver.org/spec/v2.0.0.html)。

> Release status: **Unreleased**
>
> 首个生产 GitHub Release 发布前不承诺向后兼容。预发布阶段的破坏性变更会直接移除旧实现，并记录在 `Changed` 或 `Removed`。

## [Unreleased]

### Added

- 提供 `/healthz`、`/status`、`/ws/app` 和 `/ws/agent` 端点。
- 支持 Project、Thread、Turn 消息方向校验与透明转发。
- 支持 Agent 离线拒绝、App 重连快照、Ping/Pong、有界发送队列和优雅关闭。
- 提供 `relayctl` 项目查询、会话查询、Turn 启动、事件观察和中断命令。
- 提供 Docker、Docker Compose、本地 E2E 和 Apifox 同步能力。

### Changed

- Wire Protocol 升级为 `spec_version: "2.0"`，以 Project、Thread、Turn 作为唯一执行模型。
- Relay 保持无状态，不保存 Prompt、日志、Diff、Codex Thread 或业务 Task。

### Removed

- 删除 `spec_version: "1.0"` 的 `run.*` 路由、Schema 和示例。

### Fixed

- 对齐 Relay 与 Mac Agent 的 WebSocket 单消息上限。
- iPhone 重连后可恢复最近的 Agent Hello 和状态快照。
