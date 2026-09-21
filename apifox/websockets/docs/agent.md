# Mac Agent 执行通道

## 用途与方向

`/ws/agent` 是真实 Mac Agent 连接 Relay 的执行端入口。Agent 接收 App 请求和 Run Server 内部请求，调用本地 Codex App Server 或受控工作区服务，并发送状态、快照和 Turn 事件。

- 接收方向：App/Run Server -> Relay -> Mac Agent
- 发送方向：Mac Agent -> Relay -> App
- 帧类型：WebSocket Text
- 内容格式：UTF-8 JSON Object
- 协议版本：仅接受 `spec_version: "2.0"`

> **警告：** MVP 同时只允许一个 Agent 连接。Apifox 手动连接 `/ws/agent` 前执行 `launchctl remove com.ai-coding-remote.mac-agent.debug`，否则真实 debug Agent 自动重连后会替换 Apifox，客户端通常显示 `1006 Abnormal Closure`。`iphone` profile 不受影响。

## 通用 Envelope

每一条发送和接收消息都必须包含：

| 字段 | 类型 | 必填 | 规范 |
| --- | --- | --- | --- |
| `spec_version` | string | 是 | 固定为 `2.0` |
| `message_id` | string | 是 | 当前消息唯一 ID，建议使用 UUID |
| `type` | string | 是 | 必须符合 Agent 入口允许的消息方向 |
| `occurred_at` | string | 是 | RFC 3339 时间，建议 UTC |
| `trace_id` | string | 是 | 响应和事件沿用触发请求的值 |
| `sender` | object | 是 | 包含非空 `kind`、`id`；Relay 会覆盖客户端自报身份 |
| `payload` | object | 是 | 结构由 `type` 决定 |

Relay 把 App 请求的 `sender` 规范化为 `{"kind":"user","id":"local-user"}`；把 Agent 消息规范化为 `{"kind":"device","id":"local-mac"}`。

## 接收 Body：App -> Mac Agent

| `type` | `payload` 字段 | 必填 | Agent 行为 |
| --- | --- | --- | --- |
| `project.list` | 无 | - | 扫描允许的 Git 项目并调用 Codex `thread/list` 统计会话 |
| `thread.list` | `project_id: string` | 是 | 将可信项目 ID 解析成本地 `cwd`，调用 Codex `thread/list` |
| `thread.read` | `project_id: string`, `thread_id: string` | 是 | 校验 Thread 归属后调用 Codex `thread/read(includeTurns: true)` |
| `source.read` | `project_id: string`, `path: string`; 可选 `focus_line`, `context_lines` | 是 | 仅由 Run Server 内部发送；解析可信项目根目录并读取有界文本源码窗口；`/ws/app` 不允许发送 |
| `execution.profile.list` | `project_id: string` | 是 | 解析可信项目路径并调用 `permissionProfile/list` |
| `turn.start` | `project_id: string` | 是 | 解析为本地 `cwd` |
| `turn.start` | `thread_id: string` | 否 | 省略时 `thread/start`；提供时校验归属并 `thread/resume` |
| `turn.start` | `prompt: string` | 是 | 作为 Codex `turn/start.input`；非空且最大 16 KiB |
| `turn.start` | `permission_profile_id: string` | 否 | 重新查询 allowed 后传给 Thread/Turn 的 `permissions` |
| `turn.interrupt` | `thread_id: string`, `turn_id: string` | 是 | 调用 Codex `turn/interrupt` |
| `turn.acknowledged` | `turn_id: string`, `status: string` | 是 | 接受 App 终态确认；不启动新 Turn，也不重启 Agent |
| `bootstrap.start` | `command_id: string`, `sync_id: string` | 是 | 从 Agent SQLite 水位启动或续传异步历史同步 |
| `bootstrap.durable_ack` | `sync_id: string`, `batch_no: integer`, `checksum: string` | 是 | PostgreSQL 已提交该批次，Agent 才推进本地 durable 水位 |

### 收到的 project.list

```json
{
  "spec_version": "2.0",
  "message_id": "11111111111141118111111111111111",
  "type": "project.list",
  "occurred_at": "2026-08-10T08:30:00Z",
  "trace_id": "22222222222242228222222222222222",
  "sender": { "kind": "user", "id": "local-user" },
  "payload": {}
}
```

### 收到的 turn.start

```json
{
  "spec_version": "2.0",
  "message_id": "55555555555545558555555555555555",
  "type": "turn.start",
  "occurred_at": "2026-08-10T08:30:05Z",
  "trace_id": "66666666666646668666666666666666",
  "sender": { "kind": "user", "id": "local-user" },
  "payload": {
    "project_id": "project_9e8638437f803c29d91a3844",
    "prompt": "检查当前修改并运行相关测试，不要提交代码",
    "permission_profile_id": ":workspace"
  }
}
```

