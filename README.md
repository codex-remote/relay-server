# AI Coding Remote - Relay Server

AI Coding Remote 的无状态通信中枢。Relay 在 iPhone App 和 Mac Agent 之间转发版本化 WebSocket JSON 消息，不执行 Codex，也不保存 Prompt、日志、Diff 或任务历史。

## 技术栈

| 层 | 选择 | 原因 |
| --- | --- | --- |
| 语言 | Go 1.23+ | 单二进制、常驻连接和并发模型清晰 |
| HTTP | 标准库 `net/http` | MVP 路由很少，不需要重型 Web 框架 |
| WebSocket | `github.com/coder/websocket` | Context 支持完整，与 Mac Agent 使用同一成熟实现 |
| 状态 | 进程内存 | 当前只有一个 App 和一个 Agent，无数据库需求 |
| 日志 | Go `slog` JSON Handler | 结构化且可避免记录敏感消息正文 |
| 协议 | JSON + JSON Schema | Swift、Go 和未来其他客户端可以独立实现 |
| 部署 | 原生二进制或 Docker | 本地和私有网络均可快速启动 |

当前不使用 Gin、Python、PostgreSQL、Redis、MQ、鉴权框架或 ORM。

## 当前 MVP

- `GET /healthz`：进程健康检查。
- `GET /status`：当前 App/Agent 连接状态。
- `/ws/app`：iPhone 或 `relayctl` WebSocket 入口。
- `/ws/agent`：Mac Agent WebSocket 入口。
- 单 App、单 Agent；同角色的新连接替换旧连接。
- 双向消息转发，并根据连接端点覆盖 `sender`。
- Agent 离线时返回 `run.rejected/AGENT_OFFLINE`。
- App 重连时恢复最近的 `agent.hello` 和 `agent.status`。
- 原生 Ping/Pong、256 KiB 帧限制和有界发送队列。
- 终止信号处理和 WebSocket 连接关闭。
- `relayctl` 临时替代 iPhone App，支持运行、实时输出和取消。

MVP 无鉴权、无数据库、无 Task、无队列、无执行历史。

> 当前 Relay 拥有间接执行 Mac 本地代码的能力。只能用于可信局域网、Tailscale 等隔离网络，不能直接暴露或端口转发到公共互联网。

## 本地启动

```bash
make test
make build
./bin/relay
```

默认监听所有网卡的 `8080` 端口。检查状态：

```bash
curl http://127.0.0.1:8080/healthz
curl http://127.0.0.1:8080/status
```

返回示例：

```json
{"app_connected":false,"agent_connected":true}
```

## 局域网调试

先在 Relay 所在 Mac 上获取局域网 IP：

```bash
ipconfig getifaddr en0
```

假设返回 `192.168.1.20`，Mac Agent 使用：

```bash
cd ../mac-agent
make build

AGENT_RELAY_URL=ws://192.168.1.20:8080/ws/agent \
AGENT_WORKING_DIR=/absolute/path/to/your/git-project \
./bin/mac-agent serve
```

在 Relay 机器或局域网另一台开发机提交任务：

```bash
cd ../relay-server

./bin/relayctl run \
  --url ws://192.168.1.20:8080/ws/app \
  --prompt "修复当前失败测试，运行测试，不要提交代码"
```

运行过程中按 `Ctrl+C` 会发送 `run.cancel`。只观察原始事件：

```bash
./bin/relayctl watch --url ws://192.168.1.20:8080/ws/app
```

同一台 Mac 上调试时，把 IP 换成 `127.0.0.1`。

## Apifox 调试

Apifox 项目：`CodexRemote`（项目 ID `8693796`），默认模块：`Relay Server`（模块 ID `8356476`）。

仓库包含项目专属 skill：

```text
.codex/skills/ai-coding-remote-apifox-sync/
```

本地校验和只读检查：

```bash
make apifox-validate
make apifox-check
```

同步 HTTP 与 WebSocket 定义：

```bash
make apifox-sync
```

同步内容：

- OpenAPI：`GET /healthz`、`GET /status`
- WebSocket：`ws://127.0.0.1:8080/ws/app`
- WebSocket：`ws://127.0.0.1:8080/ws/agent`

