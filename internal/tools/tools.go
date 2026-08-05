// Package tools owns the MCP tool surface for sqldb-mcp.
//
// Each tool is a handler function together with its typed input/output structs.
// Register wires them all onto a *mcp.Server, so adding a new tool means
// writing one handler + structs and adding a single mcp.AddTool line here.
//
// The SQL tools are RDBMS-agnostic: they resolve a database alias through the
// db.Registry and dispatch to the resolved dialect, so no handler changes when
// a new engine is added.
package tools

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/dewadg/sqldb-mcp/internal/db"
)

// Register attaches every tool exposed by this package to the given server.
// name and version are the server implementation identity reported by the ping
// tool; they should match the values passed to mcp.NewServer so the initialize
// response and the ping tool never disagree. reg resolves database aliases for
// the SQL tools; it may be nil when no databases are configured, in which case
// the SQL tools still register but return a clear error when called while ping
// and list_databases keep working.
func Register(s *mcp.Server, name, version string, reg *db.Registry) {
	mcp.AddTool(s,
		&mcp.Tool{
			Name:        "ping",
			Description: "health check; returns server build info and the current time",
		},
		pingHandler(name, version),
	)

	mcp.AddTool(s,
		&mcp.Tool{
			Name:        "list_databases",
			Description: "list configured database aliases with their driver and read-only flag; never exposes connection URLs or credentials",
		},
		listDatabasesHandler(reg),
	)

	mcp.AddTool(s,
		&mcp.Tool{
			Name:        "list_objects",
			Description: "list tables, views, sequences, and extensions in a database, optionally filtered by schema and/or type",
		},
		listObjectsHandler(reg),
	)

	mcp.AddTool(s,
		&mcp.Tool{
			Name:        "get_object_details",
			Description: "return columns, constraints, and indexes for one table or view",
		},
		getObjectDetailsHandler(reg),
	)

	mcp.AddTool(s,
		&mcp.Tool{
			Name:        "execute_query",
			Description: "run a SQL query against a database and return column metadata and rows (capped by row_limit); reads are safe by default via a read-only transaction",
		},
		executeQueryHandler(reg),
	)

	mcp.AddTool(s,
		&mcp.Tool{
			Name:        "explain_query",
			Description: "run EXPLAIN for a query and return the plan; optionally ANALYZE to execute it (still read-only when the database is restricted)",
		},
		explainQueryHandler(reg),
	)
}

// resolve maps a database alias to its pool, dialect, and config, surfacing the
// registry's resolution errors (empty/ambiguous/unknown alias, no databases).
func resolve(reg *db.Registry, alias string) (*sql.DB, db.Dialect, db.DatabaseConfig, error) {
	if reg == nil {
		return nil, nil, db.DatabaseConfig{}, errors.New("no databases configured")
	}
	return reg.Resolve(alias)
}

// effectiveReadonly returns the resolved readonly flag for a config, defaulting
// to the hardcoded constant when unset.
func effectiveReadonly(cfg db.DatabaseConfig) bool {
	if cfg.Readonly != nil {
		return *cfg.Readonly
	}
	return db.DefaultReadonly
}

// effectiveRowLimit returns the resolved row limit, defaulting to the constant.
func effectiveRowLimit(cfg db.DatabaseConfig) int {
	if cfg.RowLimit != nil {
		return *cfg.RowLimit
	}
	return db.DefaultRowLimit
}

// ---------------------------------------------------------------------------
// ping
// ---------------------------------------------------------------------------

// PingInput is the (empty) input schema for the ping tool.
type PingInput struct{}

// PingOutput is the structured result returned by the ping tool.
type PingOutput struct {
	Server  string `json:"server" jsonschema:"the server implementation name"`
	Version string `json:"version" jsonschema:"the server implementation version"`
	At      string `json:"at" jsonschema:"RFC3339 UTC timestamp at which the server responded"`
}

