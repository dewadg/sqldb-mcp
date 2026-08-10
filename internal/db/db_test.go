package db

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// --- test doubles --------------------------------------------------------

// noopDriver/noopConn/noopConnector back a *sql.DB that needs no real server.
// Registry unit tests never run SQL through the pool; they only need a pool
// that exists and is Closeable.

type noopConnector struct{}

func (noopConnector) Connect(context.Context) (driver.Conn, error) { return noopConn{}, nil }
func (noopConnector) Driver() driver.Driver                        { return noopDriver{} }

type noopDriver struct{}

func (noopDriver) Open(string) (driver.Conn, error) { return noopConn{}, nil }

type noopConn struct{}

func (noopConn) Prepare(string) (driver.Stmt, error) { return nil, errors.New("noop conn") }
func (noopConn) Close() error                        { return nil }
func (noopConn) Begin() (driver.Tx, error)           { return nil, errors.New("noop conn") }

func fakePool() *sql.DB { return sql.OpenDB(noopConnector{}) }

// fakeDialect is a hand-written double for db.Dialect used by registry and
// tool-handler tests. Each method records its input and returns the configured
// result/error, so tests assert on dispatch and policy without a database.
type fakeDialect struct {
	name         string
	openErr      error
	openErrByURL map[string]error // per-URL open behavior; wins over openErr when the URL matches
	lastSpec     QuerySpec

	listResult    []Object
	listErr       error
	detailsResult *ObjectDetails
	detailsErr    error
	queryResult   *QueryResult
	queryErr      error
	explainResult *ExplainResult
	explainErr    error
}

func (f *fakeDialect) Name() string { return f.name }
func (f *fakeDialect) OpenDB(_ context.Context, cfg DatabaseConfig) (*sql.DB, error) {
	if err, ok := f.openErrByURL[cfg.URL]; ok {
		return nil, err
	}
	if f.openErr != nil {
		return nil, f.openErr
	}
	return fakePool(), nil
}
func (f *fakeDialect) ListObjects(_ context.Context, _ *sql.DB, _, _ string) ([]Object, error) {
	return f.listResult, f.listErr
}
func (f *fakeDialect) GetObjectDetails(_ context.Context, _ *sql.DB, _, _, _ string) (*ObjectDetails, error) {
	return f.detailsResult, f.detailsErr
}
func (f *fakeDialect) ExecQuery(_ context.Context, _ *sql.DB, spec QuerySpec) (*QueryResult, error) {
	f.lastSpec = spec
	return f.queryResult, f.queryErr
}
func (f *fakeDialect) Explain(_ context.Context, _ *sql.DB, _ ExplainSpec) (*ExplainResult, error) {
	return f.explainResult, f.explainErr
}

// --- DatabaseConfig.ApplyDefaults (task 7.1) ----------------------------

func TestDatabaseConfig_ApplyDefaults(t *testing.T) {
	tr := true
	fa := false
	open := 20
	idle := 2
	limit := 50

	tests := []struct {
		name string
		args Config
		want map[string]DatabaseConfig
	}{
		{
			name: "omitted fields fall back to constants",
			args: Config{Databases: map[string]DatabaseConfig{
				"primary": {Driver: "postgres", URL: "u"},
			}},
			want: map[string]DatabaseConfig{
				"primary": {
					Driver: "postgres", URL: "u", Readonly: &tr,
					MaxOpenConns: ptrInt(DefaultMaxOpenConns), MaxIdleConns: ptrInt(DefaultMaxIdleConns),
					ConnMaxIdleTime: Duration(DefaultConnMaxIdleTime), QueryTimeout: Duration(DefaultQueryTimeout),
					RowLimit: ptrInt(DefaultRowLimit),
				},
			},
		},
		{
			name: "set fields override the constants",
			args: Config{Databases: map[string]DatabaseConfig{
				"rw": {Driver: "postgres", URL: "u", Readonly: &fa, MaxOpenConns: &open, MaxIdleConns: &idle,
					ConnMaxIdleTime: Duration(time.Minute), QueryTimeout: Duration(5 * time.Second), RowLimit: &limit},
			}},
			want: map[string]DatabaseConfig{
				"rw": {Driver: "postgres", URL: "u", Readonly: &fa, MaxOpenConns: &open, MaxIdleConns: &idle,
					ConnMaxIdleTime: Duration(time.Minute), QueryTimeout: Duration(5 * time.Second), RowLimit: &limit},
			},
		},
		{
			name: "one db readonly and one not stay independent",
			args: Config{Databases: map[string]DatabaseConfig{
				"a": {Driver: "postgres", URL: "ua", Readonly: &fa},
				"b": {Driver: "postgres", URL: "ub"},
			}},
			want: map[string]DatabaseConfig{
				"a": {Driver: "postgres", URL: "ua", Readonly: &fa, MaxOpenConns: ptrInt(DefaultMaxOpenConns),
					MaxIdleConns: ptrInt(DefaultMaxIdleConns), ConnMaxIdleTime: Duration(DefaultConnMaxIdleTime),
					QueryTimeout: Duration(DefaultQueryTimeout), RowLimit: ptrInt(DefaultRowLimit)},
				"b": {Driver: "postgres", URL: "ub", Readonly: &tr, MaxOpenConns: ptrInt(DefaultMaxOpenConns),
					MaxIdleConns: ptrInt(DefaultMaxIdleConns), ConnMaxIdleTime: Duration(DefaultConnMaxIdleTime),
					QueryTimeout: Duration(DefaultQueryTimeout), RowLimit: ptrInt(DefaultRowLimit)},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.args.ApplyDefaults()
			assert.Equal(t, tt.want, tt.args.Databases)
		})
	}
}

