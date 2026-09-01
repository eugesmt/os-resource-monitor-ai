.PHONY: run test race vet build tidy

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
