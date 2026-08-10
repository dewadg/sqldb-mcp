// Package db owns the multi-database abstraction behind sqldb-mcp: the
// configuration types, the Dialect interface that every RDBMS implementation
// satisfies, the shared result types, and the Registry that resolves a database
// alias to an open connection pool plus its dialect.
//
// Adding a new RDBMS means writing one package that implements Dialect and
// registering it with the Registry — no tool or handler code changes.
package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Hardcoded fallback constants applied to per-database fields that a config
// entry leaves unset. There is no shared defaults block; every setting lives on
// its database entry and an omitted optional field resolves to one of these.
const (
	DefaultReadonly        = true
	DefaultQueryTimeout    = 30 * time.Second
	DefaultMaxOpenConns    = 10
	DefaultMaxIdleConns    = 5
	DefaultConnMaxIdleTime = 5 * time.Minute
	DefaultRowLimit        = 100
)

// Duration wraps time.Duration so config files can spell it as a "30s"-style
// string. yaml.v3 cannot decode such a string straight into time.Duration
// (whose underlying type is int64), so Duration implements its own decoding.
type Duration time.Duration

// UnmarshalYAML parses a human duration string ("30s", "5m") into a Duration.
func (d *Duration) UnmarshalYAML(value *yaml.Node) error {
	var s string
	if err := value.Decode(&s); err != nil {
		return err
	}
	parsed, err := time.ParseDuration(s)
	if err != nil {
		return fmt.Errorf("invalid duration %q: %w", s, err)
	}
	*d = Duration(parsed)
	return nil
}

// DatabaseConfig describes one configured database. Optional fields use a
// pointer or a zero value so ApplyDefaults can tell "unset" from "set to zero";
// ApplyDefaults then fills each unset field with its hardcoded constant.
type DatabaseConfig struct {
	Driver          string   `yaml:"driver"`
	URL             string   `yaml:"url"`
	Readonly        *bool    `yaml:"readonly"`
	MaxOpenConns    *int     `yaml:"max_open_conns"`
	MaxIdleConns    *int     `yaml:"max_idle_conns"`
	ConnMaxIdleTime Duration `yaml:"conn_max_idle_time"`
	QueryTimeout    Duration `yaml:"query_timeout"`
	RowLimit        *int     `yaml:"row_limit"`
}

// Config is the root of the YAML database config file: a map of alias to entry.
type Config struct {
	Databases map[string]DatabaseConfig `yaml:"databases"`
}

// ApplyDefaults resolves every unset optional field on each database entry to
// its hardcoded constant, mutating the config in place. It is idempotent.
func (c *Config) ApplyDefaults() {
	for alias := range c.Databases {
		e := c.Databases[alias]
		if e.Readonly == nil {
			r := DefaultReadonly
			e.Readonly = &r
		}
		if e.MaxOpenConns == nil {
			v := DefaultMaxOpenConns
			e.MaxOpenConns = &v
		}
		if e.MaxIdleConns == nil {
			v := DefaultMaxIdleConns
			e.MaxIdleConns = &v
		}
		if e.ConnMaxIdleTime == 0 {
			e.ConnMaxIdleTime = Duration(DefaultConnMaxIdleTime)
		}
		if e.QueryTimeout == 0 {
			e.QueryTimeout = Duration(DefaultQueryTimeout)
		}
		if e.RowLimit == nil {
			v := DefaultRowLimit
			e.RowLimit = &v
		}
		c.Databases[alias] = e
	}
}

// Object is one database object listed by ListObjects.
type Object struct {
	Schema string `json:"schema,omitempty"`
	Name   string `json:"name"`
	Type   string `json:"type"`
}

// Column is one column of a table or view, as reported by GetObjectDetails.
type Column struct {
	Name       string `json:"name"`
	DataType   string `json:"data_type"`
	IsNullable bool   `json:"is_nullable"`
	Default    any    `json:"default,omitempty"`
	Ordinal    int    `json:"ordinal_position"`
}

// Constraint is one table constraint (primary key, foreign key, unique, check).
type Constraint struct {
	Name    string   `json:"name"`
	Type    string   `json:"type"`
	Columns []string `json:"columns"`
}

