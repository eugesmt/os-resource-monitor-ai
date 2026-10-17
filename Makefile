.PHONY: tools run db-up db-status build test race vet tidy

GOOSE_VERSION ?= v3.28.0
DB_PATH ?= system-monitor-context.db
GO_BUILD_FLAGS ?= -trimpath
GO_LDFLAGS ?= -s -w

tools: bin/goose

bin/goose:
	mkdir -p bin
	GOBIN="$(CURDIR)/bin" go install github.com/pressly/goose/v3/cmd/goose@$(GOOSE_VERSION)

db-up: tools
	"$(CURDIR)/bin/goose" -dir migrations sqlite3 "$(DB_PATH)" up

db-status: tools
	"$(CURDIR)/bin/goose" -dir migrations sqlite3 "$(DB_PATH)" status

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
	mkdir -p bin
	go build $(GO_BUILD_FLAGS) -ldflags "$(GO_LDFLAGS)" -o bin/system-monitor-ai ./cmd/server
