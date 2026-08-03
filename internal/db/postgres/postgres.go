// Package postgres implements the db.Dialect interface for PostgreSQL via the
// pure-Go pgx driver registered under the database/sql "pgx" name. All
// RDBMS-specific SQL lives here; adding another engine means a sibling package,
// not edits to the tools.
//
// SQL is constructed by small, pure builder functions (buildObjectsQuery,
// buildExplainSQL, regclassName) so it can be golden-tested without a database.
package postgres

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	// Register the pgx driver under the database/sql "pgx" name so sql.Open
	// can use it without cgo or a native libpq.
	_ "github.com/jackc/pgx/v5/stdlib"

	"github.com/dewadg/sqldb-mcp/internal/db"
)

// Dialect is the PostgreSQL implementation of db.Dialect.
type Dialect struct{}

// New returns a PostgreSQL dialect.
func New() *Dialect { return &Dialect{} }

// Name is the driver string used in config.
func (Dialect) Name() string { return "postgres" }

// driverName is the database/sql driver name pgx registers.
const driverName = "pgx"

// OpenDB opens a pool for one database, applies pool sizing and idle lifetime,
// and pings before returning so a misconfigured URL fails fast at startup.
func (Dialect) OpenDB(ctx context.Context, cfg db.DatabaseConfig) (*sql.DB, error) {
	pool, err := sql.Open(driverName, cfg.URL)
	if err != nil {
		return nil, err
	}
	if cfg.MaxOpenConns != nil {
		pool.SetMaxOpenConns(*cfg.MaxOpenConns)
	}
	if cfg.MaxIdleConns != nil {
		pool.SetMaxIdleConns(*cfg.MaxIdleConns)
	}
	pool.SetConnMaxIdleTime(time.Duration(cfg.ConnMaxIdleTime))
	if err := pool.PingContext(ctx); err != nil {
		_ = pool.Close()
		return nil, err
	}
	return pool, nil
}

// supportedObjectTypes are the object types this dialect can list.
var supportedObjectTypes = map[string]struct{}{
	"table":     {},
	"view":      {},
	"sequence":  {},
	"extension": {},
}

// ListObjects lists objects, optionally filtered by schema and/or type. An empty
// type returns every supported type; an unsupported type returns an error
// naming the supported set.
func (Dialect) ListObjects(ctx context.Context, pool *sql.DB, schema, objectType string) ([]db.Object, error) {
	objectType = strings.TrimSpace(objectType)
	types := []string{objectType}
	if objectType == "" {
		types = []string{"table", "view", "sequence", "extension"}
	} else if _, ok := supportedObjectTypes[objectType]; !ok {
		return nil, fmt.Errorf("unsupported object type %q (supported: table, view, sequence, extension)", objectType)
	}

	var out []db.Object
	for _, t := range types {
		query, args := buildObjectsQuery(t, schema)
		objs, err := scanObjects(ctx, pool, query, args, t)
		if err != nil {
			return nil, err
		}
		out = append(out, objs...)
	}
	return out, nil
}

// buildObjectsQuery returns the SQL and bind args to list objects of one type,
// optionally filtered by schema. It is pure so the generated SQL can be golden-
// tested without a database.
func buildObjectsQuery(t, schema string) (string, []any) {
	switch t {
	case "table", "view":
		q := `
SELECT table_schema, table_name
FROM information_schema.tables
WHERE table_type = $1
  AND table_schema NOT IN ('pg_catalog', 'information_schema')`
		args := []any{tableTypeFor(t)}
		if schema != "" {
			q += " AND table_schema = $2"
			args = append(args, schema)
		}
		q += " ORDER BY table_schema, table_name"
		return q, args
	case "sequence":
		q := `
SELECT sequence_schema, sequence_name
FROM information_schema.sequences
WHERE sequence_schema NOT IN ('pg_catalog', 'information_schema')`
		args := []any{}
		if schema != "" {
			q += " AND sequence_schema = $1"
			args = append(args, schema)
		}
		q += " ORDER BY sequence_schema, sequence_name"
		return q, args
	case "extension":
		return `SELECT '' AS schema, extname FROM pg_extension ORDER BY extname`, nil
	}
	return "", nil
}

