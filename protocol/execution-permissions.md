# Execution Permission Negotiation

This is the canonical cross-client contract for selecting Codex App Server execution permissions from iPhone.

The upstream behavior is defined by the [official Codex App Server documentation](https://developers.openai.com/codex/app-server/). `permissionProfile/list` and named `permissions` require an initialized client with `experimentalApi: true`.

## Flow

1. Agent advertises `supports_permission_profiles: true` in `agent.capabilities`.
2. App sends `execution.profile.list` with a trusted `project_id`.
3. Agent resolves that ID to its own project path and calls experimental App Server `permissionProfile/list` with the project `cwd`.
4. Agent returns `execution.profile.snapshot` with the App Server profile IDs, descriptions, allowed flags, and `default_profile_id`.
5. App displays only `allowed=true` profiles, persists the selection per project, and sends it as `turn.start.permission_profile_id`.
6. Agent queries the profiles again before execution, rejects missing or disallowed IDs, and passes the selected ID as `permissions` to `thread/start` or `thread/resume` and to `turn/start`.

The App never supplies a filesystem path, writable root, sandbox object, or approval policy. The Agent must never combine App Server `permissions` with legacy `sandbox`. When no profile is supplied, the compatibility default remains `workspace-write` with `approvalPolicy=never`.

## Security Boundary

Profile names are not authority. App Server and effective Mac management requirements determine `allowed`; Agent performs the final validation using the selected project's trusted path. Relay only checks message direction and transparently forwards the payload.

`:danger-full-access` can expose host resources and device services. It must be a deliberate user selection and must only be used over the trusted LAN, VPN, or authenticated protected Relay transport described by this project.

## Compatibility

Older Agents omit `supports_permission_profiles`; older Apps continue using the compatibility default. A new App must not submit a task until a valid profile has been selected when the Agent declares profile support.