func ptrInt(v int) *int { return &v }

// --- Registry.Open validation + connect tolerance ----------------------

func TestRegistry_Open(t *testing.T) {
	type args struct {
		ctx context.Context
		cfg Config
	}
	// fields wires a fresh fake dialect per row so cases can configure
	// per-URL open behavior without cross-row state leakage.
	type fields struct {
		dialect *fakeDialect
	}
	tests := []struct {
		name            string
		fields          fields
		args            args
		wantErr         assert.ErrorAssertionFunc
		wantErrContains string
		// postOpen runs additional state assertions on the registry after a
		// successful Open. It is nil for the fatal-validation rows; the loop
		// body nil-checks it before calling.
		postOpen func(t *testing.T, reg *Registry)
	}{
		{
			name:            "empty driver is fatal",
			fields:          fields{dialect: &fakeDialect{name: "fake"}},
			args:            args{ctx: context.Background(), cfg: Config{Databases: map[string]DatabaseConfig{"x": {Driver: "", URL: "u"}}}},
			wantErr:         assert.Error,
			wantErrContains: "driver is required",
		},
		{
			name:    "unknown driver is fatal",
			fields:  fields{dialect: &fakeDialect{name: "fake"}},
			args:    args{ctx: context.Background(), cfg: Config{Databases: map[string]DatabaseConfig{"x": {Driver: "mysql", URL: "u"}}}},
			wantErr: assert.Error,
		},
		{
			name:    "missing url is fatal",
			fields:  fields{dialect: &fakeDialect{name: "fake"}},
			args:    args{ctx: context.Background(), cfg: Config{Databases: map[string]DatabaseConfig{"x": {Driver: "fake"}}}},
			wantErr: assert.Error,
		},
		{
			name:   "one of two aliases failing connect is tolerated",
			fields: fields{dialect: &fakeDialect{name: "fake", openErrByURL: map[string]error{"fail": errors.New("boom")}}},
			args: args{ctx: context.Background(), cfg: Config{Databases: map[string]DatabaseConfig{
				"ok":   {Driver: "fake", URL: "ok"},
				"down": {Driver: "fake", URL: "fail"},
			}}},
			wantErr: assert.NoError,
			postOpen: func(t *testing.T, reg *Registry) {
				assert.Len(t, reg.Unavailable(), 1, "expected exactly one unavailable entry")
				_, _, _, err := reg.Resolve("down")
				if assert.Error(t, err) {
					assert.Contains(t, err.Error(), "unavailable")
				}
			},
		},
		{
			name:    "every DB failing connect is tolerated",
			fields:  fields{dialect: &fakeDialect{name: "fake", openErr: errors.New("net is down")}},
			args:    args{ctx: context.Background(), cfg: Config{Databases: map[string]DatabaseConfig{"a": {Driver: "fake", URL: "u1"}, "b": {Driver: "fake", URL: "u2"}}}},
			wantErr: assert.NoError,
			postOpen: func(t *testing.T, reg *Registry) {
				assert.Len(t, reg.Unavailable(), 2, "expected every configured DB to be unavailable")
			},
		},
		{
			name:    "valid config opens pools",
			fields:  fields{dialect: &fakeDialect{name: "fake"}},
			args:    args{ctx: context.Background(), cfg: Config{Databases: map[string]DatabaseConfig{"x": {Driver: "fake", URL: "u"}}}},
			wantErr: assert.NoError,
			postOpen: func(t *testing.T, reg *Registry) {
				assert.Empty(t, reg.Unavailable(), "no entries should be unavailable for a valid config")
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reg, err := NewRegistry(tt.fields.dialect)
			assert.NoError(t, err)
			err = reg.Open(tt.args.ctx, tt.args.cfg)
			if !tt.wantErr(t, err, fmt.Sprintf("Open(%v)", tt.args.cfg)) {
				_ = reg.Close()
				return
			}
			if err != nil {
				if tt.wantErrContains != "" {
					assert.Contains(t, err.Error(), tt.wantErrContains)
				}
				_ = reg.Close()
				return
			}
			if tt.postOpen != nil {
				tt.postOpen(t, reg)
			}
			_ = reg.Close()
		})
	}
}

