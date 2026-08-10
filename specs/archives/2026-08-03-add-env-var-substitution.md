# Add env var substitution

> Allow `${ENV_VAR}` placeholders inside the YAML database config so secrets (passwords) stay out of the checked-in file while the config remains in version control.

## Context

Database connection URLs carry secrets (passwords) that should not sit in a checked-in YAML file. Today the only options are hardcoding the DSN in `sqldb-mcp.yaml` or omitting a config file entirely. Allowing `${ENV_VAR}` placeholders inside the YAML lets operators keep the config in version control while sourcing secrets from the environment or a secrets manager, without changing any tool or dialect.

Today `internal/config.LoadDatabaseConfig` decodes the YAML into `db.Config`, calls `ApplyDefaults`, then validates non-empty `url`/alias. Connection URLs are the primary secret-bearing value; this change interpolates environment variables into decoded string values before validation. The pipeline is otherwise unchanged.

## Goals
- `${VAR}` substitution applied uniformly to string scalar fields of each database entry (practically `driver` and `url`).
- Reuse the existing validation path so a substitution that empties a required field is rejected by the current rules — no new validation concept.
- Zero new dependencies; pure-Go, table-testable.
- Unset variable behavior: a `${VAR}` whose variable is unset resolves to an empty string and is reported as a config validation error when it lands in a required field (e.g. an empty `url`), preserving the existing "missing url rejected" guarantee. No silent partial substitution.

## Non-goals
- Default-value syntax (`${VAR:-default}`) — keep the notation to plain `${VAR}`; a missing variable is an error, not a defaulted one.
- Escaping the `${...}` token (e.g. `\${VAR}` to keep it literal). Out of scope; if a value genuinely needs a literal `${`, it can be sourced from an env var that contains it.
- Recursive/indirect references (`${${NESTED}}`) and command substitution (`$(...)`).
- Substitution in keys or non-scalar (map/list) structure — only scalar values.
- Changing typed fields (`int`, `bool`, `Duration`); only string scalars can contain `${}`.
- Changes to server identity/transport/log env loading (already env-driven) or to the runtime config resolution order.

## Decisions

### D1: Post-decode string substitution, not a custom YAML unmarshaler

After `yaml.Unmarshal` into `db.Config`, walk each `DatabaseConfig` and replace `${VAR}` tokens in its string fields (`Driver`, `URL`) using `os.Getenv`. Regex `\$\{([A-Za-z_][A-Za-z0-9_]*)\}` finds tokens; `os.Getenv(name)` resolves them.

- **Why over a custom `UnmarshalYAML`:** a single walk over the already-decoded struct applies uniformly to every string field with no per-field decode logic, and keeps the substitution step isolated and unit-testable.
- **Why only string fields:** typed fields (`*int`, `*bool`, `Duration`) cannot hold a `${}` token in valid YAML for their type; restricting to strings avoids pretending they can.

### D2: Unset variable resolves to empty string, surfaced by existing validation

`os.Getenv` returns `""` for an unset variable, and there is no reliable way to distinguish unset-from-empty for arbitrary env. So an unresolved `${VAR}` becomes an empty substring. A `${MISSING_URL}` therefore yields an empty `url`, which the existing "missing url rejected" rule catches at startup. This reuses one validation path instead of adding a bespoke "unset variable" error.

- **Trade-off:** a partially-substituted URL with one unset token (e.g. `postgresql://:${MISSING}@host`) silently produces a syntactically-plausible but wrong DSN and fails later at connection time, not at config load. *Mitigation:* documented; operators source all referenced variables. The connection ping at startup still surfaces it fast.

### D3: Pipeline order — decode → substitute → defaults → validate

Substitute after decode and before `ApplyDefaults` and validation. This guarantees every field read by validation and by the registry has already had tokens resolved, and keeps `ApplyDefaults` (which fills `nil`/zero fields) unchanged.

## Risks / Trade-offs
- **Partial substitution hides a missing var** → *Mitigation:* ping-on-startup fails fast; documented limitation (D2).
- **Literal `${...}` not escapable** → *Mitigation:* source such values from an env var that contains them; documented as a non-goal.
- **Secret still visible in process env / `ps`** → out of scope; this change moves secrets out of the repo, not out of the environment. Consistent with the existing `.env` test helper and D8 of the prior change.

## Testing

### Scenario

| Given | When | Then |
|-------|------|------|
| `substituteEnv` with a single `${VAR}` token (env set) | called | token replaced with the env value; unmatched text intact |
| value has multiple `${VAR}` tokens at start/middle/end | called | all tokens replaced |
| value has no `${}` tokens | called | byte-for-byte unchanged |
| `${MISSING}` (env unset) | called | resolves to empty substring |
| `LoadDatabaseConfig` with `${DB_PASSWORD}` in `url` (env set) | loaded | resolved value equals the concatenated DSN; resolved (not literal) value reaches the registry-bound config |
| `LoadDatabaseConfig` with `url: ${MISSING_URL}` (env unset) | loaded | returns the missing-url error naming the alias |
| config with no `${}` tokens | loaded | byte-for-byte unchanged from pre-change behavior (regression guard) |

## Migration Plan

1. Add the substitution walk in `LoadDatabaseConfig` between decode and `ApplyDefaults`.
2. Add unit tests (substitution in `url`, multi-token, unset→empty→rejected, no-token unchanged).
3. Document the notation in README and `sqldb-mcp.example.yaml`.
4. Rollback = revert; existing literal configs are unaffected (no `${}` → no change).

## Open Questions
- None — the notation, unset behavior, and scope are fixed by the proposal/spec.

## Todo

### 1. Substitution implementation — `internal/config`
- [x] 1.1 Add an unexported `substituteEnv(s string) string` helper in `internal/config` that replaces every `${VAR}` token (regex `\$\{([A-Za-z_][A-Za-z0-9_]*)\}`) with `os.Getenv(VAR)`, leaving unmatched text intact
- [x] 1.2 Add an unexported walk `applyEnvSubstitution(cfg *db.Config)` that substitutes the string fields (`Driver`, `URL`) of every entry in `cfg.Databases`; leave typed fields untouched
- [x] 1.3 Wire it into `LoadDatabaseConfig` between `yaml.Unmarshal` and `ApplyDefaults`, so validation and the registry see resolved values
- [x] 1.4 Confirm an unset variable resolves to empty and still trips the existing "missing url rejected" validation (no new error path)

### 2. Tests — `internal/config`
- [x] 2.1 Unit test `substituteEnv`: single token, multiple tokens, token at start/middle/end, no-token unchanged, unset variable → empty substring
- [x] 2.2 Unit test `LoadDatabaseConfig` with `${DB_PASSWORD}` in `url` (env set) → resolved value equals the concatenated DSN; assert the resolved value (not the literal) reaches the registry-bound config
- [x] 2.3 Unit test `LoadDatabaseConfig` with `url: ${MISSING_URL}` (env unset) → returns the missing-url error naming the alias
- [x] 2.4 Unit test that a config with no `${}` tokens is byte-for-byte unchanged from the pre-change behavior (regression guard)

### 3. Documentation
- [x] 3.1 Update `README.md` "Databases" section: document the `${VAR}` notation, the unset-variable behavior, and a secrets-from-env example
- [x] 3.2 Update `sqldb-mcp.example.yaml` with a commented `${DB_PASSWORD}` example in the `url`