`sender`、`message_id`、`occurred_at` 不作为 Codex 参数。真正决定调用的是 `type` 和 `payload`；`trace_id` 只用于关联响应与执行事件。

### 收到的 thread.read

```json
{
  "spec_version": "2.0",
  "message_id": "77777777777747778777777777777777",
  "type": "thread.read",
  "occurred_at": "2026-08-10T08:30:04Z",
  "trace_id": "88888888888848888888888888888888",
  "sender": { "kind": "user", "id": "local-user" },
  "payload": {
    "project_id": "project_9e8638437f803c29d91a3844",
    "thread_id": "019fe7da-7105-7831-8c0c-8997786ddd3f"
  }
}
```

## 发送 Body：Mac Agent -> App

| `type` | `payload` 结构 | 何时发送 |
| --- | --- | --- |
| `agent.hello` | `name`, `version`, `status` | Agent 建立或重建连接 |
| `agent.status` | `status`; 可选 `project_id`, `thread_id`, `turn_id` | 连接、开始 Turn、Turn 结束 |
| `agent.capabilities` | `restricted`, `sandbox_mode`, `approval_policy`, `writable_scope` 和能力布尔值 | Agent 建立或重建连接；声明远程 Turn 的实际执行边界 |
| `execution.profile.snapshot` | `project_id`, `default_profile_id`, `profiles[]` | App Server 对可信项目路径返回的权限档位 |
| `project.snapshot` | `projects[]` | 响应 `project.list` |
| `thread.snapshot` | `project_id`, `threads[]` | 响应 `thread.list` |
| `thread.detail` | `project_id`, `thread`, `truncated` | 响应 `thread.read`；包含持久化 Turns 和结构化 Items |
| `source.snapshot` | `project_id`, 相对 `path`, `content`, 行范围、`sha256`, `modified_at` | 响应内部 `source.read`；由 Run Server 消费，不转发到 App |
| `source.read.failed` | `code`, `message` | 源码路径、安全边界、类型或读取失败；由 Run Server 映射为 HTTP 错误 |
| `turn.started` | `project_id`, `thread_id`, `turn_id`, `started_at` | Codex 已创建 Turn |
| `turn.output` | `project_id`, `thread_id`, `turn_id`, `stream`, `text` | 兼容旧客户端的流式输出 |
| `turn.item.started` | 执行 ID、`sequence`, `item` | Codex Item 开始 |
| `turn.item.delta` | 执行 ID、`sequence`, `item_id`, `field`, `delta` | Item 的 `text` 或 `output` 增量 |
| `turn.item.completed` | 执行 ID、`sequence`, `item` | Codex Item 的最终完整状态 |
| `turn.snapshot` | 执行 ID、`status`, `started_at`, `recent_output[]`; 可选 `live_items[]`, `last_sequence`, `live_items_truncated` | Agent/App 重连恢复 |
| `turn.interrupted` | `project_id`, `thread_id`, `turn_id`, `duration_ms` | 中断完成 |
| `turn.completed` | `project_id`, `thread_id`, `turn_id`, `duration_ms`, `summary`, `changed_files[]`, `diff`, `diff_truncated` | 执行和 Git 结果收集成功 |
| `turn.failed` | 可选执行 ID 和 `duration_ms`; `code`, `message` | Codex、超时或结果收集失败 |
| `turn.rejected` | `code`, `message`; 可选 `required_capabilities`, `recovery_action`, `execution_context` | 请求 payload 或当前状态不允许执行 |
| `bootstrap.batch` | `command_id`, `sync_id`, `snapshot_id`, `batch_no`, `checksum`, `total_sessions`, `processed_sessions`, `reconciliation_safe`, 可选 `project`/`thread`, `done` | 逐批上传冻结清单和历史详情；等待 durable ACK 后继续 |

`reconciliation_safe` 只在未从既有 durable 批次水位续传的最终 `done` 批次为 `true`。断线后重新读取的目录可能与已经提交的批次前缀不再属于同一清单，因此续传任务只能幂等导入，不能触发 Project/Session 隐藏；Run Server 会通过 Sync Job 的 `reconciliation_applied=false` 向客户端暴露这一结果。

### agent.hello

```json
{
  "spec_version": "2.0",
  "message_id": "77777777777747778777777777777777",
  "type": "agent.hello",
  "occurred_at": "2026-08-10T08:29:59Z",
  "trace_id": "88888888888848888888888888888888",
  "sender": { "kind": "device", "id": "apifox-agent" },
  "payload": {
    "name": "Apifox Agent",
    "version": "test",
    "status": "idle"
  }
}
```

### agent.capabilities

Agent 在初始消息中发送；Relay 会与 Hello、Status 一起缓存，供 App 重连后立即恢复：