// --- Registry.Resolve (task 7.2) ----------------------------------------

func TestRegistry_Resolve(t *testing.T) {
	fd := &fakeDialect{name: "fake"}

	type fields struct {
		dialects map[string]Dialect
		entries  map[string]*entry
	}
	type args struct {
		alias string
	}
	tests := []struct {
		name            string
		fields          fields
		args            args
		wantDialect     string
		wantURL         string
		wantErr         assert.ErrorAssertionFunc
		wantErrContains string
	}{
		{
			name:        "explicit alias resolves",
			fields:      singleEntryFields(fd),
			args:        args{alias: "primary"},
			wantDialect: "fake",
			wantURL:     "u1",
			wantErr:     assert.NoError,
		},
		{
			name:        "empty alias resolves to sole database",
			fields:      singleEntryFields(fd),
			args:        args{alias: ""},
			wantDialect: "fake",
			wantURL:     "u1",
			wantErr:     assert.NoError,
		},
		{
			name:    "empty alias ambiguous with multiple databases",
			fields:  multiEntryFields(fd),
			args:    args{alias: ""},
			wantErr: assert.Error,
		},
		{
			name:    "unknown alias errors",
			fields:  multiEntryFields(fd),
			args:    args{alias: "nope"},
			wantErr: assert.Error,
		},
		{
			name:    "empty registry errors",
			fields:  fields{dialects: map[string]Dialect{"fake": fd}, entries: map[string]*entry{}},
			args:    args{alias: ""},
			wantErr: assert.Error,
		},
		{
			name: "unavailable alias by name surfaces connect err",
			fields: fields{
				dialects: map[string]Dialect{"fake": fd},
				entries: map[string]*entry{
					"primary": {cfg: DatabaseConfig{URL: "u1", Driver: "fake"}, pool: fakePool(), dialect: fd},
					"down":    {cfg: DatabaseConfig{URL: "u2", Driver: "fake"}, pool: nil, dialect: fd, connectErr: errors.New("refused")},
				},
			},
			args:            args{alias: "down"},
			wantErr:         assert.Error,
			wantErrContains: `database "down" unavailable`,
		},
		{
			name: "sole DB unavailable surfaces connect err via empty alias",
			fields: fields{
				dialects: map[string]Dialect{"fake": fd},
				entries: map[string]*entry{
					"only": {cfg: DatabaseConfig{URL: "u1", Driver: "fake"}, pool: nil, dialect: fd, connectErr: errors.New("refused")},
				},
			},
			args:            args{alias: ""},
			wantErr:         assert.Error,
			wantErrContains: `database "only" unavailable`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := &Registry{dialects: tt.fields.dialects, entries: tt.fields.entries}
			pool, dialect, cfg, err := r.Resolve(tt.args.alias)
			if !tt.wantErr(t, err, fmt.Sprintf("Resolve(%v)", tt.args.alias)) {
				return
			}
			if err != nil {
				if tt.wantErrContains != "" {
					assert.Contains(t, err.Error(), tt.wantErrContains)
				}
				return
			}
			assert.NotNil(t, pool)
			assert.Equal(t, tt.wantDialect, dialect.Name())
			assert.Equal(t, tt.wantURL, cfg.URL)
		})
	}
}

