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
export AI_CONTEXT_WINDOW_TOKENS=128000
export AI_INPUT_PRICE_PER_MILLION_TOKENS=0.14
export AI_OUTPUT_PRICE_PER_MILLION_TOKENS=0.28
```

Metrics variables are optional. Context status is `unknown` when the model
window is not configured; prices are used only when both rates are present.
The application loads variables from `.env` when present. Variables already
set in the process environment take precedence.

## API

```text
GET  /api/health
GET  /api/snapshot
GET  /api/history?limit=120
POST /api/ai/ask
GET  /api/ai/history
GET  /api/events       # SSE live snapshots
```

`POST /api/ai/ask` keeps the existing `answer` object and adds `usage` with
estimated `current_request_tokens`, `history_tokens`, `system_prompt_tokens`
and `estimated_input_tokens`, provider-reported nullable `input_tokens`,
`output_tokens`, `total_tokens`, optional `estimated_cost_usd`,
`component_token_count="estimated"`, `usage_available`,
`usage_inconsistent`, `context_status`, and `history_truncated`. The history
endpoint returns the same usage object on persisted assistant messages; legacy
messages have no usage metadata.

The application sends the complete dialogue history on every request. It does
not prune, summarize, truncate, or clear history automatically. Therefore
`history_truncated=false` describes application behavior, not undocumented
provider internals. A context rejection is returned as HTTP 422 with a safe
diagnostic and does not create a failed chat turn. `finish_reason=length` is a
separate output-truncation outcome.

Each started provider request emits one structured `AI model call` info event.
Events contain call id, model, outcome, duration, estimates, exact usage when
available, context status, and no prompt or response content.

## Manual token-usage protocol

Use a mock OpenAI-compatible endpoint when checking these cases; do not point
tests at the real provider.

1. Short dialogue: make one request and verify the core usage fields in the UI
   and structured log, including a small history estimate.
2. Long dialogue: make several requests and verify that every message remains
   in order and history/input estimates grow without application truncation.
3. Near limit: set a valid `AI_CONTEXT_WINDOW_TOKENS`, grow the history until
   the UI shows `approaching_limit` and a visible warning; the request is not
   rejected locally.
4. Exceeded: continue until the mock returns compatible context overflow; the
   API responds 422 with `context_window_exceeded`, the UI explains that full
   history was sent, and no failed turn is stored.
5. Return `finish_reason=length` with usage and verify output truncation is
   reported separately from context overflow.

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
