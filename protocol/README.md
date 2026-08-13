# Protocol v2

This directory is the language-neutral contract source for AI Coding Remote MVP.

- `schema/message.schema.json` defines the `2.0` envelope and message names.
- `fixtures/` contains Agent capability and Project/Thread/Turn messages, including `agent.capabilities`, `thread.read`, `thread.detail`, and terminal `turn.acknowledged`, for cross-client contract tests.
- `execution-permissions.md` defines project-scoped permission profile discovery, selection, validation, and Thread/Turn forwarding.
- `2.0` is the only accepted version; removed `1.0 run.*` messages are not translated.

Relay validates the envelope and message direction. Mac Agent and iPhone validate payloads they consume. Breaking pre-release changes require an explicit protocol major-version update across all three repositories.

After applying a terminal Turn event, App sends `turn.acknowledged` with the terminal `turn_id` and `status`. Maintenance jobs that would terminate or replace the App must wait for this acknowledgement instead of relying on a fixed delay.