func singleEntryFields(fd Dialect) struct {
	dialects map[string]Dialect
	entries  map[string]*entry
} {
	return struct {
		dialects map[string]Dialect
		entries  map[string]*entry
	}{
		dialects: map[string]Dialect{"fake": fd},
		entries: map[string]*entry{
			"primary": {cfg: DatabaseConfig{URL: "u1", Driver: "fake"}, pool: fakePool(), dialect: fd},
		},
	}
}

func multiEntryFields(fd Dialect) struct {
	dialects map[string]Dialect
	entries  map[string]*entry
} {
	return struct {
		dialects map[string]Dialect
		entries  map[string]*entry
	}{
		dialects: map[string]Dialect{"fake": fd},
		entries: map[string]*entry{
			"primary":   {cfg: DatabaseConfig{URL: "u1", Driver: "fake"}, pool: fakePool(), dialect: fd},
			"warehouse": {cfg: DatabaseConfig{URL: "u2", Driver: "fake"}, pool: fakePool(), dialect: fd},
		},
	}
}

// --- Registry.ListDatabases (task 7.7) ----------------------------------

func TestRegistry_ListDatabases(t *testing.T) {
	fd := &fakeDialect{name: "fake"}
	tr := true
	fa := false

	tests := []struct {
		name   string
		fields struct {
			dialects map[string]Dialect
			entries  map[string]*entry
		}
		want []DatabaseInfo
	}{
		{
			name: "lists aliases with driver, readonly, status, sorted, no url",
			fields: struct {
				dialects map[string]Dialect
				entries  map[string]*entry
			}{
				dialects: map[string]Dialect{"fake": fd},
				entries: map[string]*entry{
					"warehouse": {cfg: DatabaseConfig{URL: "secret://u2", Driver: "postgres", Readonly: &fa}, pool: fakePool(), dialect: fd},
					"primary":   {cfg: DatabaseConfig{URL: "secret://u1", Driver: "postgres", Readonly: &tr}, pool: fakePool(), dialect: fd},
				},
			},
			want: []DatabaseInfo{
				{Alias: "primary", Driver: "postgres", Readonly: true, Status: StatusAvailable},
				{Alias: "warehouse", Driver: "postgres", Readonly: false, Status: StatusAvailable},
			},
		},
		{
			name: "failed entry reports unavailable status",
			fields: struct {
				dialects map[string]Dialect
				entries  map[string]*entry
			}{
				dialects: map[string]Dialect{"fake": fd},
				entries: map[string]*entry{
					"primary": {cfg: DatabaseConfig{URL: "secret://u1", Driver: "postgres", Readonly: &tr}, pool: fakePool(), dialect: fd},
					"down":    {cfg: DatabaseConfig{URL: "secret://u2", Driver: "postgres", Readonly: &tr}, pool: nil, dialect: fd, connectErr: errors.New("refused")},
				},
			},
			want: []DatabaseInfo{
				{Alias: "down", Driver: "postgres", Readonly: true, Status: StatusUnavailable},
				{Alias: "primary", Driver: "postgres", Readonly: true, Status: StatusAvailable},
			},
		},
		{
			name: "empty registry returns empty list",
			fields: struct {
				dialects map[string]Dialect
				entries  map[string]*entry
			}{
				dialects: map[string]Dialect{"fake": fd},
				entries:  map[string]*entry{},
			},
			want: []DatabaseInfo{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := &Registry{dialects: tt.fields.dialects, entries: tt.fields.entries}
			got := r.ListDatabases()
			assert.Equal(t, tt.want, got)
		})
	}
}

// --- Registry.Unavailable -----------------------------------------------

