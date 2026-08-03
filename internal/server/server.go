// Package server wires the configured MCP server: it constructs the
// *mcp.Server implementation from config and registers every tool exposed by
// internal/tools.
package server

import (
	"fmt"
	"log/slog"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/dewadg/sqldb-mcp/internal/config"
	"github.com/dewadg/sqldb-mcp/internal/tools"
)

// New builds a configured *mcp.Server. The returned server has all tools from
// internal/tools registered against it but is not yet running; the caller is
// responsible for picking a transport and invoking Run.
func New(cfg *config.Config, logger *slog.Logger) *mcp.Server {
	if cfg == nil {
		panic("server.New: nil config")
	}
	if logger == nil {
		logger = slog.Default()
	}

	srv := mcp.NewServer(
		&mcp.Implementation{Name: cfg.ServerName, Version: cfg.ServerVersion},
		&mcp.ServerOptions{
			Instructions: fmt.Sprintf(
				"%s exposes SQL-database tools over MCP. Available tools start with the 'ping' health check.",
				cfg.ServerName,
			),
			Logger: logger,
		},
	)

	tools.Register(srv, cfg.ServerName, cfg.ServerVersion)
	return srv
}
