# sqldb-mcp

A [Model Context Protocol](https://modelcontextprotocol.io) server scaffold in Go, built on the official Go MCP SDK (`github.com/modelcontextprotocol/go-sdk/mcp`).

The project exposes a small set of SQL-database tools — schema introspection, read-only query execution, and query-plan explanation — over a driver abstraction with a PostgreSQL implementation built on the pure-Go `pgx` driver. It serves several databases from one server, configured by a YAML file, and ships as a single static binary with no CGo or native runtime dependencies.

## Layout

```
sqldb-mcp/
  cmd/sqldb-mcp/main.go              entrypoint: flags, logging, transport selection
  internal/config/config.go          env-driven server config + YAML database config loading
  internal/config/databases.go       parse + validate the database YAML, apply fallback constants
  internal/db/db.go                  Dialect interface, result types, Registry, config structs
  internal/db/postgres/postgres.go   PostgreSQL dialect (information_schema / EXPLAIN SQL)
  internal/server/server.go          builds the *mcp.Server, the Registry, and wires tools in
  internal/tools/tools.go            tool registration + SQL tool handlers
  internal/tools/tools_test.go       table-driven unit tests
```

## Configuration

All settings come from environment variables with sensible defaults:

| Variable              | Default          | Description                                                                                                        |
| --------------------- | ---------------- | ------------------------------------------------------------------------------------------------------------------ |
| `MCP_SERVER_NAME`     | `sqldb-mcp`      | Server implementation name advertised in the MCP initialize payload and the `ping` tool.                           |
| `MCP_SERVER_VERSION`  | `v0.1.0`         | Server implementation version.                                                                                     |
| `MCP_HTTP_ADDR`       | `127.0.0.1:8080` | Address used when running the streamable-HTTP transport. Defaults to loopback; set `:8080` for remote access.      |
| `MCP_LOG_LEVEL`       | `info`           | One of `debug`, `info`, `warn`, `error`.                                                                           |

Server identity/transport/log stay env-driven. Database connections are configured by a YAML file (see [Databases](#databases)).

## Tools

Each SQL tool takes a `database` argument holding a configured alias; an empty `database` resolves to the sole configured database.

| Tool                | Description                                                                                          |
| ------------------- | ---------------------------------------------------------------------------------------------------- |
| `list_databases`    | List configured aliases with their `driver` and `readonly` flag; never exposes connection URLs.      |
| `list_objects`      | List tables, views, sequences, and extensions, optionally filtered by `schema` and/or `type`.        |
| `get_object_details`| Return columns, constraints, and indexes for one table or view.                                      |
| `execute_query`     | Run a SQL `query` (with optional bind `args`) and return columns + rows, capped by `row_limit`.      |
| `explain_query`     | Run `EXPLAIN` for a query; optional `format` (`text`/`json`) and `analyze` to execute while planning. |
| `ping`              | Health check; returns server build info and the current time.                                        |

`execute_query` is read-only by default: it runs inside a read-only transaction so PostgreSQL rejects any write (including writes inside functions). Set `readonly: false` on a database to opt into writes.

## Databases

Configure databases in a YAML file. Resolution order: `--config <path>` flag, then `SQLDB_MCP_CONFIG` env, then an auto-loaded `./sqldb-mcp.yaml` if present. With no config the server still starts (empty registry; `ping` and `list_databases` work, other SQL tools return a clear error).

`${VAR}` tokens in scalar values are replaced with the value of the environment variable `VAR` after the YAML is parsed, so secrets stay out of the checked-in file. An unset variable resolves to an empty string; if that empties a required field (e.g. `url`), startup rejects it. The notation has no default-value or escape syntax.

```yaml
# sqldb-mcp.yaml — every setting lives on its database entry; no defaults block.
databases:
  primary:                       # alias used as the `database` tool argument
    driver: postgres
    url: postgresql://user:${DB_PASSWORD}@host:5432/appdb?sslmode=disable   # secret from env
    readonly: true               # default true; tools refuse writes
    max_open_conns: 10
    max_idle_conns: 5
    conn_max_idle_time: 5m
    query_timeout: 30s
    row_limit: 100
  warehouse:
    driver: postgres
    url: postgresql://user:${DB_PASSWORD}@host:5432/warehouse?sslmode=disable
    readonly: false             # unrestricted: tools may run write statements
```

Omitted optional fields fall back to hardcoded constants (not an inherited defaults block):

| Field                | Default | Notes                                            |
| -------------------- | ------- | ------------------------------------------------ |
| `readonly`           | `true`  | forces read-only transactions                    |
| `query_timeout`      | `30s`   | per-query context timeout                        |
| `row_limit`          | `100`   | caps `execute_query` rows                        |
| `max_open_conns`     | `10`    | `database/sql` pool sizing                       |
| `max_idle_conns`     | `5`     | `database/sql` pool sizing                       |
| `conn_max_idle_time` | `5m`    | idle connection lifetime                         |

Unknown `driver` values and missing `url`s are rejected at startup. See [`sqldb-mcp.example.yaml`](sqldb-mcp.example.yaml).

## Run

Stdio (default; what MCP clients spawning the binary use):

```
go run ./cmd/sqldb-mcp
```

Streamable HTTP:

```
# Address given directly on the command line:
go run ./cmd/sqldb-mcp --http :8080

# Or pull the address from the environment by passing the literal "env" value.
# MCP_HTTP_ADDR is consulted ONLY when --http env is passed; without --http the
# server runs in stdio mode regardless of MCP_HTTP_ADDR.
MCP_HTTP_ADDR=:8080 go run ./cmd/sqldb-mcp --http env
```

The default `MCP_HTTP_ADDR` is `127.0.0.1:8080` (loopback only). To expose the server on all interfaces, set `MCP_HTTP_ADDR=:8080` (equivalent to `0.0.0.0:8080`) or pass `--http :8080` directly. Make sure you understand the security implications before binding to a non-loopback address.

SIGINT/SIGTERM trigger a clean shutdown of the HTTP server.

### Flags

```
go run ./cmd/sqldb-mcp --help
```

- `--http <addr>` — serve the streamable-HTTP transport on `<addr>`; absent means stdio. Pass `--http env` to use the value of `MCP_HTTP_ADDR` (this is the only code path that reads `MCP_HTTP_ADDR`).
- `--config <path>` — path to the database YAML config; overrides `SQLDB_MCP_CONFIG` and the auto-loaded `./sqldb-mcp.yaml`.

Logs go to stderr as text so the stdio JSON-RPC stream on stdout is uncontaminated.

## MCP client configuration

The server speaks stdio when spawned by an MCP client, so no `--http` flag is needed in these configs.

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

During development you can substitute `go run ./cmd/sqldb-mcp` for the built binary:

```json
{
  "mcpServers": {
    "sqldb-mcp": {
      "command": "go",
      "args": ["run", "/absolute/path/to/sqldb-mcp/cmd/sqldb-mcp"]
    }
  }
}
```

## Build / vet / test

```
go build ./...
go vet ./...
go test ./...
```

PostgreSQL integration tests run against a real database selected by `SQLDB_MCP_TEST_POSTGRES_URL` (a libpq keyword/value DSN). A test helper auto-loads a repo-root `.env`, so `go test ./...` works once `.env` contains the variable; an explicit export or CI secret still wins. When unset the suite skips:

```
go test ./internal/db/postgres/...
```

The build is CGo-free — `CGO_ENABLED=0 go build ./cmd/sqldb-mcp` produces a single statically-linked binary with no native runtime dependencies.

## Adding a new tool

1. Add input/output structs and a typed handler in `internal/tools/tools.go` (or a new file in the same package). Resolve the database via the `db.Registry` passed to `tools.Register` and dispatch to its `Dialect`; handlers stay RDBMS-agnostic.
2. Register it inside `tools.Register` with one line:

   ```go
   mcp.AddTool(s, &mcp.Tool{Name: "your_tool", Description: "..."}, yourHandler)
   ```

3. Add table-driven tests next to the handler.

## Adding a new RDBMS

All engine-specific behavior lives behind the `db.Dialect` interface in `internal/db`. Add a new package (e.g. `internal/db/mysql`) implementing `Dialect` — `OpenDB`, `ListObjects`, `GetObjectDetails`, `ExecQuery`, `Explain` — then register it in `server.buildRegistry`:

```go
reg, err := db.NewRegistry(postgres.New(), mysql.New())
```

No tool or handler code changes. Read-only enforcement is dialect-owned: PostgreSQL/MySQL use a read-only transaction; map `readonly: true` onto whatever the engine supports.

## Module path

The module path is `github.com/dewadg/sqldb-mcp`. Change it to suit your own org with:

```
go mod edit -module <your/path>
```

and update the internal imports in `cmd/sqldb-mcp/main.go` and `internal/server/server.go` (these are the only files that import the module path).
