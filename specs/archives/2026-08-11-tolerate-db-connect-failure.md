# Tolerate individual DB connect failure at startup

> Stop one unreachable database from killing the whole sqldb-mcp process; start with the reachable ones and mark the rest unavailable.

## Context
`Registry.Open` (`internal/db/db.go:251`) opens and pings every configured database in a single loop. On the first `dialect.OpenDB` failure it closes every pool already opened and returns an error. That error flows `server.buildRegistry` → `server.New` → `main.go:68`, which logs `"failed to build server"` and `os.Exit(1)`. So a single down host — routine in multi-DB setups — takes down the entire server, including the databases that *are* reachable, plus `ping` and `list_databases`. The desired behavior is the opposite of fail-fast: open what can be opened, keep going, and surface the broken alias as unavailable rather than aborting.

## Goals
- A connect failure (ping/network/auth error) on one or more databases does not stop the server from starting or serving the reachable ones.
- A down database is queryable in the only sense that matters: calling a SQL tool against it returns a clear "database unavailable" error with the connect-failure reason, instead of crashing or silently misbehaving.
- `list_databases` reports per-alias availability so the assistant can see what is reachable without trial-and-error.
- Startup logs each failed connect with its redacted URL and reason, so operators see what went wrong without the process dying.
- Existing behavior for structural config errors (unknown driver, missing driver/url) stays fail-fast — those are config bugs, not transient outages.

## Non-goals
- No background reconnection / health-check loop or retry backoff. This change is about startup tolerance only; a DB that comes up later still needs a process restart to be picked up. (Follow-up if wanted.)
- No per-database `required`/strict flag or global "fail-fast on connect" toggle. Tolerance is the new default, full stop.
- No change to transport, tool schemas (other than an additive `status` field on `list_databases` output), or the read-only/timeout policy.
- No mid-session liveness tracking: a pool that goes bad *after* a successful open still surfaces errors the way it does today (dialect-level query error).

## Flow
```mermaid
sequenceDiagram
  participant M as main
  participant S as server.New
  participant R as Registry.Open
  participant D as dialect.OpenDB
  M->>S: New(cfg, dbCfg, logger)
  S->>R: Open(ctx, cfg)
  R->>R: validate all entries (fatal on config error)
  loop per configured DB
    R->>D: OpenDB(ctx, entry)
    alt success
      D-->>R: pool
      R->>R: store entry (available)
    else connect failure
      D-->>R: error
      R->>R: store entry (unavailable, keep err)
    end
  end
  R-->>S: nil (even if some DBs failed)
  S->>S: log each unavailable alias (warn, redacted URL)
  S-->>M: server + registry
  M->>M: serve (ping/list_databases work; SQL tools gate on availability)
```

## Decisions

### D1: Fix lives in Registry.Open, not in main
- **Choice**: `Registry.Open` stops aborting on connect failures; it records the failure per-entry and returns `nil` as long as no *structural* validation error occurred.
- **Why**: Open owns pool lifecycle and is the single choke point every DB passes through. Tolerating there means `server.New` and `main` need no error-handling change to avoid the exit — the exit path simply never fires for a connect failure. Validation errors still return non-nil and still kill startup, which is the right call for a malformed config.
- **Alternatives**: Catching the error in `main` (rejected — by the time Open returns an error all reachable pools are already closed and lost; main would have to re-open, duplicating lifecycle logic). A wrapper "best-effort opener" around the registry (rejected — adds a layer for no gain; the registry is already the lifecycle owner).

### D2: Per-entry availability state on `entry`
- **Choice**: Add a `connectErr error` field to the unexported `entry` struct (`db.go:217`). `nil` means available; a non-nil value marks the alias unavailable and carries the reason. Failed entries still live in `entries` (so they show in `list_databases` and resolve with a clear error); their `pool` is `nil`.
- **Why**: Reuses the existing alias→entry map; no parallel "failed" map to keep in sync. `Resolve`, `ListDatabases`, and `Close` each get one cheap nil/err check. The reason is preserved end-to-end (tool error, log line) without re-deriving it.
- **Alternatives**: Dropping failed aliases from `entries` entirely (rejected — then `list_databases` hides them and `Resolve` returns a generic "unknown alias", so the assistant cannot tell a down DB from a typo). A separate `failed map[string]error` (rejected — two maps to keep coherent; the entry already exists).

### D3: `list_databases` gains an additive `status` field
- **Choice**: Extend `DatabaseInfo` (`db.go:210`) with `Status string` — `"available"` or `"unavailable"`. `ListDatabases` fills it from `entry.connectErr`.
- **Why**: The assistant needs a way to discover availability without probing every DB. An additive JSON field is non-breaking for existing clients; the existing `alias`/`driver`/`readonly` fields keep their positions and meaning.
- **Alternatives**: A separate `list_database_health` tool (rejected — splits the source of truth; the comment on `list_databases` already calls it authoritative). Omitting status and making callers infer from query errors (rejected — forces a failed query per down DB; defeats the goal).

### D4: Tolerant by default, no config flag
- **Choice**: Connect failures are always non-fatal. There is no opt-in/opt-out knob.
- **Why**: The user's ask is to fix the kill, not to make resilience configurable. A flag would add a config surface, tests, and docs for a behavior almost everyone wants on. Strict-startup needs, if they ever appear, can be a follow-up.
- **Alternatives**: A global `--strict-startup` / per-DB `required: true` flag (rejected — scope creep; can be added later without re-architecting).

