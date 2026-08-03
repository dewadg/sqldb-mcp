# Tasks

## 1. Dependencies & scaffold

- [ ] 1.1 Add `github.com/jackc/pgx/v5` (stdlib adapter) and `gopkg.in/yaml.v3` to `go.mod`; run `go mod tidy`
- [ ] 1.2 Verify `CGO_ENABLED=0 go build ./cmd/sqldb-mcp` still produces a CGo-free binary

## 2. Database layer — `internal/db`

- [ ] 2.1 Define `DatabaseConfig` struct (driver, url, readonly pointer, max_open/idle conns, conn_max_idle_time, query_timeout, row_limit) with YAML tags
- [ ] 2.2 Define `Config` root struct (`databases map[string]DatabaseConfig`); no `defaults` block — apply hardcoded fallback constants to unset per-DB fields at load
- [ ] 2.3 Define the `Dialect` interface (`Name`, `OpenDB`, `ListObjects`, `GetObjectDetails`, `ExecQuery`, `Explain`) and the result types (`Object`, `ObjectDetails`, `Column`, `Constraint`, `Index`, `QueryResult`, `ExplainResult`)
- [ ] 2.4 Implement `Registry`: register dialects by name, open pools at startup with ping, `Resolve(alias)` returning `(*sql.DB, Dialect, DatabaseConfig)`, empty-alias → sole-DB-or-error logic, alias validation
- [ ] 2.5 Add a password-redacting helper for connection-error logging

## 3. Postgres dialect — `internal/db/postgres`

- [ ] 3.1 Implement `OpenDB`: register/lookup pgx stdlib driver, `sql.Open`, apply pool sizing + idle lifetime, `PingContext`
- [ ] 3.2 Implement `ListObjects` via `information_schema.tables`/`sequences` and `pg_extension`; validate `type` ∈ {table,view,sequence,extension}
- [ ] 3.3 Implement `GetObjectDetails` via `information_schema.columns`, `table_constraints`+`key_column_usage`, and `pg_indexes`
- [ ] 3.4 Implement `ExecQuery`: `BeginTx(ctx, TxOptions{ReadOnly: cfg.Readonly})`, `QueryContext` with bound args, enforce `row_limit`, rollback; return columns + rows
- [ ] 3.5 Implement `Explain`: build `EXPLAIN (FORMAT text|json[, ANALYZE]) <query>`, map requested `format`/`analyze` to native options, run under readonly tx when restricted, bounded by `query_timeout`

## 4. Configuration — `internal/config`

- [ ] 4.1 Extend `Load` to resolve the config file path (`--config` → `SQLDB_MCP_CONFIG` → auto `./sqldb-mcp.yaml` → none) and parse it into `db.Config`
- [ ] 4.2 Add config validation: known drivers, non-empty `url`, unique aliases; return descriptive errors
- [ ] 4.3 Add hardcoded fallback constants (`readonly:true`, `query_timeout:30s`, `max_open_conns:10`, `max_idle_conns:5`, `row_limit:100`) used when a per-DB field is unset
- [ ] 4.4 Keep existing env-based server identity/transport/log loading unchanged

## 5. Tool handlers — `internal/tools`

- [ ] 5.1 Add `list_objects` input/output structs + handler dispatching to `Dialect.ListObjects`; `database` arg required-via-registry
- [ ] 5.2 Add `get_object_details` input/output structs + handler
- [ ] 5.3 Add `execute_query` input/output structs (query, optional args, columns+rows) + handler using `Dialect.ExecQuery`
- [ ] 5.4 Add `explain_query` input/output structs (query, format, analyze) + handler using `Dialect.Explain`
- [ ] 5.5 Add `list_databases` input/output structs (`alias`, `driver`, `readonly`; no `url`) + handler returning registry entries
- [ ] 5.6 Update `Register` signature to accept the `*db.Registry`; keep `ping`

## 6. Wiring — `cmd/sqldb-mcp`, `internal/server`

- [ ] 6.1 Add `--config` flag in `main.go`; load db config, build registry, pass to `server.New`
- [ ] 6.2 Update `server.New` to build and close the registry and pass it to `tools.Register`; update server `Instructions` to describe the SQL tools and `list_databases`, and list configured aliases

## 7. Tests

- [ ] 7.1 Config load + validation unit tests (per-field constant fallback when omitted, unknown driver, missing url, unique aliases)
- [ ] 7.2 Registry resolution tests (explicit alias, empty→sole, empty→ambiguous error, unknown alias error) using a fake dialect
- [ ] 7.3 Readonly-enforcement test with a fake dialect asserting `TxOptions.ReadOnly` is set per `cfg.Readonly`
- [ ] 7.4 SQL-builder golden tests for postgres `ListObjects`/`GetObjectDetails`/`Explain` generated SQL
- [ ] 7.5 `execute_query` row-cap test with a stub returning > `row_limit` rows
- [ ] 7.6 Tool handler unit tests (fake dialect) for the SQL tools incl. error paths
- [ ] 7.7 `list_databases` unit test asserting every configured alias is returned with `driver`+`readonly` and that no `url`/credential appears in the output
- [ ] 7.8 Integration tests guarded by `SQLDB_MCP_TEST_POSTGRES_URL` covering list/details/read/rejected-write/explain (skipped when env unset)

## 8. Documentation

- [ ] 8.1 Update `README.md`: add the tools (`list_databases`, `list_objects`, `get_object_details`, `execute_query`, `explain_query`), the YAML config format (per-database entries + hardcoded constant fallback, multi-database example), `--config`/`SQLDB_MCP_CONFIG`, readonly/row-limit/timeout behavior, and build note (pure-Go single binary)
- [ ] 8.2 Add a sample `sqldb-mcp.example.yaml` at repo root
- [ ] 8.3 Update the "Adding a new tool" / "Adding a new RDBMS" sections to document the `Dialect` interface
