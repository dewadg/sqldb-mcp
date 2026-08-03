## MODIFIED Requirements

### Requirement: execute_query tool

The system SHALL expose an `execute_query` tool that runs a SQL `query` against a selected database. The tool SHALL accept an optional `args` array bound as parameters to the query, and SHALL execute via real bound parameters rather than string interpolation.

The tool SHALL route the statement by kind. Row-returning statements (e.g. `SELECT`, `WITH`, `VALUES`, `TABLE`) SHALL be executed as queries and the result SHALL contain column metadata and rows, capped by `row_limit`. Non-row statements (e.g. `INSERT`, `UPDATE`, `DELETE`, `MERGE`, and DDL) SHALL be executed as commands and the result SHALL contain the affected row count in place of columns and rows. Statement kind is decided by a lightweight leading-keyword inspection used only for routing; it is not a security control.

#### Scenario: Read query returns columns and rows

- **WHEN** `execute_query` is called with `query: "SELECT 1 AS n"`
- **THEN** the result reports column `n` and one row whose value is `1`

#### Scenario: Bind parameters safely

- **WHEN** `execute_query` is called with `query: "SELECT * FROM t WHERE id = $1"` and `args: [42]`
- **THEN** the query executes with `42` as a bound parameter and the value never reaches the SQL string as a literal

#### Scenario: Row-returning statements are rolled back

- **WHEN** `execute_query` runs a `SELECT` (or other row-returning statement) against a database with `readonly: false`
- **THEN** the result returns columns and rows and the transaction is rolled back, so reads never persist side effects

### Requirement: Read-only enforcement by default

When a database is configured with `readonly: true` (the default), `execute_query` SHALL run the statement inside a read-only transaction so that Postgres rejects any write, including writes inside functions. Routing does not weaken this: a write routed to the command path still executes inside the read-only transaction and is rejected before it can commit.

#### Scenario: Write rejected in restricted mode

- **WHEN** `execute_query` runs `DELETE FROM t` against a database with `readonly: true`
- **THEN** Postgres rejects the statement and the tool returns an error reporting that the transaction is read-only

#### Scenario: Read succeeds in restricted mode

- **WHEN** `execute_query` runs a `SELECT` against a database with `readonly: true`
- **THEN** the query succeeds and returns rows

### Requirement: Unrestricted mode

When a database is configured with `readonly: false`, `execute_query` SHALL open a normal (read-write) transaction and SHALL commit write statements (e.g. `INSERT`, `UPDATE`, `DELETE`, DDL) on success, persisting them. The tool SHALL roll back on error. The result of a committed write SHALL report the affected row count.

#### Scenario: Write succeeds in unrestricted mode

- **WHEN** `execute_query` runs an `INSERT ... VALUES (1)` against a database with `readonly: false`
- **THEN** the statement is committed, the affected row count in the result is `1`, and the row is durable in the table

#### Scenario: Write rolled back on error

- **WHEN** `execute_query` runs a write that fails (e.g. a constraint violation) against a database with `readonly: false`
- **THEN** the transaction is rolled back, no partial change persists, and the tool returns the error

#### Scenario: RETURNING data is not surfaced

- **WHEN** `execute_query` runs `INSERT ... RETURNING *` against a database with `readonly: false`
- **THEN** the statement is committed and the result reports the affected row count; the `RETURNING` row set is not returned

### Requirement: Row limit and timeout

The system SHALL cap the number of returned rows at a configurable `row_limit` (default `100`) and SHALL bound every query with a `query_timeout` (default `30s`).

#### Scenario: Result truncated to row limit

- **WHEN** a query matches 250 rows and `row_limit` is `100`
- **THEN** the returned result contains at most 100 rows

#### Scenario: Query timeout aborts execution

- **WHEN** a query runs longer than `query_timeout`
- **THEN** its context is cancelled, the query is aborted, and the tool returns a timeout error
