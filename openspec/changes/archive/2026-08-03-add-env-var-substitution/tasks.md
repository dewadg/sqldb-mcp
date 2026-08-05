# Tasks

## 1. Substitution implementation — `internal/config`

- [x] 1.1 Add an unexported `substituteEnv(s string) string` helper in `internal/config` that replaces every `${VAR}` token (regex `\$\{([A-Za-z_][A-Za-z0-9_]*)\}`) with `os.Getenv(VAR)`, leaving unmatched text intact
- [x] 1.2 Add an unexported walk `applyEnvSubstitution(cfg *db.Config)` that substitutes the string fields (`Driver`, `URL`) of every entry in `cfg.Databases`; leave typed fields untouched
- [x] 1.3 Wire it into `LoadDatabaseConfig` between `yaml.Unmarshal` and `ApplyDefaults`, so validation and the registry see resolved values
- [x] 1.4 Confirm an unset variable resolves to empty and still trips the existing "missing url rejected" validation (no new error path)

## 2. Tests — `internal/config`

- [x] 2.1 Unit test `substituteEnv`: single token, multiple tokens, token at start/middle/end, no-token unchanged, unset variable → empty substring
- [x] 2.2 Unit test `LoadDatabaseConfig` with `${DB_PASSWORD}` in `url` (env set) → resolved value equals the concatenated DSN; assert the resolved value (not the literal) reaches the registry-bound config
- [x] 2.3 Unit test `LoadDatabaseConfig` with `url: ${MISSING_URL}` (env unset) → returns the missing-url error naming the alias
- [x] 2.4 Unit test that a config with no `${}` tokens is byte-for-byte unchanged from the pre-change behavior (regression guard)

## 3. Documentation

- [x] 3.1 Update `README.md` "Databases" section: document the `${VAR}` notation, the unset-variable behavior, and a secrets-from-env example
- [x] 3.2 Update `sqldb-mcp.example.yaml` with a commented `${DB_PASSWORD}` example in the `url`
