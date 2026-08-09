---
name: ai-coding-remote-apifox-sync
description: Validate, sync, and verify AI Coding Remote Relay Server HTTP and WebSocket definitions in Apifox project 8693796. Use when Relay endpoints or JSON messages change, when the user wants to debug /healthz, /status, /ws/app, or /ws/agent in Apifox, or when checking whether Apifox matches the Go Relay protocol.
---

# AI Coding Remote Apifox Sync

Run all commands from the `relay-server` repository root.

## Workflow

1. Read `.apifox/settings.json`; confirm `projectId` is `8693796` and module is `Relay Server` (`8356476`). Never guess or silently substitute IDs.
2. Inspect the changed Go handler and `internal/protocol/message.go`. Keep Apifox descriptions Chinese-first and technical identifiers unchanged.
3. Update the matching source definitions:
   - HTTP: `apifox/openapi.json`
   - WebSocket: `apifox/websockets/app.json` and `apifox/websockets/agent.json`
4. Run local validation before any remote write:

   ```bash
   .codex/skills/ai-coding-remote-apifox-sync/scripts/sync.sh validate
   ```

5. Inspect the remote project without modifying it:

   ```bash
   .codex/skills/ai-coding-remote-apifox-sync/scripts/sync.sh check
   ```

6. Sync only after validation succeeds and the requested changes are intentional:

   ```bash
   .codex/skills/ai-coding-remote-apifox-sync/scripts/sync.sh sync
   ```

7. Re-run `check` and report HTTP paths, WebSocket paths, resource IDs, and any mismatch. Never report access tokens.

## Rules

- Use the official `apifox` CLI. Verify with `apifox --version` and `apifox whoami`.
- Full sync requires direct external AI editing permission on the Apifox main branch. Apifox CLI `2.2.9` does not support `--branch` for WebSocket resources, so an AI branch cannot sync the complete Relay contract.
- If Apifox returns `Automation caller branch required`, stop and ask the user to enable `项目设置 -> 功能设置 -> 外部 AI 编辑权限`; do not create or merge an AI branch automatically.
- Run `apifox cli-schema validate` before every WebSocket create or update.
- Match existing WebSockets by exact `path`; update in place and never create duplicates.
- Before an update, read the current resource with `apifox websocket get`.
- OpenAPI describes only HTTP endpoints. Maintain WebSocket resources with `apifox websocket` commands.
- Keep the MVP unauthenticated in Apifox. Do not invent Bearer or device authentication before the code implements it.
- Use `ws://127.0.0.1:8080` for Apifox desktop debugging. Connecting Apifox to `/ws/agent` replaces the real Mac Agent connection.
- Do not delete remote resources unless the user explicitly requests deletion.
- Do not print, copy, or commit Apifox tokens. CLI login state belongs in the user profile.

Read [references/protocol.md](references/protocol.md) when message directions, examples, or the manual debugging sequence are involved.
