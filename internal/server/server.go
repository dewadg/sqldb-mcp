// Package server wires the configured MCP server: it constructs the
// *mcp.Server implementation from config, builds the database registry, and
// registers every tool exposed by internal/tools.
package server

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/dewadg/sqldb-mcp/internal/config"
	"github.com/dewadg/sqldb-mcp/internal/db"
	"github.com/dewadg/sqldb-mcp/internal/db/postgres"
	"github.com/dewadg/sqldb-mcp/internal/tools"
)

// New builds a configured *mcp.Server and the database registry backing the SQL
// tools. dbCfg may be nil/empty to start with no databases (ping and
// list_databases still work; other SQL tools return a clear error). The caller
// owns the returned registry and must Close it on shutdown. The returned server
// is not yet running; the caller picks a transport and invokes Run.
func New(cfg *config.Config, dbCfg *db.Config, logger *slog.Logger) (*mcp.Server, *db.Registry, error) {
	if cfg == nil {
		return nil, nil, fmt.Errorf("server.New: nil config")
	}
	if logger == nil {
		logger = slog.Default()
	}

	reg, err := buildRegistry(dbCfg)
	if err != nil {
		return nil, nil, err
	}
	// Surface per-DB connect failures at startup without killing the server.
	// The registry stays logger-free (Open owns pool lifecycle, not logging);
	// server.New is the composition root that owns both the logger and the
	// registry, so the warn belongs here. The URL is redacted before logging.
	for alias, u := range reg.Unavailable() {
		logger.Warn("database unavailable at startup",
			"alias", alias,
			"url", db.RedactURL(u.Config.URL),
			"error", u.Err,
		)
	}

	srv := mcp.NewServer(
		&mcp.Implementation{Name: cfg.ServerName, Version: cfg.ServerVersion},
		&mcp.ServerOptions{
			Instructions: buildInstructions(cfg.ServerName, reg),
			Logger:       logger,
		},
	)

	tools.Register(srv, cfg.ServerName, cfg.ServerVersion, reg)
	return srv, reg, nil
}

// buildRegistry opens and validates the configured databases. An empty config
// yields an empty, usable registry (no pools to open).
func buildRegistry(dbCfg *db.Config) (*db.Registry, error) {
	reg, err := db.NewRegistry(postgres.New())
	if err != nil {
		return nil, err
	}
	if dbCfg != nil && len(dbCfg.Databases) > 0 {
		if err := reg.Open(context.Background(), *dbCfg); err != nil {
			_ = reg.Close()
			return nil, err
		}
	}
	return reg, nil
}

// buildInstructions describes the SQL tools and the configured aliases as a
// hint; list_databases remains the source of truth.
func buildInstructions(serverName string, reg *db.Registry) string {
	aliases := "none configured"
	if reg != nil {
		if dbs := reg.ListDatabases(); len(dbs) > 0 {
			parts := make([]string, 0, len(dbs))
			for _, d := range dbs {
				parts = append(parts, fmt.Sprintf("%s (%s, readonly=%v)", d.Alias, d.Driver, d.Readonly))
			}
			aliases = strings.Join(parts, ", ")
		}
	}
	return fmt.Sprintf(
		"%s exposes SQL-database tools over MCP: list_databases, list_objects, "+
			"get_object_details, execute_query, and explain_query (plus the ping health check). "+
			"Each SQL tool takes a database alias; configured databases: %s. "+
			"Call list_databases for the authoritative list.",
		serverName, aliases,
	)
}
