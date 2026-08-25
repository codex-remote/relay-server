# Protocol v2

This directory is the language-neutral contract source for AI Coding Remote MVP.

- `schema/message.schema.json` defines the `2.0` envelope and message names.
- `fixtures/` contains Agent capability, Project/Thread/Turn, project-scoped source read, and resumable Bootstrap messages, including determinate session progress in `bootstrap.batch`, for WebSocket protocol tests. HTTP Runtime JSON polling response examples live under `apifox/fixtures/`.
- `source-read.md` defines the authenticated HTTP to ephemeral Agent source-read bridge and its filesystem safety rules.
- `execution-permissions.md` defines project-scoped permission profile discovery, selection, validation, and Thread/Turn forwarding.
- `runtime-polling.md` records the implemented JSON polling transport constraints; production rate limiting, cursor expiry and edge validation remain pending.
- `2.0` is the only accepted version; removed `1.0 run.*` messages are not translated.

Relay validates the envelope and message direction. Mac Agent and iPhone validate payloads they consume. Breaking pre-release changes require an explicit protocol major-version update across all three repositories.

Bootstrap batches report `processed_sessions` against a snapshot-stable `total_sessions`. A final batch sets `reconciliation_safe=true` only when the Agent completed a newly captured manifest without resuming from a durable batch watermark. Relay reconciles missing Projects and Codex Threads only for that safe final batch; incomplete or resumed snapshots remain import-only and never hide Runtime resources.

After applying a terminal Turn event, App sends `turn.acknowledged` with the terminal `turn_id` and `status`. Maintenance jobs that would terminate or replace the App must wait for this acknowledgement instead of relying on a fixed delay.
