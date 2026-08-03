## ADDED Requirements

### Requirement: Environment-variable substitution in config values

The database config loader SHALL replace every `${VAR}` token inside a YAML scalar value with the value of the environment variable named `VAR`, performed after YAML decoding and before validation and use. Substitution SHALL apply to all scalar values (for example `url`), not to keys or the map/list structure.

An unresolved `${VAR}` whose environment variable is unset resolves to an empty string. When that empties a required field the existing validation still applies: for example a `${DB_URL}` whose variable is unset yields an empty `url`, which is rejected as a missing url at startup rather than silently producing a broken connection.

The `${VAR}` notation SHALL always be substituted; literal dollar-brace sequences that must be preserved SHALL be sourced from an environment variable that already contains them.

#### Scenario: url secret sourced from the environment

- **WHEN** a database entry declares `url: postgresql://user:${DB_PASSWORD}@host:5432/appdb` and `DB_PASSWORD=secret` is set in the environment
- **THEN** the resolved `url` is `postgresql://user:secret@host:5432/appdb`

#### Scenario: multiple tokens in one value

- **WHEN** a scalar value contains `${A}://${B}` with `A=postgres` and `B=host`
- **THEN** the resolved value is `postgres://host`

#### Scenario: unset variable empties the field and is rejected

- **WHEN** a database entry declares `url: ${MISSING_URL}` and `MISSING_URL` is not set in the environment
- **THEN** the resolved `url` is empty and configuration loading returns the missing-url error naming the alias

#### Scenario: values without tokens are unchanged

- **WHEN** a scalar value contains no `${...}` token
- **THEN** the value is used verbatim
