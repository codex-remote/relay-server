# Relay Apifox Protocol Reference

## Resources

| Resource | Direction | Purpose |
| --- | --- | --- |
| `GET /healthz` | Client -> Relay | Process health |
| `GET /status` | Client -> Relay | Current App and Agent connections |
| `/ws/app` | App <-> Relay | Project/Thread discovery and Turn control |
| `/ws/agent` | Mac Agent <-> Relay | Local inventory and Codex Turn execution |

## Protocol baseline

Only `spec_version: "2.0"` is accepted. There is no translation for removed `1.0 run.*` messages.

App may send `project.list`, `thread.list`, `turn.start`, and `turn.interrupt`.

Agent may send `agent.hello`, `agent.status`, `project.snapshot`, `thread.snapshot`, `turn.started`, `turn.output`, `turn.snapshot`, `turn.interrupted`, `turn.completed`, `turn.failed`, and `turn.rejected`.

Relay overwrites `sender` according to the WebSocket endpoint. Envelope fields `spec_version`, `message_id`, `type`, `occurred_at`, `trace_id`, `sender`, and `payload` are required.

## Manual test

1. Start Relay on `:8080`.
2. Start the real Mac Agent with at least one `--workspace-root`.
3. Open `iPhone App 控制通道` in Apifox Desktop and connect.
4. Confirm `agent.hello` and `agent.status`.
5. Send the `project.list` example and copy a returned `project_id`.
6. Send `thread.list` for that project.
7. Send `turn.start`, omitting `thread_id` for a new session or using a returned Thread ID.
8. Observe `turn.started`, streamed `turn.output`, and one terminal event.
9. To stop an active Turn, send `turn.interrupt` with its `thread_id` and `turn_id`.

Do not connect the Apifox Agent channel while the real Mac Agent is active; MVP allows one Agent connection.
