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

App may send `project.list`, `execution.profile.list`, `thread.list`, `thread.read`, `turn.start`, and `turn.interrupt`.

Agent may send `agent.hello`, `agent.status`, `agent.capabilities`, `execution.profile.snapshot`, `project.snapshot`, `thread.snapshot`, `thread.detail`, `turn.started`, `turn.output`, `turn.snapshot`, `turn.interrupted`, `turn.completed`, `turn.failed`, and `turn.rejected`.

Relay overwrites `sender` according to the WebSocket endpoint. Envelope fields `spec_version`, `message_id`, `type`, `occurred_at`, `trace_id`, `sender`, and `payload` are required.

The authoritative Apifox sent/received Body contracts are `apifox/websockets/docs/app.md` and `apifox/websockets/docs/agent.md`. The sync script publishes these files as the WebSocket interface descriptions.

## Manual test

1. Run `./run debug` to restart the manual-debug Relay on fixed port `18765`.
2. Start the real Mac Agent. It scans `/Users/leehooo/work` by default; `--workspace-root` overrides that root.
3. Select the local or current LAN Relay environment. The WebSocket resources use relative paths and inherit that environment's pre-URL.
4. Open `iPhone App 控制通道` in Apifox Desktop and connect.
5. Confirm `agent.hello`, `agent.status`, and `agent.capabilities`.
6. Send the `project.list` example and copy a returned `project_id`.
7. Send `thread.list` for that project.
8. Send `thread.read` with that `project_id` and Thread ID; verify `thread.detail` uses the same `trace_id`.
9. Send `execution.profile.list` for the project and choose an allowed ID from `execution.profile.snapshot`.
10. Send `turn.start` with `permission_profile_id`, omitting `thread_id` for a new session or using a returned Thread ID.
11. Observe `turn.started`, streamed `turn.output`, and one terminal event.
12. To stop an active Turn, send `turn.interrupt` with its `thread_id` and `turn_id`.

Do not connect the Apifox Agent channel while the real Mac Agent is active; MVP allows one Agent connection.

The response to `project.list` is `project.snapshot`; the response to `thread.list` is `thread.snapshot`; the response to `thread.read` is `thread.detail`. A response uses the same `trace_id` as its request. Completed Codex threads commonly report `status: "notLoaded"`, which means persisted on disk but not loaded into the current App Server process.
