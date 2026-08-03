// Package config loads runtime configuration from environment variables.
//
// All settings have sensible defaults so the server runs out of the box in
// stdio mode; override individual values via the environment for deployments.
package config

import (
	"fmt"
	"os"
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
