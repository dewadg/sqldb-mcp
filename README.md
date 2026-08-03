# sqldb-mcp

A [Model Context Protocol](https://modelcontextprotocol.io) server scaffold in Go, built on the official Go MCP SDK (`github.com/modelcontextprotocol/go-sdk/mcp`).

The project ships with a sample `ping` health-check tool so the scaffold builds, runs, and is testable today. Real SQL-database tools can be added under `internal/tools` later (see [Adding a new tool](#adding-a-new-tool)).

## Layout

```
sqldb-mcp/
  cmd/sqldb-mcp/main.go        entrypoint: flags, logging, transport selection
  internal/config/config.go    env-driven configuration with validation
  internal/server/server.go    builds the *mcp.Server and wires tools in
  internal/tools/tools.go      tool registration + sample ping tool
  internal/tools/tools_test.go table-driven unit tests
```

## Configuration

All settings come from environment variables with sensible defaults:

| Variable              | Default          | Description                                                                                                        |
| --------------------- | ---------------- | ------------------------------------------------------------------------------------------------------------------ |
| `MCP_SERVER_NAME`     | `sqldb-mcp`      | Server implementation name advertised in the MCP initialize payload and the `ping` tool.                           |
| `MCP_SERVER_VERSION`  | `v0.1.0`         | Server implementation version.                                                                                     |
| `MCP_HTTP_ADDR`       | `127.0.0.1:8080` | Address used when running the streamable-HTTP transport. Defaults to loopback; set `:8080` for remote access.      |
| `MCP_LOG_LEVEL`       | `info`           | One of `debug`, `info`, `warn`, `error`.                                                                           |

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

## Adding a new tool

1. Add input/output structs and a typed handler in `internal/tools/tools.go` (or a new file in the same package). The handler follows the `mcp.ToolHandlerFor[In, Out]` signature.
2. Register it inside `tools.Register` with one line:

   ```go
   mcp.AddTool(s, &mcp.Tool{Name: "your_tool", Description: "..."}, yourHandler)
   ```

3. Add table-driven tests next to the handler.

## Module path

The module path is `github.com/dewadg/sqldb-mcp`. Change it to suit your own org with:

```
go mod edit -module <your/path>
```

and update the internal imports in `cmd/sqldb-mcp/main.go` and `internal/server/server.go` (these are the only files that import the module path).
