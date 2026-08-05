// Package config loads runtime configuration.
//
// Server identity/transport/log level are read from environment variables (kept
// unchanged from the scaffold) so nothing already documented breaks. The
// database registry is loaded from a YAML config file whose path is resolved
// from a --config flag, the SQLDB_MCP_CONFIG env var, or an auto-loaded
// ./sqldb-mcp.yaml.
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Timeouts for the HTTP transport. Kept here so main can reference them
// without importing net/http-specific knobs into the Config struct itself.
const (
	HTTPReadHeaderTimeout = 10 * time.Second
	HTTPShutdownTimeout   = 10 * time.Second
)

// Config holds all runtime settings for the server.
type Config struct {
	ServerName    string
	ServerVersion string
	HTTPAddr      string
	LogLevel      string
}

// Load reads configuration from the environment, applying defaults where a
// variable is unset. It validates the result and returns an error describing
// the first invalid value encountered.
func Load() (Config, error) {
	cfg := Config{
		ServerName:    envOr("MCP_SERVER_NAME", "sqldb-mcp"),
		ServerVersion: envOr("MCP_SERVER_VERSION", "v0.1.0"),
		HTTPAddr:      envOr("MCP_HTTP_ADDR", "127.0.0.1:8080"),
		LogLevel:      envOr("MCP_LOG_LEVEL", "info"),
	}

	if strings.TrimSpace(cfg.ServerName) == "" {
		return Config{}, fmt.Errorf("MCP_SERVER_NAME must not be empty")
	}
	if strings.TrimSpace(cfg.ServerVersion) == "" {
		return Config{}, fmt.Errorf("MCP_SERVER_VERSION must not be empty")
	}
	switch cfg.LogLevel {
	case "debug", "info", "warn", "error":
	default:
		return Config{}, fmt.Errorf("MCP_LOG_LEVEL %q is invalid (want one of debug, info, warn, error)", cfg.LogLevel)
	}
	return cfg, nil
}

// envOr returns the named env var or the provided fallback when unset or empty.
func envOr(key, fallback string) string {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
	}
	return fallback
}

// ResolveDatabaseConfigPath resolves the database config file path in this
// order: the explicit --config value, then the SQLDB_MCP_CONFIG env var, then
// an auto-loaded ./sqldb-mcp.yaml if it exists. It returns the path and whether
// one was found; an empty path with found=false means "run with no databases".
func ResolveDatabaseConfigPath(explicit string) (string, bool) {
	if v := strings.TrimSpace(explicit); v != "" {
		return v, true
	}
	if v, ok := os.LookupEnv("SQLDB_MCP_CONFIG"); ok && strings.TrimSpace(v) != "" {
		return v, true
	}
	const auto = "sqldb-mcp.yaml"
	if info, err := os.Stat(auto); err == nil && !info.IsDir() {
		abs, err := filepath.Abs(auto)
		if err != nil {
			return auto, true
		}
		return abs, true
	}
	return "", false
}