// pingHandler returns a handler closure for the ping health-check tool.
func pingHandler(name, version string) func(context.Context, *mcp.CallToolRequest, PingInput) (*mcp.CallToolResult, PingOutput, error) {
	return func(_ context.Context, _ *mcp.CallToolRequest, _ PingInput) (*mcp.CallToolResult, PingOutput, error) {
		return nil, PingOutput{
			Server:  name,
			Version: version,
			At:      time.Now().UTC().Format(time.RFC3339),
		}, nil
	}
}

// ---------------------------------------------------------------------------
// list_databases
// ---------------------------------------------------------------------------

// ListDatabasesInput takes no arguments.
type ListDatabasesInput struct{}

// ListDatabasesOutput is the credential-free list of configured databases.
type ListDatabasesOutput struct {
	Databases []db.DatabaseInfo `json:"databases"`
}

func listDatabasesHandler(reg *db.Registry) func(context.Context, *mcp.CallToolRequest, ListDatabasesInput) (*mcp.CallToolResult, ListDatabasesOutput, error) {
	return func(_ context.Context, _ *mcp.CallToolRequest, _ ListDatabasesInput) (*mcp.CallToolResult, ListDatabasesOutput, error) {
		if reg == nil {
			return nil, ListDatabasesOutput{Databases: []db.DatabaseInfo{}}, nil
		}
		return nil, ListDatabasesOutput{Databases: reg.ListDatabases()}, nil
	}
}

// ---------------------------------------------------------------------------
// list_objects
// ---------------------------------------------------------------------------

// ListObjectsInput selects a database and optional schema/type filters.
type ListObjectsInput struct {
	Database string `json:"database,omitempty" jsonschema:"database alias; resolves to the sole database when empty"`
	Schema   string `json:"schema,omitempty" jsonschema:"optional schema filter"`
	Type     string `json:"type,omitempty" jsonschema:"optional object type: table, view, sequence, or extension"`
}

// ListObjectsOutput holds the matched objects.
type ListObjectsOutput struct {
	Objects []db.Object `json:"objects"`
}

func listObjectsHandler(reg *db.Registry) func(context.Context, *mcp.CallToolRequest, ListObjectsInput) (*mcp.CallToolResult, ListObjectsOutput, error) {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in ListObjectsInput) (*mcp.CallToolResult, ListObjectsOutput, error) {
		pool, dialect, _, err := resolve(reg, in.Database)
		if err != nil {
			return nil, ListObjectsOutput{}, err
		}
		objs, err := dialect.ListObjects(ctx, pool, in.Schema, in.Type)
		if err != nil {
			return nil, ListObjectsOutput{}, err
		}
		return nil, ListObjectsOutput{Objects: objs}, nil
	}
}

// ---------------------------------------------------------------------------
// get_object_details
// ---------------------------------------------------------------------------

// GetObjectDetailsInput selects a database and one object by schema/name.
type GetObjectDetailsInput struct {
	Database string `json:"database,omitempty" jsonschema:"database alias; resolves to the sole database when empty"`
	Schema   string `json:"schema,omitempty" jsonschema:"object schema; defaults to the database default when empty"`
	Name     string `json:"name" jsonschema:"object name"`
	Type     string `json:"type,omitempty" jsonschema:"object type: table (default) or view"`
}

// GetObjectDetailsOutput holds the object's full description.
type GetObjectDetailsOutput struct {
	*db.ObjectDetails
}

func getObjectDetailsHandler(reg *db.Registry) func(context.Context, *mcp.CallToolRequest, GetObjectDetailsInput) (*mcp.CallToolResult, GetObjectDetailsOutput, error) {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in GetObjectDetailsInput) (*mcp.CallToolResult, GetObjectDetailsOutput, error) {
		if strings.TrimSpace(in.Name) == "" {
			return nil, GetObjectDetailsOutput{}, errors.New("name is required")
		}
		pool, dialect, _, err := resolve(reg, in.Database)
		if err != nil {
			return nil, GetObjectDetailsOutput{}, err
		}
		details, err := dialect.GetObjectDetails(ctx, pool, in.Schema, in.Name, in.Type)
		if err != nil {
			return nil, GetObjectDetailsOutput{}, err
		}
		return nil, GetObjectDetailsOutput{ObjectDetails: details}, nil
	}
}

