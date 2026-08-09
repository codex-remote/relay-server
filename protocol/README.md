# Protocol v2

This directory is the language-neutral contract source for AI Coding Remote MVP.

- `schema/message.schema.json` defines the `2.0` envelope and message names.
- `fixtures/` contains Project/Thread/Turn messages for cross-client contract tests.
- `2.0` is the only accepted version; removed `1.0 run.*` messages are not translated.

Relay validates the envelope and message direction. Mac Agent and iPhone validate payloads they consume. Breaking pre-release changes require an explicit protocol major-version update across all three repositories.
