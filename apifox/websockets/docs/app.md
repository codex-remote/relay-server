# iPhone App 控制通道

## 用途与方向

`/ws/app` 是 iPhone App、Apifox 或其他控制端连接 Relay 的入口。控制端发送查询与 Turn 控制消息，接收 Mac Agent 的状态、项目、会话和执行事件。

- 发送方向：App -> Relay -> Mac Agent
- 接收方向：Mac Agent -> Relay -> App
- 帧类型：WebSocket Text
- 内容格式：UTF-8 JSON Object
- 协议版本：仅接受 `spec_version: "2.0"`

正常联调时启动真实 Mac Agent，只在 Apifox 连接 `/ws/app`。不要同时用 Apifox 连接 `/ws/agent`，否则会替换真实 Agent。

## 通用 Envelope

每一条发送和接收消息都必须使用以下 Envelope：

| 字段 | 类型 | 必填 | 规范 |
| --- | --- | --- | --- |
| `spec_version` | string | 是 | 固定为 `2.0` |
| `message_id` | string | 是 | 当前消息唯一 ID，建议使用 UUID；响应必须生成新 ID |
| `type` | string | 是 | 必须是本页允许方向中的消息类型 |
| `occurred_at` | string | 是 | RFC 3339 时间，建议 UTC，例如 `2026-08-10T08:30:00Z` |
| `trace_id` | string | 是 | 一次请求链路的关联 ID；响应和后续 Turn 事件沿用请求值 |
| `sender` | object | 是 | 必须包含非空 `kind`、`id`；Relay 会按入口覆盖客户端自报身份 |
| `payload` | object | 是 | 结构由 `type` 决定；无参数时也必须发送 `{}` |

Relay 转发 `/ws/app` 消息时统一设置：

```json
{"kind":"user","id":"local-user"}
```

Relay 转发 `/ws/agent` 消息时统一设置：

```json
{"kind":"device","id":"local-mac"}
```

## 发送 Body：App -> Mac Agent

| `type` | `payload` 字段 | 必填 | 说明 | 对应结果 |
| --- | --- | --- | --- | --- |
| `project.list` | 无 | - | 查询允许访问的全部 Git 项目 | `project.snapshot` |
| `thread.list` | `project_id: string` | 是 | 查询指定项目的 Codex 会话 | `thread.snapshot` |
| `thread.read` | `project_id: string`, `thread_id: string` | 是 | 读取指定会话的持久化 Turns 和 Items | `thread.detail` |
| `execution.profile.list` | `project_id: string` | 是 | 查询该项目由 App Server 允许的权限档位 | `execution.profile.snapshot` |
| `turn.start` | `project_id: string` | 是 | 选择本地项目；Agent 将其解析为可信 `cwd` | `turn.started` 及后续事件 |
| `turn.start` | `thread_id: string` | 否 | 省略时创建新 Thread；提供时继续已有 Thread | 同上 |
| `turn.start` | `prompt: string` | 是 | 非空 UTF-8 指令，最大 16 KiB | 同上 |
| `turn.start` | `permission_profile_id: string` | 新 Agent 必需 | 必须来自同项目 snapshot 且 `allowed=true` | 同上 |
| `turn.interrupt` | `thread_id: string` | 是 | 当前正在运行的 Thread ID | `turn.interrupted` |
| `turn.interrupt` | `turn_id: string` | 是 | 当前正在运行的 Turn ID | `turn.interrupted` |
| `turn.acknowledged` | `turn_id: string`, `status: string` | 是 | App 已应用终态；`status` 为 `completed`、`failed` 或 `interrupted` | 无；供延迟维护任务释放 |

### project.list

```json
{
  "spec_version": "2.0",
  "message_id": "11111111111141118111111111111111",
  "type": "project.list",
  "occurred_at": "2026-08-10T08:30:00Z",
  "trace_id": "22222222222242228222222222222222",
  "sender": { "kind": "user", "id": "apifox" },
  "payload": {}
}
```

### thread.list

```json
{
  "spec_version": "2.0",
  "message_id": "33333333333343338333333333333333",
  "type": "thread.list",
  "occurred_at": "2026-08-10T08:30:02Z",
  "trace_id": "44444444444444448444444444444444",
  "sender": { "kind": "user", "id": "apifox" },
  "payload": {
    "project_id": "project_9e8638437f803c29d91a3844"
  }
}
```

