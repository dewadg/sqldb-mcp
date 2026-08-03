# Tasks

## 1. Dependencies & scaffold

- [x] 1.1 Add `github.com/jackc/pgx/v5` (stdlib adapter) and `gopkg.in/yaml.v3` to `go.mod`; run `go mod tidy`
- [x] 1.2 Verify `CGO_ENABLED=0 go build ./cmd/sqldb-mcp` still produces a CGo-free binary

## 2. Database layer — `internal/db`

- [x] 2.1 Define `DatabaseConfig` struct (driver, url, readonly pointer, max_open/idle conns, conn_max_idle_time, query_timeout, row_limit) with YAML tags
- [x] 2.2 Define `Config` root struct (`databases map[string]DatabaseConfig`); no `defaults` block — apply hardcoded fallback constants to unset per-DB fields at load
- [x] 2.3 Define the `Dialect` interface (`Name`, `OpenDB`, `ListObjects`, `GetObjectDetails`, `ExecQuery`, `Explain`) and the result types (`Object`, `ObjectDetails`, `Column`, `Constraint`, `Index`, `QueryResult`, `ExplainResult`)
- [x] 2.4 Implement `Registry`: register dialects by name, open pools at startup with ping, `Resolve(alias)` returning `(*sql.DB, Dialect, DatabaseConfig)`, empty-alias → sole-DB-or-error logic, alias validation
- [x] 2.5 Add a password-redacting helper for connection-error logging

## 3. Postgres dialect — `internal/db/postgres`

- [x] 3.1 Implement `OpenDB`: register/lookup pgx stdlib driver, `sql.Open`, apply pool sizing + idle lifetime, `PingContext`
- [x] 3.2 Implement `ListObjects` via `information_schema.tables`/`sequences` and `pg_extension`; validate `type` ∈ {table,view,sequence,extension}
- [x] 3.3 Implement `GetObjectDetails` via `information_schema.columns`, `table_constraints`+`key_column_usage`, and `pg_indexes`
- [x] 3.4 Implement `ExecQuery`: `BeginTx(ctx, TxOptions{ReadOnly: cfg.Readonly})`, `QueryContext` with bound args, enforce `row_limit`, rollback; return columns + rows
- [x] 3.5 Implement `Explain`: build `EXPLAIN (FORMAT text|json[, ANALYZE]) <query>`, map requested `format`/`analyze` to native options, run under readonly tx when restricted, bounded by `query_timeout`

## 4. Configuration — `internal/config`

- [x] 4.1 Extend `Load` to resolve the config file path (`--config` → `SQLDB_MCP_CONFIG` → auto `./sqldb-mcp.yaml` → none) and parse it into `db.Config`
- [x] 4.2 Add config validation: known drivers, non-empty `url`, unique aliases; return descriptive errors
- [x] 4.3 Add hardcoded fallback constants (`readonly:true`, `query_timeout:30s`, `max_open_conns:10`, `max_idle_conns:5`, `row_limit:100`) used when a per-DB field is unset
- [x] 4.4 Keep existing env-based server identity/transport/log loading unchanged

## 5. Tool handlers — `internal/tools`

- [x] 5.1 Add `list_objects` input/output structs + handler dispatching to `Dialect.ListObjects`; `database` arg required-via-registry
- [x] 5.2 Add `get_object_details` input/output structs + handler
- [x] 5.3 Add `execute_query` input/output structs (query, optional args, columns+rows) + handler using `Dialect.ExecQuery`
- [x] 5.4 Add `explain_query` input/output structs (query, format, analyze) + handler using `Dialect.Explain`
- [x] 5.5 Add `list_databases` input/output structs (`alias`, `driver`, `readonly`; no `url`) + handler returning registry entries
- [x] 5.6 Update `Register` signature to accept the `*db.Registry`; keep `ping`

## 6. Wiring — `cmd/sqldb-mcp`, `internal/server`

- [x] 6.1 Add `--config` flag in `main.go`; load db config, build registry, pass to `server.New`
- [x] 6.2 Update `server.New` to build and close the registry and pass it to `tools.Register`; update server `Instructions` to describe the SQL tools and `list_databases`, and list configured aliases

## 7. Tests

- [x] 7.1 Config load + validation unit tests (per-field constant fallback when omitted, unknown driver, missing url, unique aliases)
- [x] 7.2 Registry resolution tests (explicit alias, empty→sole, empty→ambiguous error, unknown alias error) using a fake dialect
- [x] 7.3 Readonly-enforcement test with a fake dialect asserting `TxOptions.ReadOnly` is set per `cfg.Readonly`
- [x] 7.4 SQL-builder golden tests for postgres `ListObjects`/`GetObjectDetails`/`Explain` generated SQL
- [x] 7.5 `execute_query` row-cap test with a stub returning > `row_limit` rows
- [x] 7.6 Tool handler unit tests (fake dialect) for the SQL tools incl. error paths
- [x] 7.7 `list_databases` unit test asserting every configured alias is returned with `driver`+`readonly` and that no `url`/credential appears in the output
- [x] 7.8 Postgres integration tests in `internal/db/postgres`, guarded by `SQLDB_MCP_TEST_POSTGRES_URL` (skip with a clear message when unset)
  - [x] 7.8.1 Add a repo-root `.env` loader helper (`testenv_test.go`, no new dependency): parse `KEY=VALUE` / `KEY='VALUE'` lines from `<repo>/.env`, stripping surrounding quotes, and set each key into the process env only when not already set — so `go test ./...` runs without a manual `export`
  - [x] 7.8.2 Add a fixture helper: open the DSN via the real postgres dialect, idempotently create a scratch schema+table (e.g. `sqldb_mcp_test.t_item(id INT PK, label TEXT)`), seed a known row count, and `DROP` it in `t.Cleanup`
  - [x] 7.8.3 `list_objects`: scratch table appears; `type:"table"` filter returns it; an unsupported `type` returns the dialect's supported-types error
  - [x] 7.8.4 `get_object_details`: assert columns (name/type/nullability), the primary-key constraint, and at least one index match the fixture
  - [x] 7.8.5 `execute_query` read: `SELECT` returns the seeded rows, truncated at `row_limit`; a bind-param query (`WHERE id = $1`) returns exactly the bound row, value never inlined
  - [x] 7.8.6 `execute_query` rejected write: with `readonly: true`, `DELETE`/`INSERT` fails with Postgres' read-only-transaction error and no rows change
  - [x] 7.8.7 `explain_query`: text plan non-empty; `format:"json"` output parses as JSON; `analyze:true` runs under the read-only tx and returns a plan (per the query-explanation spec)
  - [x] 7.8.8 Run via `go test ./internal/db/postgres/...`; the env var may be overridden inline to point tests at another Postgres

## 8. Documentation

- [x] 8.1 Update `README.md`: add the tools (`list_databases`, `list_objects`, `get_object_details`, `execute_query`, `explain_query`), the YAML config format (per-database entries + hardcoded constant fallback, multi-database example), `--config`/`SQLDB_MCP_CONFIG`, readonly/row-limit/timeout behavior, and build note (pure-Go single binary)
- [x] 8.2 Add a sample `sqldb-mcp.example.yaml` at repo root
- [x] 8.3 Update the "Adding a new tool" / "Adding a new RDBMS" sections to document the `Dialect` interface
