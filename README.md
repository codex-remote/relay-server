# Codex Remote - Relay Server

Go 实现的 WebSocket Relay 与 Run Server。WebSocket Relay 负责 iPhone/Mac Agent 实时通信；Run Server 负责 Mobile Web 的 Runtime HTTP/SSE、PostgreSQL 权威状态和 Redis 活跃事件。Runtime Auth 通过独立包、`auth` Schema 和 `RuntimeAuthModule` 接入。它们共用当前二进制，但代码、路由和数据边界分离；这里的 Relay 不应与 `mobile-web` 仓库的 Mobile Web Gateway 混称。

> Codex Remote 是独立开源项目，与 OpenAI 没有关联或背书关系。

## 技术栈

| 层 | 选择 |
| --- | --- |
| 语言 | Go 1.25+ |
| HTTP | `net/http` |
| WebSocket | `github.com/coder/websocket` |
| 状态 | PostgreSQL `runtime/auth` + Redis 活跃流 + 进程内 Connection Registry |
| 日志 | `slog` JSON Handler |
| 协议 | JSON + JSON Schema，`spec_version: "2.0"` |
| 部署 | 原生二进制或 Docker |

当前不使用 Gin、Python、ORM、MQ 或外部鉴权框架。Runtime/Auth 直接使用 `pgx`，实时加速使用 `go-redis`。

## MVP 能力

- `GET /healthz`：进程健康。
- `GET /status`：App/Agent 当前连接状态、App 连接代次和最近的终态确认。
- `/ws/app`：iPhone 或 `relayctl` 入口。
- `/ws/agent`：Mac Agent 入口。
- v2 Project/Thread/Turn 消息方向白名单和透明转发，包括 `thread.read -> thread.detail` 历史查询。
- 提供项目范围的源码读取 API；统一 Runtime Auth 要求 `source:read` Scope，请求只在内存中关联到 Mac Agent，不持久化源码正文。
- 转发按项目的 `execution.profile.list -> execution.profile.snapshot` 权限协商，并在 `turn.start` 传递所选 profile ID。
- Agent 离线时返回 `turn.rejected/AGENT_OFFLINE`。
- App 重连时恢复最近的 `agent.hello`、`agent.status` 和 `agent.capabilities`。
- Ping/Pong、256 KiB 帧限制、有界发送队列和优雅关闭。
- Runtime v1 Project/Session/Run/Bootstrap HTTP 与 Session/Run SSE。
- Runtime JSON 增量轮询接口已加入 OpenAPI、Runtime Handler 和 Gateway allowlist；公网限流、Cursor 过期和边缘行为仍待验收，契约见工作区 `Run Server JSON 轮询接口规范`。
- 一次性配对、短期 Opaque Access Token、HttpOnly Refresh Cookie Rotation 和重放撤销。
- `pairqr` 可自动生成局域网配对链接和二维码；`relayctl` 还可创建纯文本链接、列出和撤销 Runtime 客户端。

旧 iPhone `/ws/app` MVP 仍是无应用层鉴权的历史接口，只能用于可信私有网络。Mobile Web Runtime 已启用独立鉴权，但完成 TLS、限流和公网硬化前仍不能直接暴露到公共互联网。

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
./run mobileweb
```

`debug` 固定使用 `18765`；`simulator` 使用 `18767`；`iphone` 使用 `18768`。这三个旧 WebSocket profile 显式关闭 Runtime Auth Control。`mobileweb` 使用 Loopback `127.0.0.1:18775`、Auth Control `127.0.0.1:18776` 和 Gateway `18774`，保留给 Homebrew Runtime/发布兼容链路；`mobileweb-debug` 使用 Loopback `127.0.0.1:18875`、Auth Control `127.0.0.1:18876` 和本地 Gateway `18874`，供源码工作区的 `devrun crweb`。五个 profile 使用独立的 `launchctl` 服务、PID、日志和重启锁。

```bash
curl http://127.0.0.1:18765/healthz
curl http://127.0.0.1:18765/status
curl http://127.0.0.1:18767/status
curl http://127.0.0.1:18768/status
curl http://127.0.0.1:18775/status
```

`/status` 返回示例：

```json
{"app_connected":true,"agent_connected":true,"app_connection_id":3,"last_turn_acknowledged":"019fe7db-0100-7000-8000-000000000001","last_turn_acknowledged_status":"completed"}
```

`app_connection_id` 只在 App 已连接时出现，并随连接替换递增。`last_turn_acknowledged*` 是进程内最近一次 `turn.acknowledged`，用于让延迟真机维护任务确认最终输出已由 App 应用；Relay 重启后会清空。

### Mobile Web 二维码配对

`mobileweb`、Gateway 和 Auth Control 已启动时，直接运行：

```bash
./bin/pairqr
devrun crpair
```

`devrun crpair` 是注册后的推荐入口，等价全名为 `devrun codexremote mobileweb-pairing qr`，也可按 Auth Control 端口运行 `devrun 18776`。三个入口默认把带 quiet zone 的大号黑白二维码绘制到终端末尾；每个 QR 模块使用两个等宽全块字符，避免终端字体和行高把二维码压扁，普通 iPhone 相机可直接识别。空间受限时可显式传入 `--terminal-render compact`，极度受限时使用 `--terminal-render small`；这两种模式依赖终端字体，不作为通用扫码路径。交互终端中的标题、有效期、链接、路径和安全警告使用不同颜色，设置 `NO_COLOR` 后恢复纯文本。嵌入其他已提供安全提示的启动器时，可用 `--print-metadata=false` 隐藏重复标题和警告。工具自动选择 Mac 的私有局域网 IPv4，使用 Gateway 端口 `18774`；地址选择不正确时可以显式覆盖，需要保存图片时使用 `--output`，生成文件权限固定为 `0600`：

```bash
devrun crpair --origin http://192.168.3.8:18774 --name "My iPhone"
devrun crpair --output .run/mobileweb/pairing.png
```

二维码包含默认 10 分钟有效且只能兑换一次的 Pairing Grant。PNG 和终端链接都属于短期凭证，不应上传或发送到公开频道。`pairqr` 只连接 Loopback Auth Control；手机仍只访问 Mobile Web Gateway。

## 局域网联调

假设 Relay 所在 Mac IP 是 `192.168.1.20`。

启动 Mac Agent，根目录下可以包含多个 Git 项目：

```bash
cd ../mac-agent
make build

