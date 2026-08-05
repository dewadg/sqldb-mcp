## Context

sqldb-mcp is a Go MCP server scaffold built on the official `github.com/modelcontextprotocol/go-sdk` (v1.7.0). It runs stdio or streamable-HTTP, loads server identity from env, and exposes a single `ping` tool via typed `mcp.ToolHandlerFor[In, Out]` handlers registered in `internal/tools`. There is no SQL capability yet.

The target shape is a **lean, universal SQL-DB MCP for development assistance**: introspect objects, run read-only queries, and explain plans. It must support multiple RDBMS later (Postgres now), manage several databases from one server, and ship as a single static binary with no CGo or native runtime dependencies.

Reference: `crystaldba/postgres-mcp` (Python/FastMCP) defines a similar four-tool surface and validates it as a useful dev-assist API. We reuse the tool taxonomy but not the implementation; see decisions below.

## Goals / Non-Goals

**Goals:**

- The SQL tools (`list_objects`, `get_object_details`, `execute_query`, `explain_query`) — each selecting a database by alias — plus `list_databases` for alias discovery.
- A `Dialect` abstraction so a new RDBMS is added by implementing one package, not by editing tools.
- One server, many databases, configured by a YAML file.
- Read-only safety by default, enforced as a hard server-side guarantee.
- Single pure-Go binary.

**Non-Goals:**

- Mutation/DDL tooling, migration, schema diffing, data export/paging beyond a capped row count.
- RDBMS other than Postgres in this change (abstraction added, only Postgres implemented).
- Production connection governance (HA, failover, auth/pool tuning services).

## Decisions

### D1. `database/sql` + pure-Go `pgx` driver

Use Go's standard `database/sql` as the connection layer and register `pgx` (`github.com/jackc/pgx/v5/stdlib`) as the Postgres driver.

- *Why over pgx-native API:* `database/sql` is RDBMS-agnostic; swapping to MySQL/SQLite later means importing a different driver and adjusting dialect SQL, not rewriting the pool/handler code.
- *Why over GORM/sqlx:* this is a thin metadata + read tool; an ORM adds weight and hides the exact SQL needed for `information_schema`/`EXPLAIN`.
- *Alternatives considered:* `lib/pq` (older, less maintained), pgx-native pool (faster but couples every tool to pgx types, hurting multi-RDBMS goals).

### D2. `Dialect` interface owns all RDBMS-specific behavior

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

- *Why:* isolates dialect SQL in one package (`internal/db/postgres`); adding MySQL = new package + one registry entry.
- *Alternative:* a giant switch per tool — rejected; scatters RDBMS logic across tools.

### D3. Read-only enforced by `sql.TxOptions{ReadOnly: true}` + context timeout, not a SQL parser

