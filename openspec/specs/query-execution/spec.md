# query-execution

## Purpose

The `execute_query` tool: runs a SQL query against a selected database and
returns columns and rows. Enforces read-only by default via a read-only
transaction, supports unrestricted mode by opt-in, and caps results with a row
limit and per-query timeout.

## Requirements

### Requirement: execute_query tool

The system SHALL expose an `execute_query` tool that runs a SQL `query` against a selected database and returns a result containing column metadata and rows. The tool SHALL accept an optional `args` array bound as parameters to the query, and SHALL execute via real bound parameters rather than string interpolation.

#### Scenario: Read query returns columns and rows

- **WHEN** `execute_query` is called with `query: "SELECT 1 AS n"`
- **THEN** the result reports column `n` and one row whose value is `1`

#### Scenario: Bind parameters safely

- **WHEN** `execute_query` is called with `query: "SELECT * FROM t WHERE id = $1"` and `args: [42]`
- **THEN** the query executes with `42` as a bound parameter and the value never reaches the SQL string as a literal

### Requirement: Read-only enforcement by default

When a database is configured with `readonly: true` (the default), `execute_query` SHALL run the query inside a read-only transaction so that Postgres rejects any write, including writes inside functions.

#### Scenario: Write rejected in restricted mode

- **WHEN** `execute_query` runs `DELETE FROM t` against a database with `readonly: true`
- **THEN** Postgres rejects the statement and the tool returns an error reporting that the transaction is read-only

#### Scenario: Read succeeds in restricted mode

- **WHEN** `execute_query` runs a `SELECT` against a database with `readonly: true`
- **THEN** the query succeeds and returns rows

### Requirement: Unrestricted mode

When a database is configured with `readonly: false`, `execute_query` SHALL NOT open a read-only transaction, allowing writes by explicit user opt-in.

#### Scenario: Write succeeds in unrestricted mode

- **WHEN** `execute_query` runs an `INSERT` against a database with `readonly: false`
- **THEN** the statement executes and the tool reports the affected row count

### Requirement: Row limit and timeout

The system SHALL cap the number of returned rows at a configurable `row_limit` (default `100`) and SHALL bound every query with a `query_timeout` (default `30s`).

#### Scenario: Result truncated to row limit

- **WHEN** a query matches 250 rows and `row_limit` is `100`
- **THEN** the returned result contains at most 100 rows

#### Scenario: Query timeout aborts execution

- **WHEN** a query runs longer than `query_timeout`
- **THEN** its context is cancelled, the query is aborted, and the tool returns a timeout error
