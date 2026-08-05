## Why

Database connection URLs carry secrets (passwords) that should not sit in a checked-in YAML file. Today the only options are hardcoding the DSN in `sqldb-mcp.yaml` or omitting a config file entirely. Allowing `${ENV_VAR}` placeholders inside the YAML lets operators keep the config in version control while sourcing secrets from the environment or a secrets manager, without changing any tool or dialect.

## What Changes

- Add `${VAR}` substitution to the database config loader: any `${VAR}` token inside a YAML scalar value is replaced with the value of the environment variable `VAR` before the value is used.
- Apply substitution across all scalar values (not just `url`), so `row_limit`-style values can also be environment-driven if desired.
- Unset variable behavior: a `${VAR}` whose variable is unset resolves to an empty string and is reported as a config validation error when it lands in a required field (e.g. an empty `url`), preserving the existing "missing url rejected" guarantee. No silent partial substitution.
- `${VAR}` tokens inside literal strings that are not meant as placeholders can be escaped/ignored by... (see Non-goals: no escaping in this change; the token is always substituted).

## Capabilities

### New Capabilities

<!-- None -->

### Modified Capabilities

- `database-connections`: adds a requirement that the YAML config loader substitutes `${VAR}` environment-variable references in scalar values before validation and use.

## Non-goals

- Default-value syntax (`${VAR:-default}`) — keep the notation to plain `${VAR}`; a missing variable is an error, not a defaulted one.
- Escaping the `${...}` token (e.g. `\${VAR}` to keep it literal). Out of scope; if a value genuinely needs a literal `${`, it can be sourced from an env var that contains it.
- Recursive/indirect references (`${${NESTED}}`) and command substitution (`$(...)`).
- Substitution in keys or non-scalar (map/list) structure — only scalar values.
- Changes to server identity/transport/log env loading (already env-driven) or to the runtime config resolution order.

## Impact

- `internal/config/databases.go`: insert an env-substitution pass between YAML decode and validation.
- New unit tests covering substitution in `url`, multi-token values, and unset-variable errors.
- README + `sqldb-mcp.example.yaml`: document the `${VAR}` notation with a secrets-from-env example.
- No new dependencies, no dialect or tool changes, no breaking changes — existing literal values without `${}` are unaffected.
