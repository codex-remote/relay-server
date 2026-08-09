# Protocol v1

This directory is the language-neutral contract source for AI Coding Remote.

- `schema/message.schema.json` defines the forward-compatible MVP envelope.
- `fixtures/` contains messages that every Go and Swift implementation must decode.
- Existing fields are not renamed within protocol `1.x`; optional fields and message types may be added.

The Relay validates the envelope and message direction. Each endpoint remains responsible for validating the payload it consumes.
