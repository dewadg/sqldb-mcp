## Why

The dialect-level integration tests in `internal/db/postgres/integration_test.go` call `Dialect` methods directly, bypassing the MCP server, tool handlers, and registry resolution. They verify the dialect but not the path a client actually uses. A regression in `server.New`, tool registration, alias resolution, or the handler→dialect contract would slip past them. An end-to-end test that drives the real `*mcp.Server` through the SDK client covers the whole stack with the same effort and fewer tests to maintain.

## What Changes

- **Remove** `internal/db/postgres/integration_test.go` (and the now-unused `testenv_test.go` `.env` loader if nothing else needs it).
- **Add** an in-process e2e test package that builds the server via `server.New` with a real-Postgres `db.Config`, connects an MCP client over the SDK's in-memory transport, and asserts `tools/call` responses for the SQL surface.
- Keep the real-database guard: tests skip when `SQLDB_MCP_TEST_POSTGRES_URL` is unset, auto-loading repo-root `.env` as before.
- Cover the same behaviors the integration tests did, but through the tool boundary: `list_objects`, `get_object_details`, `execute_query` (read, row-cap via config, bind args, write rejected under `readonly`), `explain_query` (text/json/analyze), plus `list_databases` (which the old integration tests never exercised).

## Capabilities

### New Capabilities

<!-- None — this is a test-strategy change. -->

### Modified Capabilities

<!-- None — no product behavior changes; `skip_specs: true` set in .openspec.yaml. -->

## Non-goals

- Subprocess or real-stdio/HTTP transport testing. The e2e client uses the SDK in-memory transport (`mcp.NewInMemoryTransports`); `main()` flag/transport wiring is covered by existing unit/manual paths, not here.
- Removing or altering the unit tests (`internal/db`, `internal/config`, `internal/tools`, postgres builder golden tests) — those stay.
- Changing any production code. If the e2e test surfaces a wiring bug, that is fixed in scope; otherwise server/tools/config/db are untouched.
- Testing engines other than Postgres.

## Impact

- Delete: `internal/db/postgres/integration_test.go`.
- Add: a new e2e test (e.g. `tests/e2e/e2e_test.go`) plus a shared test helper for server+client bootstrap and `.env`/DSN loading (reuse the loader logic, relocated).
- No dependency changes. No changes to shipped code unless a wiring defect is found.