// tableTypeFor maps our object type to information_schema.tables.table_type.
func tableTypeFor(t string) string {
	if t == "view" {
		return "VIEW"
	}
	return "BASE TABLE"
}

func scanObjects(ctx context.Context, pool *sql.DB, query string, args []any, t string) ([]db.Object, error) {
	rows, err := pool.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []db.Object
	for rows.Next() {
		var schema, name string
		if err := rows.Scan(&schema, &name); err != nil {
			return nil, err
		}
		obj := db.Object{Name: name, Type: t}
		if schema != "" {
			obj.Schema = schema
		}
		out = append(out, obj)
	}
	return out, rows.Err()
}

// supportedDetailTypes are the object types GetObjectDetails fully describes.
var supportedDetailTypes = map[string]struct{}{
	"table": {},
	"view":  {},
}

// GetObjectDetails returns columns, constraints, and indexes for a table or
// view. Other types are not supported and return an error.
func (Dialect) GetObjectDetails(ctx context.Context, pool *sql.DB, schema, name, objectType string) (*db.ObjectDetails, error) {
	if objectType == "" {
		objectType = "table"
	}
	if _, ok := supportedDetailTypes[objectType]; !ok {
		return nil, fmt.Errorf("get_object_details supports table and view (got %q)", objectType)
	}
	if schema == "" {
		schema = "public"
	}

	columns, err := queryColumns(ctx, pool, schema, name)
	if err != nil {
		return nil, err
	}
	constraints, err := queryConstraints(ctx, pool, schema, name)
	if err != nil {
		return nil, err
	}
	indexes, err := queryIndexes(ctx, pool, schema, name)
	if err != nil {
		return nil, err
	}

	return &db.ObjectDetails{
		Schema:      schema,
		Name:        name,
		Type:        objectType,
		Columns:     columns,
		Constraints: constraints,
		Indexes:     indexes,
	}, nil
}

// columnQuery is the static SQL for fetching columns; golden-stable.
const columnQuery = `
SELECT column_name, data_type, is_nullable, column_default, ordinal_position
FROM information_schema.columns
WHERE table_schema = $1 AND table_name = $2
ORDER BY ordinal_position`

// constraintQuery is the static SQL for fetching constraints; golden-stable.
const constraintQuery = `
SELECT c.conname, c.contype, COALESCE(string_agg(a.attname, ', ' ORDER BY k.ord), '')
FROM pg_constraint c
JOIN unnest(c.conkey) WITH ORDINALITY AS k(attnum, ord) ON true
JOIN pg_attribute a ON a.attrelid = c.conrelid AND a.attnum = k.attnum
WHERE c.conrelid = $1::regclass
GROUP BY c.conname, c.contype
ORDER BY c.conname`

// indexQuery is the static SQL for fetching indexes; golden-stable.
const indexQuery = `
SELECT indexname, indexdef
FROM pg_indexes
WHERE schemaname = $1 AND tablename = $2
ORDER BY indexname`