需要先安装并登录官方 Apifox CLI。完整同步还要求在 Apifox 的 `项目设置 -> 功能设置 -> 外部 AI 编辑权限` 中允许主分支直接编辑；CLI `2.2.9` 的 WebSocket 命令不支持 AI 分支。

在 Apifox Desktop 打开“iPhone App 控制通道”，连接后发送接口说明中的 `run.start` 示例，即可代替 iPhone App 调试。不要同时用 Apifox 和真实 Mac Agent 连接 `/ws/agent`，因为 MVP 同角色只保留一个连接。

## 一键集成测试

```bash
make e2e
```

该测试会构建 Relay、`relayctl` 和相邻目录中的 Mac Agent，创建临时 Git 项目，并使用可预测的假 Codex 进程验证：

```text
relayctl -> /ws/app -> Relay -> /ws/agent -> Mac Agent
         <- output / completed / Git Diff <-
```

测试不调用真实模型，不消耗 Codex 用量。

## Docker

```bash
docker compose up --build
```

或者：

```bash
docker build -t ai-coding-remote-relay .
docker run --rm -p 8080:8080 ai-coding-remote-relay
```

发布镜像时可以注入版本号：

```bash
docker build \
  --build-arg VERSION=0.1.0 \
  -t registry.example.com/ai-coding-remote-relay:0.1.0 .
```

未来线上部署保持以下边界：

- Relay 容器继续保持无状态，数据库、Redis/NATS 等作为外部服务接入。
- TLS/WSS 由 Caddy、Nginx、Traefik 或云负载均衡终止，容器内部监听 `:8080`。
- 平台使用 `/healthz` 做存活检查，通过 `SIGTERM` 触发 HTTP 停机和 WebSocket 关闭握手。
- 配置仅通过环境变量或 Secret 注入，不烘焙进镜像。
- 在 Auth Middleware 和设备身份完成前，线上环境不能开放 `/ws/app` 与 `/ws/agent`。

## 配置

| 环境变量 | 默认值 | 说明 |
| --- | --- | --- |
| `RELAY_LISTEN_ADDR` | `:8080` | HTTP/WebSocket 监听地址 |
| `RELAY_MAX_MESSAGE_BYTES` | `262144` | 单个入站和出站消息上限 |
| `RELAY_WRITE_QUEUE_SIZE` | `128` | 每条连接的有界发送队列 |
| `RELAY_PING_INTERVAL` | `20s` | WebSocket Ping 周期 |
| `RELAY_SHUTDOWN_TIMEOUT` | `10s` | HTTP 优雅停机等待时间 |
| `RELAY_LOG_LEVEL` | `info` | `debug`、`info`、`warn` 或 `error` |

也可以通过参数覆盖监听地址：

```bash
./bin/relay --listen 127.0.0.1:9090
```

## 项目结构

```text
.
├── cmd/
│   ├── relay/             # Relay Server 入口
│   └── relayctl/          # 临时 App 调试客户端
├── internal/
│   ├── config/            # 强类型配置
│   ├── hub/               # 单 App/Agent Connection Registry
│   ├── protocol/          # Go 消息模型与方向规则
│   ├── router/            # 角色路由和离线拒绝
│   ├── server/            # HTTP 服务组装
│   └── websocket/         # 连接读写、心跳和队列
├── protocol/
│   ├── schema/            # 跨语言 JSON Schema
│   └── fixtures/          # Swift/Go 契约测试样例
├── scripts/e2e.sh
├── Dockerfile
├── compose.yaml
└── Makefile
```

## 演进边界

现有代码把 WebSocket Handler、Router 和 Connection Registry 分开，未来扩展不需要重写传输循环：

| 未来需求 | 新增或替换组件 | 保持不变 |
| --- | --- | --- |
| 用户鉴权 | HTTP/WS Auth Middleware、Principal | WebSocket Peer、`run.*` 协议 |
| 设备身份 | Device Registry、设备握手 | Router、Mac Agent Runner |
| 数据库存储 | Task Service、Event Store | Connection Registry 接口与传输层 |
| 多 Mac | 按 Device ID 索引的 Registry | Peer 和消息 Envelope |
| 任务队列 | Scheduler、Dispatcher、Lease | Dispatcher 仍发送 `run.start` |
| 多实例 Relay | Redis/NATS 路由适配器 | 单连接读写循环 |

本地架构说明位于 `../Codex Remote/01-架构设计/MVP 极简架构.md`。
