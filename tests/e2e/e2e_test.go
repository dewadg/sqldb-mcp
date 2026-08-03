// Package e2e drives the real sqldb-mcp *mcp.Server end-to-end through the MCP
// tools/call boundary against a live Postgres, replacing the old
// dialect-level integration tests. An in-memory transport connects an SDK
// client to the server built by server.New, so registration, registry
// resolution, and the handler→dialect contract are all exercised.
//
// Tests skip when SQLDB_MCP_TEST_POSTGRES_URL is unset. A test helper
// auto-loads a repo-root .env so plain `go test ./internal/e2e/...` works.
package e2e

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"

	"github.com/dewadg/sqldb-mcp/internal/config"
	"github.com/dewadg/sqldb-mcp/internal/db"
	"github.com/dewadg/sqldb-mcp/internal/server"
	"github.com/dewadg/sqldb-mcp/internal/tools"
)

const testSchema = "sqldb_mcp_test"

// --- .env loader + DSN guard -------------------------------------------

// loadDotEnv applies a repo-root .env to the process env for keys not already
// set (explicit export / CI secret still wins). Mirrors the loader the deleted
// integration tests used; .env is test-only, never read at runtime.
func loadDotEnv(t *testing.T) {
	t.Helper()
	path := findDotEnv()
	if path == "" {
		return
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		value = trimQuotes(strings.TrimSpace(value))
		if _, set := os.LookupEnv(key); !set {
			_ = os.Setenv(key, value)
		}
	}
}

func findDotEnv() string {
	dir, err := os.Getwd()
	if err != nil {
		return ""
	}
	for {
		p := filepath.Join(dir, ".env")
		if info, err := os.Stat(p); err == nil && !info.IsDir() {
			return p
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}

func trimQuotes(s string) string {
	if len(s) >= 2 && (s[0] == '\'' && s[len(s)-1] == '\'' || s[0] == '"' && s[len(s)-1] == '"') {
		return s[1 : len(s)-1]
	}
	return s
}

// testDSN loads .env and returns SQLDB_MCP_TEST_POSTGRES_URL, skipping when unset.
func testDSN(t *testing.T) string {
	t.Helper()
	loadDotEnv(t)
	dsn := os.Getenv("SQLDB_MCP_TEST_POSTGRES_URL")
	if dsn == "" {
		t.Skip("SQLDB_MCP_TEST_POSTGRES_URL not set; skipping e2e tests")
	}
	return dsn
}

// --- fixture -----------------------------------------------------------

// setupFixture opens a raw pool over the DSN, creates the scratch schema and
// t_item table, seeds seedCount rows, and drops everything on cleanup.
func setupFixture(t *testing.T, dsn string, seedCount int) *sql.DB {
	t.Helper()
	pool, err := sql.Open("pgx", dsn)
	assert.NoError(t, err)
	t.Cleanup(func() { _ = pool.Close() })

	ctx := context.Background()
	_, err = pool.ExecContext(ctx, "DROP SCHEMA IF EXISTS "+testSchema+" CASCADE")
	assert.NoError(t, err)
	_, err = pool.ExecContext(ctx, "CREATE SCHEMA "+testSchema)
	assert.NoError(t, err)
	_, err = pool.ExecContext(ctx,
		"CREATE TABLE "+testSchema+".t_item(id INTEGER PRIMARY KEY, label TEXT NOT NULL DEFAULT 'x')")
	assert.NoError(t, err)

	if seedCount > 0 {
		stmt, err := pool.PrepareContext(ctx, "INSERT INTO "+testSchema+".t_item(id) VALUES ($1)")
		assert.NoError(t, err)
		for i := 1; i <= seedCount; i++ {
			_, err := stmt.ExecContext(ctx, i)
			assert.NoError(t, err)
		}
		_ = stmt.Close()
	}

	t.Cleanup(func() {
		_, _ = pool.ExecContext(context.Background(), "DROP SCHEMA IF EXISTS "+testSchema+" CASCADE")
	})
	return pool
}

// --- server + client bootstrap -----------------------------------------

// harness wires a real server (server.New) to an SDK client over an in-memory
// transport and returns a ready client session plus a cleanup. The returned
// registry handle is closed in cleanup after the server's Run goroutine exits.
func harness(t *testing.T, dsn string, rowLimit int) *mcp.ClientSession {
	t.Helper()

	dbCfg := db.Config{Databases: map[string]db.DatabaseConfig{
		"primary": {Driver: "postgres", URL: dsn, RowLimit: &rowLimit},
	}}
	dbCfg.ApplyDefaults()

	srvCfg := &config.Config{ServerName: "sqldb-mcp-e2e", ServerVersion: "test", LogLevel: "info"}
	srv, reg, err := server.New(srvCfg, &dbCfg, nil)
	assert.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	clientT, serverT := mcp.NewInMemoryTransports()

	// server.Run blocks until the transport closes / ctx is cancelled.
	runErr := make(chan error, 1)
	go func() { runErr <- srv.Run(ctx, serverT) }()

	cl := mcp.NewClient(&mcp.Implementation{Name: "e2e-client", Version: "test"}, nil)
	session, err := cl.Connect(ctx, clientT, nil)
	assert.NoError(t, err)

	t.Cleanup(func() {
		_ = session.Close()
		cancel()
		select {
		case <-runErr:
		case <-time.After(5 * time.Second):
			t.Error("server.Run did not exit within 5s")
		}
		assert.NoError(t, reg.Close())
	})

	// give other goroutines a tick to settle is unnecessary; the handshake is synchronous.
	return session
}

// --- callTool helper ----------------------------------------------------

// callText invokes a tool and returns its first text content block, failing the
// test if the call errors or the result is an error result with no content.
func callText(t *testing.T, session *mcp.ClientSession, name string, args map[string]any) string {
	t.Helper()
	res, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args})
	assert.NoError(t, err)
	if res == nil {
		t.Fatalf("CallTool %q: nil result", name)
	}
	if res.IsError {
		t.Fatalf("CallTool %q returned IsError: %s", name, contentText(res))
	}
	return contentText(res)
}

