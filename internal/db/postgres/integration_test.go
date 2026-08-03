package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/dewadg/sqldb-mcp/internal/db"
)

// These integration tests run against a real Postgres selected by
// SQLDB_MCP_TEST_POSTGRES_URL (a libpq keyword/value DSN). The testenv helper
// loads repo-root .env so plain `go test` works; if the URL is still unset the
// suite skips with a clear message (see design D8).

const testSchema = "sqldb_mcp_test"

// testDSN loads .env and returns the configured DSN, skipping the test when it
// is absent.
func testDSN(t *testing.T) string {
	t.Helper()
	loadDotEnv(t)
	dsn := os.Getenv("SQLDB_MCP_TEST_POSTGRES_URL")
	if dsn == "" {
		t.Skip("SQLDB_MCP_TEST_POSTGRES_URL not set; skipping postgres integration tests")
	}
	return dsn
}

// setupIntegration opens a pool, installs the scratch fixture, and returns the
// pool plus the dialect under test. The fixture is dropped on cleanup.
func setupIntegration(t *testing.T, seedCount int) (*sql.DB, *Dialect) {
	t.Helper()
	dsn := testDSN(t)
	pool, err := sql.Open(driverName, dsn)
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

	return pool, New()
}

// --- list_objects (task 7.8.3) ------------------------------------------

func TestDialect_ListObjects_Integration(t *testing.T) {
	pool, dialect := setupIntegration(t, 0)

	type args struct {
		schema string
		typ    string
	}
	tests := []struct {
		name       string
		args       args
		wantName   string
		wantType   string
		wantErr    assert.ErrorAssertionFunc
		wantNoRows bool
	}{
		{name: "scratch table present", args: args{schema: testSchema, typ: "table"}, wantName: "t_item", wantType: "table", wantErr: assert.NoError},
		{name: "unsupported type errors", args: args{schema: testSchema, typ: "materialized_view"}, wantErr: assert.Error},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			objs, err := dialect.ListObjects(context.Background(), pool, tt.args.schema, tt.args.typ)
			if !tt.wantErr(t, err, "ListObjects") {
				return
			}
			if err != nil {
				return
			}
			if tt.wantName != "" {
				found := false
				for _, o := range objs {
					if o.Name == tt.wantName && o.Type == tt.wantType {
						found = true
					}
				}
				assert.True(t, found, "expected object %s/%s in result", tt.wantType, tt.wantName)
			}
		})
	}
}

// --- get_object_details (task 7.8.4) ------------------------------------

func TestDialect_GetObjectDetails_Integration(t *testing.T) {
	pool, dialect := setupIntegration(t, 0)

	details, err := dialect.GetObjectDetails(context.Background(), pool, testSchema, "t_item", "table")
	assert.NoError(t, err)
	assert.Equal(t, "t_item", details.Name)

	assert.Len(t, details.Columns, 2, "expected two columns")
	if assert.NotEmpty(t, details.Columns) {
		id := details.Columns[0]
		assert.Equal(t, "id", id.Name)
		assert.False(t, id.IsNullable, "id column is primary key, not nullable")
	}

	var hasPK bool
	for _, c := range details.Constraints {
		if c.Type == "primary_key" {
			hasPK = true
		}
	}
	assert.True(t, hasPK, "expected a primary key constraint")

	assert.NotEmpty(t, details.Indexes, "expected at least the primary-key index")
}

// --- execute_query (task 7.8.5/7.8.6) -----------------------------------

func TestDialect_ExecQuery_Integration(t *testing.T) {
	roPool, roDialect := setupIntegration(t, 25) // seed 25 rows for cap test

	type args struct {
		spec db.QuerySpec
	}
	tests := []struct {
		name        string
		args        args
		wantRows    int
		wantColumns []string
		wantErr     assert.ErrorAssertionFunc
		errContains string
	}{
		{
			name:        "read returns columns and rows",
			args:        args{spec: db.QuerySpec{SQL: "SELECT id FROM " + testSchema + ".t_item ORDER BY id", Readonly: true, Timeout: timeout, RowLimit: 100}},
			wantRows:    25,
			wantColumns: []string{"id"},
			wantErr:     assert.NoError,
		},
		{
			name:        "row cap truncates",
			args:        args{spec: db.QuerySpec{SQL: "SELECT id FROM " + testSchema + ".t_item ORDER BY id", Readonly: true, Timeout: timeout, RowLimit: 10}},
			wantRows:    10,
			wantColumns: []string{"id"},
			wantErr:     assert.NoError,
		},
		{
			name:        "bind parameter returns one row",
			args:        args{spec: db.QuerySpec{SQL: "SELECT id FROM " + testSchema + ".t_item WHERE id = $1", Args: []any{7}, Readonly: true, Timeout: timeout, RowLimit: 100}},
			wantRows:    1,
			wantColumns: []string{"id"},
			wantErr:     assert.NoError,
		},
		{
			name:        "write rejected under readonly",
			args:        args{spec: db.QuerySpec{SQL: "DELETE FROM " + testSchema + ".t_item", Readonly: true, Timeout: timeout, RowLimit: 100}},
			wantErr:     assert.Error,
			errContains: "read-only",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res, err := roDialect.ExecQuery(context.Background(), roPool, tt.args.spec)
			if !tt.wantErr(t, err, "ExecQuery") {
				return
			}
			if err != nil {
				if tt.errContains != "" {
					assert.Contains(t, err.Error(), tt.errContains)
				}
				return
			}
			assert.Equal(t, tt.wantColumns, res.Columns)
			assert.Len(t, res.Rows, tt.wantRows)
		})
	}

	// Verify the rejected write did not mutate the table.
	var n int
	assert.NoError(t, roPool.QueryRowContext(context.Background(), "SELECT count(*) FROM "+testSchema+".t_item").Scan(&n))
	assert.Equal(t, 25, n, "readonly transaction must not have deleted rows")
}

// --- explain_query (task 7.8.7) -----------------------------------------

func TestDialect_Explain_Integration(t *testing.T) {
	pool, dialect := setupIntegration(t, 1)

	type args struct {
		spec db.ExplainSpec
	}
	tests := []struct {
		name      string
		args      args
		wantJSON  bool
		wantErr   assert.ErrorAssertionFunc
	}{
		{name: "text plan non-empty", args: args{spec: db.ExplainSpec{SQL: "SELECT * FROM " + testSchema + ".t_item", Readonly: true, Timeout: timeout}}, wantErr: assert.NoError},
		{name: "json format parses", args: args{spec: db.ExplainSpec{SQL: "SELECT * FROM " + testSchema + ".t_item", Format: "json", Readonly: true, Timeout: timeout}}, wantJSON: true, wantErr: assert.NoError},
		{name: "analyze under readonly succeeds for read", args: args{spec: db.ExplainSpec{SQL: "SELECT * FROM " + testSchema + ".t_item", Analyze: true, Readonly: true, Timeout: timeout}}, wantErr: assert.NoError},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res, err := dialect.Explain(context.Background(), pool, tt.args.spec)
			if !tt.wantErr(t, err, "Explain") {
				return
			}
			if err != nil {
				return
			}
			assert.NotEmpty(t, res.Output)
			if tt.wantJSON {
				assert.True(t, json.Valid([]byte(res.Output)), "json format output must be valid JSON: %s", res.Output)
			}
		})
	}
}

// timeout is a readability alias for the per-spec context timeout used in the
// table rows above.
const timeout = 10 * time.Second
