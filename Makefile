.PHONY: build pairqr test test-race vet e2e run apifox-validate apifox-check apifox-sync apifox-sync-websockets clean

build:
	go build -o bin/relay ./cmd/relay
	go build -o bin/relayctl ./cmd/relayctl
	go build -o bin/pairqr ./cmd/pairqr

pairqr:
	go build -o bin/pairqr ./cmd/pairqr

test:
	go test ./...

test-race:
	go test -race ./...

vet:
	go vet ./...

e2e:
	./scripts/e2e.sh

run: build
	./bin/relay

apifox-validate:
	.codex/skills/ai-coding-remote-apifox-sync/scripts/sync.sh validate

apifox-check:
	.codex/skills/ai-coding-remote-apifox-sync/scripts/sync.sh check

apifox-sync:
	.codex/skills/ai-coding-remote-apifox-sync/scripts/sync.sh sync

apifox-sync-websockets:
	.codex/skills/ai-coding-remote-apifox-sync/scripts/sync.sh sync-websockets

clean:
	go clean
	rm -f bin/relay bin/relayctl bin/pairqr
