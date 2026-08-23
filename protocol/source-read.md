# Project-scoped source read

`POST /v1/runtime/projects/{project_id}/source:read` lets Runtime clients inspect the current Mac working-tree source referenced by a Codex answer. It is a read-only, short-lived bridge and is not part of a Turn. The reference is sent in JSON rather than the URL so HTTP access logs do not capture Mac paths.

## Flow

1. The client supplies the selected `project_id`, a source reference, an optional focus line, and a bounded context size.
2. Run Server's unified Runtime middleware validates the Opaque Access Token and requires `source:read`. It then sends `source.read` to the connected Mac Agent.
3. Mac Agent resolves the project from its trusted catalog, canonicalizes the file and all symlinks, and verifies the result remains within one of that project's roots.
4. Agent returns `source.snapshot` or `source.read.failed` with the same `trace_id`.
5. Run Server returns the v1 HTTP envelope and discards the in-memory waiter. Source content is never written to PostgreSQL, Redis, or service logs.

`source.read` is an internal Run Server to Agent message. Relay's `/ws/app` direction allowlist rejects it, so legacy WebSocket clients cannot bypass the HTTP entry point and its request validation.

The HTTP request times out after eight seconds. Agent disconnects fail outstanding reads immediately. The operation is deliberately not durable because retrying a read is safe and the result always represents the current working tree.

Run Server accepts at most 16 concurrent source reads per process and returns `SOURCE_BUSY` above that limit. Public deployments must use the Gateway, HTTPS, Runtime Auth and request rate limits.

## Compatibility and safety

- `path` should be project-relative. Absolute paths already present in historical model answers are accepted only as compatibility input and only when Agent proves containment in the selected project.
- Responses always return a project-relative path.
- Traversal, symlink escape, directories, binary files, sensitive credential file patterns, files over 1 MiB, and response bodies over the WebSocket safety limit are rejected.
- Small text files return in full. Large text files return a bounded window around `focus_line` with `start_line`, `end_line`, `total_lines`, and `truncated` metadata.
- A source snapshot includes SHA-256 and modification time. It describes click-time working-tree state, not the historical state when the answer was generated.
- Source reads never have an anonymous exception in the authenticated Mobile Web Runtime profile.

## Deployment order

| Component | Compatibility behavior |
| --- | --- |
| Mac Agent first | Advertises `supports_source_read=true`; older Run Servers ignore the field and never send `source.read`. |
| Run Server second | Requires `source:read`; an older Agent returns no source response and the HTTP request times out without affecting Runs. |
| mobile-web last | Converts recognized local Markdown references to the same-host hash route; normal web links keep external-link behavior, and source failures stay inside the viewer. |

Rollback is independent per repository. Removing the mobile viewer leaves the source API unused; removing the API or Agent causes a visible non-destructive viewer error. No database migration or source cleanup is required.
