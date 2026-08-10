# Add SQL database tools

> Ship the four core SQL tools (introspect, read-only query, explain plan) plus a multi-database driver abstraction on top of `database/sql`, Postgres first, served as one pure-Go binary.

## Context

sqldb-mcp shipped only a `ping` health-check tool — no SQL capability. To serve as a lean, universal SQL-DB MCP for development assistance it must introspect schemas, run read-only queries, and explain query plans. sqldb-mcp is a Go MCP server scaffold built on the official `github.com/modelcontextprotocol/go-sdk` (v1.7.0). It runs stdio or streamable-HTTP, loads server identity from env, and exposes a single `ping` tool via typed `mcp.ToolHandlerFor[In, Out]` handlers registered in `internal/tools`.

The target shape is a **lean, universal SQL-DB MCP for development assistance**: introspect objects, run read-only queries, and explain plans. It must support multiple RDBMS later (Postgres now), manage several databases from one server, and ship as a single static binary with no CGo or native runtime dependencies.

Reference: `crystaldba/postgres-mcp` (Python/FastMCP) defines a similar four-tool surface and validates it as a useful dev-assist API. The tool taxonomy is reused, not the implementation.

## Goals
- Add the SQL tools `list_objects`, `get_object_details`, `execute_query`, and `explain_query` — each selecting a database by alias — plus `list_databases` for alias discovery.
- A `Dialect` abstraction so a new RDBMS is added by implementing one package, not by editing tools.
- One server, many databases, configured by a YAML file.
- Read-only safety by default, enforced as a hard server-side guarantee.
- Single pure-Go binary.
- A `database/sql`-based driver abstraction with a Postgres implementation built on the pure-Go `pgx` driver, keeping a single static binary with no CGo or native runtime deps.
- Multi-database configuration via a YAML config file (path from `--config` flag or `SQLDB_MCP_CONFIG` env, auto-loaded when a default `sqldb-mcp.yaml` exists). Each entry names a database with its driver, DSN, pool limits, and a read-only flag.
- Enforce read-only by default for `execute_query` (overridable per database) so the dev-assist surface is safe.
- Existing env-based server config (name/version/transport/log) is unchanged and additive; `ping` tool is retained.

## Non-goals
- Write-path / mutation tools (INSERT/UPDATE/DDL) — dev-assist stays read-only.
- Support for RDBMS other than Postgres. The driver abstraction is added, but only Postgres is implemented in this change.
- Production-grade connection governance: replication, HA, failover, or authenticated pooling.
- Schema diffing, migrations, data export/import, or query-result paging beyond a capped row limit.
- Mutation/DDL tooling, migration, schema diffing, data export/paging beyond a capped row count.
- Production connection governance (HA, failover, auth/pool tuning services).

## Flow
```mermaid
sequenceDiagram
    participant Client as MCP Client (LLM)
    participant Server as mcp.Server
    participant Tools as internal/tools
    participant Reg as db.Registry
    participant Dialect as db/postgres.Dialect
    participant DB as Postgres (*sql.DB)

    Client->>Server: tools/call execute_query {database:"primary", query:"SELECT ..."}
    Server->>Tools: executeQueryHandler(input)
    Tools->>Reg: Resolve(input.Database) -> (*sql.DB, Dialect, cfg)
    Reg-->>Tools: resolved conn + dialect
    Tools->>Dialect: ExecQuery(ctx, db, QuerySpec{SQL, Args, Timeout})
    Dialect->>DB: BeginTx(ctx, TxOptions{ReadOnly: cfg.Readonly})
    Dialect->>DB: tx.QueryContext(SQL, args...)  [row_limit cap]
    Dialect->>DB: tx.Rollback()
    Dialect-->>Tools: QueryResult{Columns, Rows}
    Tools-->>Server: *mcp.CallToolResult (JSON)
    Server-->>Client: result
```

## Decisions

