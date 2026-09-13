# Task Manager AI — Web

Локальный web-based диспетчер задач с AI-ассистентом. Go собирает системные метрики через `gopsutil`, браузер получает live snapshots через Server-Sent Events (SSE), история хранится в потокобезопасном кольцевом буфере, AI работает через DeepSeek OpenAI-compatible API.

## Стек

- Go 1.27+
- `github.com/shirou/gopsutil/v3`
- `github.com/sashabaranov/go-openai`
- `net/http` + SSE
- SQLite via `modernc.org/sqlite`, schema managed with Goose v3
- HTML/CSS/Vanilla JS

## Запуск

```bash
go mod tidy
go test ./...
go test -race ./...
go vet ./...
go run ./cmd/server
```

`make run` applies pending Goose migrations to `taskmanager.db` before starting
the server. To apply or inspect migrations separately:

```bash
make migrate CHAT_DB_PATH=taskmanager.db
goose -dir migrations sqlite3 taskmanager.db status
```

Затем открыть `http://127.0.0.1:8080`.

Для AI задайте:

```bash
export DEEPSEEK_API_KEY=your_key
```

Опционально:

```bash
export ADDR=127.0.0.1:8080
export COLLECTOR_INTERVAL_MS=2000
export HISTORY_SIZE=300
export MAX_PROCESSES=100
export DEEPSEEK_MODEL=
export DEEPSEEK_BASE_URL=https://api.deepseek.com
export CHAT_DB_PATH=taskmanager.db
```

## API

```text
GET  /api/health
GET  /api/snapshot
GET  /api/history?limit=120
POST /api/ai/ask
GET  /api/ai/history
GET  /api/events       # SSE live snapshots
```

## Архитектура

```text
Browser
  │
  ├── REST /api/snapshot
  ├── REST /api/history
  ├── REST /api/ai/ask
  └── SSE  /api/events
          │
          ▼
      Go server
       ├── collector (gopsutil)
       ├── ring buffer
       └── AI agent → DeepSeek client
```

API key не передаётся в браузер и не встраивается в frontend. Без `DEEPSEEK_API_KEY` мониторинг работает, AI-чат сообщает об отсутствии конфигурации.
