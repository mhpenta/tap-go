# tinymcp

A skinny protocol for smart LLMs.

```
GET  /tools              → index (text/plain)
GET  /tools/{name}       → documentation (application/json)
POST /tools/{name}/run   → invoke (application/json or text/event-stream)
```

## Why

Tool protocols front-load every tool's full schema into the LLM's context window on every session — thousands of tokens before you've done anything. Most of those tools never get used.

tinymcp flips this. The agent starts with a lightweight index, fetches documentation only for the tools it actually needs, and invokes them over plain HTTP. Zero context cost until the moment a tool is needed.

## Endpoints

### `GET /tools`

Returns a plain-text index of available tools. One line per tool: `name: description`.

An optional server description may appear before the tool list, separated by a blank line.

```
XBRL Financial Data - Query SEC filings and normalized metrics

discover_companies: Search for companies by name or CIK
get_series: Get time series data for a company metric
get_financials: Get full financial statements
```

Content-Type: `text/plain`

The format is intentionally not JSON. It's compact, human-readable, and drops straight into a context window without parsing overhead.

### `GET /tools/{name}`

Returns documentation for a single tool. Describes what it does and what fields it accepts. Parameters follow [JSON Schema](https://json-schema.org/).

```json
{
  "name": "search",
  "description": "Search indexed research documents by keyword or semantic query.",
  "parameters": {
    "type": "object",
    "properties": {
      "query":  {"type": "string", "description": "Search query"},
      "limit":  {"type": "integer", "description": "Max results", "default": 10},
      "format": {"type": "string", "description": "\"short\" or \"full\"", "default": "short"}
    },
    "required": ["query"]
  }
}
```

Content-Type: `application/json`

### `POST /tools/{name}/run`

Invoke a tool. Request body is a JSON object with the tool's parameters as fields.

```
POST /tools/search/run
Content-Type: application/json

{"query": "attention mechanisms", "limit": 5}
```

The request body must be valid JSON. Use `{}` for tools with no required parameters.

#### Standard response

Content-Type: `application/json`

```json
{
  "result": "Found 5 results:\n\n1. Vaswani et al. 2017 - Attention Is All You Need..."
}
```

The `result` field contains the tool's return value. It can be a string, number, object, or array.

#### Streaming response

For long-running tools, the server may stream results back using [SSE framing](https://html.spec.whatwg.org/multipage/server-sent-events.html#parsing-an-event-stream) (`event:` / `data:` / blank line). The client signals it accepts streaming via the `Accept: text/event-stream` header. Servers that don't support streaming ignore this header and return a standard JSON response.

> **Note:** This uses the SSE *wire format* over a POST response — not the browser `EventSource` API, which is GET-only. tinymcp clients are programs, not browsers. Any HTTP client that can read a streaming response body handles this natively (Go, Python, Node, curl, etc.).

Content-Type: `text/event-stream`

Four event types:

| Event | Data | Purpose |
|---|---|---|
| `progress` | `{"progress": 0.0–1.0, "message": "..."}` | Optional progress updates |
| `result` | The tool's return value (same as `{"result": ...}.result`) | Final or incremental result |
| `error` | `{"code": "<code>", "message": "..."}` | Terminal error (see [Errors](#errors)) |
| `done` | `{}` | Terminal, stream finished successfully |

**Pattern 1 — Single result (most tools)**

```
event: result
data: "Found 5 results: ..."

event: done
data: {}

```

**Pattern 2 — Progress updates, then result**

```
event: progress
data: {"progress": 0.25, "message": "Scanning 2019 filings..."}

event: progress
data: {"progress": 0.75, "message": "Scanning 2023 filings..."}

event: result
data: {"series": [{"year": 2019, "revenue": 161857}, ...]}

event: done
data: {}

```

**Pattern 3 — Chunked / incremental results**

Each `result` event is one piece. The client collects them.

```
event: result
data: {"line": "2024-01-15T10:23:01Z ERROR connection timeout", "index": 0}

event: result
data: {"line": "2024-01-15T10:23:02Z ERROR retry failed", "index": 1}

event: result
data: {"line": "2024-01-15T10:23:05Z INFO connection restored", "index": 2}

event: done
data: {}

```

Rules:
- A stream ends with either a `done` event (success) or an `error` event (failure). These are mutually exclusive — exactly one is sent.
- If the connection drops without `done` or `error`, the stream was interrupted.
- `progress` events are optional and purely informational.
- Multiple `result` events mean incremental/chunked delivery — the client collects them into a list.
- Each `data` field is a valid JSON value (string, number, object, array).

## Auth

Optional. If the server requires authentication, it should accept the `x-api-key` header:

```
x-api-key: sk-abc123
```

Servers may also accept `Authorization: Bearer <token>` as an alternative. The spec does not mandate a specific auth mechanism — use whatever fits your deployment. The server should return `401` for missing or invalid credentials.

## Errors

All errors — HTTP responses and SSE `error` events — use the same JSON shape:

```json
{"code": "not_found", "message": "tool not found: nope"}
```

- `code` — a machine-readable error code (see table below)
- `message` — a human-readable description

### Error codes

| Code | HTTP Status | Meaning |
|---|---|---|
| `invalid_request` | `400` | Invalid JSON body, missing required field, or bad arguments |
| `unauthorized` | `401` | Missing or invalid credentials |
| `not_found` | `404` | Tool not found |
| `timeout` | `408` | Request or tool execution timed out |
| `rate_limited` | `429` | Too many requests |
| `execution_error` | `500` | Tool handler failed |
| `internal_server_error` | `500` | Server-level failure (not tool-specific) |

Tool handlers may return any of these codes to indicate specific failure modes. If a handler returns a plain error (no code), the server defaults to `execution_error`.

### Errors during streaming

For streaming responses, the HTTP status is already `200` by the time an error occurs. Errors are sent as an SSE `error` event with the same JSON shape:

```
event: error
data: {"code": "execution_error", "message": "database connection lost"}

```

An `error` event is terminal — no further events follow.

## Client Conventions

The intended usage pattern for LLM agents:

1. **Discover** — call `GET /tools` to get the lightweight index. This costs minimal tokens.
2. **Inspect** — when the agent decides to use a tool, call `GET /tools/{name}` to fetch its full schema.
3. **Invoke** — call `POST /tools/{name}/run` with the parameters.

This lazy-loading approach means the agent only pays context-window cost for tools it actually uses.

## Design Notes

- **No sessions, no handshakes, no capabilities negotiation.** Each request is independent.
- **No tool schema in the initial listing.** The index is intentionally minimal — the full schema is fetched on demand.
- **Plain HTTP.** Works with any language, any HTTP client, any reverse proxy.
- **SSE wire format for streaming** rather than WebSockets or custom framing. We use the `event:`/`data:` line protocol, not the browser `EventSource` API. One-directional, works over POST, works through proxies, trivial to implement in any language.
