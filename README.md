# AI Coding Remote - Relay Server

Go 实现的无状态通信中枢。Relay 在 iPhone App 和 Mac Agent 之间转发 WebSocket JSON 消息，不执行 Codex，也不保存 Project、Thread、Prompt、输出、Diff 或业务 Task。

## 技术栈

| 层 | 选择 |
| --- | --- |
| 语言 | Go 1.23+ |
| HTTP | `net/http` |
| WebSocket | `github.com/coder/websocket` |
| 状态 | 进程内单 App/Agent Connection Registry |
| 日志 | `slog` JSON Handler |
| 协议 | JSON + JSON Schema，`spec_version: "2.0"` |
| 部署 | 原生二进制或 Docker |

当前不使用 Gin、Python、数据库、Redis、MQ、鉴权框架或 ORM。

## MVP 能力

- `GET /healthz`：进程健康。
- `GET /status`：App/Agent 当前连接状态、App 连接代次和最近的终态确认。
- `/ws/app`：iPhone 或 `relayctl` 入口。
- `/ws/agent`：Mac Agent 入口。
- v2 Project/Thread/Turn 消息方向白名单和透明转发，包括 `thread.read -> thread.detail` 历史查询。
- 转发按项目的 `execution.profile.list -> execution.profile.snapshot` 权限协商，并在 `turn.start` 传递所选 profile ID。
- Agent 离线时返回 `turn.rejected/AGENT_OFFLINE`。
- App 重连时恢复最近的 `agent.hello`、`agent.status` 和 `agent.capabilities`。
- Ping/Pong、256 KiB 帧限制、有界发送队列和优雅关闭。
- `relayctl` 查询项目、会话列表和会话详情，启动 Turn、观察事件和中断。

MVP 无鉴权、数据库、业务 Task、队列和执行历史。只能用于可信局域网、Tailscale 或等价私有网络，不能直接暴露到公网。

## Admin 与日志边界

Relay 只承担 iPhone 与 Mac Agent 的实时通信，不承载 Admin 管理页面、Diagnostics API、用户信息、用户行为、SLS 查询、诊断附件或 CoreDevice 操作。下一阶段这些能力属于独立的 `admin-platform`：Admin Server 与 Admin Web 同一服务交付，Diagnostics Collector 作为独立进程运行。

Relay 负责生产标准 JSONL 服务日志，未来由本地 Diagnostics Collector 或线上 LoongCollector 增量采集。日志采集失败不得改变 WebSocket 行为，也不能反向阻塞连接读写循环。

目标日志契约：

- 一行一条 UTF-8 JSON，使用稳定 `event` 名称和 `event_id`；
- 保留 `timestamp`、`level`、`source`、`service_instance`、`environment`、`session_id`、`trace_id` 和版本字段；
- Prompt、响应、Diff、Token、Authorization、完整 URL 和任意用户正文不得进入服务日志；
- 循环、Ping/Pong、流式转发和重试事件必须采样或聚合；
- 日志写入使用有界队列，压力下先丢弃低等级事件并记录累计丢弃数；
- Relay 不直接写 Admin 的 SQLite/PostgreSQL，也不持有 SLS 管理凭据。

这是已接受的演进契约，当前仍只有现有 `slog` JSON 输出，Collector 和 SLS 接入尚未标记为已实现。

## 发布状态

当前版本为 **0.0.1**，是首个最小可用快照。`0.x` 阶段继续快速迭代，后续变更按实际影响决定是否兼容，并在 [CHANGELOG.md](CHANGELOG.md) 中记录；此版本不代表兼容性冻结。

## 本地启动

推荐直接使用固定重启脚本：

```bash
./run debug
./run simulator
./run iphone
```

`debug` 固定使用 `18765`，供 Apifox 和手动协议调试；`simulator` 固定使用 `18767`，供本机 iPhone Simulator；`iphone` 固定使用 `18768`，供真机 iPhone。三个 profile 使用独立的 `launchctl` 服务、PID、日志和重启锁，可以同时运行。脚本只会清理当前 profile 的端口监听并等待健康检查通过，启动成功后打印可直接复制的本机和局域网 HTTP/WebSocket 地址。

```bash
curl http://127.0.0.1:18765/healthz
curl http://127.0.0.1:18765/status
curl http://127.0.0.1:18767/status
curl http://127.0.0.1:18768/status
```

`/status` 返回示例：

```json
{"app_connected":true,"agent_connected":true,"app_connection_id":3,"last_turn_acknowledged":"019fe7db-0100-7000-8000-000000000001","last_turn_acknowledged_status":"completed"}
```

`app_connection_id` 只在 App 已连接时出现，并随连接替换递增。`last_turn_acknowledged*` 是进程内最近一次 `turn.acknowledged`，用于让延迟真机维护任务确认最终输出已由 App 应用；Relay 重启后会清空。

## 局域网联调

