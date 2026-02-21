# Context Tool Lookup Protocol (CTLP)

*Aka "Tiny MCP"*

An API pattern for tool use with remote servers, inspired by the simplicity of agentic CLI use.

## Why

Large language models in an agentic loop are trained and designed to work well with CLI-style calls, which can be combined with a filtering mechanism like jq. The goal is to allow an LLM to precisely add information to context with an eye on token efficiency from remote servers — without having to tailor a specific local CLI for the task.

Instead of including all tools within the agent's context, we include a directory reference which the agent can later access to discover more information.

## Specification

```
GET  /tools              → index (text/plain)
GET  /tools/{name}       → documentation (application/json)
POST /tools/{name}/run   → invoke (application/json or text/event-stream)
```

### `GET /tools`

Returns a plain-text index of available tools. One line per tool: `name: description`.

An optional server description may appear before the tool list, separated by a blank line.

```
Book Database - Search and browse classic literature

search: Find books by title, author, or keyword
get_book: Get details for a specific book
get_reviews: Get reader reviews for a book
```

### `GET /tools/{name}`

Returns documentation for a single tool as JSON. Parameters follow [JSON Schema](https://json-schema.org/).

```json
{
  "name": "search",
  "description": "Find books by title, author, or keyword.",
  "parameters": {
    "type": "object",
    "properties": {
      "query":  {"type": "string", "description": "Search query"},
      "limit":  {"type": "integer", "description": "Max results", "default": 10}
    },
    "required": ["query"]
  }
}
```

### `POST /tools/{name}/run`

Invoke a tool. Request body is a JSON object with the tool's parameters as fields.

```
POST /tools/search/run
Content-Type: application/json

{"query": "Vonnegut", "limit": 3}
```

#### Standard response

```json
{"result": [{"title": "Cat's Cradle", "author": "Kurt Vonnegut", "year": 1963}, {"title": "Slaughterhouse-Five", "author": "Kurt Vonnegut", "year": 1969}, {"title": "Breakfast of Champions", "author": "Kurt Vonnegut", "year": 1973}]}
```

The `result` field contains the tool's return value — string, number, object, or array.

#### Streaming response

For long-running tools, the server may stream results using SSE framing (`event:` / `data:` / blank line). The client signals it accepts streaming via `Accept: text/event-stream`. Servers that don't support streaming ignore this header and return a standard JSON response.

**Pattern 1 — Single result (most tools)**

```
event: result
data: {"title": "The Hitchhiker's Guide to the Galaxy", "author": "Douglas Adams", "year": 1979}

event: done
data: {}

```

**Pattern 2 — Progress updates, then result**

```
event: progress
data: {"progress": 0.25, "message": "Searching titles..."}

event: progress
data: {"progress": 0.75, "message": "Ranking results..."}

event: result
data: [{"title": "Good Omens", "author": "Terry Pratchett & Neil Gaiman", "year": 1990}, {"title": "Catch-22", "author": "Joseph Heller", "year": 1961}]

event: done
data: {}

```

**Pattern 3 — Chunked / incremental results**

Each `result` event is one piece. The client collects them.

```
event: result
data: {"title": "The Princess Bride", "author": "William Goldman", "year": 1973}

event: result
data: {"title": "Hitchhiker's Guide to the Galaxy", "author": "Douglas Adams", "year": 1979}

event: result
data: {"title": "Lamb", "author": "Christopher Moore", "year": 2002}

event: done
data: {}

```

Stream rules:
- A stream ends with either `done` (success) or `error` (failure). Exactly one is sent.
- If the connection drops without either, the stream was interrupted.
- `progress` events are optional and informational.
- Multiple `result` events mean incremental delivery — the client collects them into a list.

## Authentication

For now, authentication is handled by `x-api-key` or bearer tokens:

```
x-api-key: sk-abc123
```

Servers return `401` for missing or invalid credentials.

## Client-Side Filtering

The CTLP client can filter the server response before it reaches the LLM using jq. This is a client-side directive — the server never sees it.

Pass `_jq` in the tool arguments. The client strips it before calling the server, then applies the filter to the response:

```json
{"query": "Vonnegut", "_jq": "[.[] | {title, year}]"}
```

The server receives `{"query": "Vonnegut"}`. The client applies the jq filter to the result.

In the Go client, all operations are context-first (`List`, `Get`, `Run`, `RunStream` all take `context.Context`).

## Errors

All errors use the same JSON shape:

```json
{"code": "not_found", "message": "tool not found: nope"}
```

| Code | HTTP Status | Meaning |
|---|---|---|
| `invalid_request` | `400` | Invalid JSON body, missing required field, or bad arguments |
| `unauthorized` | `401` | Missing or invalid credentials |
| `not_found` | `404` | Tool not found |
| `timeout` | `408` | Request or tool execution timed out |
| `rate_limited` | `429` | Too many requests |
| `execution_error` | `500` | Tool handler failed |
| `internal_server_error` | `500` | Server-level failure |

During streaming, errors are sent as an SSE `error` event (the HTTP status is already `200`):

```
event: error
data: {"code": "execution_error", "message": "database connection lost"}

```

## Design Notes

- **No sessions, no handshakes, no capabilities negotiation.** Each request is independent.
- **No tool schema in the initial listing.** The index is minimal — full schema is fetched on demand.
- **Plain HTTP.** Works with any language, any HTTP client, any reverse proxy.
- **SSE wire format for streaming** rather than WebSockets or custom framing.

## See Also

- [SPEC.md](./SPEC.md) — Full protocol specification
