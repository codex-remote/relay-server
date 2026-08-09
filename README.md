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
- `GET /status`：App/Agent 当前连接状态。
- `/ws/app`：iPhone 或 `relayctl` 入口。
- `/ws/agent`：Mac Agent 入口。
- v2 Project/Thread/Turn 消息方向白名单和透明转发。
- Agent 离线时返回 `turn.rejected/AGENT_OFFLINE`。
- App 重连时恢复最近的 `agent.hello` 和 `agent.status`。
- Ping/Pong、256 KiB 帧限制、有界发送队列和优雅关闭。
- `relayctl` 查询项目/会话、启动 Turn、观察事件和中断。

MVP 无鉴权、数据库、业务 Task、队列和执行历史。只能用于可信局域网、Tailscale 或等价私有网络，不能直接暴露到公网。

## 本地启动

```bash
make test
make build
./bin/relay
```

默认监听 `:8080`：

```bash
curl http://127.0.0.1:8080/healthz
curl http://127.0.0.1:8080/status
```

`/status` 返回示例：

```json
{"app_connected":false,"agent_connected":true}
```

## 局域网联调

假设 Relay 所在 Mac IP 是 `192.168.68.125`。

启动 Mac Agent，根目录下可以包含多个 Git 项目：

```bash
cd ../mac-agent
make build

./bin/mac-agent serve \
  --relay-url ws://192.168.68.125:8080/ws/agent \
  --workspace-root /Users/leehooo/work/selftools/codexremote \
  --project-scan-depth 2 \
  --name leehoo-mac
```

列出项目：

```bash
cd ../relay-server
./bin/relayctl projects --url ws://192.168.68.125:8080/ws/app
```

查询某项目的 Codex 会话：

```bash
./bin/relayctl threads \
  --url ws://192.168.68.125:8080/ws/app \
  --project project_xxx
```

新建会话并执行：

```bash
./bin/relayctl turn \
  --url ws://192.168.68.125:8080/ws/app \
  --project project_xxx \
  --prompt "检查当前修改并运行相关测试，不要提交代码"
```

继续已有会话时增加 `--thread thread_xxx`。执行过程中按 `Ctrl+C` 发送 `turn.interrupt`。

观察原始事件：

```bash
./bin/relayctl watch --url ws://192.168.68.125:8080/ws/app
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
```

同步范围：`GET /healthz`、`GET /status`、`/ws/app`、`/ws/agent`。WebSocket 示例统一使用 v2 Project/Thread/Turn，不保留旧 `run.*` 示例。

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
docker run --rm -p 8080:8080 ai-coding-remote-relay
```

未来线上部署仍建议由 Caddy、Nginx、Traefik 或云负载均衡终止 TLS/WSS。完成鉴权和设备身份前，不开放 `/ws/app` 与 `/ws/agent` 到公网。

## 配置

| 环境变量 | 默认值 | 说明 |
| --- | --- | --- |
| `RELAY_LISTEN_ADDR` | `:8080` | HTTP/WebSocket 监听地址 |
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

未来鉴权接入握手中间件，数据库/Task 接入独立控制面，多 Mac 替换 Registry，可靠投递增加序号与 Inbox/Outbox。这些扩展不需要重写 WebSocket 读写循环或 Mac Codex Adapter，但不承诺兼容删除的预发布 `1.0` 协议。
