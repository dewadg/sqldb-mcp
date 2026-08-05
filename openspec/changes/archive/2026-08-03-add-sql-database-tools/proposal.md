## Why

sqldb-mcp ships only a `ping` health-check tool today — no actual SQL capability. To serve as a lean, universal SQL-DB MCP for development assistance it must introspect schemas, run read-only queries, and explain query plans. This change adds the four core SQL tools, a driver abstraction for multiple RDBMS (Postgres first), and a configuration model that serves several databases from one server.

## What Changes

- Add the SQL tools `list_objects`, `get_object_details`, `execute_query`, and `explain_query` — each takes a `database` alias to select the target — plus `list_databases`, which lists the configured aliases so clients can discover targets at runtime.
- Add a `database/sql`-based driver abstraction with a Postgres implementation built on the pure-Go `pgx` driver, keeping a single static binary with no CGo or native runtime deps.
- Add multi-database configuration via a YAML config file (path from `--config` flag or `SQLDB_MCP_CONFIG` env, auto-loaded when a default `sqldb-mcp.yaml` exists). Each entry names a database with its driver, DSN, pool limits, and a read-only flag.
- Enforce read-only by default for `execute_query` (overridable per database) so the dev-assist surface is safe.
- Existing env-based server config (name/version/transport/log) is unchanged and additive; `ping` tool is retained.

## Capabilities

### New Capabilities

- `database-connections`: configuration and connection management for multiple named databases behind a driver abstraction, exposed to clients via a `list_databases` discovery tool
- `object-introspection`: `list_objects` and `get_object_details` tools
- `query-execution`: `execute_query` tool with read-only enforcement
- `query-explanation`: `explain_query` tool

### Modified Capabilities

<!-- None — this is the first feature change; no prior specs exist. -->

## Non-goals

- Write-path / mutation tools (INSERT/UPDATE/DDL) — dev-assist stays read-only.
- Support for RDBMS other than Postgres. The driver abstraction is added, but only Postgres is implemented in this change.
- Production-grade connection governance: replication, HA, failover, or authenticated pooling.
- Schema diffing, migrations, data export/import, or query-result paging beyond a capped row limit.

## Impact

- New packages: `internal/db` (registry + driver interface), `internal/db/postgres` (dialect queries), config-file loading in `internal/config`, four handlers under `internal/tools`.
- New dependencies: `github.com/jackc/pgx/v5` (pure-Go Postgres) and a YAML decoder (`gopkg.in/yaml.v3`).
- `server.New` / `tools.Register` accept a DB registry; `main.go` gains a `--config` flag.
- README updated with the new tools, the config-file format, and multi-database examples.