假设 Relay 所在 Mac IP 是 `192.168.68.125`。

启动 Mac Agent，根目录下可以包含多个 Git 项目：

```bash
cd ../mac-agent
make build

./bin/mac-agent serve \
  --relay-url ws://192.168.68.125:18765/ws/agent \
  --workspace-root /Users/leehooo/work/selftools/codexremote \
  --name leehoo-mac
```

列出项目：

```bash
cd ../relay-server
./bin/relayctl projects --url ws://192.168.68.125:18765/ws/app
```

查询某项目的 Codex 会话：

```bash
./bin/relayctl threads \
  --url ws://192.168.68.125:18765/ws/app \
  --project project_xxx
```

查询该项目的 App Server 执行档位：

```bash
./bin/relayctl profiles \
  --url ws://192.168.68.125:18765/ws/app \
  --project project_xxx
```

读取某个会话的持久化 Turns 和 Items：

```bash
./bin/relayctl thread \
  --url ws://192.168.68.125:18765/ws/app \
  --project project_xxx \
  --thread thread_xxx
```

新建会话并执行：

```bash
./bin/relayctl turn \
  --url ws://192.168.68.125:18765/ws/app \
  --project project_xxx \
  --profile :workspace \
  --prompt "检查当前修改并运行相关测试，不要提交代码"
```

继续已有会话时增加 `--thread thread_xxx`。执行过程中按 `Ctrl+C` 发送 `turn.interrupt`。

观察原始事件：

```bash
./bin/relayctl watch --url ws://192.168.68.125:18765/ws/app
```

## Apifox

Apifox 项目 `CodexRemote`，项目 ID `8693796`。仓库内项目专属 skill：

```text
.codex/skills/ai-coding-remote-apifox-sync/
```

```bash
make apifox-validate
make apifox-check
make apifox-sync
make apifox-sync-websockets
```

同步范围：`GET /healthz`、`GET /status`、`/ws/app`、`/ws/agent`。WebSocket 示例统一使用 v2 Project/Thread/Turn，不保留旧 `run.*` 示例。

两个 WebSocket 接口在 Apifox 中保存为相对路径，并使用当前所选环境的前置 URL。局域网调试时选择 `当前局域网 Relay` 环境。

WebSocket 消息没有 HTTP 风格的请求/响应 Schema。完整的发送 Body、接收 Body、字段规则和可复制示例维护在 `apifox/websockets/docs/`，同步时自动写入对应接口说明。`thread.read` 和 `thread.detail` 的完整示例位于 `protocol/fixtures/`。`/ws/app` 的默认 Message 使用 `protocol/fixtures/project.list.json`，打开接口、连接 Relay 后可直接发送；只修改 WebSocket 契约时使用 `make apifox-sync-websockets`。

## 集成测试

```bash
make e2e
```

E2E 使用临时 Git 项目和本地假 Codex App Server，不调用真实模型：

```text
relayctl projects / turn
        ⇅
      Relay
        ⇅
    Mac Agent
        ⇅
fake Codex App Server → Git Diff
```

## Docker

```bash
docker compose up --build
```

或：

```bash
docker build -t ai-coding-remote-relay .
docker run --rm -p 18765:18765 ai-coding-remote-relay
```

未来线上部署仍建议由 Caddy、Nginx、Traefik 或云负载均衡终止 TLS/WSS。完成鉴权和设备身份前，不开放 `/ws/app` 与 `/ws/agent` 到公网。

## 配置

| 环境变量 | 默认值 | 说明 |
| --- | --- | --- |
| `RELAY_LISTEN_ADDR` | `:18765` | HTTP/WebSocket 监听地址 |
| `RELAY_MAX_MESSAGE_BYTES` | `262144` | 单消息上限 |
| `RELAY_WRITE_QUEUE_SIZE` | `128` | 每连接发送队列 |
| `RELAY_PING_INTERVAL` | `20s` | Ping 周期 |
| `RELAY_SHUTDOWN_TIMEOUT` | `10s` | 优雅停机等待 |
| `RELAY_LOG_LEVEL` | `info` | 日志级别 |

## 结构与演进

```text
cmd/relay/       Server 入口
cmd/relayctl/    CLI 调试 App
internal/hub/    Connection Registry
internal/router/ 角色路由和离线拒绝
internal/server/ HTTP 组装
internal/websocket/ 连接读写与心跳
internal/protocol/ v2 Go 模型
protocol/        Schema 与 Fixtures
apifox/          HTTP/WebSocket 同步源
```

未来 Relay 鉴权接入握手中间件，多 Mac 替换 Registry，可靠投递增加序号与 Inbox/Outbox。用户管理、后台查询、诊断任务和日志存储由独立 Admin Platform 提供，不再作为 Relay 内部控制面演进项。这些扩展不需要重写 WebSocket 读写循环或 Mac Codex Adapter，但不承诺兼容删除的预发布 `1.0` 协议。
