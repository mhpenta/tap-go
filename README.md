# TAP (Tool API Pattern)

A tiny HTTP pattern for tool discovery and execution.

This repository is the Go implementation:
- module path: `github.com/mhpenta/tap-go`
- current package name: `tinymcp` (historical name, protocol name is TAP)

## Protocol

```text
GET  /tools              -> index (text/plain)
GET  /tools/{name}       -> tool doc (application/json)
POST /tools/{name}/run   -> invoke (application/json or text/event-stream)
```

## Behavior In This Tooling

- `GET /tools` returns one `name: description` per line, with an optional server description before a blank line.
- `GET /tools/{name}` returns `{name, description, parameters}` JSON.
- `POST /tools/{name}/run` requires valid JSON. For no-arg tools, send `{}`.
- Standard invoke responses are `{ "result": <any json value> }`.
- Streaming uses SSE framing (`event:` + `data:` + blank line), with events: `progress`, `result`, `error`, `done`.
- If `Accept: text/event-stream` is sent but the tool has no stream handler, the server returns normal JSON (the Go client normalizes this to `result` then `done` in `RunStream`).
- Default max request body is `1 MiB` (`server.WithMaxRequestBodyBytes` to override).
- Auth is middleware-driven on server side. The client supports `x-api-key` via `client.WithAPIKey(...)`.

## Client-Side JQ Directive

The TAP Go client supports a client-only `_jq` directive on `Run`:

```json
{"query":"Vonnegut","_jq":"[.[] | {title, year}]"}
```

`_jq` is stripped before the request is sent. The server receives only tool args.  
`_jq` currently applies to `Run` (not `RunStream`).

## Errors

All protocol errors use:

```json
{"code":"not_found","message":"tool not found: nope"}
```

Canonical codes: `invalid_request`, `unauthorized`, `not_found`, `timeout`, `rate_limited`, `execution_error`, `internal_server_error`.

## Full Spec

See [SPEC.md](./SPEC.md) for the complete TAP specification and wire-format details.
