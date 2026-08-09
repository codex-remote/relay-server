#!/usr/bin/env bash
set -euo pipefail

relay_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
mac_agent_root="$(cd "$relay_root/../mac-agent" && pwd)"
test_root="$(mktemp -d "${TMPDIR:-/tmp}/ai-coding-remote-relay-e2e.XXXXXX")"
project_dir="$test_root/project"
port="${RELAY_E2E_PORT:-18080}"
relay_log="$test_root/relay.log"
agent_log="$test_root/agent.log"

relay_pid=""
agent_pid=""
cleanup() {
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

cat > "$test_root/fake-codex" <<'EOF'
#!/bin/sh
cat > greeting.go <<'GOEOF'
package greeting

func Greet() string { return "after" }
GOEOF
echo "fake Codex changed greeting.go" >&2
echo "Relay end-to-end run completed"
EOF
chmod +x "$test_root/fake-codex"

go build -C "$relay_root" -o "$test_root/relay" ./cmd/relay
go build -C "$relay_root" -o "$test_root/relayctl" ./cmd/relayctl
go build -C "$mac_agent_root" -o "$test_root/mac-agent" ./cmd/agent

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
AGENT_WORKING_DIR="$project_dir" \
AGENT_CODEX_BINARY="$test_root/fake-codex" \
"$test_root/mac-agent" serve >"$agent_log" 2>&1 &
agent_pid=$!
for _ in {1..50}; do
	if curl -fsS "http://127.0.0.1:$port/status" | grep -q '"agent_connected":true'; then
		break
	fi
	sleep 0.1
done
curl -fsS "http://127.0.0.1:$port/status" | grep -q '"agent_connected":true'

"$test_root/relayctl" run \
	--url "ws://127.0.0.1:$port/ws/app" \
	--prompt "Change Greet to return after and run the tests."

(cd "$project_dir" && go test ./...)
git -C "$project_dir" diff --check
grep -q 'return "after"' "$project_dir/greeting.go"

echo
echo "Relay + Mac Agent end-to-end test passed."
echo "Artifacts: $test_root"
