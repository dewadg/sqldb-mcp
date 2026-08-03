package postgres

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// loadDotEnv finds the repo-root .env (walking up from the test's working
// directory) and applies it to the process env for any key not already set, so
// an explicit export or a CI secret still wins. See design decision D8. It is
// test-only: the server never reads .env at runtime.
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
		value = strings.TrimSpace(value)
		value = trimQuotes(value)
		if _, set := os.LookupEnv(key); !set {
			_ = os.Setenv(key, value)
		}
	}
}

// findDotEnv walks up from the cwd looking for a .env file.
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

// trimQuotes removes one matching surrounding pair of single or double quotes.
func trimQuotes(s string) string {
	if len(s) >= 2 {
		if (s[0] == '\'' && s[len(s)-1] == '\'') || (s[0] == '"' && s[len(s)-1] == '"') {
			return s[1 : len(s)-1]
		}
	}
	return s
}
