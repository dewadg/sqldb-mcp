package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/dewadg/sqldb-mcp/internal/db"
)

func TestLoadDatabaseConfig(t *testing.T) {
	tr := true
	open := db.DefaultMaxOpenConns
	idle := db.DefaultMaxIdleConns
	limit := db.DefaultRowLimit

	type args struct {
		contents string
	}
	tests := []struct {
		name    string
		args    args
		want    db.Config
		wantErr assert.ErrorAssertionFunc
	}{
		{
			name: "omitted fields resolve to constants after parse",
			args: args{contents: "databases:\n  primary:\n    driver: postgres\n    url: postgresql://u:p@h/db\n"},
			want: db.Config{Databases: map[string]db.DatabaseConfig{
				"primary": {Driver: "postgres", URL: "postgresql://u:p@h/db", Readonly: &tr,
					MaxOpenConns: &open, MaxIdleConns: &idle,
					ConnMaxIdleTime: db.Duration(db.DefaultConnMaxIdleTime), QueryTimeout: db.Duration(db.DefaultQueryTimeout),
					RowLimit: &limit},
			}},
			wantErr: assert.NoError,
		},
		{
			name:    "missing url rejected",
			args:    args{contents: "databases:\n  primary:\n    driver: postgres\n"},
			wantErr: assert.Error,
		},
		{
			name:    "empty alias rejected",
			args:    args{contents: "databases:\n  \"\":\n    driver: postgres\n    url: u\n"},
			wantErr: assert.Error,
		},
		{
			name:    "malformed yaml rejected",
			args:    args{contents: "databases: [oops\n"},
			wantErr: assert.Error,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := writeTemp(t, tt.args.contents)
			got, err := LoadDatabaseConfig(path)
			if !tt.wantErr(t, err, "LoadDatabaseConfig") {
				return
			}
			if err != nil {
				return
			}
			assert.Equal(t, tt.want, got)
		})
	}

	t.Run("empty path returns empty config", func(t *testing.T) {
		got, err := LoadDatabaseConfig("")
		assert.NoError(t, err)
		assert.Equal(t, db.Config{}, got)
	})
}

func TestResolveDatabaseConfigPath(t *testing.T) {
	t.Run("explicit flag wins", func(t *testing.T) {
		t.Setenv("SQLDB_MCP_CONFIG", "/from/env.yaml")
		got, ok := ResolveDatabaseConfigPath("/from/flag.yaml")
		assert.True(t, ok)
		assert.Equal(t, "/from/flag.yaml", got)
	})
	t.Run("env used when flag empty", func(t *testing.T) {
		t.Setenv("SQLDB_MCP_CONFIG", "/from/env.yaml")
		got, ok := ResolveDatabaseConfigPath("")
		assert.True(t, ok)
		assert.Equal(t, "/from/env.yaml", got)
	})
	t.Run("nothing set returns false", func(t *testing.T) {
		t.Setenv("SQLDB_MCP_CONFIG", "")
		got, ok := ResolveDatabaseConfigPath("")
		assert.False(t, ok)
		assert.Empty(t, got)
	})
}

func writeTemp(t *testing.T, contents string) string {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "cfg.yaml")
	if err := os.WriteFile(p, []byte(contents), 0o600); err != nil {
		t.Fatalf("write temp config: %v", err)
	}
	return p
}

// --- substituteEnv (task 2.1) ------------------------------------------

func TestSubstituteEnv(t *testing.T) {
	t.Setenv("SUB_A", "postgres")
	t.Setenv("SUB_B", "host")
	// MISSING deliberately unset.

	type args struct {
		s string
	}
	tests := []struct {
		name string
		args args
		want string
	}{
		{name: "single token", args: args{s: "postgresql://user:${SUB_A}@h/db"}, want: "postgresql://user:postgres@h/db"},
		{name: "token at start", args: args{s: "${SUB_A}://rest"}, want: "postgres://rest"},
		{name: "token at end", args: args{s: "proto://${SUB_A}"}, want: "proto://postgres"},
		{name: "multiple tokens", args: args{s: "${SUB_A}://${SUB_B}"}, want: "postgres://host"},
		{name: "no token unchanged", args: args{s: "postgresql://user:pass@host/db"}, want: "postgresql://user:pass@host/db"},
		{name: "unset variable empties", args: args{s: "u:${MISSING}:v"}, want: "u::v"},
		{name: "invalid name token left intact", args: args{s: "${1BAD}"}, want: "${1BAD}"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, substituteEnv(tt.args.s))
		})
	}
}

// --- env substitution through LoadDatabaseConfig (task 2.2/2.3/2.4) ----

func TestLoadDatabaseConfig_EnvSubstitution(t *testing.T) {
	tr := true
	open := db.DefaultMaxOpenConns
	idle := db.DefaultMaxIdleConns
	limit := db.DefaultRowLimit

	type args struct {
		contents string
	}
	tests := []struct {
		name    string
		args    args
		env     map[string]string
		wantURL string
		wantErr assert.ErrorAssertionFunc
	}{
		{
			name:    "url secret sourced from env",
			args:    args{contents: "databases:\n  primary:\n    driver: postgres\n    url: postgresql://user:${DB_PASSWORD}@host:5432/appdb\n"},
			env:     map[string]string{"DB_PASSWORD": "s3cret"},
			wantURL: "postgresql://user:s3cret@host:5432/appdb",
			wantErr: assert.NoError,
		},
		{
			name:    "unset url variable rejected as missing url",
			args:    args{contents: "databases:\n  primary:\n    driver: postgres\n    url: ${MISSING_URL}\n"},
			env:     nil,
			wantErr: assert.Error,
		},
		{
			name: "no tokens unchanged (regression)",
			args: args{contents: "databases:\n  primary:\n    driver: postgres\n    url: postgresql://u:p@h/db\n"},
			env:  nil,
			wantURL: "postgresql://u:p@h/db",
			wantErr: assert.NoError,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("DB_PASSWORD", "")
			t.Setenv("MISSING_URL", "")
			for k, v := range tt.env {
				t.Setenv(k, v)
			}
			path := writeTemp(t, tt.args.contents)
			got, err := LoadDatabaseConfig(path)
			if !tt.wantErr(t, err, "LoadDatabaseConfig") {
				return
			}
			if err != nil {
				return
			}
			entry := got.Databases["primary"]
			assert.Equal(t, tt.wantURL, entry.URL)
			// defaults still applied after substitution
			assert.Equal(t, &tr, entry.Readonly)
			assert.Equal(t, &open, entry.MaxOpenConns)
			assert.Equal(t, &idle, entry.MaxIdleConns)
			assert.Equal(t, &limit, entry.RowLimit)
		})
	}
}