// Index is one index defined on a table.
type Index struct {
	Name       string `json:"name"`
	Definition string `json:"definition"`
}

// ObjectDetails is the full description of one object returned by
// GetObjectDetails. Columns/Constraints/Indexes are populated for table and
// view types; other types may return an error instead (dialect-defined).
type ObjectDetails struct {
	Schema      string       `json:"schema,omitempty"`
	Name        string       `json:"name"`
	Type        string       `json:"type"`
	Columns     []Column     `json:"columns"`
	Constraints []Constraint `json:"constraints"`
	Indexes     []Index      `json:"indexes"`
}

// QuerySpec carries one execute_query request into the dialect.
type QuerySpec struct {
	SQL      string
	Args     []any
	Readonly bool
	Timeout  time.Duration
	RowLimit int
}

// QueryResult is the column metadata plus the (row-limit-capped) rows returned
// by a row-returning statement, or the affected row count returned by a write.
// For a SELECT, Columns/Rows are populated and RowsAffected is zero; for a
// write, RowsAffected is populated and Columns/Rows are empty.
type QueryResult struct {
	Columns      []string `json:"columns,omitempty"`
	Rows         []Row    `json:"rows,omitempty"`
	RowsAffected int64    `json:"rows_affected,omitempty"`
}

// Row is one result row as an ordered slice of JSON-encodable values.
type Row []any

// ExplainSpec carries one explain_query request into the dialect.
type ExplainSpec struct {
	SQL      string
	Format   string // "text" (default) or "json"
	Analyze  bool
	Readonly bool
	Timeout  time.Duration
}

// ExplainResult holds the EXPLAIN output. For text format Output is the plan
// text; for json format Output is the JSON document emitted by the engine.
type ExplainResult struct {
	Output string `json:"output"`
}

// Dialect isolates all RDBMS-specific behavior behind one interface. The
// Registry owns pool lifecycle; tool handlers dispatch through the dialect and
// stay RDBMS-agnostic.
type Dialect interface {
	// Name is the driver name used in config (e.g. "postgres").
	Name() string

	// OpenDB opens and pings a connection pool for one database.
	OpenDB(ctx context.Context, cfg DatabaseConfig) (*sql.DB, error)

	// ListObjects lists objects, optionally filtered by schema and/or type.
	// objectType must be supported by the dialect or the call returns an error
	// naming the supported types.
	ListObjects(ctx context.Context, db *sql.DB, schema, objectType string) ([]Object, error)

	// GetObjectDetails returns detail for one object.
	GetObjectDetails(ctx context.Context, db *sql.DB, schema, name, objectType string) (*ObjectDetails, error)

	// ExecQuery runs one query under the read-only/timeout/row-limit policy in
	// spec and returns its columns and rows.
	ExecQuery(ctx context.Context, db *sql.DB, spec QuerySpec) (*QueryResult, error)

	// Explain runs EXPLAIN for one query, honoring the requested format, the
	// analyze flag, and the read-only/timeout policy.
	Explain(ctx context.Context, db *sql.DB, spec ExplainSpec) (*ExplainResult, error)
}

// Status values reported by DatabaseInfo.
const (
	StatusAvailable   = "available"
	StatusUnavailable = "unavailable"
)

// DatabaseInfo is the public, credential-free view of one configured database,
// returned by list_databases. The connection URL is deliberately omitted.
// Status is "available" when the pool opened and pinged at startup, or
// "unavailable" when the connect failed (the alias is still listed so callers
// can tell a down DB apart from an unknown one).
type DatabaseInfo struct {
	Alias    string `json:"alias"`
	Driver   string `json:"driver"`
	Readonly bool   `json:"readonly"`
	Status   string `json:"status"`
}

// entry is one database held by the Registry. When the connect at startup
// failed, pool is nil and connectErr carries the reason; the entry stays in the
// map so list_databases still shows it and Resolve can surface a clear
// "database unavailable" error instead of "unknown alias".
type entry struct {
	cfg        DatabaseConfig
	pool       *sql.DB
	dialect    Dialect
	connectErr error
}

// Registry resolves database aliases to their open pool and dialect. It owns
// pool lifecycle: Open opens and pings every configured database, Close closes
// them all.
type Registry struct {
	dialects map[string]Dialect
	entries  map[string]*entry
}

