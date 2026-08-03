## Context

See `proposal.md` for motivation. Today `postgres.ExecQuery` always calls `tx.QueryContext` and always rolls back — so writes neither execute correctly (a command like `INSERT` is not row-returning) nor persist. The existing design (change `add-sql-database-tools`, D3) deliberately rejected a full SQL parser; the read-only transaction is the sole safety boundary. This change keeps that boundary and adds only a lightweight statement-kind routing so writes can run on the exec path and commit when permitted.

## Goals / Non-Goals

**Goals:**

- Make `readonly: false` actually grant durable write access through `execute_query`.
- Preserve the read-only guarantee exactly: `readonly: true` rejects every write before commit.
- Keep reads (row-returning statements) rolled back and capped, unchanged.

**Non-Goals:**

- A SQL parser/AST allowlist; full statement classification; `RETURNING` row sets; `EXPLAIN ... ANALYZE` committing; per-table permissions or auditing (see proposal).

## Decisions

### D1. Route by a leading-keyword sniff, not by try-query-then-exec

Add `isRowReturning(sql string) bool` that trims leading whitespace and SQL comments (`/* */`, `--`), reads the first token, and returns true for Postgres row-returning leaders: `SELECT`, `WITH`, `VALUES`, `TABLE`. Everything else routes to the exec path.

- *Why over try-`QueryContext`-then-`ExecContext` on error:* a failed `QueryContext` for a write has already executed the statement server-side; re-running it via `ExecContext` would double-execute (duplicate inserts, double decrements). Sniffing first picks one path and runs the statement exactly once.
- *Why this is not the rejected "parser":* the original design rejected a pglast AST allowlist used as a *security* control. This sniff is a routing heuristic only — `readonly: true` still rejects writes at the transaction regardless of how they are routed. Misrouting a statement changes the result shape, not safety.

### D2. Exec path commits when unrestricted; reads always roll back

`ExecQuery` opens `BeginTx(ReadOnly: cfg.Readonly)` and branches:

- **Row path** (`isRowReturning` true): `tx.QueryContext`, read columns + cap rows at `RowLimit`, then `Rollback`. Reads never commit, even when `readonly: false`, so a SELECT with side-effecting functions cannot persist.
- **Exec path** (`isRowReturning` false): `tx.ExecContext` → `sql.Result`. If `cfg.Readonly` is true, Postgres rejects the write inside the read-only transaction → `ExecContext` errors → `Rollback` → return the error (existing, tested behavior, unchanged). If `cfg.Readonly` is false → `Commit` on success (persist), `Rollback` on error.

- *Why row path rolls back under `readonly: false` too:* `execute_query` is read-assist for row-returning statements; committing side effects from a SELECT's volatile functions would surprise users. Writes are an explicit opt-in via the exec path.

### D3. Result shape gains an affected-row count

Extend `db.QueryResult` with `RowsAffected int64` (zero for the row path; the command tag count for the exec path). The tool output already embeds `QueryResult`, so it surfaces automatically. A client reads `rows`/`columns` for selects and `rows_affected` for writes; the field is simply zero/empty for the unused path.

### D4. `explain_query` is unchanged

`Explain` keeps `QueryContext` + always `Rollback`. `EXPLAIN ANALYZE` of a write still runs under the read-only tx when restricted (rejected) or rolls back when unrestricted — it plans, it never commits. Documented as a non-goal.

## Risks / Trade-offs

- **Sniff misroutes** a row-returning statement with an unusual leader (e.g. a parenthesized expression statement) to the exec path → it would error or report 0 affected. *Mitigation:* the recognized leaders cover standard Postgres read statements; exotic forms are rare in dev-assist. The error is surfaced, not silent.
- **Comment stripping edge cases** (nested block comments) → *Mitigation:* strip one leading run of `/*…*/`/`--…`/whitespace; sufficient for typical inputs, not a security path.
- **`RETURNING` data dropped** → accepted non-goal; affected count still returned.
- **Commit on the unrestricted exec path is a real mutation** → that is the explicit, configured opt-in (`readonly: false`); the README restates it plainly.

## Migration Plan

1. Add `isRowReturning` + rewrite `postgres.ExecQuery` to branch, commit/rollback per D2.
2. Add `RowsAffected` to `db.QueryResult`; thread through the tool output.
3. Add e2e: insert under `readonly: false` → assert affected count and that the row is durable; keep the rejected-write case.
4. Update README: writes are real when `readonly: false`; SELECTs stay rolled back.
5. Rollback = revert; `readonly: false` returns to its current (broken) no-op behavior.

## Open Questions

None — routing heuristic, commit policy, result shape, and the read-only guarantee are settled.
