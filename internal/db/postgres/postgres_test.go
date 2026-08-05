package postgres

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/dewadg/sqldb-mcp/internal/db"
)

// --- ListObjects SQL builder golden tests (task 7.4) --------------------

func TestBuildObjectsQuery(t *testing.T) {
	type args struct {
		t      string
		schema string
	}
	tests := []struct {
		name     string
		args     args
		wantQuery string
		wantArgs []any
	}{
		{
			name:      "table without schema filter",
			args:      args{t: "table", schema: ""},
			wantQuery: "\nSELECT table_schema, table_name\nFROM information_schema.tables\nWHERE table_type = $1\n  AND table_schema NOT IN ('pg_catalog', 'information_schema') ORDER BY table_schema, table_name",
			wantArgs:  []any{"BASE TABLE"},
		},
		{
			name:      "view with schema filter",
			args:      args{t: "view", schema: "app"},
			wantQuery: "\nSELECT table_schema, table_name\nFROM information_schema.tables\nWHERE table_type = $1\n  AND table_schema NOT IN ('pg_catalog', 'information_schema') AND table_schema = $2 ORDER BY table_schema, table_name",
			wantArgs:  []any{"VIEW", "app"},
		},
		{
			name:      "sequence with schema filter",
			args:      args{t: "sequence", schema: "app"},
			wantQuery: "\nSELECT sequence_schema, sequence_name\nFROM information_schema.sequences\nWHERE sequence_schema NOT IN ('pg_catalog', 'information_schema') AND sequence_schema = $1 ORDER BY sequence_schema, sequence_name",
			wantArgs:  []any{"app"},
		},
		{
			name:      "extension ignores schema filter",
			args:      args{t: "extension", schema: "app"},
			wantQuery: "SELECT '' AS schema, extname FROM pg_extension ORDER BY extname",
			wantArgs:  nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotQuery, gotArgs := buildObjectsQuery(tt.args.t, tt.args.schema)
			assert.Equal(t, tt.wantQuery, gotQuery)
			assert.Equal(t, tt.wantArgs, gotArgs)
		})
	}
}

// --- EXPLAIN SQL builder golden tests (task 7.4) ------------------------

func TestBuildExplainSQL(t *testing.T) {
	type args struct {
		spec db.ExplainSpec
	}
	tests := []struct {
		name    string
		args    args
		want    string
		wantErr assert.ErrorAssertionFunc
	}{
		{name: "default is text", args: args{spec: db.ExplainSpec{SQL: "SELECT 1"}}, want: "EXPLAIN (FORMAT text) SELECT 1", wantErr: assert.NoError},
		{name: "explicit text", args: args{spec: db.ExplainSpec{Format: "text", SQL: "SELECT 1"}}, want: "EXPLAIN (FORMAT text) SELECT 1", wantErr: assert.NoError},
		{name: "json format", args: args{spec: db.ExplainSpec{Format: "json", SQL: "SELECT 1"}}, want: "EXPLAIN (FORMAT json) SELECT 1", wantErr: assert.NoError},
		{name: "analyze appended", args: args{spec: db.ExplainSpec{Format: "json", Analyze: true, SQL: "SELECT 1"}}, want: "EXPLAIN (FORMAT json, ANALYZE) SELECT 1", wantErr: assert.NoError},
		{name: "unsupported format errors", args: args{spec: db.ExplainSpec{Format: "xml", SQL: "SELECT 1"}}, wantErr: assert.Error},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := buildExplainSQL(tt.args.spec)
			if !tt.wantErr(t, err, "buildExplainSQL") {
				return
			}
			if err != nil {
				return
			}
			assert.Equal(t, tt.want, got)
		})
	}
}

// --- regclassName golden tests (task 7.4) -------------------------------

func TestRegclassName(t *testing.T) {
	type args struct {
		schema string
		name   string
	}
	tests := []struct {
		name string
		args args
		want string
	}{
		{name: "plain identifiers quoted", args: args{schema: "app", name: "users"}, want: `"app"."users"`},
		{name: "embedded quote escaped", args: args{schema: "a\"b", name: "c"}, want: `"a""b"."c"`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, regclassName(tt.args.schema, tt.args.name))
		})
	}
}

// --- statement routing sniff -------------------------------------------

func TestIsRowReturning(t *testing.T) {
	type args struct {
		sql string
	}
	tests := []struct {
		name string
		args args
		want bool
	}{
		{name: "select", args: args{sql: "SELECT 1"}, want: true},
		{name: "with", args: args{sql: "WITH x AS (SELECT 1) SELECT * FROM x"}, want: true},
		{name: "values", args: args{sql: "VALUES (1), (2)"}, want: true},
		{name: "table", args: args{sql: "TABLE t"}, want: true},
		{name: "lowercase leader", args: args{sql: "select 1"}, want: true},
		{name: "leading whitespace", args: args{sql: "   \n\t SELECT 1"}, want: true},
		{name: "leading block comment", args: args{sql: "/* hint */ SELECT 1"}, want: true},
		{name: "leading line comment", args: args{sql: "-- probe\nSELECT 1"}, want: true},
		{name: "mixed comments and space", args: args{sql: "/* a */ -- b\n  SELECT 1"}, want: true},
		{name: "insert", args: args{sql: "INSERT INTO t VALUES (1)"}, want: false},
		{name: "update", args: args{sql: "UPDATE t SET a=1"}, want: false},
		{name: "delete", args: args{sql: "DELETE FROM t"}, want: false},
		{name: "create", args: args{sql: "CREATE TABLE t (a int)"}, want: false},
		{name: "parenthesized expression statement", args: args{sql: "(SELECT 1)"}, want: false},
		{name: "empty string", args: args{sql: ""}, want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, isRowReturning(tt.args.sql))
		})
	}
}

// --- row cap (task 7.5) -------------------------------------------------

func TestTruncateRows(t *testing.T) {
	rows := func(n int) []db.Row {
		out := make([]db.Row, n)
		for i := range out {
			out[i] = db.Row{i}
		}
		return out
	}

	type args struct {
		rows  []db.Row
		limit int
	}
	tests := []struct {
		name string
		args args
		want []db.Row
	}{
		{name: "under limit unchanged", args: args{rows: rows(50), limit: 100}, want: rows(50)},
		{name: "over limit truncated", args: args{rows: rows(250), limit: 100}, want: rows(100)},
		{name: "exactly limit unchanged", args: args{rows: rows(100), limit: 100}, want: rows(100)},
		{name: "negative limit empties", args: args{rows: rows(5), limit: -1}, want: rows(0)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, truncateRows(tt.args.rows, tt.args.limit))
		})
	}
}
