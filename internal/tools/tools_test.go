package tools

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"

	"github.com/dewadg/sqldb-mcp/internal/db"
)

// --- test doubles -------------------------------------------------------

type tConnector struct{}

func (tConnector) Connect(context.Context) (driver.Conn, error) { return tConn{}, nil }
func (tConnector) Driver() driver.Driver                       { return tDriver{} }

type tDriver struct{}

func (tDriver) Open(string) (driver.Conn, error) { return tConn{}, nil }

type tConn struct{}

func (tConn) Prepare(string) (driver.Stmt, error) { return nil, errors.New("noop") }
func (tConn) Close() error                         { return nil }
func (tConn) Begin() (driver.Tx, error)            { return nil, errors.New("noop") }

func tPool() *sql.DB { return sql.OpenDB(tConnector{}) }

// tDialect records the last ExecQuery spec and returns configured results.
type tDialect struct {
	name          string
	lastQuery     db.QuerySpec
	listResult    []db.Object
	detailsResult *db.ObjectDetails
	queryResult   *db.QueryResult
	explainResult *db.ExplainResult
}

func (d *tDialect) Name() string { return d.name }
func (d *tDialect) OpenDB(_ context.Context, _ db.DatabaseConfig) (*sql.DB, error) {
	return tPool(), nil
}
func (d *tDialect) ListObjects(_ context.Context, _ *sql.DB, _, _ string) ([]db.Object, error) {
	return d.listResult, nil
}
func (d *tDialect) GetObjectDetails(_ context.Context, _ *sql.DB, _, _, _ string) (*db.ObjectDetails, error) {
	return d.detailsResult, nil
}
func (d *tDialect) ExecQuery(_ context.Context, _ *sql.DB, spec db.QuerySpec) (*db.QueryResult, error) {
	d.lastQuery = spec
	return d.queryResult, nil
}
func (d *tDialect) Explain(_ context.Context, _ *sql.DB, _ db.ExplainSpec) (*db.ExplainResult, error) {
	return d.explainResult, nil
}

// newRegistry builds a registry with one "primary" fake database and the given
// readonly flag, returning the dialect so tests can read its recorded spec.
func newRegistry(t *testing.T, readonly *bool) (*db.Registry, *tDialect) {
	t.Helper()
	fd := &tDialect{name: "fake"}
	reg, err := db.NewRegistry(fd)
	assert.NoError(t, err)
	err = reg.Open(context.Background(), db.Config{Databases: map[string]db.DatabaseConfig{
		"primary": {Driver: "fake", URL: "u", Readonly: readonly},
	}})
	assert.NoError(t, err)
	t.Cleanup(func() { _ = reg.Close() })
	return reg, fd
}

// --- ping (rewritten to the table-driven format) ------------------------

func TestPingHandler(t *testing.T) {
	type args struct {
		in PingInput
	}
	tests := []struct {
		name        string
		serverName  string
		serverVer   string
		args        args
		wantServer  string
		wantVersion string
	}{
		{name: "defaults reflect configured server identity", serverName: "sqldb-mcp", serverVer: "v0.1.0", args: args{in: PingInput{}}, wantServer: "sqldb-mcp", wantVersion: "v0.1.0"},
		{name: "honors overridden name and version", serverName: "custom-srv", serverVer: "9.9.9", args: args{in: PingInput{}}, wantServer: "custom-srv", wantVersion: "9.9.9"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			handler := pingHandler(tt.serverName, tt.serverVer)
			_, out, err := handler(context.Background(), &mcp.CallToolRequest{}, tt.args.in)
			assert.NoError(t, err)
			assert.Equal(t, tt.wantServer, out.Server)
			assert.Equal(t, tt.wantVersion, out.Version)
			_, perr := time.Parse(time.RFC3339, out.At)
			assert.NoError(t, perr)
		})
	}
}

// --- list_databases (task 7.7) ------------------------------------------