### turn.start

```json
{
  "spec_version": "2.0",
  "message_id": "55555555555545558555555555555555",
  "type": "turn.start",
  "occurred_at": "2026-08-10T08:30:05Z",
  "trace_id": "66666666666646668666666666666666",
  "sender": { "kind": "user", "id": "apifox" },
  "payload": {
    "project_id": "project_9e8638437f803c29d91a3844",
    "prompt": "检查当前修改并运行相关测试，不要提交代码",
    "permission_profile_id": ":workspace"
  }
}
```

继续已有会话时，在 `payload` 增加：

```json
"thread_id": "019fe7da-7105-7831-8c0c-8997786ddd3f"
```

### thread.read

先通过 `project.list -> project.snapshot` 获得 `project_id`，再通过 `thread.list -> thread.snapshot` 获得该项目下的 `thread_id`：

```json
{
  "spec_version": "2.0",
  "message_id": "77777777777747778777777777777777",
  "type": "thread.read",
  "occurred_at": "2026-08-10T08:30:04Z",
  "trace_id": "88888888888848888888888888888888",
  "sender": { "kind": "user", "id": "apifox" },
  "payload": {
    "project_id": "project_9e8638437f803c29d91a3844",
    "thread_id": "019fe7da-7105-7831-8c0c-8997786ddd3f"
  }
}
```

Mac Agent 会先验证 Thread 确实属于 Project，再调用 Codex App Server 稳定接口 `thread/read`，并固定传入 `includeTurns: true`。

### turn.interrupt

```json
{
  "spec_version": "2.0",
  "message_id": "77777777777747778777777777777777",
  "type": "turn.interrupt",
  "occurred_at": "2026-08-10T08:31:00Z",
  "trace_id": "66666666666646668666666666666666",
  "sender": { "kind": "user", "id": "apifox" },
  "payload": {
    "thread_id": "019fe7da-7105-7831-8c0c-8997786ddd3f",
    "turn_id": "019fe7db-0100-7000-8000-000000000001"
  }
}
```

### turn.acknowledged

App 在应用 `turn.completed`、`turn.failed` 或 `turn.interrupted` 后立即发送。需要终止或覆盖安装 App 的维护任务必须等待该确认，不能使用固定延迟猜测最终消息是否已送达。

```json
{
  "spec_version": "2.0",
  "message_id": "aaaaaaaaaaaa4aaaaaaaaaaaaaaaaaac",
  "type": "turn.acknowledged",
  "occurred_at": "2026-08-11T08:00:00Z",
  "trace_id": "66666666666646668666666666666666",
  "sender": { "kind": "user", "id": "iphone" },
  "payload": {
    "turn_id": "019fe7db-0100-7000-8000-000000000001",
    "status": "completed"
  }
}
```

## 接收 Body：Mac Agent -> App

| `type` | `payload` 结构 | 说明 |
| --- | --- | --- |
| `agent.hello` | `name`, `version`, `status` | Agent 连接后的身份和初始状态 |
| `agent.status` | `status`; 可选 `project_id`, `thread_id`, `turn_id` | `idle`、`running` 或 `offline` |
| `agent.capabilities` | `restricted`, `sandbox_mode`, `approval_policy`, `writable_scope` 和能力布尔值 | 提交任务前可用的远程执行权限快照 |
| `execution.profile.snapshot` | `project_id`, `default_profile_id`, `profiles[]` | 当前项目由 App Server 返回的档位和 allowed 状态 |
| `project.snapshot` | `projects[]` | 项目列表快照 |
| `thread.snapshot` | `project_id`, `threads[]` | 指定项目的会话快照 |
| `thread.detail` | `project_id`, `thread`, `truncated` | 指定会话的元数据、Turns 和结构化 Items |
| `turn.started` | `project_id`, `thread_id`, `turn_id`, `started_at` | Turn 已由 Codex 启动 |
| `turn.output` | `project_id`, `thread_id`, `turn_id`, `stream`, `text` | 兼容旧客户端的流式输出；新客户端以 `turn.item.*` 为准 |
| `turn.item.started` | 执行 ID、`sequence`, `item` | 一个消息、命令或工具 Item 开始 |
| `turn.item.delta` | 执行 ID、`sequence`, `item_id`, `field`, `delta` | 追加更新 Item 的 `text` 或 `output` 字段 |
| `turn.item.completed` | 执行 ID、`sequence`, `item` | 使用最终完整 Item 替换流式中的 Item |
| `turn.snapshot` | 执行 ID、`status`, `started_at`, `recent_output[]`; 可选 `live_items[]`, `last_sequence`, `live_items_truncated` | App 重连时恢复正在执行的 Turn |
| `turn.interrupted` | `project_id`, `thread_id`, `turn_id`, `duration_ms` | Turn 已中断 |
| `turn.completed` | `project_id`, `thread_id`, `turn_id`, `duration_ms`, `summary`, `changed_files[]`, `diff`, `diff_truncated` | Turn 成功及 Git 结果 |
| `turn.failed` | 可选 `project_id`, `thread_id`, `turn_id`, `duration_ms`; `code`, `message` | Turn 或结果收集失败 |
| `turn.rejected` | `code`, `message`; 可选 `required_capabilities`, `recovery_action`, `execution_context` | Relay 或 Agent 拒绝请求 |

