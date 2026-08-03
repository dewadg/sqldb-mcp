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
