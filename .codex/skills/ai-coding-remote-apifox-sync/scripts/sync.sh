#!/usr/bin/env bash
set -euo pipefail

skill_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
repo_root="$(cd "$skill_dir/../../.." && pwd)"
settings_file="$repo_root/.apifox/settings.json"
openapi_file="$repo_root/apifox/openapi.json"
websocket_dir="$repo_root/apifox/websockets"
action="${1:-check}"

websocket_doc_path() {
	local definition="$1"
	local name
	name="$(basename "${definition%.json}")"
	echo "$websocket_dir/docs/$name.md"
}

websocket_message_path() {
	local definition="$1"
	if [[ "$(basename "$definition")" == "app.json" ]]; then
		echo "$repo_root/protocol/fixtures/project.list.json"
	fi
	return 0
}

write_websocket_payload() {
	local definition="$1"
	local output="$2"
	local doc
	doc="$(websocket_doc_path "$definition")"
	local message
	message="$(websocket_message_path "$definition")"
	if [[ -n "$message" ]]; then
		jq --rawfile description "$doc" --rawfile message "$message" '
			.description = $description
			| .requestBody.parameters = (.requestBody.parameters // [])
			| .requestBody.message = ($message | rtrimstr("\n"))
			| .requestBody.messageType = "json"
		' "$definition" >"$output"
	else
		jq --rawfile description "$doc" '.description = $description' "$definition" >"$output"
	fi
}

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
		doc="$(websocket_doc_path "$definition")"
		if [[ ! -s "$doc" ]]; then
			echo "missing WebSocket Body documentation: $doc" >&2
			exit 1
		fi
		if ! grep -q '^## 发送 Body' "$doc" || ! grep -q '^## 接收 Body' "$doc"; then
			echo "WebSocket documentation must contain 发送 Body and 接收 Body sections: $doc" >&2
			exit 1
		fi
		message="$(websocket_message_path "$definition")"
		if [[ -n "$message" ]]; then
			jq -e '.spec_version == "2.0" and .type == "project.list" and .payload == {}' "$message" >/dev/null
		fi
		jq -e --argjson module_id "$module_id" '.moduleId == $module_id and (.path == "/ws/app" or .path == "/ws/agent")' "$definition" >/dev/null
		payload="$(mktemp "${TMPDIR:-/tmp}/ai-coding-remote-apifox.XXXXXX")"
		write_websocket_payload "$definition" "$payload"
		apifox cli-schema validate websocket-create --file "$payload" >/dev/null
		apifox cli-schema validate websocket-update --file "$payload" >/dev/null
		rm -f "$payload"
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
	list_json="$(apifox websocket list --project "$project_id")"
	jq -r '.data[]? | "  \(.path // "?") [id=\(.id), name=\(.name)]"' <<<"$list_json"
	for definition in "$websocket_dir"/*.json; do
		path="$(jq -r '.path' "$definition")"
		doc="$(websocket_doc_path "$definition")"
		matches="$(jq --arg path "$path" '[.data[]? | select(.path == $path)] | length' <<<"$list_json")"
		if [[ "$matches" -ne 1 ]]; then
			echo "expected exactly one remote WebSocket for $path, found $matches" >&2
			exit 1
		fi
		websocket_id="$(jq -r --arg path "$path" '.data[]? | select(.path == $path) | .id' <<<"$list_json")"
		remote_json="$(apifox websocket get "$websocket_id" --project "$project_id")"
		if ! jq -e --rawfile expected "$doc" '.data.description == $expected' <<<"$remote_json" >/dev/null; then
			echo "remote WebSocket Body documentation differs: $path [id=$websocket_id]" >&2
			exit 1
		fi
		echo "  Body docs match: $path [id=$websocket_id]"
		message="$(websocket_message_path "$definition")"
		if [[ -n "$message" ]]; then
			if ! jq -e --rawfile expected "$message" '
				.data.requestBody.messageType == "json"
				and .data.requestBody.message == ($expected | rtrimstr("\n"))
			' <<<"$remote_json" >/dev/null; then
				echo "remote default WebSocket Message differs: $path [id=$websocket_id]" >&2
				exit 1
			fi
			echo "  Default Message matches: $path [type=project.list]"
		fi
	done
}

sync_http() {
	apifox import --project "$project_id" --format openapi --file "$openapi_file"
}

sync_websockets() {
	for definition in "$websocket_dir"/*.json; do
		path="$(jq -r '.path' "$definition")"
		payload="$(mktemp "${TMPDIR:-/tmp}/ai-coding-remote-apifox.XXXXXX")"
		write_websocket_payload "$definition" "$payload"
		list_json="$(apifox websocket list --project "$project_id")"
		matches="$(jq --arg path "$path" '[.data[]? | select(.path == $path)] | length' <<<"$list_json")"
		if [[ "$matches" -gt 1 ]]; then
			echo "duplicate WebSocket path in Apifox: $path" >&2
			exit 1
		fi
		websocket_id="$(jq -r --arg path "$path" '.data[]? | select(.path == $path) | .id' <<<"$list_json")"
		if [[ -n "$websocket_id" ]]; then
			apifox websocket get "$websocket_id" --project "$project_id" >/dev/null
			apifox cli-schema validate websocket-update --file "$payload" >/dev/null
			apifox websocket update "$websocket_id" --project "$project_id" --file "$payload"
		else
			apifox cli-schema validate websocket-create --file "$payload" >/dev/null
			apifox websocket create --project "$project_id" --file "$payload"
		fi
		rm -f "$payload"
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
sync-websockets)
	validate_local
	apifox whoami >/dev/null
	sync_websockets
	check_remote
	;;
*)
	echo "usage: $0 {validate|check|sync|sync-websockets}" >&2
	exit 2
	;;
esac