### D5: Logging happens in `server.New`, not Registry.Open
- **Choice**: `Registry.Open` stays logger-free. `server.New` (which already holds the `*slog.Logger`) calls a new `Registry.Unavailable() map[string]error` after `Open` and logs each entry at warn with `RedactURL(cfg.URL)` + the reason.
- **Why**: The registry has no logger today and threading one in touches every caller. `server.New` is already the composition root that owns both the logger and the registry, so the warn naturally belongs there. Keeping Open pure (no I/O beyond DB) also makes it unit-testable without a logger stub.
- **Alternatives**: Passing the logger into `Open` (rejected — couples the db package to a specific logger and changes the Open signature). Returning an aggregated error from Open and letting main log it (rejected — reconnects the kill path to the error return; muddles "fatal validation error" from "informational connect failure").

## Configurations
None added. No new env vars, flags, YAML fields, or migrations. (A `status` JSON field appears on `list_databases` output — additive, not a config change.)

## Testing

### Pre-test requirements
- `go test ./internal/db/...` runs against the existing in-package `fakeDialect` (`db_test.go:41`); no live server needed for the core change.
- The `e2e` suite (`tests/e2e`) still needs `SQLDB_MCP_TEST_POSTGRES_URL` for its live cases; the new startup-tolerance e2e case does **not** need a live Postgres because it drives two in-process fake/unreachable DBs through `server.New`. Keep the `.env` loader as-is.

### Scenario
| Given | When | Then |
|-------|------|------|
| config with two DBs where `fake` dialect A opens and B's `OpenDB` returns an error | `Registry.Open(ctx, cfg)` | returns `nil`; A's entry is available, B's entry is unavailable with the err stored |
| registry has an unavailable entry "b" | `Resolve("b")` | returns a non-nil error naming "b" and the connect-failure reason; pool/dialect are nil |
| registry has an available entry "a" alongside unavailable "b" | `Resolve("a")` and `Resolve("")` (sole-DB case omitted) | "a" resolves normally with its real pool |
| registry has one available + one unavailable entry | `ListDatabases()` | returns both, available one `status:"available"`, failed one `status:"unavailable"`, sorted by alias |
| all configured DBs fail to connect | `Registry.Open` then `server.New` | both return no error; server starts; SQL tools return the per-DB unavailable error; `ping` and `list_databases` work |
| config has an unknown driver | `Registry.Open` | still returns a non-nil error (validation stays fatal); server does not start |
| entry is unavailable (nil pool) | `Registry.Close()` | no panic; close of available pools still joins errors as before |
| `server.New` built with a registry containing unavailable entries | build returns | logger emitted one warn line per unavailable alias with redacted URL + reason |

## Open Questions
- [x] Should a fully-failed config (every DB down) start the server? **Yes** — consistent with the existing "no config / empty registry" path: `ping` and `list_databases` stay useful, SQL tools return a clear error. Recorded in Goals/Scenarios.
- [x] Does `Resolve("")` (empty alias → sole DB) change? **No behavioral change to dispatch** — if the sole entry is unavailable, `Resolve` returns that entry's `connectErr` instead of its nil pool, so the tool surfaces "database unavailable" rather than dereferencing a nil pool. The multi-DB-ambiguous and unknown-alias branches are untouched.

## Todo
### Wave 1 — implement core tolerance + tests (single Go feature spawn)
- [ ] [golang-eng] make `Registry.Open` non-fatal on connect failure — `internal/db/db.go`
  - add `connectErr error` to unexported `entry`; keep `pool` nil when connect fails
  - in `Open`: keep the validation loop fatal; in the open loop, on `dialect.OpenDB` error store the failed entry with its err and **continue** (do not close the already-opened pools, do not return the error)
  - `Resolve`: before returning a matched entry, if `entry.connectErr != nil` return a wrapped "database %q unavailable: %w" error; empty-alias-sole-DB path must hit this check too
  - `ListDatabases`: set `Status` from `connectErr`; `Close`: nil-guard `pool` so failed entries don't panic
  - add `func (r *Registry) Unavailable() map[string]error` for logging (alias → connect err)
- [ ] [golang-eng] add `Status string` to `DatabaseInfo` and populate it — `internal/db/db.go`
  - JSON tag `status`; values `"available"` / `"unavailable"`
- [ ] [golang-eng] log unavailable DBs at startup — `internal/server/server.go`
  - after `reg.Open` succeeds in `buildRegistry`, loop `reg.Unavailable()` and `logger.Warn("database unavailable at startup", "alias", alias, "url", db.RedactURL(cfg.URL), "error", err)`; this needs the cfg URL, so expose it via the entry (add an unexported accessor or have `Unavailable` return `DatabaseConfig` alongside the err)
- [ ] [golang-eng] unit tests (table-driven, `/golang-unit-test` format) — `internal/db/db_test.go`
  - reuse `fakeDialect` with `openErr` on one of two aliases; assert `Open` returns nil, one available + one unavailable entry, `Resolve` errors on the failed alias with the reason, `ListDatabases` reports both with correct `Status`, `Close` is safe; add a fully-failed case and a validation-still-fatal case
- [ ] [golang-eng] startup-tolerance e2e — `tests/e2e/e2e_test.go`
  - drive `server.New` with a `db.Config` of two entries where one URL is an unreachable port (no live Postgres needed); assert `server.New` returns no error, `list_databases` shows both with correct `status`, and a tool call against the down alias returns an "unavailable" error (use a registered fake dialect or the existing postgres dialect against a refused port)

### Wave 2 — verify & review
- [ ] [code-reviewer] review Wave 1 diff — focus: nil-pool safety in `Close`/`Resolve`, validation-vs-connect split, no new fatal path leaked, redaction kept on log
- [ ] [general-purpose] build + test green (`make build && make test`, plus `go test ./internal/db/... ./internal/server/... ./tests/e2e/...`)