### D1: `database/sql` + pure-Go `pgx` driver

Use Go's standard `database/sql` as the connection layer and register `pgx` (`github.com/jackc/pgx/v5/stdlib`) as the Postgres driver.

- **Why over pgx-native API:** `database/sql` is RDBMS-agnostic; swapping to MySQL/SQLite later means importing a different driver and adjusting dialect SQL, not rewriting the pool/handler code.
- **Why over GORM/sqlx:** this is a thin metadata + read tool; an ORM adds weight and hides the exact SQL needed for `information_schema`/`EXPLAIN`.
- **Alternatives:** `lib/pq` (older, less maintained); pgx-native pool (faster but couples every tool to pgx types, hurting multi-RDBMS goals).

### D2: `Dialect` interface owns all RDBMS-specific behavior

A `Dialect` in `internal/db` exposes connection open + the four behaviors. The registry owns pool lifecycle; tool handlers stay RDBMS-agnostic and dispatch through the dialect.

```go
type Dialect interface {
    Name() string
    OpenDB(ctx context.Context, cfg DatabaseConfig) (*sql.DB, error)
    ListObjects(ctx context.Context, db *sql.DB, schema, objectType string) ([]Object, error)
    GetObjectDetails(ctx context.Context, db *sql.DB, schema, name, objectType string) (*ObjectDetails, error)
    ExecQuery(ctx context.Context, db *sql.DB, q QuerySpec) (*QueryResult, error)
    Explain(ctx context.Context, db *sql.DB, spec ExplainSpec) (*ExplainResult, error)
}
```

- **Why:** isolates dialect SQL in one package (`internal/db/postgres`); adding MySQL = new package + one registry entry.
- **Alternative:** a giant switch per tool — rejected; scatters RDBMS logic across tools.

### D3: Read-only enforced by `sql.TxOptions{ReadOnly: true}` + context timeout, not a SQL parser

