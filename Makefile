.PHONY: run migrate test race vet build tidy

CHAT_DB_PATH ?= taskmanager.db

migrate:
	goose -dir migrations sqlite3 $(CHAT_DB_PATH) up

run:
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