// NewRegistry builds an empty registry and registers the given dialects by
// their Name(). Duplicate dialect names are an error.
func NewRegistry(dialects ...Dialect) (*Registry, error) {
	r := &Registry{
		dialects: make(map[string]Dialect, len(dialects)),
		entries:  make(map[string]*entry),
	}
	for _, d := range dialects {
		name := d.Name()
		if _, ok := r.dialects[name]; ok {
			return nil, fmt.Errorf("dialect %q registered more than once", name)
		}
		r.dialects[name] = d
	}
	return r, nil
}

// Open validates cfg, opens and pings a pool for every configured database, and
// stores the result. Structural config errors (unknown driver, missing driver or
// url) are fatal and returned as a non-nil error, aborting startup.
//
// Per-database connect failures are NOT fatal: the failed alias is recorded with
// a nil pool and its connect error, and the rest are still opened. The entry is
// kept in the map so list_databases reports it as unavailable and Resolve
// surfaces a clear "database unavailable" error rather than "unknown alias".
// Callers wanting startup to be all-or-nothing should validate configs before
// calling Open; this method's contract is "open what can be opened, keep going".
func (r *Registry) Open(ctx context.Context, cfg Config) error {
	// Structural validation first, before opening anything. These are config
	// bugs and stay fatal — tolerance is for transient connect failures.
	for alias, e := range cfg.Databases {
		if strings.TrimSpace(e.Driver) == "" {
			return fmt.Errorf("database %q: driver is required", alias)
		}
		if _, ok := r.dialects[e.Driver]; !ok {
			return fmt.Errorf("database %q: unknown driver %q (registered: %s)", alias, e.Driver, r.registeredDrivers())
		}
		if strings.TrimSpace(e.URL) == "" {
			return fmt.Errorf("database %q: url is required", alias)
		}
	}

	// Open + ping. A connect failure is stored on the entry and the loop
	// continues; already-opened pools are NOT torn down so the reachable DBs
	// keep serving.
	opened := make(map[string]*entry, len(cfg.Databases))
	for alias, e := range cfg.Databases {
		dialect := r.dialects[e.Driver]
		pool, err := dialect.OpenDB(ctx, e)
		if err != nil {
			opened[alias] = &entry{cfg: e, pool: nil, dialect: dialect, connectErr: err}
			continue
		}
		opened[alias] = &entry{cfg: e, pool: pool, dialect: dialect}
	}
	r.entries = opened
	return nil
}

func (r *Registry) registeredDrivers() string {
	names := make([]string, 0, len(r.dialects))
	for n := range r.dialects {
		names = append(names, n)
	}
	sort.Strings(names)
	return strings.Join(names, ", ")
}

// Resolve maps an alias to its pool, dialect, and config. An empty alias
// resolves to the sole configured database; with more than one database it is
// ambiguous and returns an error. An unknown alias returns an error listing the
// configured aliases. A matched alias whose connect failed at startup returns a
// wrapped "database unavailable" error carrying the original reason — the
// empty-alias/sole-DB path hits the same check so a nil pool is never handed
// back to the caller.
func (r *Registry) Resolve(alias string) (*sql.DB, Dialect, DatabaseConfig, error) {
	alias = strings.TrimSpace(alias)
	if alias != "" {
		en, ok := r.entries[alias]
		if !ok {
			return nil, nil, DatabaseConfig{}, fmt.Errorf("unknown database alias %q (configured: %s)", alias, r.configuredAliases())
		}
		if en.connectErr != nil {
			return nil, nil, DatabaseConfig{}, fmt.Errorf("database %q unavailable: %w", alias, en.connectErr)
		}
		return en.pool, en.dialect, en.cfg, nil
	}

	switch len(r.entries) {
	case 0:
		return nil, nil, DatabaseConfig{}, errors.New("no databases configured")
	case 1:
		for alias, en := range r.entries {
			if en.connectErr != nil {
				return nil, nil, DatabaseConfig{}, fmt.Errorf("database %q unavailable: %w", alias, en.connectErr)
			}
			return en.pool, en.dialect, en.cfg, nil
		}
	default:
		return nil, nil, DatabaseConfig{}, fmt.Errorf("database alias is required when more than one is configured (specify one of: %s)", r.configuredAliases())
	}
	return nil, nil, DatabaseConfig{}, errors.New("no databases configured")
}