```json
{
  "spec_version": "2.0",
  "message_id": "11111111111141118111111111111111",
  "type": "agent.capabilities",
  "occurred_at": "2026-08-11T08:30:00Z",
  "trace_id": "agent",
  "sender": { "kind": "device", "id": "local-mac" },
  "payload": {
    "restricted": true,
    "sandbox_mode": "workspace-write",
    "approval_policy": "never",
    "writable_scope": "selected_project",
    "network_access": false,
    "can_request_approval": false,
    "host_process_control": false,
    "user_library_write": false,
    "xcode_device_control": false,
    "supports_permission_profiles": true,
    "supports_source_read": true
  }
}
```

`workspace-write` 表示可在所选项目内工作，不等同于拥有 Mac 完整权限。需要宿主进程控制、用户 Library 写入或 Xcode 真机控制的任务必须转到完整权限的 Mac 会话。Agent 自己产生的 `turn.rejected` 应在 `execution_context` 中附带同一份 payload；Relay 自己产生的离线拒绝可以省略。

### project.snapshot

发送时必须复用对应 `project.list` 的 `trace_id`：

```json
{
  "spec_version": "2.0",
  "message_id": "aaaaaaaaaaaa4aaaaaaaaaaaaaaaaaaa",
  "type": "project.snapshot",
  "occurred_at": "2026-08-10T08:30:01Z",
  "trace_id": "22222222222242228222222222222222",
  "sender": { "kind": "device", "id": "apifox-agent" },
  "payload": {
    "projects": [
      {
        "id": "project_9e8638437f803c29d91a3844",
        "name": "relay-server",
        "path": "/Users/developer/work/codexremote/relay-server",
        "thread_count": 1,
        "updated_at": "2026-08-10T02:52:30Z"
      }
    ]
  }
}
```

项目字段：`id`、`name`、`path`、`thread_count` 必填，`updated_at` 可选。App 后续只能回传 `id` 作为 `project_id`，不能回传任意路径。

### thread.snapshot

```json
{
  "spec_version": "2.0",
  "message_id": "bbbbbbbbbbbb4bbbbbbbbbbbbbbbbbbb",
  "type": "thread.snapshot",
  "occurred_at": "2026-08-10T08:30:03Z",
  "trace_id": "44444444444444448444444444444444",
  "sender": { "kind": "device", "id": "apifox-agent" },
  "payload": {
    "project_id": "project_9e8638437f803c29d91a3844",
    "threads": [
      {
        "id": "019fe7da-7105-7831-8c0c-8997786ddd3f",
        "project_id": "project_9e8638437f803c29d91a3844",
        "title": "只读检查这个项目",
        "preview": "请仅回答 Go module 路径",
        "latest_message_preview": "Go module 路径是 github.com/codex-remote/relay-server。",
        "status": "notLoaded",
        "source": "vscode",
        "updated_at": "2026-08-10T02:52:30Z"
      }
    ]
  }
}
```

### thread.detail

完整可复制响应见仓库 `protocol/fixtures/thread.detail.json`。Mac Agent 不透传 Codex 原始 union JSON，而是稳定输出：

- `thread`：`id`、`project_id`、`title`、`preview`、`status`、`source`、`created_at`、`updated_at`、`turns[]`
- Turn：`id`、`status`、可选时间/耗时/错误、`items[]`、`truncated`
- Item：必填 `id`、`type`、`truncated`，并按类型提供消息、命令、输出、文件变更、工具名称或查询字段
- 顶层 `truncated`：为 `true` 表示为了满足 256 KiB Relay 帧上限省略了较早历史

`thread.detail.trace_id` 必须复用对应 `thread.read.trace_id`，但 `message_id` 必须重新生成。

### Turn 事件顺序

```text
agent.status(running)
-> turn.started
-> turn.item.started (0..N)
-> turn.item.delta (0..N)
-> turn.item.completed (0..N)
-> turn.output (0..N, legacy compatibility)
-> turn.completed | turn.failed | turn.interrupted
-> agent.status(idle)
```

终态事件只能出现一个。所有事件复用原始 `turn.start.trace_id`，每条事件使用新的 `message_id`。结构化事件的 `sequence` 在单个 Turn 内严格递增；`turn.item.completed.item` 是 Item 的最终权威状态。Agent 必须在 `turn.snapshot` 中附带当前 `live_items` 和 `last_sequence`，让 App 重连后原位恢复。

## 手动双通道调试

1. 停止真实 debug Agent：`launchctl remove com.ai-coding-remote.mac-agent.debug`。
2. 在 Apifox 连接 `/ws/agent`，发送 `agent.hello`。
3. 另开一个 Apifox 标签连接 `/ws/app`。
4. 从 `/ws/app` 发送 `project.list`，确认 `/ws/agent` 收到规范化后的请求。
5. 从 `/ws/agent` 发送同 `trace_id` 的 `project.snapshot`，确认 `/ws/app` 收到响应。

Agent 主动发送消息不会在当前 `/ws/agent` 标签回显；只有已连接的 `/ws/app` 能收到。