func TestRegistry_Unavailable(t *testing.T) {
	fd := &fakeDialect{name: "fake"}
	connectErr := errors.New("refused")

	type fields struct {
		dialects map[string]Dialect
		entries  map[string]*entry
	}
	tests := []struct {
		name   string
		fields fields
		want   map[string]UnavailableDB
	}{
		{
			name: "empty when every DB connected",
			fields: fields{
				dialects: map[string]Dialect{"fake": fd},
				entries: map[string]*entry{
					"a": {cfg: DatabaseConfig{URL: "u1", Driver: "fake"}, pool: fakePool(), dialect: fd},
				},
			},
			want: map[string]UnavailableDB{},
		},
		{
			name: "returns alias -> {config, err} for the failed entry only",
			fields: fields{
				dialects: map[string]Dialect{"fake": fd},
				entries: map[string]*entry{
					"a":    {cfg: DatabaseConfig{URL: "u1", Driver: "fake"}, pool: fakePool(), dialect: fd},
					"down": {cfg: DatabaseConfig{URL: "u2", Driver: "fake"}, pool: nil, dialect: fd, connectErr: connectErr},
				},
			},
			want: map[string]UnavailableDB{
				"down": {Config: DatabaseConfig{URL: "u2", Driver: "fake"}, Err: connectErr},
			},
		},
		{
			name: "returns every failed alias when all are down",
			fields: fields{
				dialects: map[string]Dialect{"fake": fd},
				entries: map[string]*entry{
					"a": {cfg: DatabaseConfig{URL: "u1", Driver: "fake"}, pool: nil, dialect: fd, connectErr: connectErr},
					"b": {cfg: DatabaseConfig{URL: "u2", Driver: "fake"}, pool: nil, dialect: fd, connectErr: connectErr},
				},
			},
			want: map[string]UnavailableDB{
				"a": {Config: DatabaseConfig{URL: "u1", Driver: "fake"}, Err: connectErr},
				"b": {Config: DatabaseConfig{URL: "u2", Driver: "fake"}, Err: connectErr},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := &Registry{dialects: tt.fields.dialects, entries: tt.fields.entries}
			got := r.Unavailable()
			assert.Equal(t, tt.want, got)
		})
	}
}

// --- Registry.Close (nil-pool safety) -----------------------------------

func TestRegistry_Close(t *testing.T) {
	fd := &fakeDialect{name: "fake"}

	type fields struct {
		dialects map[string]Dialect
		entries  map[string]*entry
	}
	tests := []struct {
		name    string
		fields  fields
		wantErr assert.ErrorAssertionFunc
	}{
		{
			name: "mixed available and nil-pool entries close without panic",
			fields: fields{
				dialects: map[string]Dialect{"fake": fd},
				entries: map[string]*entry{
					"a":    {cfg: DatabaseConfig{URL: "u1", Driver: "fake"}, pool: fakePool(), dialect: fd},
					"down": {cfg: DatabaseConfig{URL: "u2", Driver: "fake"}, pool: nil, dialect: fd, connectErr: errors.New("refused")},
				},
			},
			wantErr: assert.NoError,
		},
		{
			name: "only nil-pool entries close without panic",
			fields: fields{
				dialects: map[string]Dialect{"fake": fd},
				entries: map[string]*entry{
					"down": {cfg: DatabaseConfig{URL: "u1", Driver: "fake"}, pool: nil, dialect: fd, connectErr: errors.New("refused")},
				},
			},
			wantErr: assert.NoError,
		},
		{
			name: "empty registry closes cleanly",
			fields: fields{
				dialects: map[string]Dialect{"fake": fd},
				entries:  map[string]*entry{},
			},
			wantErr: assert.NoError,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := &Registry{dialects: tt.fields.dialects, entries: tt.fields.entries}
			err := r.Close()
			tt.wantErr(t, err, fmt.Sprintf("Close()"))
		})
	}
}

// --- RedactURL (task 2.5) -----------------------------------------------

func TestRedactURL(t *testing.T) {
	type args struct {
		s string
	}
	tests := []struct {
		name string
		args args
		want string
	}{
		{name: "url form password redacted", args: args{s: "postgresql://user:secret@host:5432/db"}, want: "postgresql://user:***@host:5432/db"},
		{name: "url form no password unchanged", args: args{s: "postgresql://user@host:5432/db"}, want: "postgresql://user@host:5432/db"},
		{name: "keyword form password redacted", args: args{s: "host=h user=u password=secret port=5432 dbname=d"}, want: "host=h user=u password=*** port=5432 dbname=d"},
		{name: "keyword form quoted password redacted", args: args{s: "host=h password='se cret' dbname=d"}, want: "host=h password=*** dbname=d"},
		{name: "no password keyword form unchanged", args: args{s: "host=h user=u dbname=d"}, want: "host=h user=u dbname=d"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, RedactURL(tt.args.s))
		})
	}
}