func (r *Registry) configuredAliases() string {
	aliases := make([]string, 0, len(r.entries))
	for a := range r.entries {
		aliases = append(aliases, a)
	}
	sort.Strings(aliases)
	return strings.Join(aliases, ", ")
}

// ListDatabases returns the credential-free view of every configured database,
// including those that failed to connect: their Status reports "unavailable" so
// callers can tell a down DB from an unknown alias without probing.
func (r *Registry) ListDatabases() []DatabaseInfo {
	out := make([]DatabaseInfo, 0, len(r.entries))
	for alias, en := range r.entries {
		readonly := true
		if en.cfg.Readonly != nil {
			readonly = *en.cfg.Readonly
		}
		status := StatusAvailable
		if en.connectErr != nil {
			status = StatusUnavailable
		}
		out = append(out, DatabaseInfo{Alias: alias, Driver: en.cfg.Driver, Readonly: readonly, Status: status})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Alias < out[j].Alias })
	return out
}

// UnavailableDB describes one database whose connect failed at startup. Config
// is included so callers (only the composition root, for logging) can redact
// and print the URL; Err is the dialect-level failure. Err is never nil inside
// the map.
type UnavailableDB struct {
	Config DatabaseConfig
	Err    error
}

// Unavailable returns one entry per database that failed to connect at startup,
// keyed by alias. It is the single source of truth for the startup log line:
// server.New iterates this map and warns with db.RedactURL(Config.URL) plus Err.
// Returns an empty (non-nil) map when every configured DB connected.
func (r *Registry) Unavailable() map[string]UnavailableDB {
	out := make(map[string]UnavailableDB)
	for alias, en := range r.entries {
		if en.connectErr != nil {
			out[alias] = UnavailableDB{Config: en.cfg, Err: en.connectErr}
		}
	}
	return out
}

// Close closes every open pool. Entries whose connect failed (nil pool) are
// skipped so Close stays safe alongside tolerance. Errors from individual
// closes are joined.
func (r *Registry) Close() error {
	var errs []error
	for _, en := range r.entries {
		if en.pool == nil {
			continue
		}
		if err := en.pool.Close(); err != nil {
			errs = append(errs, err)
		}
	}
	r.entries = nil
	return errors.Join(errs...)
}

// RedactURL returns the given connection string with any password removed, so
// connection errors can be logged safely. It handles both URL form
// (scheme://user:pass@host/db) and libpq keyword form (host=... password=pass).
func RedactURL(s string) string {
	if i := strings.Index(s, "://"); i >= 0 {
		return redactURLForm(s, i)
	}
	return redactKeywordForm(s)
}

func redactURLForm(s string, schemeEnd int) string {
	rest := s[schemeEnd+3:]
	at := strings.IndexByte(rest, '@')
	if at < 0 {
		return s // no userinfo
	}
	userinfo := rest[:at]
	if c := strings.IndexByte(userinfo, ':'); c >= 0 {
		return s[:schemeEnd+3] + userinfo[:c] + ":***" + rest[at:]
	}
	return s
}

func redactKeywordForm(s string) string {
	// Match password=... up to the next whitespace or end. Covers both
	// "password=x" and password='x'.
	var b strings.Builder
	b.Grow(len(s))
	i := 0
	for i < len(s) {
		if j := strings.Index(s[i:], "password="); j >= 0 {
			b.WriteString(s[i : i+j])
			b.WriteString("password=***")
			i += j + len("password=")
			// Skip the value: if quoted, skip to closing quote; else skip to whitespace.
			if i < len(s) && (s[i] == '\'' || s[i] == '"') {
				q := s[i]
				i++ // opening quote
				for i < len(s) && s[i] != q {
					i++
				}
				if i < len(s) {
					i++ // closing quote
				}
			} else {
				for i < len(s) && s[i] != ' ' && s[i] != '\t' {
					i++
				}
			}
			continue
		}
		break
	}
	b.WriteString(s[i:])
	return b.String()
}