func queryColumns(ctx context.Context, pool *sql.DB, schema, name string) ([]db.Column, error) {
	rows, err := pool.QueryContext(ctx, columnQuery, schema, name)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []db.Column
	for rows.Next() {
		var c db.Column
		var nullable string
		var def sql.NullString
		if err := rows.Scan(&c.Name, &c.DataType, &nullable, &def, &c.Ordinal); err != nil {
			return nil, err
		}
		c.IsNullable = nullable == "YES"
		if def.Valid {
			c.Default = def.String
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func queryConstraints(ctx context.Context, pool *sql.DB, schema, name string) ([]db.Constraint, error) {
	rows, err := pool.QueryContext(ctx, constraintQuery, regclassName(schema, name))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []db.Constraint
	for rows.Next() {
		var con db.Constraint
		var contype, cols string
		if err := rows.Scan(&con.Name, &contype, &cols); err != nil {
			return nil, err
		}
		con.Type = constraintType(contype)
		con.Columns = splitCSV(cols)
		out = append(out, con)
	}
	return out, rows.Err()
}

// regclassName builds a quoted "schema"."name" regclass literal. Pure so it can
// be golden-tested.
func regclassName(schema, name string) string {
	return quoteIdent(schema) + "." + quoteIdent(name)
}

func constraintType(contype string) string {
	switch contype {
	case "p":
		return "primary_key"
	case "f":
		return "foreign_key"
	case "u":
		return "unique"
	case "c":
		return "check"
	default:
		return contype
	}
}

func queryIndexes(ctx context.Context, pool *sql.DB, schema, name string) ([]db.Index, error) {
	rows, err := pool.QueryContext(ctx, indexQuery, schema, name)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []db.Index
	for rows.Next() {
		var idx db.Index
		if err := rows.Scan(&idx.Name, &idx.Definition); err != nil {
			return nil, err
		}
		out = append(out, idx)
	}
	return out, rows.Err()
}

// ExecQuery runs one query under a transaction whose read-only flag matches
// spec.Readonly. Postgres rejects any write inside a READ ONLY transaction —
// including writes inside functions — giving a single strong safety wall. Rows
// are capped at spec.RowLimit; the transaction is always rolled back.
func (Dialect) ExecQuery(ctx context.Context, pool *sql.DB, spec db.QuerySpec) (*db.QueryResult, error) {
	ctx, cancel := context.WithTimeout(ctx, spec.Timeout)
	defer cancel()

	tx, err := pool.BeginTx(ctx, &sql.TxOptions{ReadOnly: spec.Readonly})
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()

	rows, err := tx.QueryContext(ctx, spec.SQL, spec.Args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	cols, err := rows.Columns()
	if err != nil {
		return nil, err
	}

	result := &db.QueryResult{Columns: cols}
	for rows.Next() {
		values := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range values {
			ptrs[i] = &values[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return nil, err
		}
		result.Rows = append(result.Rows, values)
		if len(result.Rows) >= spec.RowLimit {
			break
		}
	}
	return result, rows.Err()
}

// truncateRows caps rows at limit. Pure so the cap can be unit-tested.
func truncateRows(rows []db.Row, limit int) []db.Row {
	if limit < 0 {
		limit = 0
	}
	if len(rows) <= limit {
		return rows
	}
	return rows[:limit]
}

// supportedExplainFormats are the EXPLAIN formats this dialect emits.
var supportedExplainFormats = map[string]struct{}{
	"":     {}, // default text
	"text": {},
	"json": {},
}

// Explain runs EXPLAIN for one query, honoring format and analyze. When
// spec.Readonly is set the EXPLAIN (including ANALYZE) runs under a read-only
// transaction so it cannot mutate. Execution is bounded by spec.Timeout.
func (Dialect) Explain(ctx context.Context, pool *sql.DB, spec db.ExplainSpec) (*db.ExplainResult, error) {
	explain, err := buildExplainSQL(spec)
	if err != nil {
		return nil, err
	}

	ctx, cancel := context.WithTimeout(ctx, spec.Timeout)
	defer cancel()

	tx, err := pool.BeginTx(ctx, &sql.TxOptions{ReadOnly: spec.Readonly})
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()

	rows, err := tx.QueryContext(ctx, explain)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var lines []string
	for rows.Next() {
		var line string
		if err := rows.Scan(&line); err != nil {
			return nil, err
		}
		lines = append(lines, line)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return &db.ExplainResult{Output: strings.Join(lines, "\n")}, nil
}

// buildExplainSQL composes the EXPLAIN statement from a spec, validating the
// format. Pure so the generated SQL can be golden-tested.
func buildExplainSQL(spec db.ExplainSpec) (string, error) {
	format := strings.TrimSpace(spec.Format)
	if _, ok := supportedExplainFormats[format]; !ok {
		return "", fmt.Errorf("unsupported explain format %q (supported: text, json)", format)
	}
	if format == "" {
		format = "text"
	}
	stmt := "EXPLAIN (FORMAT " + format
	if spec.Analyze {
		stmt += ", ANALYZE"
	}
	stmt += ") " + spec.SQL
	return stmt, nil
}

// quoteIdent wraps an identifier in double quotes for safe regclass building.
func quoteIdent(s string) string {
	return `"` + strings.ReplaceAll(s, `"`, `""`) + `"`
}

// splitCSV splits a ", "-joined column list, trimming each element and dropping
// empties.
func splitCSV(s string) []string {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if t := strings.TrimSpace(p); t != "" {
			out = append(out, t)
		}
	}
	return out
}
