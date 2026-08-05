# sqldb-mcp

Give your AI assistant safe, read-only access to your PostgreSQL databases over the [Model Context Protocol](https://modelcontextprotocol.io).

sqldb-mcp is an MCP server that lets a client like Claude explore a database schema, run queries, and read query plans — without you pasting connection strings or results into a chat. You point it at one or more databases; your assistant calls the tools. Reads are read-only by default, so the assistant can look but not break anything.

- **One server, many databases** — connect several Postgres instances and let the assistant pick by alias.
- **Safe by default** — queries run inside a read-only transaction; writes are rejected unless you opt in per database.
- **Secrets stay out of config** — pull passwords from the environment with `${VAR}`.
- **Single static binary** — pure Go, no CGo, no native runtime dependencies.

## Quick start

1. Build the binary:

   ```
   CGO_ENABLED=0 go build -o sqldb-mcp ./cmd/sqldb-mcp
   ```

2. Describe your database in `sqldb-mcp.yaml` (next to the binary, or anywhere you point `--config`):

   ```yaml
   databases:
     primary:                                  # alias your assistant uses as the "database" argument
       driver: postgres
       url: postgresql://user:${DB_PASSWORD}@host:5432/appdb?sslmode=disable
   ```

3. Export the secret and run (stdio is the default — what MCP clients expect):

   ```
   DB_PASSWORD=secret ./sqldb-mcp
   ```

4. Point your client at it — see [Connect your AI client](#connect-your-ai-client).

## Connect your AI client

The server speaks stdio when spawned by a client, so no extra flags are needed.

**Claude Desktop** — `~/Library/Application Support/Claude/claude_desktop_config.json`:

```json
{
  "mcpServers": {
    "sqldb-mcp": {
      "command": "/absolute/path/to/sqldb-mcp",
      "args": []
    }
  }
}
```

**Claude Code** — project-local `.mcp.json`:

```json
{
  "mcpServers": {
    "sqldb-mcp": {
      "command": "/absolute/path/to/sqldb-mcp",
      "args": []
    }
  }
}
```

If you keep a config somewhere other than `./sqldb-mcp.yaml`, pass it with `args`:

```json
"args": ["--config", "/absolute/path/to/sqldb-mcp.yaml"]
```

## What your assistant can do

Every SQL tool takes a `database` argument. Leave it empty and the server targets the one database you configured; with several, the assistant must name one (call `list_databases` to discover them).

| Tool                | What it does                                                                              |
| ------------------- | ----------------------------------------------------------------------------------------- |
| `list_databases`    | Lists configured databases by alias with their driver and read-only flag. Never shows URLs. |
| `list_objects`      | Lists tables, views, sequences, and extensions, optionally filtered by schema and/or type. |
| `get_object_details`| Returns columns, constraints, and indexes for one table or view.                          |
| `execute_query`     | Runs a SQL statement: returns columns + rows for `SELECT`/`WITH`/`VALUES`/`TABLE`, or the affected row count for writes (`INSERT`/`UPDATE`/`DELETE`/DDL). Optional bind parameters. |
| `explain_query`     | Runs `EXPLAIN`; optionally `analyze` to execute while planning.                           |
| `ping`              | Health check — server build info and the current time.                                    |

**Read-only by default.** `execute_query` runs inside a read-only transaction, so PostgreSQL refuses any write — including writes hidden inside functions. Set `readonly: false` on a database to let the assistant make changes: writes then **commit** (they are durable) and the result reports the affected row count. This is an explicit opt-in; `SELECT`s always roll back, so reads never persist side effects. Statement kind is routed by a lightweight keyword sniff (`SELECT`/`WITH`/`VALUES`/`TABLE` are reads; everything else is a write) — this is routing only, not a security control; the read-only transaction remains the wall. `explain_query ... analyze` honors the same rule, so analyzing a write also fails when restricted.

Results are capped (`row_limit`, default 100 rows) and every query is bounded by a timeout (`query_timeout`, default 30s).

## Configuring databases

Each database is one entry under `databases`, keyed by its alias. Optional fields you omit fall back to built-in defaults — there is no shared defaults block.

```yaml
databases:
  primary:
    driver: postgres
    url: postgresql://user:${DB_PASSWORD}@host:5432/appdb?sslmode=disable
    readonly: true          # default; refuses writes
    query_timeout: 30s
    row_limit: 100
    max_open_conns: 10
    max_idle_conns: 5
    conn_max_idle_time: 5m

  warehouse:                # a second database served from the same server
    driver: postgres
    url: postgresql://user:${DB_PASSWORD}@host:5432/warehouse?sslmode=disable
    readonly: false         # opt in: writes are allowed
```

| Field                | Default | Notes                                     |
| -------------------- | ------- | ----------------------------------------- |
| `driver`             | —       | Required. Currently `postgres`.           |
| `url`                | —       | Required. A libpq connection string/URL.  |
| `readonly`           | `true`  | `false` allows writes from `execute_query`. |
| `query_timeout`      | `30s`   | Per-query timeout.                        |
| `row_limit`          | `100`   | Caps rows returned by `execute_query`.    |
| `max_open_conns`     | `10`    | Connection-pool sizing.                   |
| `max_idle_conns`     | `5`     | Connection-pool sizing.                   |
| `conn_max_idle_time` | `5m`    | Idle connection lifetime.                 |

**Keeping secrets out of the file.** `${VAR}` in any value is replaced with the environment variable `VAR` after the YAML is parsed:

```yaml
url: postgresql://user:${DB_PASSWORD}@host:5432/appdb
```

There is no default-value or escape syntax. An unset variable resolves to an empty string; if that empties a required field like `url`, startup fails with a clear error rather than connecting with a broken DSN. See [`sqldb-mcp.example.yaml`](sqldb-mcp.example.yaml) for a full template.

**Where the server looks for the config**, in order: the `--config <path>` flag, the `SQLDB_MCP_CONFIG` environment variable, then an auto-loaded `./sqldb-mcp.yaml` if present. With none of these the server still starts — `ping` and `list_databases` work, and the other tools report that no database is configured.

Unknown drivers and missing URLs are rejected at startup.

## Running the server

**Stdio** (default — what MCP clients spawning the binary use):

```
./sqldb-mcp
```

**Streamable HTTP** (for remote or browser-based clients):

```
./sqldb-mcp --http :8080
```

Flags:

- `--config <path>` — path to the database YAML. Overrides `SQLDB_MCP_CONFIG` and the auto-loaded `./sqldb-mcp.yaml`.
- `--http <addr>` — serve streamable-HTTP on `<addr>`; omit for stdio. Pass `--http env` to read the address from `MCP_HTTP_ADDR`.

Server identity and logging come from the environment:

| Variable             | Default          | Meaning                                                                                          |
| -------------------- | ---------------- | ------------------------------------------------------------------------------------------------ |
| `MCP_SERVER_NAME`    | `sqldb-mcp`      | Name advertised to the client and the `ping` tool.                                               |
| `MCP_SERVER_VERSION` | `v0.1.0`         | Version advertised to the client.                                                                |
| `MCP_HTTP_ADDR`      | `127.0.0.1:8080` | HTTP address; used only when you pass `--http env`. Loopback by default — understand the risk before binding to `:8080`. |
| `MCP_LOG_LEVEL`      | `info`           | One of `debug`, `info`, `warn`, `error`.                                                         |

Logs go to stderr so the stdio JSON-RPC stream on stdout stays clean. SIGINT/SIGTERM shut the HTTP server down cleanly.

## For contributors

Built on the official Go MCP SDK (`github.com/modelcontextprotocol/go-sdk`). Layout: `cmd/sqldb-mcp` (entrypoint), `internal/config` (env + YAML config), `internal/db` (Dialect interface + registry), `internal/db/postgres` (PostgreSQL dialect), `internal/server` (wiring), `internal/tools` (tool handlers).

```
go build ./...
go vet ./...
go test ./...
```

End-to-end tests in `tests/e2e` drive the real server over an in-memory transport against a live Postgres. They skip unless `SQLDB_MCP_TEST_POSTGRES_URL` is set (a repo-root `.env` is auto-loaded):

```
go test ./tests/e2e/...
```

The build is CGo-free: `CGO_ENABLED=0 go build ./cmd/sqldb-mcp` produces a single statically-linked binary.

**Add a database engine.** All engine-specific behavior sits behind the `db.Dialect` interface. Add a package implementing it (e.g. `internal/db/mysql`) and register it in `server.buildRegistry` — no tool or handler changes. Read-only enforcement is dialect-owned (Postgres/MySQL use a read-only transaction; map `readonly: true` onto whatever the engine supports).

**Add a tool.** Add typed input/output structs and a handler in `internal/tools`, resolve the database through the `*db.Registry`, and register it with one `mcp.AddTool(...)` line.

The module path is `github.com/dewadg/sqldb-mcp`; change it with `go mod edit -module <your/path>` and update the imports in `cmd/sqldb-mcp/main.go` and `internal/server/server.go`.
