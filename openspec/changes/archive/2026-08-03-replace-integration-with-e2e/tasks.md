# Tasks

## 1. E2E harness — `tests/e2e`

- [x] 1.1 Create `tests/e2e` package with a `.env` loader (relocated from `internal/db/postgres/testenv_test.go`): walk up to repo root, parse `KEY=VALUE`/`KEY='VALUE'`, strip quotes, set env only when unset
- [x] 1.2 Add a DSN helper: load `.env`, read `SQLDB_MCP_TEST_POSTGRES_URL`, `t.Skip` with a clear message when unset
- [x] 1.3 Add a fixture helper: open a raw `*sql.DB` over the DSN, idempotently create scratch schema `sqldb_mcp_test` + `t_item(id INT PK, label TEXT NOT NULL DEFAULT 'x')`, seed N rows, `DROP SCHEMA ... CASCADE` in `t.Cleanup`
- [x] 1.4 Add a server+client bootstrap helper: build `db.Config` (alias `primary`, `driver: postgres`, small `row_limit`) from the DSN, call `server.New`, wire `mcp.NewInMemoryTransports`, run `srv.Run` in a goroutine, `client.Connect`, return `(*mcp.ClientSession, cleanup)`; cleanup cancels context, waits `Run`, and `reg.Close()`
- [x] 1.5 Add a `callTool` helper wrapping `ClientSession.CallTool` that returns the first text content block (for JSON parsing) and surfaces `IsError`/error

## 2. E2E scenarios (table-driven, `tools/call` boundary)

- [x] 2.1 `list_databases`: returns `primary` (postgres, readonly); assert no `url`/credential in the serialized result
- [x] 2.2 `list_objects`: scratch table present (type `table`); unsupported `type` returns an error result
- [x] 2.3 `get_object_details`: columns (id not-null), a primary-key constraint, at least one index
- [x] 2.4 `execute_query` read: `SELECT` returns seeded rows; bind-param query (`WHERE id = $1`) returns exactly the bound row
- [x] 2.5 `execute_query` row-cap: with the configured small `row_limit`, a query matching more rows returns at most that many
- [x] 2.6 `execute_query` rejected write: under `readonly: true`, `DELETE` returns an error result containing the read-only message; assert the table row count is unchanged
- [x] 2.7 `explain_query`: text plan non-empty; `format: json` output parses as JSON; `analyze: true` returns a plan under the read-only tx

## 3. Removal + cleanup

- [x] 3.1 Delete `internal/db/postgres/integration_test.go`
- [x] 3.2 Delete `internal/db/postgres/testenv_test.go` (loader relocated to `tests/e2e`)
- [x] 3.3 Run `go test ./...` — unit tests pass, e2e passes against the real DB (or skips when DSN unset); `go vet ./...` clean
- [x] 3.4 Update `README.md` test section: integration-test reference → e2e test reference (`go test ./tests/e2e/...`, same `SQLDB_MCP_TEST_POSTGRES_URL` guard)
