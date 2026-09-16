.PHONY: build test lint check

build:
	mkdir -p build
	GOWORK=off go build -mod=readonly -o build/taskchain-task-manager ./cmd/taskchain-task-manager

test:
	GOWORK=off go test -mod=readonly -race ./...

lint:
	GOWORK=off go vet -mod=readonly ./...
	test -z "$$(gofmt -l cmd internal)"

check: lint test build
