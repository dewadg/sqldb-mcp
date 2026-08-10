# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project

sqldb-mcp — MCP server exposing SQL-database (PostgreSQL) tools over stdio or streamable-HTTP. Pure Go, no CGo, single static binary. Built on `github.com/modelcontextprotocol/go-sdk`.

## Commands

```bash
make build              # optimized static binary → ./bin/sqldb-mcp (injects version/commit/date via ldflags)
make run                # build + run stdio
make test               # go test ./...
make vet                # go vet ./...
go test ./...           # unit tests
go test -run TestName ./internal/tools/...        # single test
go test -run TestName -v ./internal/db/...        # single test, verbose
go test ./tests/e2e/...                           # e2e (skips unless SQLDB_MCP_TEST_POSTGRES_URL set; repo-root .env auto-loaded)
CGO_ENABLED=0 go build -o sqldb-mcp ./cmd/sqldb-mcp   # raw build (no ldflags)
./sqldb-mcp                                       # stdio (default)
./sqldb-mcp --http :8080                          # streamable-HTTP
./sqldb-mcp --config path/to/sqldb-mcp.yaml       # explicit DB config
```

## Architecture

**Transport & lifecycle** — `cmd/sqldb-mcp/main.go` is the only entrypoint. It loads env config (`config.Load`), resolves + loads the DB YAML (`config.ResolveDatabaseConfigPath` + `config.LoadDatabaseConfig`), builds the server+registry via `server.New`, then runs stdio or HTTP based solely on `--http`. Stdio blocks on stdin close; `isGracefulShutdown` treats the SDK's wrapped `ErrServerClosing`/`io.EOF`/`context.Canceled` as clean exits. Logs go to **stderr only** — stdout carries the JSON-RPC stream and must stay uncontaminated.

**Composition root** — `internal/server/server.go` (`server.New`): builds the `*mcp.Server`, constructs the `db.Registry`, registers all tools (`tools.Register`), owns the logger, and warns about DBs unavailable at startup. This is the only place that touches both logger and registry.

**Dialect abstraction** — `internal/db/db.go` defines the `Dialect` interface (`Name`, `OpenDB`, `ListObjects`, `GetObjectDetails`, `ExecQuery`, `Explain`) plus shared result types (`Object`, `ObjectDetails`, `QuerySpec/Result`, `ExplainSpec/Result`) and config types (`Config`, `DatabaseConfig`). The `Registry` owns pool lifecycle and maps alias → `{pool, dialect, cfg, connectErr}`. Tool handlers are dialect-agnostic; they dispatch through whatever dialect the alias resolves to.

**Registry tolerance** — `Registry.Open` distinguishes *structural* config errors (unknown driver, missing driver/url → fatal, abort startup) from *connect* failures (ping fails → recorded on the entry as `connectErr` with `pool=nil`, other DBs still open). Down DBs stay listed so `list_databases` reports `unavailable` and `Resolve` surfaces "database unavailable" instead of "unknown alias".

**Tool layer** — `internal/tools/tools.go`: each tool = typed `Input`/`Output` struct + handler closure + one `mcp.AddTool(...)` line in `Register`. Handlers call `resolve(reg, alias)` to get the pool + dialect + config. `effectiveReadonly`/`effectiveRowLimit` resolve per-entry values.

**Read-only enforcement** — the read-only **transaction** is the security wall, not the keyword sniff. `execute_query` opens a read-only tx by default (Postgres rejects all writes, including writes hidden in functions); `readonly: false` makes writes commit durably and report affected rows. `SELECT`/`WITH`/`VALUES`/`TABLE` → rollback (read path); everything else → write path. This keyword routing is a hint, not a control — keep the transaction as the guarantee.

**Config** — `internal/config/`: `config.go` (env vars: `MCP_SERVER_NAME`, `MCP_SERVER_VERSION`, `MCP_HTTP_ADDR`, `MCP_LOG_LEVEL`), `databases.go` (YAML load + `${VAR}` env substitution). Defaults are **per-entry hardcoded constants** (`DefaultReadonly=true`, `DefaultQueryTimeout=30s`, etc.) applied by `Config.ApplyDefaults` — there is no shared defaults block. An unset `${VAR}` resolves to empty; emptying a required field fails startup. Config path resolution order: `--config` flag → `SQLDB_MCP_CONFIG` env → auto-loaded `./sqldb-mcp.yaml`.

## Conventions

- **Adding a DB engine** — write one package implementing `Dialect` (e.g. `internal/db/mysql`), register it in `server.buildRegistry` (`db.NewRegistry(postgres.New(), mysql.New())`). No tool or handler changes. Read-only enforcement is dialect-owned — map `readonly: true` onto whatever the engine supports.
- **Adding a tool** — add `XxxInput`/`XxxOutput` structs + `xxxHandler` in `internal/tools`, resolve via `*db.Registry`, register with one `mcp.AddTool(...)` line in `Register`.
- **URLs never leak** — `db.RedactURL` before logging; `DatabaseInfo` (the `list_databases` output) deliberately omits the URL.
- **Tests** — Go unit/integration tests are table-driven. Handler tests use a fake `Dialect` + noop `sql.DB` (`tDialect`, `tPool`) so they run without a DB. E2E tests drive the real `*mcp.Server` over an in-memory MCP transport against live Postgres.

- Shipped/abandoned specs live in `specs/archives/` — historical, not part of the build.
