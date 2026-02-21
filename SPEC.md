# TAP (Tool API Pattern)

A small HTTP protocol for tool discovery and invocation by LLM agents.

This repository is the Go reference implementation (`github.com/mhpenta/tap-go`).  
Note: the Go package name is currently `tinymcp` for compatibility; protocol name is TAP.

```text
GET  /tools              -> index (text/plain)
GET  /tools/{name}       -> documentation (application/json)
POST /tools/{name}/run   -> invoke (application/json or text/event-stream)
```

## Why

Many tool protocols front-load every tool schema into context up front. TAP keeps startup cheap:
1. Discover a compact index.
2. Fetch docs only for the tool you want.
3. Invoke it.

## Endpoints

### `GET /tools`

Returns a plain-text index with one tool per line:

```text
name: description
```

An optional server description may appear first, followed by a blank line.

```text
XBRL Financial Data - Query SEC filings and normalized metrics

discover_companies: Search for companies by name or CIK
get_series: Get time series data for a company metric
get_financials: Get full financial statements
```

Content-Type: `text/plain`

### `GET /tools/{name}`

Returns tool documentation:

```json
{
  "name": "search",
  "description": "Search indexed documents.",
  "parameters": {
    "type": "object",
    "properties": {
      "query": {"type": "string"},
      "limit": {"type": "integer", "default": 10}
    },
    "required": ["query"]
  }
}
```

`parameters` follows JSON Schema conventions.

Content-Type: `application/json`

### `POST /tools/{name}/run`

Invokes a tool.

```text
POST /tools/search/run
Content-Type: application/json

{"query":"attention mechanisms","limit":5}
```

Request body must be valid JSON.  
Recommended shape is a JSON object. Use `{}` for no-arg tools.

#### Standard response

Content-Type: `application/json`

```json
{"result":"Found 5 results"}
```

`result` can be any JSON value.

#### Streaming response

If the client sends `Accept: text/event-stream` and the tool supports streaming, the response uses SSE wire framing over the POST response body.

Content-Type: `text/event-stream`

Event types:

| Event | Data | Purpose |
|---|---|---|
| `progress` | `{"progress": 0.0-1.0, "message": "..."}` | Optional progress updates |
| `result` | Any JSON value | Final or incremental results |
| `error` | `{"code":"<code>","message":"..."}` | Terminal failure |
| `done` | `{}` | Terminal success |

Examples:

```text
event: progress
data: {"progress":0.5,"message":"halfway"}

event: result
data: {"answer":42}

event: done
data: {}
```

```text
event: result
data: {"chunk":1}

event: result
data: {"chunk":2}

event: done
data: {}
```

Stream rules:
- Terminal event is exactly one of `done` or `error`.
- If the connection closes without either terminal event, the stream is interrupted.
- `progress` is optional and informational.
- Multiple `result` events mean incremental delivery.

If streaming is requested but unsupported by the tool/server, returning normal JSON is valid fallback behavior.

## Auth

TAP does not mandate auth mechanism. Common choices:
- `x-api-key: <key>`
- `Authorization: Bearer <token>`

Missing/invalid credentials should return `401`.

## Errors

HTTP error bodies and SSE `error` events share this shape:

```json
{"code":"not_found","message":"tool not found: nope"}
```

| Code | HTTP Status | Meaning |
|---|---|---|
| `invalid_request` | `400` | Invalid JSON body, bad arguments, or malformed request |
| `unauthorized` | `401` | Missing or invalid credentials |
| `not_found` | `404` | Tool not found |
| `timeout` | `408` | Request or tool execution timed out |
| `rate_limited` | `429` | Too many requests |
| `execution_error` | `500` | Tool handler failed |
| `internal_server_error` | `500` | Server-level failure |

If a handler returns a plain error, implementations may normalize it to `execution_error`.

## Client Conventions

Recommended agent flow:

1. Discover with `GET /tools`.
2. Inspect with `GET /tools/{name}` only when needed.
3. Invoke with `POST /tools/{name}/run`.

## Reference Go Tooling Notes

The current `tap-go` implementation adds these practical conventions:
- Client methods are context-first: `List`, `Get`, `Run`, `RunStream`.
- `client.WithAPIKey(...)` sets `x-api-key`.
- `server.WithMaxRequestBodyBytes(...)` controls max request size (default `1 MiB`).
- `RunStream` gracefully handles non-stream JSON fallback by emitting `result` then `done`.
- `Run` supports a client-only `_jq` directive (removed before request); `_jq` is not applied in `RunStream`.

## Design Notes

- No sessions or handshakes.
- Index stays small; schema fetched on demand.
- Plain HTTP.
- SSE line protocol over POST for streaming.