// callResult invokes a tool and returns the raw result, used for error-path
// assertions where IsError is expected.
func callResult(t *testing.T, session *mcp.ClientSession, name string, args map[string]any) *mcp.CallToolResult {
	t.Helper()
	res, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args})
	assert.NoError(t, err)
	return res
}

// contentText joins all text content blocks of a result into one string.
func contentText(res *mcp.CallToolResult) string {
	var b strings.Builder
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			b.WriteString(tc.Text)
		}
	}
	return b.String()
}

// decode unmarshals a JSON text block into out.
func decode(t *testing.T, text string, out any) {
	t.Helper()
	if err := json.Unmarshal([]byte(text), out); err != nil {
		t.Fatalf("decode tool output: %v (text: %s)", err, text)
	}
}

// --- list_databases -----------------------------------------------------

func TestE2E_ListDatabases(t *testing.T) {
	dsn := testDSN(t)
	setupFixture(t, dsn, 0)
	session := harness(t, dsn, db.DefaultRowLimit)

	var out tools.ListDatabasesOutput
	decode(t, callText(t, session, "list_databases", map[string]any{}), &out)
	if assert.Len(t, out.Databases, 1) {
		assert.Equal(t, "primary", out.Databases[0].Alias)
		assert.Equal(t, "postgres", out.Databases[0].Driver)
		assert.True(t, out.Databases[0].Readonly)
	}
	// credential hygiene: the DSN must not appear anywhere in the result
	raw := callText(t, session, "list_databases", map[string]any{})
	assert.NotContains(t, raw, dsn)
}

// --- list_objects -------------------------------------------------------

func TestE2E_ListObjects(t *testing.T) {
	dsn := testDSN(t)
	setupFixture(t, dsn, 0)
	session := harness(t, dsn, db.DefaultRowLimit)

	type args struct {
		typ string
	}
	tests := []struct {
		name         string
		args         args
		wantName     string
		wantIsError  bool
	}{
		{name: "scratch table present", args: args{typ: "table"}, wantName: "t_item"},
		{name: "unsupported type errors", args: args{typ: "materialized_view"}, wantIsError: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res := callResult(t, session, "list_objects", map[string]any{
				"database": "primary", "schema": testSchema, "type": tt.args.typ,
			})
			if tt.wantIsError {
				assert.True(t, res.IsError, "expected an error result")
				return
			}
			assert.False(t, res.IsError, contentText(res))
			var out tools.ListObjectsOutput
			decode(t, contentText(res), &out)
			found := false
			for _, o := range out.Objects {
				if o.Name == tt.wantName {
					found = true
				}
			}
			assert.True(t, found, "expected %s in objects", tt.wantName)
		})
	}
}