func TestListDatabasesHandler(t *testing.T) {
	tests := []struct {
		name string
		reg  *db.Registry
		want []db.DatabaseInfo
	}{
		{name: "nil registry returns empty list", reg: nil, want: []db.DatabaseInfo{}},
		{name: "returns alias driver readonly, no url", reg: singleReadOnlyRegistry(t), want: []db.DatabaseInfo{{Alias: "primary", Driver: "fake", Readonly: true}}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			handler := listDatabasesHandler(tt.reg)
			_, out, err := handler(context.Background(), &mcp.CallToolRequest{}, ListDatabasesInput{})
			assert.NoError(t, err)
			assert.Equal(t, tt.want, out.Databases)
		})
	}
}

func singleReadOnlyRegistry(t *testing.T) *db.Registry {
	t.Helper()
	reg, _ := newRegistry(t, nil)
	return reg
}

// --- list_objects (task 7.6) --------------------------------------------

func TestListObjectsHandler(t *testing.T) {
	type args struct {
		in ListObjectsInput
	}
	tests := []struct {
		name    string
		reg     *db.Registry
		fd      *tDialect
		args    args
		want    []db.Object
		wantErr assert.ErrorAssertionFunc
	}{
		{
			name:    "dispatches to dialect",
			reg: regWith(t, &tDialect{name: "fake", listResult: []db.Object{{Name: "t", Type: "table"}}}),
			args:    args{in: ListObjectsInput{Database: "primary"}},
			want:    []db.Object{{Name: "t", Type: "table"}},
			wantErr: assert.NoError,
		},
		{
			name:    "nil registry errors",
			reg:     nil,
			args:    args{in: ListObjectsInput{Database: "primary"}},
			wantErr: assert.Error,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			handler := listObjectsHandler(tt.reg)
			_, out, err := handler(context.Background(), &mcp.CallToolRequest{}, tt.args.in)
			if !tt.wantErr(t, err, "listObjectsHandler") {
				return
			}
			if err != nil {
				return
			}
			assert.Equal(t, tt.want, out.Objects)
		})
	}
}

func regWith(t *testing.T, fd *tDialect) *db.Registry {
	t.Helper()
	reg, err := db.NewRegistry(fd)
	assert.NoError(t, err)
	err = reg.Open(context.Background(), db.Config{Databases: map[string]db.DatabaseConfig{
		"primary": {Driver: "fake", URL: "u"},
	}})
	assert.NoError(t, err)
	t.Cleanup(func() { _ = reg.Close() })
	return reg
}

// --- get_object_details (task 7.6) --------------------------------------

func TestGetObjectDetailsHandler(t *testing.T) {
	details := &db.ObjectDetails{Name: "users", Type: "table", Columns: []db.Column{{Name: "id"}}}
	type args struct {
		in GetObjectDetailsInput
	}
	tests := []struct {
		name    string
		reg     *db.Registry
		args    args
		want    *db.ObjectDetails
		wantErr assert.ErrorAssertionFunc
	}{
		{name: "dispatches to dialect", reg: regWith(t, &tDialect{name: "fake", detailsResult: details}), args: args{in: GetObjectDetailsInput{Database: "primary", Name: "users"}}, want: details, wantErr: assert.NoError},
		{name: "missing name errors", reg: regWith(t, &tDialect{name: "fake"}), args: args{in: GetObjectDetailsInput{Database: "primary"}}, wantErr: assert.Error},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			handler := getObjectDetailsHandler(tt.reg)
			_, out, err := handler(context.Background(), &mcp.CallToolRequest{}, tt.args.in)
			if !tt.wantErr(t, err, "getObjectDetailsHandler") {
				return
			}
			if err != nil {
				return
			}
			assert.Equal(t, tt.want, out.ObjectDetails)
		})
	}
}

// --- execute_query + readonly propagation (task 7.3/7.6) ---------------

