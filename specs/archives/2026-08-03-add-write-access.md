# Add write access

> Make `readonly: false` actually grant durable write access through `execute_query` — route statements by kind, commit writes when unrestricted, report affected row count — while `readonly: true` stays the hard safety wall.

## Context

`readonly: false` is already a documented, specced opt-in, and the `query-execution` spec claims writes report an affected row count. But the implementation cannot fulfill it: `execute_query` always runs the SQL as a row-returning statement (`QueryContext`) and always rolls back the transaction, so an `INSERT`/`UPDATE`/`DELETE`/DDL either errors at the driver level or is silently undone. The feature is advertised but not real. This change makes write access work, gated by the existing `readonly` flag.

Today `postgres.ExecQuery` always calls `tx.QueryContext` and always rolls back — so writes neither execute correctly (a command like `INSERT` is not row-returning) nor persist. The existing design (change `add-sql-database-tools`, D3) deliberately rejected a full SQL parser; the read-only transaction is the sole safety boundary. This change keeps that boundary and adds only a lightweight statement-kind routing so writes can run on the exec path and commit when permitted.

## Goals
- Make `readonly: false` actually grant durable write access through `execute_query`.
- Preserve the read-only guarantee exactly: `readonly: true` rejects every write before commit.
- Keep reads (row-returning statements) rolled back and capped, unchanged.

## Non-goals
- A full SQL parser or an AST-based statement allowlist. Statement kind is decided by a lightweight leading-keyword sniff used only for **routing**, never as a security control — the read-only transaction remains the only safety boundary, exactly as the existing design decided.
- Returning rows from `INSERT ... RETURNING` / `UPDATE ... RETURNING`. Such statements are treated as writes: they commit and report the affected count; the `RETURNING` data set is not surfaced. (Can be revisited later.)
- `EXPLAIN ... ANALYZE` committing writes. `explain_query` keeps its current always-rollback behavior; it plans, it does not persist.
- Per-statement or per-table write permissions, auditing, or rate limiting.

## Decisions

### D1: Route by a leading-keyword sniff, not by try-query-then-exec

Add `isRowReturning(sql string) bool` that trims leading whitespace and SQL comments (`/* */`, `--`), reads the first token, and returns true for Postgres row-returning leaders: `SELECT`, `WITH`, `VALUES`, `TABLE`. Everything else routes to the exec path.

- **Why over try-`QueryContext`-then-`ExecContext` on error:** a failed `QueryContext` for a write has already executed the statement server-side; re-running it via `ExecContext` would double-execute (duplicate inserts, double decrements). Sniffing first picks one path and runs the statement exactly once.
- **Why this is not the rejected "parser":** the original design rejected a pglast AST allowlist used as a *security* control. This sniff is a routing heuristic only — `readonly: true` still rejects writes at the transaction regardless of how they are routed. Misrouting a statement changes the result shape, not safety.

### D2: Exec path commits when unrestricted; reads always roll back

`ExecQuery` opens `BeginTx(ReadOnly: cfg.Readonly)` and branches:

- **Row path** (`isRowReturning` true): `tx.QueryContext`, read columns + cap rows at `RowLimit`, then `Rollback`. Reads never commit, even when `readonly: false`, so a SELECT with side-effecting functions cannot persist.
- **Exec path** (`isRowReturning` false): `tx.ExecContext` → `sql.Result`. If `cfg.Readonly` is true, Postgres rejects the write inside the read-only transaction → `ExecContext` errors → `Rollback` → return the error (existing, tested behavior, unchanged). If `cfg.Readonly` is false → `Commit` on success (persist), `Rollback` on error.

- **Why row path rolls back under `readonly: false` too:** `execute_query` is read-assist for row-returning statements; committing side effects from a SELECT's volatile functions would surprise users. Writes are an explicit opt-in via the exec path.

### D3: Result shape gains an affected-row count

Extend `db.QueryResult` with `RowsAffected int64` (zero for the row path; the command tag count for the exec path). The tool output embeds `QueryResult`, so it surfaces automatically. A client reads `rows`/`columns` for selects and `rows_affected` for writes; the field is simply zero/empty for the unused path.

### D4: `explain_query` is unchanged

`Explain` keeps `QueryContext` + always `Rollback`. `EXPLAIN ANALYZE` of a write still runs under the read-only tx when restricted (rejected) or rolls back when unrestricted — it plans, it never commits. Documented as a non-goal.

