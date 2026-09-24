# Task Manager MCP Server

## Purpose

`taskmanager-mcp` is a separate, read-only MCP server for AI agents. It supports
both locally managed stdio and independently started Streamable HTTP transports.
It exposes the operating-system data already collected by Task Manager without
coupling an external agent to the application's AI pipeline, memory, profiles,
invariants, or task state machine.

## Architecture

```text
AI agent -- MCP stdio or Streamable HTTP --> taskmanager-mcp
                                                    |
                                                    | HTTP GET
                                                    v
                                             Task Manager API
                                              |--> /api/snapshot
                                              `--> /api/history
                                                       |
                                                       v
                                            Task Manager collector
```

The Task Manager HTTP server must already be running. The MCP process never
collects metrics itself and never changes application state.

## Tools

### `get_system_overview`

Returns a compact current snapshot without the process list.

Input:

```json
{}
```

Example result shape:

```json
{
  "snapshot_id": "snap-...",
  "captured_at": "2026-09-24T12:00:00Z",
  "hostname": "workstation",
  "cpu_cores": 8,
  "cpu_percent": 31.2,
  "memory_used": 8589934592,
  "memory_total": 17179869184,
  "memory_percent": 50,
  "missing_data": []
}
```

The complete result also includes per-core CPU, load, swap, disks, disk I/O,
network and GPU fields. It uses `GET /api/snapshot`.

### `get_top_processes`

Returns at most 20 processes ranked by CPU or memory. Equal metric values are
ordered by PID for deterministic results.

Input schema:

```json
{
  "sort_by": "cpu | memory",
  "limit": 1
}
```

Example call:

```json
{"sort_by":"cpu","limit":5}
```

Example result shape:

```json
{
  "snapshot_id": "snap-...",
  "captured_at": "2026-09-24T12:00:00Z",
  "sort_by": "cpu",
  "processes": [
    {"pid":1234,"name":"worker","cpu_percent":84.1,"mem_percent":2.4}
  ],
  "missing_data": []
}
```

It uses `GET /api/snapshot`. If `missing_data` contains `processes`, the tool
returns an explicit tool error rather than pretending that the process list is
empty.

### `get_resource_history`

Returns a compact time series and statistics for one metric. The allowed
metrics are `cpu`, `memory`, `load`, `network_receive`, `network_send`,
`disk_read`, and `disk_write`. `samples` must be from 1 through 120.

Example call:

```json
{"metric":"memory","samples":30}
```

Example result shape:

```json
{
  "metric": "memory",
  "unit": "percent",
  "points": [
    {"snapshot_id":"snap-1","captured_at":"2026-09-24T12:00:00Z","value":50}
  ],
  "minimum": 50,
  "maximum": 50,
  "average": 50,
  "missing_data": []
}
```

Mapping to the HTTP API:

| MCP metric | Snapshot field | Unit |
| --- | --- | --- |
| `cpu` | `cpu_percent` | `percent` |
| `memory` | `mem_percent` | `percent` |
| `load` | `load_1` | `load_average` |
| `network_receive` | `network.recv_per_sec` | `bytes_per_second` |
| `network_send` | `network.send_per_sec` | `bytes_per_second` |
| `disk_read` | `disk_io.read_per_sec` | `bytes_per_second` |
| `disk_write` | `disk_io.write_per_sec` | `bytes_per_second` |

It uses `GET /api/history?limit=<samples>`. Samples whose relevant section is
listed in `missing_data` are omitted from `points` and the section is reported
in the result. For an empty usable history, statistics are omitted rather than
reported as misleading zeroes.

### `get_metrics_summary`

Returns the durable aggregate produced from the collector's SQLite metric log
for either a relative lookback or an absolute RFC3339 range. The range must be
at least three minutes and at most seven days; future end times are rejected.

Use exactly one of these input forms:

```json
{"last_minutes":60}
```

or:

```json
{"from":"2026-09-24T10:00:00Z","to":"2026-09-24T12:00:00Z"}
```

The result contains nullable average/minimum/maximum aggregates for CPU,
memory, load averages, network rates and disk I/O rates, actual data coverage,
sample and rollup counts, missing sections, counter deltas when available, and
the generation timestamp. The Task Manager application persists samples and
materializes completed three-minute UTC buckets in the background; the MCP
process only reads the summary through `GET /api/metrics/summary`.

## Build

From the repository root:

```bash
go build -o /tmp/taskmanager-mcp ./cmd/taskmanager-mcp
```

The main application remains:

```bash
go run ./cmd/server
```

## Run

Start the Task Manager HTTP server first. Then choose one of the MCP transports.

### Independently started Streamable HTTP

Run the MCP process as a network service:

```bash
TASKMANAGER_MCP_TRANSPORT=http \
TASKMANAGER_MCP_ADDR=127.0.0.1:8090 \
TASKMANAGER_API_URL=http://127.0.0.1:8080 \
/tmp/taskmanager-mcp
```

The MCP endpoint is:

```text
http://127.0.0.1:8090/mcp
```

HTTP mode is stateless Streamable HTTP. The listener accepts only loopback
addresses because this version has no MCP authentication or TLS.

### Client-managed stdio

An MCP client can instead launch the binary itself. In this mode the server uses
newline-delimited JSON over stdin/stdout. stdout is reserved for MCP protocol
messages; diagnostics go to stderr.

```bash
TASKMANAGER_API_URL=http://127.0.0.1:8080 /tmp/taskmanager-mcp
```

For local stdio development:

```bash
TASKMANAGER_API_URL=http://127.0.0.1:8080 go run ./cmd/taskmanager-mcp
```

## Configuration

| Variable | Default | Purpose |
| --- | --- | --- |
| `TASKMANAGER_API_URL` | `http://127.0.0.1:8080` | Existing Task Manager HTTP API |
| `TASKMANAGER_MCP_TRANSPORT` | `stdio` | `stdio` or `http` |
| `TASKMANAGER_MCP_ADDR` | `127.0.0.1:8090` | Streamable HTTP listen address |