// ---------------------------------------------------------------------------
// execute_query
// ---------------------------------------------------------------------------

// ExecuteQueryInput carries one query plus optional bound parameters.
type ExecuteQueryInput struct {
	Database string `json:"database,omitempty" jsonschema:"database alias; resolves to the sole database when empty"`
	Query    string `json:"query" jsonschema:"SQL query to run"`
	Args     []any  `json:"args,omitempty" jsonschema:"optional positional bind parameters ($1, $2, ...)"`
}

// ExecuteQueryOutput holds the columns and row-limit-capped rows.
type ExecuteQueryOutput struct {
	*db.QueryResult
}

func executeQueryHandler(reg *db.Registry) func(context.Context, *mcp.CallToolRequest, ExecuteQueryInput) (*mcp.CallToolResult, ExecuteQueryOutput, error) {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in ExecuteQueryInput) (*mcp.CallToolResult, ExecuteQueryOutput, error) {
		if strings.TrimSpace(in.Query) == "" {
			return nil, ExecuteQueryOutput{}, errors.New("query is required")
		}
		pool, dialect, cfg, err := resolve(reg, in.Database)
		if err != nil {
			return nil, ExecuteQueryOutput{}, err
		}
		res, err := dialect.ExecQuery(ctx, pool, db.QuerySpec{
			SQL:      in.Query,
			Args:     in.Args,
			Readonly: effectiveReadonly(cfg),
			Timeout:  time.Duration(cfg.QueryTimeout),
			RowLimit: effectiveRowLimit(cfg),
		})
		if err != nil {
			return nil, ExecuteQueryOutput{}, err
		}
		return nil, ExecuteQueryOutput{QueryResult: res}, nil
	}
}

// ---------------------------------------------------------------------------
// explain_query
// ---------------------------------------------------------------------------

// ExplainQueryInput carries one query plus format/analyze options.
type ExplainQueryInput struct {
	Database string `json:"database,omitempty" jsonschema:"database alias; resolves to the sole database when empty"`
	Query    string `json:"query" jsonschema:"SQL query to explain"`
	Format   string `json:"format,omitempty" jsonschema:"plan format: text (default) or json"`
	Analyze  bool   `json:"analyze,omitempty" jsonschema:"when true, append ANALYZE to execute the query while planning"`
}

// ExplainQueryOutput holds the EXPLAIN output.
type ExplainQueryOutput struct {
	*db.ExplainResult
}

func explainQueryHandler(reg *db.Registry) func(context.Context, *mcp.CallToolRequest, ExplainQueryInput) (*mcp.CallToolResult, ExplainQueryOutput, error) {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in ExplainQueryInput) (*mcp.CallToolResult, ExplainQueryOutput, error) {
		if strings.TrimSpace(in.Query) == "" {
			return nil, ExplainQueryOutput{}, errors.New("query is required")
		}
		pool, dialect, cfg, err := resolve(reg, in.Database)
		if err != nil {
			return nil, ExplainQueryOutput{}, err
		}
		res, err := dialect.Explain(ctx, pool, db.ExplainSpec{
			SQL:      in.Query,
			Format:   in.Format,
			Analyze:  in.Analyze,
			Readonly: effectiveReadonly(cfg),
			Timeout:  time.Duration(cfg.QueryTimeout),
		})
		if err != nil {
			return nil, ExplainQueryOutput{}, err
		}
		return nil, ExplainQueryOutput{ExplainResult: res}, nil
	}
}
