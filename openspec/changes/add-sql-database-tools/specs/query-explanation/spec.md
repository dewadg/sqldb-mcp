## ADDED Requirements

### Requirement: explain_query tool

The system SHALL expose an `explain_query` tool that runs `EXPLAIN` for a given `query` against a selected database and returns the query plan. It SHALL accept an optional `format` (`text` default, or `json`) and an optional `analyze` boolean that appends `ANALYZE` to actually execute the query while planning.

#### Scenario: Plain explain returns plan text

- **WHEN** `explain_query` is called with `query: "SELECT * FROM t"` and default options
- **THEN** the result contains the `EXPLAIN` plan text and the query is not executed

#### Scenario: JSON format returns structured plan

- **WHEN** `explain_query` is called with `format: "json"`
- **THEN** the result contains the plan as produced by `EXPLAIN (FORMAT JSON)`

#### Scenario: Analyze executes the query

- **WHEN** `explain_query` is called with `analyze: true`
- **THEN** the plan includes execution statistics produced by `EXPLAIN ANALYZE`

### Requirement: Analyze respects read-only mode

When the selected database is configured with `readonly: true`, `explain_query` with `analyze: true` SHALL still execute under a read-only transaction so it cannot mutate data.

#### Scenario: Analyze of a write fails in restricted mode

- **WHEN** `explain_query` is called with `query: "DELETE FROM t"`, `analyze: true`, against a database with `readonly: true`
- **THEN** the read-only transaction rejects the underlying write and the tool returns an error

#### Scenario: Analyze of a read succeeds in restricted mode

- **WHEN** `explain_query` is called with `query: "SELECT * FROM t"`, `analyze: true`, against a database with `readonly: true`
- **THEN** the plan with execution statistics is returned

### Requirement: Explain is timeout-bounded

The system SHALL bound `explain_query` execution with the same `query_timeout` as `execute_query`, which is especially important when `analyze: true` runs the query.

#### Scenario: Long-running analyze is aborted

- **WHEN** `explain_query` with `analyze: true` runs longer than `query_timeout`
- **THEN** the context is cancelled, execution is aborted, and the tool returns a timeout error
