#!/usr/bin/env bash
set -euo pipefail

relay_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
mac_agent_root="$(cd "$relay_root/../mac-agent" && pwd)"
test_root="$(mktemp -d "${TMPDIR:-/tmp}/ai-coding-remote-relay-e2e.XXXXXX")"
project_dir="$test_root/project"
state_file="$test_root/codex-state.json"
port="${RELAY_E2E_PORT:-18766}"
relay_log="$test_root/relay.log"
agent_log="$test_root/agent.log"

relay_pid=""
agent_pid=""
cleanup() {
	exit_status=$?
	if [[ "$exit_status" -ne 0 ]]; then
		echo "Relay E2E failed; fixture logs:" >&2
		tail -n 80 "$relay_log" >&2 2>/dev/null || true
		tail -n 80 "$agent_log" >&2 2>/dev/null || true
	fi
	if [[ -n "$agent_pid" ]]; then
		kill "$agent_pid" 2>/dev/null || true
		wait "$agent_pid" 2>/dev/null || true
	fi
	if [[ -n "$relay_pid" ]]; then
		kill "$relay_pid" 2>/dev/null || true
		wait "$relay_pid" 2>/dev/null || true
	fi
}
trap cleanup EXIT

mkdir -p "$project_dir"
cat > "$project_dir/go.mod" <<'EOF'
module example.com/relay-e2e

go 1.23.0
EOF
cat > "$project_dir/greeting.go" <<'EOF'
package greeting

func Greet() string { return "before" }
EOF
cat > "$project_dir/greeting_test.go" <<'EOF'
package greeting

import "testing"

func TestGreet(t *testing.T) {
	if got := Greet(); got != "after" {
		t.Fatalf("Greet() = %q, want after", got)
	}
}
EOF
git -C "$project_dir" init -q -b main
git -C "$project_dir" config user.name "AI Coding Remote E2E"
git -C "$project_dir" config user.email "e2e@example.invalid"
git -C "$project_dir" add .
git -C "$project_dir" commit -qm "test: create Relay E2E fixture"

cat > "$state_file" <<EOF
{
  "local-projects": {
    "relay-e2e": { "name": "Relay E2E", "rootPaths": ["$project_dir"] }
  },
  "project-order": ["relay-e2e"],
  "thread-project-assignments": {
    "thread-existing": {
      "projectKind": "local",
      "projectId": "relay-e2e",
      "cwd": "$project_dir"
    }
  }
}
EOF

cat > "$test_root/fake_app_server.go" <<'EOF'
package main

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

type request struct {
	ID     json.RawMessage `json:"id"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params"`
}

