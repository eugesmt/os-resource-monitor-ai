.PHONY: run db-up test race vet build tidy

CONTEXT_DB_PATH ?= taskmanager-context.db
export CONTEXT_DB_PATH

db-up:
	goose -dir migrations sqlite3 "$(CONTEXT_DB_PATH)" up

run: db-up
	go run ./cmd/server

tidy:
	go mod tidy

test:
	go test ./...

race:
	go test -race ./...

vet:
	go vet ./...

build:
	go build -o bin/taskmanager-ai ./cmd/server
