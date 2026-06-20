.DEFAULT_GOAL := run

.PHONY: run dev build test vet check

run:
	go run ./cmd/lazycaddy

dev: run

build:
	go build ./cmd/lazycaddy

test:
	go test ./...

vet:
	go vet ./...

check: vet test build
