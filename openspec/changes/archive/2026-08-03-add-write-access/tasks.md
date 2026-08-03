# Tasks

## 1. Routing + execution — `internal/db/postgres`

- [x] 1.1 Add `isRowReturning(sql string) bool`: trim leading whitespace and leading SQL comments (`/* */`, `--`), return true when the first token (uppercased) is one of `SELECT`, `WITH`, `VALUES`, `TABLE`
- [x] 1.2 Rewrite `ExecQuery`: open `BeginTx(ReadOnly: cfg.Readonly)`; on the row path run `QueryContext`, cap rows at `RowLimit`, then `Rollback`; on the exec path run `ExecContext`, `Commit` on success when unrestricted, `Rollback` on error or when restricted
- [x] 1.3 Populate the affected row count on the exec path from `sql.Result.RowsAffected()`; leave it zero on the row path
- [x] 1.4 Golden unit test for `isRowReturning`: SELECT, WITH, VALUES, TABLE, leading comments, INSERT/UPDATE/DELETE/CREATE, whitespace/paren edge cases

## 2. Result shape — `internal/db`, `internal/tools`

- [x] 2.1 Add `RowsAffected int64` (json `rows_affected`) to `db.QueryResult`
- [x] 2.2 Confirm `ExecuteQueryOutput` (embeds `*db.QueryResult`) surfaces `rows_affected` with no handler change; add a unit assertion if needed

## 3. Tests — `tests/e2e`

- [x] 3.1 Add a write-succeeds case under `readonly: false`: `INSERT INTO sqldb_mcp_test.t_item(id) VALUES ($1)` → assert `rows_affected == 1` and that the row is durable (re-select sees it)
- [x] 3.2 Add a write-rollback-on-error case under `readonly: false`: insert a duplicate PK → assert error and that the table is unchanged
- [x] 3.3 Keep the existing rejected-write case under `readonly: true` (DELETE → error result)
- [x] 3.4 Assert a SELECT under `readonly: false` still returns rows and rolls back (no persistence)

## 4. Documentation

- [x] 4.1 Update README "What your assistant can do" / safety note: writes are real and durable when `readonly: false`; SELECTs stay rolled back; statement kind is routed by a keyword sniff, not parsed
- [x] 4.2 Note `rows_affected` in the `execute_query` tool description and the example yaml comment if helpful