## Risks / Trade-offs
- **Sniff misroutes** a row-returning statement with an unusual leader (e.g. a parenthesized expression statement) to the exec path → it would error or report 0 affected. *Mitigation:* the recognized leaders cover standard Postgres read statements; exotic forms are rare in dev-assist. The error is surfaced, not silent.
- **Comment stripping edge cases** (nested block comments) → *Mitigation:* strip one leading run of `/*…*/`/`--…`/whitespace; sufficient for typical inputs, not a security path.
- **`RETURNING` data dropped** → accepted non-goal; affected count still returned.
- **Commit on the unrestricted exec path is a real mutation** → that is the explicit, configured opt-in (`readonly: false`); the README restates it plainly.

## Testing

### Scenario

| Given | When | Then |
|-------|------|------|
| `isRowReturning` receives `SELECT`/`WITH`/`VALUES`/`TABLE` leader | called | returns true |
| statement has leading `/* */` or `--` comments / whitespace before leader | called | comments stripped; leader still detected |
| statement is `INSERT`/`UPDATE`/`DELETE`/`CREATE` or paren-leading | called | returns false |
| `readonly: false`, `INSERT INTO sqldb_mcp_test.t_item(id) VALUES ($1)` | `execute_query` | `rows_affected == 1`; re-select sees the durable row |
| `readonly: false`, duplicate-PK insert | `execute_query` | error result; table unchanged |
| `readonly: true`, `DELETE` | `execute_query` | error result (read-only message); table unchanged |
| `readonly: false`, `SELECT` | `execute_query` | rows returned; rolled back (no persistence) |

## Migration Plan

1. Add `isRowReturning` + rewrite `postgres.ExecQuery` to branch, commit/rollback per D2.
2. Add `RowsAffected` to `db.QueryResult`; thread through the tool output.
3. Add e2e: insert under `readonly: false` → assert affected count and that the row is durable; keep the rejected-write case.
4. Update README: writes are real when `readonly: false`; SELECTs stay rolled back.
5. Rollback = revert; `readonly: false` returns to its current (broken) no-op behavior.

## Open Questions
- None — routing heuristic, commit policy, result shape, and the read-only guarantee are settled.

## Todo

### 1. Routing + execution — `internal/db/postgres`
- [x] 1.1 Add `isRowReturning(sql string) bool`: trim leading whitespace and leading SQL comments (`/* */`, `--`), return true when the first token (uppercased) is one of `SELECT`, `WITH`, `VALUES`, `TABLE`
- [x] 1.2 Rewrite `ExecQuery`: open `BeginTx(ReadOnly: cfg.Readonly)`; on the row path run `QueryContext`, cap rows at `RowLimit`, then `Rollback`; on the exec path run `ExecContext`, `Commit` on success when unrestricted, `Rollback` on error or when restricted
- [x] 1.3 Populate the affected row count on the exec path from `sql.Result.RowsAffected()`; leave it zero on the row path
- [x] 1.4 Golden unit test for `isRowReturning`: SELECT, WITH, VALUES, TABLE, leading comments, INSERT/UPDATE/DELETE/CREATE, whitespace/paren edge cases

### 2. Result shape — `internal/db`, `internal/tools`
- [x] 2.1 Add `RowsAffected int64` (json `rows_affected`) to `db.QueryResult`
- [x] 2.2 Confirm `ExecuteQueryOutput` (embeds `*db.QueryResult`) surfaces `rows_affected` with no handler change; add a unit assertion if needed

### 3. Tests — `tests/e2e`
- [x] 3.1 Add a write-succeeds case under `readonly: false`: `INSERT INTO sqldb_mcp_test.t_item(id) VALUES ($1)` → assert `rows_affected == 1` and that the row is durable (re-select sees it)
- [x] 3.2 Add a write-rollback-on-error case under `readonly: false`: insert a duplicate PK → assert error and that the table is unchanged
- [x] 3.3 Keep the existing rejected-write case under `readonly: true` (DELETE → error result)
- [x] 3.4 Assert a SELECT under `readonly: false` still returns rows and rolls back (no persistence)

### 4. Documentation
- [x] 4.1 Update README "What your assistant can do" / safety note: writes are real and durable when `readonly: false`; SELECTs stay rolled back; statement kind is routed by a keyword sniff, not parsed
- [x] 4.2 Note `rows_affected` in the `execute_query` tool description and the example yaml comment if helpful
