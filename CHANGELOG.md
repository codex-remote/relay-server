# Changelog

Codex Remote Relay Server 的重要变更记录在此文件中。

格式参考 [Keep a Changelog](https://keepachangelog.com/en/1.1.0/)。正式发布后遵循 [Semantic Versioning](https://semver.org/spec/v2.0.0.html)。

> Release status: **0.0.1**
>
> `0.x` 阶段继续快速迭代。后续变更按实际影响决定是否兼容，并记录在 `Changed` 或 `Removed`；`0.0.1` 不构成兼容性冻结。

## [Unreleased]

### Added

- Adopted Apache License 2.0, public contribution guidance, CI, and Dependabot updates.
- Added JSON `events:poll` Session/Run endpoints with durable sequence cursors, bounded waits, PostgreSQL page reads, Redis notifications and terminal-state responses; existing SSE routes remain available.
- Added an independent PostgreSQL `auth` schema and Runtime Auth module with one-time pairing grants, opaque Access/Refresh Tokens, scope enforcement, transactional Refresh rotation, replay detection, session revocation, and client revocation.
- Added a loopback-only Auth Control listener on `127.0.0.1:18776` plus `relayctl pair`, `auth-clients`, and `revoke-client` commands.
- Added the standalone `pairqr` utility with automatic LAN Origin detection, terminal QR rendering, and optional private-permission PNG output.
- Registered the project-owned `pairqr.sh` launcher with devrun through Auth Control port `18776` and the `crpair` alias, preserving terminal QR output and runtime arguments.
- Added request ID response headers and public Runtime Auth OpenAPI contracts.
- Runtime Session catalogs now expose the optional `latest_run_status` compatibility field so clients can render latest-turn state without per-Session snapshot requests.
- Added PostgreSQL Runtime Core with seven minimal tables, transactional Run creation and command outbox dispatch.
- Added Redis active Run streams, Session notifications, renewable Agent presence and Runtime HTTPS/SSE endpoints.
- Added Agent event receipt/durable ACK handling, Bootstrap batch persistence and mobile-web OpenAPI/Apifox contracts.
- Added explicit `RUNTIME_ALLOWED_ORIGIN=*` support for local and LAN development without per-IP CORS updates.
- Bootstrap jobs now expose processed/total session progress and archive missing Codex-backed Sessions only after a complete snapshot, keeping Mac deletions out of Runtime client catalogs without destroying Run history.
- Runtime Project catalogs now use reversible snapshot reconciliation: complete unresumed Bootstrap jobs hide missing Projects, restored Agent snapshots unhide them, active Runs are protected, and reconnect-resumed jobs remain import-only.
- Added `POST /v1/runtime/projects/{project_id}/source:read` with unified Runtime Auth, bounded in-memory request correlation, Agent disconnect/timeout handling, OpenAPI and v2 source message fixtures; source content is never persisted.
- Added an isolated `mobileweb` launcher profile on port `18775` with LAN-compatible Runtime CORS for the guarded one-command Mobile Web deployment flow.
- Added a `mobileweb-debug` launcher profile on `18875` with Auth Control on `18876`, keeping the Homebrew/release `mobileweb` profile on `18775/18776`.

### Changed

- The `mobileweb` Run Server profile now listens on `127.0.0.1:18775`; browsers enter through the Mobile Web Gateway instead of connecting directly.
- Runtime routes now use centralized default-deny Bearer/Scope middleware, including `source:read` for source access.
- Runtime Auth now plugs into Server composition through a minimal module interface; Runtime path-to-scope policy belongs to Server while Auth remains route-agnostic.

### Fixed

- Reduced terminal pairing QR output to roughly one quarter of its previous area by using square-proportioned half-block cells and placing the QR last so it remains visible in an 80×24 terminal.
- Added TTY-aware, `NO_COLOR`-compatible colors for pairing titles, expiration, links, PNG paths, and credential warnings.
- Kept the bright-white 2×4 Braille renderer as an explicit space-saving mode, while making the white half-block renderer the default because its full-area QR modules scan reliably across terminal fonts; link suppression and indentation controls remain available for embedded deployment output.
- Bootstrap terminal snapshots now replace stale Run status and event history by Codex Turn ID, advance the Session cursor only for material changes, and ignore nonterminal history instead of treating unknown states as completed.
- Wait for the exact managed Relay PID to exit before reusing a launchd label, preventing fast restarts from losing the replacement job after the listener closes early during graceful shutdown.
- Made concurrent idempotent Session/Run creation return one resource without unique-key races.
- Prevented replayed Agent events from advancing Run state or creating duplicate Session events.
- Return empty JSON arrays instead of `null` for empty Runtime collections.
- Bootstrap imports now resolve existing Codex Thread and Turn identifiers to their canonical Runtime Session and Run before persisting events, avoiding foreign-key failures during incremental history sync.
- Bootstrap completion now finalizes its outbox command in the same transaction, and terminal sync commands are discarded before dispatch so interrupted Relay shutdowns cannot replay completed history imports.

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