`turn.item.*` 的 `sequence` 在单个 Turn 内严格递增。App 应按 `item.id` 原位更新 UI，忽略不大于已处理序号的重复事件，并将 `turn.item.completed.item` 视为该 Item 的最终权威状态。`turn.snapshot.live_items` 用于重连恢复；`live_items_truncated` 为 `true` 时表示内容因帧大小限制被裁剪。

App 应在收到声明 `supports_permission_profiles=true` 的 `agent.capabilities` 后，按项目发送 `execution.profile.list`。只展示 snapshot 中 `allowed=true` 的档位，默认使用 `default_profile_id`，并在 `turn.start.permission_profile_id` 回传选择。完整示例见 `protocol/fixtures/execution.profile.*.json`。

### project.snapshot 项目字段

| 字段 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- |
| `id` | string | 是 | 稳定 `project_id`，后续请求必须使用该值 |
| `name` | string | 是 | Git 项目目录名 |
| `path` | string | 是 | Mac 上的只读展示路径，不能由 App 回传替代 `project_id` |
| `thread_count` | integer | 是 | 已发现的 Codex 会话数 |
| `updated_at` | string | 否 | 最近会话的 RFC 3339 更新时间 |

```json
{
  "spec_version": "2.0",
  "message_id": "aaaaaaaaaaaa4aaaaaaaaaaaaaaaaaaa",
  "type": "project.snapshot",
  "occurred_at": "2026-08-10T08:30:01Z",
  "trace_id": "22222222222242228222222222222222",
  "sender": { "kind": "device", "id": "local-mac" },
  "payload": {
    "projects": [
      {
        "id": "project_9e8638437f803c29d91a3844",
        "name": "relay-server",
        "path": "/Users/leehooo/work/selftools/codexremote/relay-server",
        "thread_count": 1,
        "updated_at": "2026-08-10T02:52:30Z"
      }
    ]
  }
}
```

### thread.snapshot 会话字段

| 字段 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- |
| `id` | string | 是 | Codex Thread ID |
| `project_id` | string | 是 | 所属项目 ID |
| `title` | string | 是 | 会话标题 |
| `preview` | string | 是 | Codex 原始会话预览；通常接近会话主题或较早内容，不保证是最新消息 |
| `latest_message_preview` | string | 是 | 最新一个可见用户或助手消息的摘要；不包含 reasoning、commentary 或 tool 信息 |
| `status` | string | 是 | Codex 状态；`notLoaded` 表示已持久化但当前未载入内存 |
| `source` | string | 是 | `cli`、`vscode`、`exec` 或 `appServer` |
| `updated_at` | string | 是 | RFC 3339 更新时间 |

### turn.completed

