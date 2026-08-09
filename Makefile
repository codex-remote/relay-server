.PHONY: build test test-race vet e2e run clean

build:
	go build -o bin/relay ./cmd/relay
	go build -o bin/relayctl ./cmd/relayctl

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

clean:
	go clean
	rm -f bin/relay bin/relayctl
