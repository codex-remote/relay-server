# Runtime JSON Polling Contract (Design)

Status: implemented locally; production hardening pending.

This document records the relay-server-side constraints for the Runtime JSON polling transport. The routes are now present in `apifox/openapi.json`; production rate limiting, cursor expiry and edge validation remain pending.

## Scope

The implemented routes are:

```text
GET /v1/runtime/sessions/{session_id}/events:poll
GET /v1/runtime/runs/{run_id}/events:poll
```

They return one bounded `application/json` response. They do not return `text/event-stream`, chunked event data, or an unbounded response body.

## Server invariants

1. `after` is a durable Session or Run sequence. The response is ordered and contains no event at or below `after`.
2. The response is built from PostgreSQL after the event has been persisted. Redis/Valkey may wake a request but is not the durable response source.
3. `wait_ms` and `limit` are bounded by server-side constants. The handler observes `request.Context()` and releases waits on cancellation.
4. A timeout is a successful empty response, not a Runtime failure.
5. Repeating a request with the same cursor is safe. Clients may receive the same event batch again after a lost response.
6. Terminal state and the terminal event use the same cursor boundary. A client stops polling only after applying the terminal event or observing `terminal=true` from a durable snapshot.

## Notification ordering

The durable event path must be:

```text
AppendAgentEvent / session event transaction commits
    -> publish Run or Session notification
    -> wake a bounded poll request
```

The handler must query PostgreSQL again after a wake. It must not treat an early Redis Stream entry as proof that the PostgreSQL transaction committed.

The current Broker exposes Session notification only. The implementation will need an explicit Run notification or an equivalent resource-scoped wake mechanism. A lost notification is acceptable only because the next bounded request retries the PostgreSQL cursor query.

## Required tests before promotion

- ordered events and `next_cursor`;
- empty timeout and request cancellation;
- `limit`/`wait_ms` validation;
- duplicate request after a lost response;
- event notification before and after PostgreSQL commit;
- Redis/Valkey unavailable fallback;
- terminal event and terminal snapshot;
- auth, scope, resource isolation and rate limiting;
- concurrent polling of independent resources;
- Gateway route allowlist and JSON response preservation.

When implemented, this file should link to the versioned OpenAPI operation IDs and fixtures. Until then, it must not be used as evidence that the route exists in a released binary.