func main() {
	scanner := bufio.NewScanner(os.Stdin)
	writer := bufio.NewWriter(os.Stdout)
	encoder := json.NewEncoder(writer)
	respond := func(id json.RawMessage, result any) {
		_ = encoder.Encode(map[string]any{"id": json.RawMessage(id), "result": result})
		_ = writer.Flush()
	}
	notify := func(method string, params any) {
		_ = encoder.Encode(map[string]any{"method": method, "params": params})
		_ = writer.Flush()
	}

	for scanner.Scan() {
		var incoming request
		if json.Unmarshal(scanner.Bytes(), &incoming) != nil || len(incoming.ID) == 0 {
			continue
		}
		switch incoming.Method {
		case "initialize":
			respond(incoming.ID, map[string]any{"userAgent": "fake-codex-app-server"})
		case "thread/list":
			cwd := os.Getenv("FAKE_PROJECT_DIR")
			respond(incoming.ID, map[string]any{"data": []any{map[string]any{
				"id": "thread-existing", "name": "Relay E2E", "cwd": cwd,
				"preview": "Relay E2E", "status": map[string]any{"type": "notLoaded"},
				"source": "appServer", "createdAt": time.Now().Unix(), "updatedAt": time.Now().Unix(),
			}}, "nextCursor": nil})
		case "thread/start", "thread/resume":
			var params map[string]any
			_ = json.Unmarshal(incoming.Params, &params)
			cwd, _ := params["cwd"].(string)
			respond(incoming.ID, map[string]any{"thread": map[string]any{
				"id": "thread-e2e", "name": "Relay E2E", "cwd": cwd,
				"preview": "Relay E2E", "status": map[string]any{"type": "idle"},
				"source": "appServer", "createdAt": time.Now().Unix(), "updatedAt": time.Now().Unix(),
			}})
		case "turn/start":
			var params map[string]any
			_ = json.Unmarshal(incoming.Params, &params)
			cwd, _ := params["cwd"].(string)
			respond(incoming.ID, map[string]any{"turn": map[string]any{"id": "turn-e2e", "status": "inProgress"}})
			_ = os.WriteFile(filepath.Join(cwd, "greeting.go"), []byte("package greeting\n\nfunc Greet() string { return \"after\" }\n"), 0o644)
			notify("item/started", map[string]any{"threadId": "thread-e2e", "turnId": "turn-e2e", "item": map[string]any{
				"id": "item-command", "type": "commandExecution", "status": "inProgress", "command": "go test ./...", "cwd": cwd,
			}})
			notify("item/commandExecution/outputDelta", map[string]any{"threadId": "thread-e2e", "turnId": "turn-e2e", "itemId": "item-command", "delta": "fake Codex changed greeting.go\n"})
			notify("item/completed", map[string]any{"threadId": "thread-e2e", "turnId": "turn-e2e", "item": map[string]any{
				"id": "item-command", "type": "commandExecution", "status": "completed", "command": "go test ./...", "cwd": cwd,
				"aggregatedOutput": "fake Codex changed greeting.go\n", "exitCode": 0,
			}})
			notify("item/started", map[string]any{"threadId": "thread-e2e", "turnId": "turn-e2e", "item": map[string]any{
				"id": "item-agent", "type": "agentMessage", "text": "",
			}})
			notify("item/agentMessage/delta", map[string]any{"threadId": "thread-e2e", "turnId": "turn-e2e", "itemId": "item-agent", "delta": "Relay end-to-end turn completed."})
			notify("item/completed", map[string]any{"threadId": "thread-e2e", "turnId": "turn-e2e", "item": map[string]any{
				"id": "item-agent", "type": "agentMessage", "phase": "final_answer", "text": "Relay end-to-end turn completed.",
			}})
			notify("turn/completed", map[string]any{"threadId": "thread-e2e", "turn": map[string]any{"id": "turn-e2e", "status": "completed", "durationMs": 25}})
		case "turn/interrupt":
			respond(incoming.ID, map[string]any{})
		}
	}
}
EOF

go build -C "$relay_root" -o "$test_root/relay" ./cmd/relay
go build -C "$relay_root" -o "$test_root/relayctl" ./cmd/relayctl
go build -C "$mac_agent_root" -o "$test_root/mac-agent" ./cmd/agent
go build -o "$test_root/fake-codex" "$test_root/fake_app_server.go"

"$test_root/relay" --listen "127.0.0.1:$port" >"$relay_log" 2>&1 &
relay_pid=$!
for _ in {1..50}; do
	if curl -fsS "http://127.0.0.1:$port/healthz" >/dev/null 2>&1; then
		break
	fi
	sleep 0.1
done
curl -fsS "http://127.0.0.1:$port/healthz" >/dev/null

AGENT_RELAY_URL="ws://127.0.0.1:$port/ws/agent" \
AGENT_WORKSPACE_ROOTS="$project_dir" \
AGENT_CODEX_STATE_FILE="$state_file" \
AGENT_PROJECT_SCAN_DEPTH=1 \
AGENT_CODEX_BINARY="$test_root/fake-codex" \
AGENT_RUNTIME_DB_PATH="$test_root/agent-runtime.sqlite3" \
FAKE_PROJECT_DIR="$project_dir" \
"$test_root/mac-agent" serve >"$agent_log" 2>&1 &
agent_pid=$!
for _ in {1..50}; do
	if curl -fsS "http://127.0.0.1:$port/status" | grep -q '"agent_connected":true'; then
		break
	fi
	sleep 0.1
done
curl -fsS "http://127.0.0.1:$port/status" | grep -q '"agent_connected":true'

projects_output="$("$test_root/relayctl" projects --url "ws://127.0.0.1:$port/ws/app")"
project_id="$(printf '%s\n' "$projects_output" | awk 'NR == 1 { print $1 }')"
if [[ -z "$project_id" ]]; then
	echo "No project returned by project.snapshot" >&2
	exit 1
fi

"$test_root/relayctl" turn \
	--url "ws://127.0.0.1:$port/ws/app" \
	--project "$project_id" \
	--prompt "Change Greet to return after and run the tests."

(cd "$project_dir" && go test ./...)
git -C "$project_dir" diff --check
grep -q 'return "after"' "$project_dir/greeting.go"

echo
echo "Relay + Mac Agent Project/Thread/Turn end-to-end test passed."
echo "Artifacts: $test_root"