func TestExecuteQueryHandler(t *testing.T) {
	result := &db.QueryResult{Columns: []string{"n"}, Rows: []db.Row{{1}}}
	type args struct {
		in ExecuteQueryInput
	}
	tests := []struct {
		name         string
		setup        func(t *testing.T) (*db.Registry, *tDialect)
		args         args
		wantResult   *db.QueryResult
		wantReadonly bool
		wantRowLimit int
		wantErr      assert.ErrorAssertionFunc
	}{
		{
			name:         "readonly true by default propagated to dialect",
			setup:        func(t *testing.T) (*db.Registry, *tDialect) { fd := &tDialect{name: "fake", queryResult: result}; return openRegistry(t, fd, nil), fd },
			args:         args{in: ExecuteQueryInput{Database: "primary", Query: "SELECT 1"}},
			wantResult:   result,
			wantReadonly: true,
			wantRowLimit: db.DefaultRowLimit,
			wantErr:      assert.NoError,
		},
		{
			name:         "readonly false propagated when configured",
			setup:        func(t *testing.T) (*db.Registry, *tDialect) { fa := false; fd := &tDialect{name: "fake", queryResult: result}; return openRegistry(t, fd, &fa), fd },
			args:         args{in: ExecuteQueryInput{Database: "primary", Query: "SELECT 1"}},
			wantResult:   result,
			wantReadonly: false,
			wantRowLimit: db.DefaultRowLimit,
			wantErr:      assert.NoError,
		},
		{
			name:    "empty query errors",
			setup:   func(t *testing.T) (*db.Registry, *tDialect) { fd := &tDialect{name: "fake"}; return openRegistry(t, fd, nil), fd },
			args:    args{in: ExecuteQueryInput{Database: "primary", Query: "  "}},
			wantErr: assert.Error,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reg, fd := tt.setup(t)
			handler := executeQueryHandler(reg)
			_, out, err := handler(context.Background(), &mcp.CallToolRequest{}, tt.args.in)
			if !tt.wantErr(t, err, "executeQueryHandler") {
				return
			}
			if err != nil {
				return
			}
			assert.Equal(t, tt.wantResult, out.QueryResult)
			assert.Equal(t, tt.wantReadonly, fd.lastQuery.Readonly)
			assert.Equal(t, tt.wantRowLimit, fd.lastQuery.RowLimit)
			assert.Greater(t, fd.lastQuery.Timeout, time.Duration(0))
		})
	}
}

// openRegistry builds a registry with one "primary" database backed by fd and
// the given readonly flag, wiring cleanup automatically.
func openRegistry(t *testing.T, fd *tDialect, readonly *bool) *db.Registry {
	t.Helper()
	reg, err := db.NewRegistry(fd)
	assert.NoError(t, err)
	cfg := db.Config{Databases: map[string]db.DatabaseConfig{
		"primary": {Driver: fd.name, URL: "u", Readonly: readonly},
	}}
	cfg.ApplyDefaults()
	err = reg.Open(context.Background(), cfg)
	assert.NoError(t, err)
	t.Cleanup(func() { _ = reg.Close() })
	return reg
}


// --- explain_query (task 7.6) -------------------------------------------

func TestExplainQueryHandler(t *testing.T) {
	explain := &db.ExplainResult{Output: "Seq Scan"}
	type args struct {
		in ExplainQueryInput
	}
	tests := []struct {
		name    string
		reg     *db.Registry
		args    args
		want    *db.ExplainResult
		wantErr assert.ErrorAssertionFunc
	}{
		{name: "dispatches to dialect", reg: regWith(t, &tDialect{name: "fake", explainResult: explain}), args: args{in: ExplainQueryInput{Database: "primary", Query: "SELECT 1"}}, want: explain, wantErr: assert.NoError},
		{name: "empty query errors", reg: regWith(t, &tDialect{name: "fake"}), args: args{in: ExplainQueryInput{Database: "primary", Query: ""}}, wantErr: assert.Error},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			handler := explainQueryHandler(tt.reg)
			_, out, err := handler(context.Background(), &mcp.CallToolRequest{}, tt.args.in)
			if !tt.wantErr(t, err, "explainQueryHandler") {
				return
			}
			if err != nil {
				return
			}
			assert.Equal(t, tt.want, out.ExplainResult)
		})
	}
}