For every `execute_query` (and `explain … ANALYZE`), open the transaction with `ReadOnly: true`. Postgres refuses any write inside a `READ ONLY` transaction — including writes inside functions — so this is a single strong guarantee rather than a statement-allowlist parser (the reference repo's `SafeSqlDriver` carries a pglast AST allowlist; we deliberately skip that).

- *Why:* pulling a full SQL parser into Go is heavy and against the lean goal; the server-side `READ ONLY` transaction is a stronger and simpler wall for an AI-facing dev tool.
- *Trade-off:* we cannot permit specific writes even if a user opts in via `readonly: false` — that is handled by simply *not* opening a read-only transaction when the database is configured unrestricted (see D5).
- Per-query timeout is applied via `context.WithTimeout` (default 30s, configurable).
- *Dialect-owned mechanism:* the read-only *mechanism* is a dialect concern, not a tool concern. Postgres and MySQL/MariaDB enforce it via a read-only transaction (`sql.TxOptions{ReadOnly: true}` / `SET TRANSACTION READ ONLY`). SQLite has no per-transaction read-only mode, so the SQLite dialect opens the file with a read-only connection flag instead. The tool contract is unchanged; each dialect maps `readonly: true` onto whatever its engine supports.

### D4. Real protocol parameters, never string interpolation

`execute_query` accepts the SQL string plus optional `args []any`; queries are executed through `db.QueryContext` so values travel as bound parameters (`$1`, `$2`). Metadata and explain queries use the same path.

- *Why:* injection safety; the reference repo inlines literals via `SQL(...).format(Literal(p))`, which we avoid.

### D5. Configuration: YAML file for databases, env retained for server identity

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

- *Why YAML over env:* multiple databases, secrets in URLs, and pool tuning are unwieldy as flat env vars; YAML also documents itself.
- *No `defaults` block:* all configuration is per-database. There is no shared defaults layer to reason about; an omitted optional field resolves to a hardcoded constant (e.g. `readonly: true`, `query_timeout: 30s`), not an inherited block.
- *Default database:* when a tool is called with an empty `database` argument, the registry resolves the single configured database, or the first one if several exist, and otherwise errors. This keeps the common single-DB case ergonomic.
- *Password handling:* connection errors are logged with the URL's password redacted.

### D6. Schema exposed as tools, not MCP resources

`list_objects`/`get_object_details` are tools (request/response), not MCP resources.

- *Why:* broader client compatibility (per the reference repo's experience); tools are the simplest surface for an LLM to call.

### D7. `list_databases` discovery tool

Expose `list_databases` (no arguments) returning one entry per configured database: `{alias, driver, readonly}`. The connection `url` is deliberately omitted so credentials never leave the server.

- *Why a tool over Instructions-only:* an LLM can call it reliably and receive structured data; prose `Instructions` are advisory and easy to ignore or hallucinate past. The server still lists aliases in `Instructions` as a hint, but `list_databases` is the source of truth.

### D8. Integration tests load repo-root `.env`; `.env` is test-only

Postgres integration tests consume `SQLDB_MCP_TEST_POSTGRES_URL`. A tiny `testenv` helper reads `<repo>/.env` (`KEY=VALUE` / `KEY='VALUE'`, quotes stripped) and populates the process env for keys not already set, so `go test ./internal/db/postgres/...` runs with no manual `export` while an explicit env var or CI secret still takes precedence. `.env` is `.gitignore`d and never read by the server at runtime — runtime config is the YAML file (D5); the loader exists only under `*_test.go`.

- *Why over `godotenv`:* one ~15-line parser avoids a new dependency for a test-only need.
- *Why skip-on-unset over a build tag:* keeps `go test ./...` as the single command; absence is reported explicitly instead of silently not compiling.

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

## Configuration

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

- **No statement allowlist** → relied on `READ ONLY` transaction. *Mitigation:* this is the strongest guarantee for Postgres; document that `readonly: false` lifts it deliberately and is the user's opt-in.
- **Stored functions with side effects** → blocked by `READ ONLY` tx in Postgres (writes fail), but volatile read functions still run. *Mitigation:* documented limitation; acceptable for dev assist.
- **New dependencies (`pgx`, `yaml.v3`)** → larger binary, but both are pure Go → still single static binary, no CGo.
- **`EXPLAIN ANALYZE` executes the query** → in restricted mode it still runs under the read-only tx, so it cannot mutate; cost is real query execution time. *Mitigation:* bounded by `query_timeout`.
- **Multi-database alias collisions / empty-alias ambiguity** → *Mitigation:* registry validates unique aliases at load and returns explicit errors for ambiguous empty-alias resolution.

## Test Scenario

Unit tests (table-driven, `internal/tools` + `internal/db`):

- **Config parse & per-field fallback**: a database entry omitting optional fields resolves them to hardcoded constants (e.g. `readonly == true`, `query_timeout == 30s`); an entry setting `readonly: false` overrides that constant.
- **Registry resolution**: empty alias resolves to sole DB; ambiguous (multiple DBs, empty alias) returns an error; unknown alias returns a descriptive error.
- **Readonly enforcement (fake dialect)**: a stub `Dialect` asserts `ExecQuery` opens `BeginTx` with `ReadOnly:true` when `cfg.Readonly=true` and a plain `BeginTx` when `false`.
- **Object/Details/Explain SQL builders**: golden-string tests over generated SQL for given schema/name/format inputs.
- **execute_query row cap**: stub returning 250 rows is truncated to `row_limit`.

Integration tests live in `internal/db/postgres` and are guarded by the `SQLDB_MCP_TEST_POSTGRES_URL` connection string — a libpq keyword/value DSN (e.g. `host=... user=... password=... dbname=sqldb_mcp_test sslmode=disable`) that the `pgx` stdlib driver accepts directly. A small `testenv` test helper loads the repo-root `.env` into the process env (only for keys not already set, so an explicit `export` or CI var still wins) so plain `go test` works; if the URL is still absent the suite `t.Skip`s with a clear message. Each test connects via the real postgres dialect, builds an idempotent scratch fixture (`sqldb_mcp_test.t_item`) dropped in `t.Cleanup`, then covers `list_objects` (presence + type filter + unsupported-type error), `get_object_details` (columns/constraint/index), `execute_query` (read with row cap, bind params, write rejected by the read-only transaction), and `explain_query` (text + JSON + `analyze` under the read-only tx).

## Migration Plan

1. Add `internal/db` registry + `Dialect` interface and `internal/db/postgres` implementation.
2. Extend `internal/config` with the YAML loader and validation; keep env path intact.
3. Add the four tool handlers in `internal/tools`; wire `server.New` to build and pass the registry.
4. Add `--config` flag to `main.go`.
5. Update README (config format, tools, multi-DB example).
6. No rollout concern — the server is a local dev tool; `ping` and env config keep working unchanged. Rollback = revert to `ping`-only scaffold.

## Resolved Questions

- **Does `execute_query` accept bind args, or only literal SQL?** → Yes. `execute_query` accepts an optional `args` array, bound as `$1`/`$2` parameters. LLMs that cannot emit bind params still work by inlining values; clients that can bind get injection-safe execution. (Already specced under `query-execution`.)
- **Are the four SQL tools generic enough to cover MySQL/MariaDB and SQLite?** (`list_databases` is excluded — it has no SQL and is DB-agnostic by nature.) → Yes, the tool *taxonomy* is generic; per-engine mechanics are absorbed by the `Dialect`. No tool-contract change is needed to add another RDBMS — only a new dialect package that normalizes results into the shared types. Two parameters are mildly Postgres-leaning and are handled by the existing error-on-unsupported behavior:
  - `list_objects` `type=extension` is Postgres-only (MySQL closest analogue is a plugin; SQLite has none). The set of supported types is dialect-defined; an unsupported type returns an error listing what the dialect supports.
  - `explain_query` `format` and `analyze` map to engine-native forms (`EXPLAIN FORMAT=JSON` / `EXPLAIN ANALYZE` on MySQL 8/MariaDB; `EXPLAIN QUERY PLAN` on SQLite with no FORMAT/ANALYZE). The dialect maps requested `format`/`analyze` onto whatever it supports, and errors when a requested option is unavailable.
  - `get_object_details` uses `information_schema` on PG/MySQL but `PRAGMA` on SQLite — fully hidden inside the dialect.
  - Read-only enforcement is dialect-owned (see D3): read-only transaction on PG/MySQL, read-only connection on SQLite.
- **Do we expose a `list_databases` tool for runtime alias discovery?** → Yes. `list_databases` returns each configured alias with its `driver` and `readonly` flag; the connection `url` is never returned, so credentials stay server-side. See D7.