```json
{
  "spec_version": "2.0",
  "message_id": "bbbbbbbbbbbb4bbbbbbbbbbbbbbbbbbb",
  "type": "turn.completed",
  "occurred_at": "2026-08-10T08:33:21Z",
  "trace_id": "66666666666646668666666666666666",
  "sender": { "kind": "device", "id": "local-mac" },
  "payload": {
    "project_id": "project_9e8638437f803c29d91a3844",
    "thread_id": "019fe7da-7105-7831-8c0c-8997786ddd3f",
    "turn_id": "019fe7db-0100-7000-8000-000000000001",
    "duration_ms": 201000,
    "summary": "相关测试已通过。",
    "changed_files": ["internal/server/server.go"],
    "diff": "diff --git a/internal/server/server.go b/internal/server/server.go",
    "diff_truncated": false
  }
}
```

### thread.detail

`thread.turns[]` 按 Codex 持久化顺序返回；每个 Turn 的 `items[]` 也保持原顺序。`trace_id` 与触发它的 `thread.read` 相同。

```json
{
  "spec_version": "2.0",
  "message_id": "99999999999949998999999999999999",
  "type": "thread.detail",
  "occurred_at": "2026-08-10T08:30:05Z",
  "trace_id": "88888888888848888888888888888888",
  "sender": { "kind": "device", "id": "local-mac" },
  "payload": {
    "project_id": "project_9e8638437f803c29d91a3844",
    "thread": {
      "id": "019fe7da-7105-7831-8c0c-8997786ddd3f",
      "project_id": "project_9e8638437f803c29d91a3844",
      "title": "修复登录问题",
      "preview": "检查登录接口并运行测试",
      "status": "notLoaded",
      "source": "appServer",
      "created_at": "2026-08-10T08:28:00Z",
      "updated_at": "2026-08-10T08:30:00Z",
      "turns": [
        {
          "id": "019fe7db-0100-7000-8000-000000000001",
          "status": "completed",
          "duration_ms": 120000,
          "items": [
            { "id": "item-user", "type": "userMessage", "role": "user", "text": "检查登录接口并运行测试", "truncated": false },
            { "id": "item-command", "type": "commandExecution", "status": "completed", "command": "go test ./...", "output": "ok", "exit_code": 0, "truncated": false },
            { "id": "item-agent", "type": "agentMessage", "role": "assistant", "phase": "final_answer", "text": "登录测试已通过。", "truncated": false }
          ],
          "truncated": false
        }
      ]
    },
    "truncated": false
  }
}
```

常见 Item `type`：`userMessage`、`agentMessage`、`plan`、`reasoning`、`commandExecution`、`fileChange`、`mcpToolCall`、`dynamicToolCall`、`collabAgentToolCall`、`webSearch`、`imageView`、`imageGeneration`、`enteredReviewMode`、`exitedReviewMode`、`contextCompaction`。所有 Item 都有 `id`、`type`、`truncated`；其余字段按类型出现：

| Item 字段 | 用途 |
| --- | --- |
| `role`, `phase`, `text` | 用户消息、AI 回复、计划和推理摘要 |
| `status`, `command`, `cwd`, `output`, `exit_code`, `duration_ms` | 命令或工具执行 |
| `changes[]` | 文件变更；元素包含 `path`、`kind`、可选 `diff` |
| `name` | MCP、动态工具或协作工具名称 |
| `query` | Web 搜索查询 |
| `path` | 图片查看或生成结果路径 |

较大的文本、命令、输出、查询、路径、错误和 Diff 字段最大 24 KiB，整个详情 payload 控制在 200 KiB 内。字段被裁剪时 `item.truncated: true`；较早 Item 或 Turn 被省略时 `turn.truncated` 或顶层 `truncated` 为 `true`。Relay 的单帧上限仍为 256 KiB。

## 错误与关联规则

- `MESSAGE_INVALID`：Envelope、方向或 payload 不合法。
- `AGENT_OFFLINE`：没有 Mac Agent 连接 Relay。
- `PROJECT_LIST_FAILED`、`THREAD_LIST_FAILED`：项目或会话列表查询失败。
- `THREAD_READ_FAILED`：项目不存在、Thread 不属于项目，或 Codex 无法读取该会话。
- `AGENT_BUSY`：已有 Turn 正在执行。
- `TURN_START_FAILED`、`TURN_NOT_FOUND`：启动或中断目标无效。
- 一个请求的响应和后续事件必须沿用请求 `trace_id`，但每条消息必须生成自己的 `message_id`。