// --- get_object_details -------------------------------------------------

func TestE2E_GetObjectDetails(t *testing.T) {
	dsn := testDSN(t)
	setupFixture(t, dsn, 0)
	session := harness(t, dsn, db.DefaultRowLimit)

	var out tools.GetObjectDetailsOutput
	decode(t, callText(t, session, "get_object_details", map[string]any{
		"database": "primary", "schema": testSchema, "name": "t_item",
	}), &out)

	assert.Equal(t, "t_item", out.Name)
	if assert.Len(t, out.Columns, 2) {
		assert.False(t, out.Columns[0].IsNullable, "id is PK, not nullable")
	}
	var hasPK bool
	for _, c := range out.Constraints {
		if c.Type == "primary_key" {
			hasPK = true
		}
	}
	assert.True(t, hasPK, "expected a primary key constraint")
	assert.NotEmpty(t, out.Indexes, "expected at least the PK index")
}

// --- execute_query ------------------------------------------------------

func TestE2E_ExecuteQuery(t *testing.T) {
	const rowLimit = 5
	dsn := testDSN(t)
	pool := setupFixture(t, dsn, 25)
	session := harness(t, dsn, rowLimit)

	type args struct {
		query string
		bind  any
	}
	tests := []struct {
		name        string
		args        args
		wantRows    int
		wantIsError bool
		errContains string
	}{
		{
			name:     "read returns rows",
			args:     args{query: "SELECT id FROM " + testSchema + ".t_item ORDER BY id"},
			wantRows: rowLimit, // capped by configured row_limit
		},
		{
			name:     "bind parameter returns one row",
			args:     args{query: "SELECT id FROM " + testSchema + ".t_item WHERE id = $1", bind: 7},
			wantRows: 1,
		},
		{
			name:        "write rejected under readonly",
			args:        args{query: "DELETE FROM " + testSchema + ".t_item"},
			wantIsError: true,
			errContains: "read-only",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			args := map[string]any{"database": "primary", "query": tt.args.query}
			if tt.args.bind != nil {
				args["args"] = []any{tt.args.bind}
			}
			res := callResult(t, session, "execute_query", args)
			if tt.wantIsError {
				assert.True(t, res.IsError, "expected an error result")
				if tt.errContains != "" {
					assert.Contains(t, contentText(res), tt.errContains)
				}
				return
			}
			assert.False(t, res.IsError, contentText(res))
			var out tools.ExecuteQueryOutput
			decode(t, contentText(res), &out)
			assert.Equal(t, []string{"id"}, out.Columns)
			assert.Len(t, out.Rows, tt.wantRows)
		})
	}

	// the rejected write must not have mutated the table
	var n int
	assert.NoError(t, pool.QueryRowContext(context.Background(), "SELECT count(*) FROM "+testSchema+".t_item").Scan(&n))
	assert.Equal(t, 25, n, "readonly transaction must not have deleted rows")
}

// --- explain_query ------------------------------------------------------

func TestE2E_ExplainQuery(t *testing.T) {
	dsn := testDSN(t)
	setupFixture(t, dsn, 1)
	session := harness(t, dsn, db.DefaultRowLimit)

	type args struct {
		format  string
		analyze bool
	}
	tests := []struct {
		name     string
		args     args
		wantJSON bool
	}{
		{name: "text plan non-empty", args: args{}},
		{name: "json format parses", args: args{format: "json"}, wantJSON: true},
		{name: "analyze under readonly", args: args{analyze: true}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			text := callText(t, session, "explain_query", map[string]any{
				"database": "primary",
				"query":    "SELECT * FROM " + testSchema + ".t_item",
				"format":   tt.args.format,
				"analyze":  tt.args.analyze,
			})
			var out tools.ExplainQueryOutput
			decode(t, text, &out)
			assert.NotEmpty(t, out.Output)
			if tt.wantJSON {
				assert.True(t, json.Valid([]byte(out.Output)), "json output must be valid JSON: %s", out.Output)
			}
		})
	}
}
