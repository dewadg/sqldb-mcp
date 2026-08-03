## Context

See `proposal.md` for motivation. The go-sdk (`github.com/modelcontextprotocol/go-sdk` v1.7.0) exposes `mcp.NewInMemoryTransports()`, returning two transports backed by `net.Pipe` — one for the client, one for the server. The server runs via `*mcp.Server.Run(ctx, transport)`; a client session is created with `client.Connect(ctx, transport, opts)` and tool calls go through `ClientSession.CallTool(ctx, &CallToolParams{Name, Arguments})`, returning `*CallToolResult` whose `Content` holds the structured output as JSON text. `server.New` already builds a fully wired `*mcp.Server` + `*db.Registry` from a `*config.Config` and `*db.Config`, so the e2e harness reuses it directly with a real-Postgres config.

## Goals / Non-Goals

**Goals:**

- One e2e test package exercising server → tool registration → registry resolution → dialect → real Postgres through the MCP `tools/call` boundary.
- Same DB guard as before: skip on unset `SQLDB_MCP_TEST_POSTGRES_URL`, auto-load repo-root `.env`.
- Keep the golang-unit-test table-driven shape for the per-tool scenarios.

**Non-Goals:**

- Real transport (stdio/HTTP) or subprocess tests.
- Touching production code unless a wiring defect surfaces.
- Dropping the unit/builder tests.

## Decisions

### D1. In-memory transport, not subprocess

Use `mcp.NewInMemoryTransports()`: hand one end to `srv.Run` in a goroutine, the other to `client.Connect`. The server is built by `server.New`, so registration/Instructions/registry lifecycle are real — only the OS transport is virtual.

- *Why over subprocess+stdio:* deterministic, no process teardown races, no JSON-RPC-over-pipes parsing on our side; still exercises the full in-process tool→DB path the integration tests missed.
- *What it does NOT cover:* `main.go` flag parsing and the stdio/HTTP transport selection. That wiring is thin and unchanged; left to manual/`run` checks.

### D2. Build the server with a real Postgres `db.Config`

The harness constructs a `db.Config` from `SQLDB_MCP_TEST_POSTGRES_URL` (alias `primary`, `driver: postgres`, plus a scratch schema fixture for deterministic object names), calls `server.New(&config.Config{...}, &dbCfg, logger)`, and holds the returned registry for `Close` in cleanup. The fixture (scratch schema `sqldb_mcp_test`, table `t_item`, seeded rows) is created over the same DSN with a raw `*sql.DB` and dropped in `t.Cleanup` — mirroring the deleted integration setup.

- *Config values:* use a small `row_limit` (e.g. 10) and seed >limit rows so the row-cap scenario is observable through `execute_query`'s returned row count without needing 250 rows.

### D3. Assert on the structured `tools/call` result

Typed handlers serialize their `Out` struct as JSON text content. The harness unmarshals the first text content block into the matching tool output struct (e.g. `tools.ExecuteQueryOutput`) and asserts fields. Error paths assert `CallToolResult.IsError` (or a non-nil `CallTool` error) and the error text.

- *Why parse JSON not raw text:* keeps assertions on structured fields (column names, row counts, plan output) and matches how a real client consumes results.

### D4. Relocate the `.env` loader

The `.env` loader currently lives in `internal/db/postgres/testenv_test.go`. Since that package's integration tests are deleted, move the loader into the e2e test package so `go test ./tests/e2e/...` works without a manual `export`. Same semantics: keys not already set, quotes stripped, walk up to repo root.

## Risks / Trade-offs

- **In-memory transport hides transport bugs** → accepted; transport is thin SDK plumbing, and the conformance/SDK tests cover it. D1 notes the gap explicitly.
- **Server `Run` goroutine lifecycle** → the harness must cancel the context (and let `Run` return) before the test exits; `t.Cleanup` cancels and the registry `Close` follows. If `Run` leaks, `go test -race` will surface it.
- **Fixture collisions on parallel runs** → the scratch schema name is fixed (`sqldb_mcp_test`); tests must not run `-parallel` against the same DB. Documented; the suite is not parallel.

## Migration Plan

1. Add `tests/e2e` package: bootstrap helper (server+client+fixture) and `.env` loader.
2. Port each integration scenario to a `tools/call`-driven table test.
3. Delete `internal/db/postgres/integration_test.go` and `testenv_test.go`.
4. `go test ./...` — unit + e2e green; e2e skips when the DSN is unset.
5. Rollback = revert; the deleted integration tests return and the e2e package is gone.

## Open Questions

None — approach (in-memory client), DB guard, and scope (remove integration tests, keep units) are settled.