For every `execute_query` (and `explain … ANALYZE`), open the transaction with `ReadOnly: true`. Postgres refuses any write inside a `READ ONLY` transaction — including writes inside functions — so this is a single strong guarantee rather than a statement-allowlist parser (the reference repo's `SafeSqlDriver` carries a pglast AST allowlist; deliberately skipped).

- **Why:** pulling a full SQL parser into Go is heavy and against the lean goal; the server-side `READ ONLY` transaction is a stronger and simpler wall for an AI-facing dev tool.
- **Trade-off:** cannot permit specific writes even if a user opts in via `readonly: false` — handled by simply *not* opening a read-only transaction when the database is configured unrestricted (see D5).
- Per-query timeout applied via `context.WithTimeout` (default 30s, configurable).
- **Dialect-owned mechanism:** the read-only *mechanism* is a dialect concern, not a tool concern. Postgres and MySQL/MariaDB enforce it via a read-only transaction (`sql.TxOptions{ReadOnly: true}` / `SET TRANSACTION READ ONLY`). SQLite has no per-transaction read-only mode, so the SQLite dialect opens the file with a read-only connection flag instead. The tool contract is unchanged; each dialect maps `readonly: true` onto whatever its engine supports.

### D4: Real protocol parameters, never string interpolation

`execute_query` accepts the SQL string plus optional `args []any`; queries executed through `db.QueryContext` so values travel as bound parameters (`$1`, `$2`). Metadata and explain queries use the same path.

- **Why:** injection safety; the reference repo inlines literals via `SQL(...).format(Literal(p))`, avoided here.

### D5: Configuration — YAML file for databases, env retained for server identity

A YAML config file holds the database registry. Server identity/transport/log remain env-driven (unchanged from the scaffold) so nothing currently documented breaks. Resolution: `--config <path>` flag → `SQLDB_MCP_CONFIG` env → auto-load `./sqldb-mcp.yaml` if present → run with an empty registry (server still starts; SQL tools return a clear "no databases configured" error, `ping` still works).

```yaml
# sqldb-mcp.yaml — every setting lives on its database entry; no defaults block.
# Unset optional fields fall back to hardcoded constants (see Configuration).
databases:
  primary:                # alias used as the `database` tool parameter
    driver: postgres
    url: postgresql://user:pass@host:5432/appdb?sslmode=disable
    readonly: true        # default true; tools refuse writes
    max_open_conns: 10
    max_idle_conns: 5
    conn_max_idle_time: 5m
    query_timeout: 30s
    row_limit: 100
  warehouse:
    driver: postgres
    url: postgresql://user:pass@host:5432/warehouse?sslmode=disable
    readonly: false       # unrestricted: tools may run write statements
```

- **Why YAML over env:** multiple databases, secrets in URLs, and pool tuning are unwieldy as flat env vars; YAML also documents itself.
- **No `defaults` block:** all configuration is per-database. No shared defaults layer to reason about; an omitted optional field resolves to a hardcoded constant (e.g. `readonly: true`, `query_timeout: 30s`), not an inherited block.
- **Default database:** when a tool is called with an empty `database` argument, the registry resolves the single configured database, or the first one if several exist, otherwise errors. Keeps the common single-DB case ergonomic.
- **Password handling:** connection errors are logged with the URL's password redacted.

### D6: Schema exposed as tools, not MCP resources

`list_objects`/`get_object_details` are tools (request/response), not MCP resources.

- **Why:** broader client compatibility (per the reference repo's experience); tools are the simplest surface for an LLM to call.

### D7: `list_databases` discovery tool

Expose `list_databases` (no arguments) returning one entry per configured database: `{alias, driver, readonly}`. The connection `url` is deliberately omitted so credentials never leave the server.

- **Why a tool over Instructions-only:** an LLM can call it reliably and receive structured data; prose `Instructions` are advisory and easy to ignore or hallucinate past. The server still lists aliases in `Instructions` as a hint, but `list_databases` is the source of truth.

### D8: Integration tests load repo-root `.env`; `.env` is test-only

Postgres integration tests consume `SQLDB_MCP_TEST_POSTGRES_URL`. A tiny `testenv` helper reads `<repo>/.env` (`KEY=VALUE` / `KEY='VALUE'`, quotes stripped) and populates the process env for keys not already set, so `go test ./internal/db/postgres/...` runs with no manual `export` while an explicit env var or CI secret still takes precedence. `.env` is `.gitignore`d and never read by the server at runtime — runtime config is the YAML file (D5); the loader exists only under `*_test.go`.

- **Why over `godotenv`:** one ~15-line parser avoids a new dependency for a test-only need.
- **Why skip-on-unset over a build tag:** keeps `go test ./...` as the single command; absence reported explicitly instead of silently not compiling.

## Configurations

| Knob | Where | Default | Notes |
| --- | --- | --- | --- |
| Server name/version/addr/log | env (`MCP_*`) | unchanged | scaffold defaults retained |
| `--config` | flag | — | explicit config-file path |
| `SQLDB_MCP_CONFIG` | env | — | config-file path fallback |
| `databases.<alias>.driver` | YAML | — | registered dialect name |
| `databases.<alias>.url` | YAML | — | libpq connection URL |
| `databases.<alias>.readonly` | YAML | `true` | forces read-only transactions |
| `databases.<alias>.row_limit` | YAML | `100` | caps `execute_query` rows |
| `databases.<alias>.query_timeout` | YAML | `30s` | per-query context timeout |
| `databases.<alias>.max_open_conns` / `max_idle_conns` | YAML | `10` / `5` | `database/sql` pool sizing |
| `databases.<alias>.conn_max_idle_time` | YAML | `5m` | idle connection lifetime |

The "Default" column lists the hardcoded constant used when a per-DB field is unset — there is no `defaults` block. Config loading adds validation: each database needs a known `driver` and a non-empty `url`; unknown drivers fail fast at startup.

## Risks / Trade-offs
- **No statement allowlist** → relied on `READ ONLY` transaction. *Mitigation:* strongest guarantee for Postgres; document that `readonly: false` lifts it deliberately and is the user's opt-in.
- **Stored functions with side effects** → blocked by `READ ONLY` tx in Postgres (writes fail), but volatile read functions still run. *Mitigation:* documented limitation; acceptable for dev assist.
- **New dependencies (`pgx`, `yaml.v3`)** → larger binary, but both are pure Go → still single static binary, no CGo.
- **`EXPLAIN ANALYZE` executes the query** → in restricted mode still runs under the read-only tx, so cannot mutate; cost is real query execution time. *Mitigation:* bounded by `query_timeout`.
- **Multi-database alias collisions / empty-alias ambiguity** → *Mitigation:* registry validates unique aliases at load and returns explicit errors for ambiguous empty-alias resolution.

## Testing

### Scenario

Unit tests (table-driven, `internal/tools` + `internal/db`):

- **Config parse & per-field fallback**: a database entry omitting optional fields resolves them to hardcoded constants (e.g. `readonly == true`, `query_timeout == 30s`); an entry setting `readonly: false` overrides that constant.
- **Registry resolution**: empty alias resolves to sole DB; ambiguous (multiple DBs, empty alias) returns an error; unknown alias returns a descriptive error.
- **Readonly enforcement (fake dialect)**: a stub `Dialect` asserts `ExecQuery` opens `BeginTx` with `ReadOnly:true` when `cfg.Readonly=true` and a plain `BeginTx` when `false`.
- **Object/Details/Explain SQL builders**: golden-string tests over generated SQL for given schema/name/format inputs.
- **execute_query row cap**: stub returning 250 rows is truncated to `row_limit`.

Integration tests live in `internal/db/postgres` and are guarded by the `SQLDB_MCP_TEST_POSTGRES_URL` connection string — a libpq keyword/value DSN that the `pgx` stdlib driver accepts directly. A small `testenv` test helper loads the repo-root `.env` into the process env (only for keys not already set, so an explicit `export` or CI var still wins) so plain `go test` works; if the URL is still absent the suite `t.Skip`s with a clear message. Each test connects via the real postgres dialect, builds an idempotent scratch fixture (`sqldb_mcp_test.t_item`) dropped in `t.Cleanup`, then covers `list_objects` (presence + type filter + unsupported-type error), `get_object_details` (columns/constraint/index), `execute_query` (read with row cap, bind params, write rejected by the read-only transaction), and `explain_query` (text + JSON + `analyze` under the read-only tx).

## Migration Plan

1. Add `internal/db` registry + `Dialect` interface and `internal/db/postgres` implementation.
2. Extend `internal/config` with the YAML loader and validation; keep env path intact.
3. Add the four tool handlers in `internal/tools`; wire `server.New` to build and pass the registry.
4. Add `--config` flag to `main.go`.
5. Update README (config format, tools, multi-DB example).
6. No rollout concern — the server is a local dev tool; `ping` and env config keep working unchanged. Rollback = revert to `ping`-only scaffold.

## Open Questions
- [x] Does `execute_query` accept bind args, or only literal SQL? **Yes.** `execute_query` accepts an optional `args` array, bound as `$1`/`$2` parameters. LLMs that cannot emit bind params still work by inlining values; clients that can bind get injection-safe execution.
- [x] Are the four SQL tools generic enough to cover MySQL/MariaDB and SQLite? (`list_databases` excluded — no SQL, DB-agnostic by nature.) **Yes**, the tool *taxonomy* is generic; per-engine mechanics absorbed by the `Dialect`. No tool-contract change needed to add another RDBMS — only a new dialect package that normalizes results into shared types. Two parameters are mildly Postgres-leaning, handled by existing error-on-unsupported behavior:
  - `list_objects` `type=extension` is Postgres-only. Supported types are dialect-defined; an unsupported type returns an error listing what the dialect supports.
  - `explain_query` `format` and `analyze` map to engine-native forms. The dialect maps requested `format`/`analyze` onto whatever it supports, and errors when a requested option is unavailable.
  - `get_object_details` uses `information_schema` on PG/MySQL but `PRAGMA` on SQLite — fully hidden inside the dialect.
  - Read-only enforcement is dialect-owned (D3): read-only transaction on PG/MySQL, read-only connection on SQLite.
- [x] Do we expose a `list_databases` tool for runtime alias discovery? **Yes.** Returns each configured alias with its `driver` and `readonly` flag; the connection `url` is never returned. See D7.

## Todo

### 1. Dependencies & scaffold
- [x] 1.1 Add `github.com/jackc/pgx/v5` (stdlib adapter) and `gopkg.in/yaml.v3` to `go.mod`; run `go mod tidy`
- [x] 1.2 Verify `CGO_ENABLED=0 go build ./cmd/sqldb-mcp` still produces a CGo-free binary

### 2. Database layer — `internal/db`
- [x] 2.1 Define `DatabaseConfig` struct (driver, url, readonly pointer, max_open/idle conns, conn_max_idle_time, query_timeout, row_limit) with YAML tags
- [x] 2.2 Define `Config` root struct (`databases map[string]DatabaseConfig`); no `defaults` block — apply hardcoded fallback constants to unset per-DB fields at load
- [x] 2.3 Define the `Dialect` interface (`Name`, `OpenDB`, `ListObjects`, `GetObjectDetails`, `ExecQuery`, `Explain`) and the result types (`Object`, `ObjectDetails`, `Column`, `Constraint`, `Index`, `QueryResult`, `ExplainResult`)
- [x] 2.4 Implement `Registry`: register dialects by name, open pools at startup with ping, `Resolve(alias)` returning `(*sql.DB, Dialect, DatabaseConfig)`, empty-alias → sole-DB-or-error logic, alias validation
- [x] 2.5 Add a password-redacting helper for connection-error logging

### 3. Postgres dialect — `internal/db/postgres`
- [x] 3.1 Implement `OpenDB`: register/lookup pgx stdlib driver, `sql.Open`, apply pool sizing + idle lifetime, `PingContext`
- [x] 3.2 Implement `ListObjects` via `information_schema.tables`/`sequences` and `pg_extension`; validate `type` ∈ {table,view,sequence,extension}
- [x] 3.3 Implement `GetObjectDetails` via `information_schema.columns`, `table_constraints`+`key_column_usage`, and `pg_indexes`
- [x] 3.4 Implement `ExecQuery`: `BeginTx(ctx, TxOptions{ReadOnly: cfg.Readonly})`, `QueryContext` with bound args, enforce `row_limit`, rollback; return columns + rows
- [x] 3.5 Implement `Explain`: build `EXPLAIN (FORMAT text|json[, ANALYZE]) <query>`, map requested `format`/`analyze` to native options, run under readonly tx when restricted, bounded by `query_timeout`

### 4. Configuration — `internal/config`
- [x] 4.1 Extend `Load` to resolve the config file path (`--config` → `SQLDB_MCP_CONFIG` → auto `./sqldb-mcp.yaml` → none) and parse it into `db.Config`
- [x] 4.2 Add config validation: known drivers, non-empty `url`, unique aliases; return descriptive errors
- [x] 4.3 Add hardcoded fallback constants (`readonly:true`, `query_timeout:30s`, `max_open_conns:10`, `max_idle_conns:5`, `row_limit:100`) used when a per-DB field is unset
- [x] 4.4 Keep existing env-based server identity/transport/log loading unchanged

### 5. Tool handlers — `internal/tools`
- [x] 5.1 Add `list_objects` input/output structs + handler dispatching to `Dialect.ListObjects`; `database` arg required-via-registry
- [x] 5.2 Add `get_object_details` input/output structs + handler
- [x] 5.3 Add `execute_query` input/output structs (query, optional args, columns+rows) + handler using `Dialect.ExecQuery`
- [x] 5.4 Add `explain_query` input/output structs (query, format, analyze) + handler using `Dialect.Explain`
- [x] 5.5 Add `list_databases` input/output structs (`alias`, `driver`, `readonly`; no `url`) + handler returning registry entries
- [x] 5.6 Update `Register` signature to accept the `*db.Registry`; keep `ping`

### 6. Wiring — `cmd/sqldb-mcp`, `internal/server`
- [x] 6.1 Add `--config` flag in `main.go`; load db config, build registry, pass to `server.New`
- [x] 6.2 Update `server.New` to build and close the registry and pass it to `tools.Register`; update server `Instructions` to describe the SQL tools and `list_databases`, and list configured aliases

### 7. Tests
- [x] 7.1 Config load + validation unit tests (per-field constant fallback when omitted, unknown driver, missing url, unique aliases)
- [x] 7.2 Registry resolution tests (explicit alias, empty→sole, empty→ambiguous error, unknown alias error) using a fake dialect
- [x] 7.3 Readonly-enforcement test with a fake dialect asserting `TxOptions.ReadOnly` is set per `cfg.Readonly`
- [x] 7.4 SQL-builder golden tests for postgres `ListObjects`/`GetObjectDetails`/`Explain` generated SQL
- [x] 7.5 `execute_query` row-cap test with a stub returning > `row_limit` rows
- [x] 7.6 Tool handler unit tests (fake dialect) for the SQL tools incl. error paths
- [x] 7.7 `list_databases` unit test asserting every configured alias is returned with `driver`+`readonly` and that no `url`/credential appears in the output
- [x] 7.8 Postgres integration tests in `internal/db/postgres`, guarded by `SQLDB_MCP_TEST_POSTGRES_URL` (skip with a clear message when unset)
  - [x] 7.8.1 Add a repo-root `.env` loader helper (`testenv_test.go`, no new dependency): parse `KEY=VALUE` / `KEY='VALUE'` lines from `<repo>/.env`, stripping surrounding quotes, and set each key into the process env only when not already set
  - [x] 7.8.2 Add a fixture helper: open the DSN via the real postgres dialect, idempotently create a scratch schema+table, seed a known row count, and `DROP` it in `t.Cleanup`
  - [x] 7.8.3 `list_objects`: scratch table appears; `type:"table"` filter returns it; an unsupported `type` returns the dialect's supported-types error
  - [x] 7.8.4 `get_object_details`: assert columns (name/type/nullability), the primary-key constraint, and at least one index match the fixture
  - [x] 7.8.5 `execute_query` read: `SELECT` returns the seeded rows, truncated at `row_limit`; a bind-param query returns exactly the bound row, value never inlined
  - [x] 7.8.6 `execute_query` rejected write: with `readonly: true`, `DELETE`/`INSERT` fails with Postgres' read-only-transaction error and no rows change
  - [x] 7.8.7 `explain_query`: text plan non-empty; `format:"json"` output parses as JSON; `analyze:true` runs under the read-only tx and returns a plan
  - [x] 7.8.8 Run via `go test ./internal/db/postgres/...`; the env var may be overridden inline to point tests at another Postgres

### 8. Documentation
- [x] 8.1 Update `README.md`: add the tools, the YAML config format (per-database entries + hardcoded constant fallback, multi-database example), `--config`/`SQLDB_MCP_CONFIG`, readonly/row-limit/timeout behavior, and build note (pure-Go single binary)
- [x] 8.2 Add a sample `sqldb-mcp.example.yaml` at repo root
- [x] 8.3 Update the "Adding a new tool" / "Adding a new RDBMS" sections to document the `Dialect` interface