./bin/mac-agent serve \
  --relay-url ws://192.168.1.20:18765/ws/agent \
  --workspace-root /Users/developer/work/codexremote \
  --name developer-mac
```

列出项目：

```bash
cd ../relay-server
./bin/relayctl projects --url ws://192.168.1.20:18765/ws/app
```

查询某项目的 Codex 会话：

```bash
./bin/relayctl threads \
  --url ws://192.168.1.20:18765/ws/app \
  --project project_xxx
```

查询该项目的 App Server 执行档位：

```bash
./bin/relayctl profiles \
  --url ws://192.168.1.20:18765/ws/app \
  --project project_xxx
```

读取某个会话的持久化 Turns 和 Items：

```bash
./bin/relayctl thread \
  --url ws://192.168.1.20:18765/ws/app \
  --project project_xxx \
  --thread thread_xxx
```

新建会话并执行：

```bash
./bin/relayctl turn \
  --url ws://192.168.1.20:18765/ws/app \
  --project project_xxx \
  --profile :workspace \
  --prompt "检查当前修改并运行相关测试，不要提交代码"
```

继续已有会话时增加 `--thread thread_xxx`。执行过程中按 `Ctrl+C` 发送 `turn.interrupt`。

观察原始事件：

```bash
./bin/relayctl watch --url ws://192.168.1.20:18765/ws/app
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

当前 Docker/Compose 示例只用于旧 Relay WebSocket 本地调试并显式设置 `AUTH_ENABLED=false`；它不是 Mobile Web Gateway/Auth 部署拓扑，不能暴露到公网。

```bash
docker compose up --build
```

或：

```bash
docker build -t codex-remote-relay .
docker run --rm -p 18765:18765 codex-remote-relay
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
| `RUNTIME_DATABASE_URL` | 本机开发 PostgreSQL | Runtime PostgreSQL 连接串 |
| `RUNTIME_REDIS_URL` | 本机开发 Redis | Runtime Redis 连接串 |
| `RUNTIME_ALLOWED_ORIGIN` | `http://127.0.0.1:4173` | 逗号分隔的 CORS allowlist；本地开发可显式设为 `*` |
| `AUTH_ENABLED` | `true` | 启用 Runtime Auth；`mobileweb` 必须保持启用 |
| `AUTH_CONTROL_ADDR` | `127.0.0.1:18776` | 仅 Loopback 的配对/设备控制面 |
| `AUTH_ACCESS_TTL` | `15m` | Access Token TTL |
| `AUTH_REFRESH_TTL` | `720h` | Refresh Session TTL |
| `AUTH_PAIRING_TTL` | `10m` | 一次性配对授权 TTL |
| `AUTH_REFRESH_COOKIE_NAME` | `codexremote_refresh` | HttpOnly Refresh Cookie 名称 |
| `AUTH_COOKIE_SECURE` | `false` | 局域网 HTTP 为 `false`；公网 HTTPS 必须为 `true` |

Mobile Web 通过 Gateway 同源访问，不依赖跨端口 CORS。`RUNTIME_ALLOWED_ORIGIN=*` 只允许明确的旧接口调试，不得用于公网或生产环境。

源码查看调用 `POST /v1/runtime/projects/{project_id}/source:read`，要求 `source:read` Scope。JSON Body 包含 `path`、`line` 和 `context_lines`；绝对路径不会进入 API URL。详细契约见 [protocol/source-read.md](protocol/source-read.md)。

## 结构与演进

```text
cmd/relay/       Server 入口
cmd/relayctl/    CLI 调试 App
cmd/pairqr/      Loopback Auth Control 配对二维码工具
internal/hub/    Connection Registry
internal/router/ 角色路由和离线拒绝
internal/server/ HTTP 组装、Runtime Scope 路由策略与 Auth 插件接口
internal/websocket/ 连接读写与心跳
internal/protocol/ v2 Go 模型
internal/runtime/ HTTP/SSE、持久 Runtime 协调与短生命周期源码读取
internal/auth/    Runtime 配对、Token、通用 Scope 中间件、Module 与独立 PostgreSQL 迁移
protocol/        Schema 与 Fixtures
apifox/          HTTP/WebSocket 同步源
```

Runtime Auth 已在 HTTP 中间件实现。Auth 包不识别 Runtime URL；Server 组装层提供所需 Scope，并通过最小 `RuntimeAuthModule` 接口挂载公开 API、保护中间件和 Control Handler。Auth Control `18776` 是同一 Relay 进程内的 Loopback Listener，不是可独立部署的微服务。旧 App/Agent WebSocket 的设备身份仍是后续独立工作。用户管理、后台查询、诊断任务和日志存储由 Admin Platform 提供，不作为 Runtime Auth 表的扩展方向。

## 开源许可

本仓库采用 [Apache License 2.0](LICENSE)。贡献前请阅读组织级
[贡献指南](https://github.com/codex-remote/.github/blob/main/CONTRIBUTING.md)；
版权与项目名称说明见 [NOTICE](NOTICE)。
