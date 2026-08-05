package config

import (
	"fmt"
	"os"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/dewadg/sqldb-mcp/internal/db"
)

// envToken matches a ${VAR} reference to a POSIX-ish environment variable name.
var envToken = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)\}`)

// substituteEnv replaces every ${VAR} token in s with the value of the matching
// environment variable. An unset variable resolves to an empty substring
// (os.Getenv semantics); callers rely on downstream validation to reject
// fields emptied by an unresolved token.
func substituteEnv(s string) string {
	return envToken.ReplaceAllStringFunc(s, func(token string) string {
		name := token[2 : len(token)-1] // strip "${" and "}"
		return os.Getenv(name)
	})
}

// applyEnvSubstitution resolves ${VAR} tokens in the string scalar fields of
// every database entry (driver, url). Typed fields cannot hold a token in valid
// YAML for their type and are left untouched.
func applyEnvSubstitution(cfg *db.Config) {
	for alias, e := range cfg.Databases {
		e.Driver = substituteEnv(e.Driver)
		e.URL = substituteEnv(e.URL)
		cfg.Databases[alias] = e
	}
}

// LoadDatabaseConfig reads and parses the YAML database config at path, applies
// hardcoded fallback constants to unset per-DB fields, and validates the
// structural rules (non-empty alias and url). Driver-known validation happens
// later in db.Registry.Open, which knows the registered dialects. An empty path
// returns a zero config (empty registry) so the server still starts and ping
// keeps working.
func LoadDatabaseConfig(path string) (db.Config, error) {
	if strings.TrimSpace(path) == "" {
		return db.Config{}, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return db.Config{}, fmt.Errorf("read database config %q: %w", path, err)
	}

	var cfg db.Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return db.Config{}, fmt.Errorf("parse database config %q: %w", path, err)
	}

	// Resolve ${VAR} environment references in scalar values before defaults
	// and validation so the registry and validation see the final values.
	applyEnvSubstitution(&cfg)

	cfg.ApplyDefaults()

	for alias, e := range cfg.Databases {
		if strings.TrimSpace(alias) == "" {
			return db.Config{}, fmt.Errorf("database alias must not be empty")
		}
		if strings.TrimSpace(e.URL) == "" {
			return db.Config{}, fmt.Errorf("database %q: url is required", alias)
		}
	}
	return cfg, nil
}
