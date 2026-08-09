# AI Coding Remote - Relay Server

AI Coding Remote 的无状态通信服务。使用 Go 开发，在 iPhone App 和 Mac Agent 之间转发版本化 WebSocket JSON 消息。

## 当前状态

仓库已初始化，尚未生成 Go Module 或业务代码。

MVP Relay 是单实例、纯内存消息路由器：无鉴权、无数据库、无 Task、无队列、无执行历史。它只能部署在 Tailscale 等私有网络或等价隔离环境中。

## 本仓库负责

- `/healthz` 进程健康检查。
- `/ws/app` iPhone WebSocket 入口。
- `/ws/agent` Mac Agent WebSocket 入口。
- 单 App、单 Agent 的内存连接注册。
- App 到 Agent、Agent 到 App 的消息路由。
- Agent 在线、离线状态通知。
- Agent 离线时返回 `run.rejected/AGENT_OFFLINE`。
- WebSocket Ping/Pong、帧限制、有界写队列和优雅停机。
- JSON Schema、协议 Fixtures 和兼容性规则。

## 本仓库不负责

- 启动 Codex CLI 或解释 Codex 输出。
- 保存 Prompt、日志、Diff 或 Run 历史。
- 用户鉴权、设备认证和权限策略。
- Task CRUD、队列、Lease 和可靠消息重放。
- iPhone 界面和 Mac 本地进程管理。

## 项目边界

Relay 不能依赖 `iphone-app` 或 `mac-agent` 的源码。集成边界只有 WebSocket 和 JSON 协议：

- 协议版本：`spec_version: "1.0"`
- Relay 仓库拥有协议 Schema 和跨语言 Fixtures。
- App 和 Agent 按发布的协议版本实现各自模型。
- WebSocket Handler 只处理连接、限制和编解码。
- Router 只决定消息目标，不启动 Codex、不维护 Run 状态。
- Connection Registry 隐藏单连接实现，未来可替换为多设备注册表。

本地架构说明位于：`../Codex Remote/01-架构设计/MVP 极简架构.md`。

## 计划中的目录结构

```text
.
├── cmd/relay/main.go
├── internal/
│   ├── config/config.go
│   ├── hub/registry.go
│   ├── router/router.go
│   ├── websocket/handler.go
│   └── observability/logging.go
├── protocol/
│   ├── schema/
│   ├── fixtures/
│   └── README.md
├── Dockerfile
├── go.mod
└── README.md
```

## 计划中的配置

| 环境变量 | 默认值 | 说明 |
| --- | --- | --- |
| `RELAY_LISTEN_ADDR` | `:8080` | HTTP/WebSocket 监听地址 |
| `RELAY_MAX_MESSAGE_BYTES` | `65536` | 单消息最大字节数 |
| `RELAY_WRITE_QUEUE_SIZE` | `64` | 每连接发送队列容量 |
| `RELAY_PING_INTERVAL` | `20s` | WebSocket Ping 周期 |
| `RELAY_LOG_LEVEL` | `info` | 结构化日志级别 |

Relay 日志不得记录 Prompt、stdout/stderr、源码或 Diff 正文。

## MVP 消息路由

```text
iPhone /ws/app  -> validate envelope -> Agent connection
Mac /ws/agent   -> validate envelope -> App connection

Agent missing   -> run.rejected { code: AGENT_OFFLINE }
New connection  -> replace and close previous connection of same role
```

## MVP 验收

- App 和 Agent 可以同时连接并双向转发消息。
- 旧连接被新连接替换，不发生并发写。
- Agent 离线时 App 获得明确错误。
- 非法 JSON、未知消息和超限帧不会导致进程崩溃。
- 慢连接不会让内存无限增长。
- 收到终止信号后停止接收新连接并优雅关闭。
- Docker 容器可在私有网络内单命令启动。

## 后续扩展

未来的鉴权通过 WebSocket Handler 前的 Middleware 加入；数据库、Task Service 和 Dispatcher 作为新应用模块接入 Router；连接读写循环和现有 `run.*` 消息保持不变。
