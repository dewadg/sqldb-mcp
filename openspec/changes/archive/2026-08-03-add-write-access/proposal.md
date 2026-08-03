## Why

`readonly: false` is already a documented, specced opt-in, and the `query-execution` spec claims writes report an affected row count. But the implementation cannot fulfill it: `execute_query` always runs the SQL as a row-returning statement (`QueryContext`) and always rolls back the transaction, so an `INSERT`/`UPDATE`/`DELETE`/DDL either errors at the driver level or is silently undone. The feature is advertised but not real. This change makes write access work, gated by the existing `readonly` flag.

## What Changes

- Route `execute_query` by statement kind: row-returning statements run through `QueryContext` and return columns + rows; write/DDL statements run through `ExecContext` and report the affected row count.
- Commit write statements on success **only when `readonly: false`**; roll back on error. Row-returning statements still roll back (reads never need to commit), so read-only semantics for SELECT are unchanged.
- `readonly: true` is unchanged and remains the hard wall: a write is routed to the exec path but the read-only transaction rejects it before commit.
- Extend the `execute_query` result to carry an affected row count for write statements.
- Document the routing heuristic and the read-only guarantee in the README.

## Capabilities

### New Capabilities

<!-- None -->

### Modified Capabilities

- `query-execution`: the `execute_query` tool routes statements between row-returning and write execution, commits writes when unrestricted, and reports the affected row count. Updates the tool output contract and clarifies the "Unrestricted mode" requirement.

## Non-goals

- A full SQL parser or an AST-based statement allowlist. Statement kind is decided by a lightweight leading-keyword sniff used only for **routing**, never as a security control — the read-only transaction remains the only safety boundary, exactly as the existing design decided.
- Returning rows from `INSERT ... RETURNING` / `UPDATE ... RETURNING`. Such statements are treated as writes: they commit and report the affected count; the `RETURNING` data set is not surfaced. (Can be revisited later.)
- `EXPLAIN ... ANALYZE` committing writes. `explain_query` keeps its current always-rollback behavior; it plans, it does not persist.
- Per-statement or per-table write permissions, auditing, or rate limiting.

## Impact

- `internal/db/postgres/postgres.go`: rewrite `ExecQuery` to route and to commit/rollback correctly; add a leading-keyword sniff helper.
- `internal/db/db.go`: add an affected-row-count field to the `QueryResult` shape.
- `internal/tools/tools.go`: surface the affected row count in `execute_query` output.
- `tests/e2e`: add a write-succeeds case (insert, verify the row landed) under `readonly: false`; keep the existing rejected-write case.
- README: restate that writes are real when `readonly: false` and that SELECTs stay rolled back.
