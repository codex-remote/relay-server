#!/usr/bin/env bash

if [[ -z "${BASH_VERSION:-}" ]]; then
	exec bash "$0" "$@"
fi

set -euo pipefail

repo_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
binary="${repo_dir}/bin/pairqr"

find_go() {
	local candidate
	if command -v go >/dev/null 2>&1; then
		command -v go
		return 0
	fi
	for candidate in \
		/opt/homebrew/opt/go@1.24/bin/go \
		/opt/homebrew/bin/go \
		/usr/local/bin/go \
		/usr/local/go/bin/go; do
		if [[ -x "${candidate}" ]]; then
			printf '%s\n' "${candidate}"
			return 0
		fi
	done
	return 1
}

needs_build=0
if [[ ! -x "${binary}" || "${repo_dir}/cmd/pairqr/main.go" -nt "${binary}" || "${repo_dir}/go.mod" -nt "${binary}" ]]; then
	needs_build=1
fi

if [[ "${needs_build}" -eq 1 ]]; then
	go_binary="$(find_go || true)"
	if [[ -z "${go_binary}" ]]; then
		printf 'pairqr needs to be rebuilt, but Go was not found. Run devrun crweb or install Go.\n' >&2
		exit 1
	fi
	mkdir -p "${repo_dir}/bin"
	(cd "${repo_dir}" && "${go_binary}" build -o "${binary}" ./cmd/pairqr)
fi

case "${1:-}" in
	-h|--help)
		exec "${binary}" "$@"
		;;
esac

if ! curl -fsS --max-time 3 http://127.0.0.1:18774/gateway/healthz >/dev/null 2>&1; then
	printf 'Mobile Web Gateway is unavailable. Run devrun crweb first.\n' >&2
	exit 1
fi

exec "${binary}" "$@"
