package config

import (
	"fmt"
	"os"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/dewadg/sqldb-mcp/internal/db"
)

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