Only `http` and `https` Task Manager API URLs with a host are accepted; URL
user info, query strings and fragments are rejected. Requests and MCP request
bodies are bounded. `TASKMANAGER_MCP_ADDR` must use a loopback host.

## Agent configuration example

For Streamable HTTP, point the MCP client at:

```text
http://127.0.0.1:8090/mcp
```

The exact configuration key depends on the MCP host. For stdio, an MCP client
can use an absolute binary path:

```json
{
  "mcpServers": {
    "taskmanager": {
      "command": "/tmp/taskmanager-mcp",
      "env": {
        "TASKMANAGER_API_URL": "http://127.0.0.1:8080"
      }
    }
  }
}
```

## Testing

Automated Streamable HTTP protocol coverage:

```bash
go test ./internal/mcpserver -run '^TestStreamableHTTP' -v
go test ./...
```

For a live test, first run `cmd/server` and the Streamable HTTP MCP process as
shown above. Then use the official MCP Inspector CLI (Node.js 22.19 or newer):

```bash
npx @modelcontextprotocol/inspector --cli \
  --server-url http://127.0.0.1:8090/mcp \
  --transport http \
  --method tools/list
```

Call the process tool:

```bash
npx @modelcontextprotocol/inspector --cli \
  --server-url http://127.0.0.1:8090/mcp \
  --transport http \
  --method tools/call \
  --tool-name get_top_processes \
  --tool-arg sort_by=cpu \
  --tool-arg limit=5
```

Other useful calls:

```bash
npx @modelcontextprotocol/inspector --cli \
  --server-url http://127.0.0.1:8090/mcp \
  --transport http \
  --method tools/call \
  --tool-name get_system_overview

npx @modelcontextprotocol/inspector --cli \
  --server-url http://127.0.0.1:8090/mcp \
  --transport http \
  --method tools/call \
  --tool-name get_resource_history \
  --tool-arg metric=cpu \
  --tool-arg samples=10
```

The Inspector validates the MCP handshake and tool protocol but is not an AI
agent. For the final product demonstration, configure an MCP-capable agent with
the same URL and ask it a question that requires one of these tools.

## Demonstration scenario

User:

> Какие процессы сейчас больше всего нагружают CPU?

Agent tool call:

```json
{"name":"get_top_processes","arguments":{"sort_by":"cpu","limit":5}}
```

The agent then summarizes the structured `processes` result, including the
snapshot timestamp and any reported missing data.

## Errors

Tool errors are returned when the API is unavailable, returns a non-200 status,
contains invalid or oversized JSON, a request is cancelled, input validation
fails, or the selected metric is unavailable. Upstream response bodies are not
copied into error messages.

## Security and limitations

- MCP is read-only and cannot execute commands, terminate processes, or alter the application.
- It exposes no AI history, memory, profile, invariant, or task-management tools.
- The default HTTP endpoint is local and has no authentication; do not publish it or the MCP process outside a trusted boundary without adding separate access control.
- Streamable HTTP refuses non-loopback listen addresses; remote access requires a future authenticated and TLS-protected deployment design.
- The MCP process depends on a running `cmd/server` instance.
- `missing_data` distinguishes unavailable measurements from valid zero values.
- The API client accepts only configured HTTP(S) base URLs and does not accept arbitrary per-tool URLs.
