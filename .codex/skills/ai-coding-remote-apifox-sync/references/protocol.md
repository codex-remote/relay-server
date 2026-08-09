# Relay Apifox Protocol Reference

## Resources

| Resource | Direction | Purpose |
| --- | --- | --- |
| `GET /healthz` | Client -> Relay | Process health |
| `GET /status` | Client -> Relay | Current App and Agent connections |
| `/ws/app` | App <-> Relay | Submit/cancel Run and receive events |
| `/ws/agent` | Mac Agent <-> Relay | Receive commands and publish execution events |

## Message directions

App may send `run.start` and `run.cancel`.

Agent may send `agent.hello`, `agent.status`, `run.started`, `run.output`, `run.snapshot`, `run.cancelled`, `run.completed`, `run.failed`, and `run.rejected`.

The Relay overwrites `sender` according to the WebSocket endpoint. The Envelope fields `spec_version`, `message_id`, `type`, `occurred_at`, `trace_id`, `sender`, and `payload` are required.

## Manual test

1. Start Relay on `:8080`.
2. Start the real Mac Agent against `ws://127.0.0.1:8080/ws/agent`.
3. In Apifox Desktop, open `iPhone App 控制通道` and connect.
4. Confirm receipt of `agent.hello` and `agent.status`.
5. Send the `run.start` example from the interface description with a new `message_id`, `trace_id`, and matching `payload.run_id`.
6. Observe `run.started`, streamed `run.output`, and one terminal event.
7. To cancel, send `run.cancel` with the same Run ID.

Do not connect the Apifox `Mac Agent 执行通道` while a real Mac Agent run is active; the MVP allows only one Agent connection.
