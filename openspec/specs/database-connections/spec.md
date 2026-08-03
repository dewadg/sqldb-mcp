# database-connections

## Purpose

Configuration, lifecycle, and discovery of the named databases served by one
sqldb-mcp instance, behind a driver abstraction. Defines how databases are
configured (YAML, per-entry, hardcoded fallback), how the config file is
resolved, how aliases are selected, how the registry is discovered via
`list_databases`, and the single static pure-Go binary constraint.

## Requirements

### Requirement: Database registry configured from a YAML file

The system SHALL load its database registry from a YAML configuration file containing a `databases` map keyed by alias. There is no shared defaults block: each entry specifies at least a `driver` and a connection `url`, plus any optional fields it wants to set. An omitted optional field SHALL resolve to a hardcoded constant, not to an inherited block.

#### Scenario: Omitted fields fall back to hardcoded constants

- **WHEN** a database entry specifies only `driver` and `url`
- **THEN** its optional fields (e.g. `readonly`, `query_timeout`) resolve to the hardcoded constants rather than any inherited block

#### Scenario: Set field overrides the constant

- **WHEN** a database `primary` declares `readonly: false`
- **THEN** the resolved configuration for `primary` has `readonly == false` while an entry omitting it resolves `readonly == true`

#### Scenario: Unknown driver rejected at startup

- **WHEN** a database entry declares `driver: mysql` and no MySQL dialect is registered
- **THEN** configuration loading returns an error naming the alias and the unknown driver, and the server does not start

#### Scenario: Missing url rejected

- **WHEN** a database entry omits `url`
- **THEN** configuration loading returns an error naming the alias, and the server does not start

### Requirement: Config file resolution order

The system SHALL resolve the config file path in this order: a `--config` flag, then the `SQLDB_MCP_CONFIG` environment variable, then an auto-loaded `./sqldb-mcp.yaml` if it exists.

#### Scenario: Server starts with no config

- **WHEN** no config source is available
- **THEN** the server starts with an empty database registry and the `ping` tool still works

#### Scenario: Flag takes precedence over env

- **WHEN** both `--config ./a.yaml` and `SQLDB_MCP_CONFIG=./b.yaml` are set
- **THEN** the system loads `./a.yaml`

### Requirement: Alias-based database selection

Every SQL tool SHALL accept a `database` argument holding a registry alias and SHALL resolve it to a connection before executing.

#### Scenario: Explicit alias resolves

- **WHEN** a tool is called with `database: "primary"` and `primary` is configured
- **THEN** the tool executes against the `primary` connection

#### Scenario: Empty alias resolves to the sole database

- **WHEN** a tool is called with an empty `database` argument and exactly one database is configured
- **THEN** the tool executes against that single database

#### Scenario: Ambiguous empty alias errors

- **WHEN** a tool is called with an empty `database` argument and more than one database is configured
- **THEN** the tool returns an error instructing the caller to specify one of the configured aliases

#### Scenario: Unknown alias errors

- **WHEN** a tool is called with `database: "nope"` and no such alias exists
- **THEN** the tool returns an error listing the configured aliases

### Requirement: list_databases tool

The system SHALL expose a `list_databases` tool taking no arguments that returns every configured database alias along with its `driver` and `readonly` flag. The result MUST NOT include the connection `url` or any credential.

#### Scenario: Lists all configured databases

- **WHEN** `list_databases` is called and the registry holds `primary` (postgres, readonly) and `warehouse` (postgres, not readonly)
- **THEN** the result contains one entry per alias, each carrying `alias`, `driver: "postgres"`, and the matching `readonly` value

#### Scenario: Credentials are not leaked

- **WHEN** `list_databases` is called
- **THEN** no entry contains a `url` field or any part of the connection string

#### Scenario: Empty registry returns empty list

- **WHEN** `list_databases` is called and no databases are configured
- **THEN** the result is an empty list

### Requirement: Single static pure-Go binary

The server SHALL build as a single statically-linkable binary with no CGo and no native runtime dependencies, using a pure-Go database driver.

#### Scenario: Build produces a CGo-free binary

- **WHEN** the project is built with `CGO_ENABLED=0 go build ./cmd/sqldb-mcp`
- **THEN** the build succeeds and links no C libraries

### Requirement: Connection pool sizing and timeout

The system SHALL apply configurable pool limits (`max_open_conns`, `max_idle_conns`, `conn_max_idle_time`) and a per-query `query_timeout` to every database connection, resolving unset fields to hardcoded constants.

#### Scenario: Constants applied when unspecified

- **WHEN** a database entry omits pool and timeout fields
- **THEN** the connection uses the hardcoded constants (e.g. `query_timeout: 30s`, `max_open_conns: 10`)
