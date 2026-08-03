## Context

See `proposal.md` for motivation (secrets out of the checked-in YAML). Today `internal/config.LoadDatabaseConfig` decodes the YAML into `db.Config`, calls `ApplyDefaults`, then validates non-empty `url`/alias. Connection URLs are the primary secret-bearing value; this change interpolates environment variables into decoded string values before validation. The pipeline is otherwise unchanged.

## Goals / Non-Goals

**Goals:**

- `${VAR}` substitution applied uniformly to string scalar fields of each database entry (practically `driver` and `url`).
- Reuse the existing validation path so a substitution that empties a required field is rejected by the current rules — no new validation concept.
- Zero new dependencies; pure-Go, table-testable.

**Non-Goals:**

- Default-value syntax (`${VAR:-default}`), escaping (`\${VAR}`), recursion, command substitution — per the proposal.
- Substitution in YAML keys or structure.
- Changing typed fields (`int`, `bool`, `Duration`); only string scalars can contain `${}`.

## Decisions

### D1. Post-decode string substitution, not a custom YAML unmarshaler

After `yaml.Unmarshal` into `db.Config`, walk each `DatabaseConfig` and replace `${VAR}` tokens in its string fields (`Driver`, `URL`) using `os.Getenv`. Regex `\$\{([A-Za-z_][A-Za-z0-9_]*)\}` finds tokens; `os.Getenv(name)` resolves them.

- *Why over a custom `UnmarshalYAML`:* a single walk over the already-decoded struct applies uniformly to every string field with no per-field decode logic, and keeps the substitution step isolated and unit-testable.
- *Why only string fields:* typed fields (`*int`, `*bool`, `Duration`) cannot hold a `${}` token in valid YAML for their type; restricting to strings avoids pretending they can.

### D2. Unset variable resolves to empty string, surfaced by existing validation

`os.Getenv` returns `""` for an unset variable, and there is no reliable way to distinguish unset-from-empty for arbitrary env. So an unresolved `${VAR}` becomes an empty substring. A `${MISSING_URL}` therefore yields an empty `url`, which the existing "missing url rejected" rule catches at startup. This reuses one validation path instead of adding a bespoke "unset variable" error.

- *Trade-off:* a partially-substituted URL with one unset token (e.g. `postgresql://:${MISSING}@host`) silently produces a syntactically-plausible but wrong DSN and fails later at connection time, not at config load. *Mitigation:* documented; operators source all referenced variables. The connection ping at startup still surfaces it fast.

### D3. Pipeline order: decode → substitute → defaults → validate

Substitute after decode and before `ApplyDefaults` and validation. This guarantees every field read by validation and by the registry has already had tokens resolved, and keeps `ApplyDefaults` (which fills `nil`/zero fields) unchanged.

## Risks / Trade-offs

- **Partial substitution hides a missing var** → *Mitigation:* ping-on-startup fails fast; documented limitation (D2).
- **Literal `${...}` not escapable** → *Mitigation:* source such values from an env var that contains them; documented as a non-goal.
- **Secret still visible in process env / `ps`** → out of scope; this change moves secrets out of the repo, not out of the environment. Consistent with the existing `.env` test helper and D8 of the prior change.

## Migration Plan

1. Add the substitution walk in `LoadDatabaseConfig` between decode and `ApplyDefaults`.
2. Add unit tests (substitution in `url`, multi-token, unset→empty→rejected, no-token unchanged).
3. Document the notation in README and `sqldb-mcp.example.yaml`.
4. Rollback = revert; existing literal configs are unaffected (no `${}` → no change).

## Open Questions

None — the notation, unset behavior, and scope are fixed by the proposal/spec.
