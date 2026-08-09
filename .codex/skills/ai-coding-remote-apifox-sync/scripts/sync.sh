#!/usr/bin/env bash
set -euo pipefail

skill_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
repo_root="$(cd "$skill_dir/../../.." && pwd)"
settings_file="$repo_root/.apifox/settings.json"
openapi_file="$repo_root/apifox/openapi.json"
websocket_dir="$repo_root/apifox/websockets"
action="${1:-check}"

require_command() {
	if ! command -v "$1" >/dev/null 2>&1; then
		echo "required command not found: $1" >&2
		exit 1
	fi
}

require_command apifox
require_command jq

project_id="$(jq -er '.projectId' "$settings_file")"
module_id="$(jq -er '.moduleId' "$settings_file")"
if [[ "$project_id" != "8693796" || "$module_id" != "8356476" ]]; then
	echo "unexpected Apifox project/module in $settings_file" >&2
	exit 1
fi

validate_local() {
	jq -e '.openapi == "3.1.0" and (.paths | has("/healthz")) and (.paths | has("/status"))' "$openapi_file" >/dev/null
	for definition in "$websocket_dir"/*.json; do
		jq -e --argjson module_id "$module_id" '.moduleId == $module_id and (.path | startswith("ws://127.0.0.1:8080/ws/"))' "$definition" >/dev/null
		apifox cli-schema validate websocket-create --file "$definition" >/dev/null
		apifox cli-schema validate websocket-update --file "$definition" >/dev/null
	done
	echo "Local Apifox definitions are valid."
}

check_remote() {
	apifox whoami >/dev/null
	project_json="$(apifox project get "$project_id")"
	if [[ "$(jq -r '.data.id' <<<"$project_json")" != "$project_id" ]]; then
		echo "Apifox project access check failed" >&2
		exit 1
	fi
	echo "HTTP endpoints:"
	apifox endpoint list --project "$project_id" | jq -r '.data[]? | "  \(.method // "?") \(.path // "?") [id=\(.id)]"'
	echo "WebSocket endpoints:"
	apifox websocket list --project "$project_id" | jq -r '.data[]? | "  \(.path // "?") [id=\(.id), name=\(.name)]"'
}

sync_http() {
	apifox import --project "$project_id" --format openapi --file "$openapi_file"
}

sync_websockets() {
	for definition in "$websocket_dir"/*.json; do
		path="$(jq -r '.path' "$definition")"
		list_json="$(apifox websocket list --project "$project_id")"
		matches="$(jq --arg path "$path" '[.data[]? | select(.path == $path)] | length' <<<"$list_json")"
		if [[ "$matches" -gt 1 ]]; then
			echo "duplicate WebSocket path in Apifox: $path" >&2
			exit 1
		fi
		websocket_id="$(jq -r --arg path "$path" '.data[]? | select(.path == $path) | .id' <<<"$list_json")"
		if [[ -n "$websocket_id" ]]; then
			apifox websocket get "$websocket_id" --project "$project_id" >/dev/null
			apifox cli-schema validate websocket-update --file "$definition" >/dev/null
			apifox websocket update "$websocket_id" --project "$project_id" --file "$definition"
		else
			apifox cli-schema validate websocket-create --file "$definition" >/dev/null
			apifox websocket create --project "$project_id" --file "$definition"
		fi
	done
}

case "$action" in
validate)
	validate_local
	;;
check)
	validate_local
	check_remote
	;;
sync)
	validate_local
	apifox whoami >/dev/null
	sync_http
	sync_websockets
	check_remote
	;;
*)
	echo "usage: $0 {validate|check|sync}" >&2
	exit 2
	;;
esac
