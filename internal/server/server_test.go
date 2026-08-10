package server

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/dewadg/sqldb-mcp/internal/config"
	"github.com/dewadg/sqldb-mcp/internal/db"
)

// TestNew_WarnRedactsUnavailableDBURL asserts that server.New, when some
// configured DBs fail to connect, logs one warn line per unavailable alias via
// db.RedactURL so the password never reaches the log. The registry stays
// logger-free (D5); the composition root owns both the logger and the redaction
// step, and this test is the regression guard for that contract.
func TestNew_WarnRedactsUnavailableDBURL(t *testing.T) {
	// 127.0.0.1:1 is never listened on; the kernel refuses immediately, so the
	// pgx ping fails fast. connect_timeout=2 caps any pathological case. The
	// password is deliberately distinctive so the redaction assertion can fail
	// loudly on a regression.
	const password = "Hunter2"
	const refusedURL = "postgresql://user:" + password + "@127.0.0.1:1/db?sslmode=disable&connect_timeout=2"

	limit := db.DefaultRowLimit
	dbCfg := &db.Config{Databases: map[string]db.DatabaseConfig{
		"primary":   {Driver: "postgres", URL: refusedURL, RowLimit: &limit},
		"secondary": {Driver: "postgres", URL: refusedURL, RowLimit: &limit},
	}}
	dbCfg.ApplyDefaults()

	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo}))

	srvCfg := &config.Config{ServerName: "sqldb-mcp-test", ServerVersion: "test", LogLevel: "info"}
	_, reg, err := New(srvCfg, dbCfg, logger)
	if !assert.NoError(t, err, "server.New must tolerate connect failures") {
		return
	}
	t.Cleanup(func() { _ = reg.Close() })

	logged := buf.String()
	assert.Contains(t, logged, "database unavailable at startup", "expected one warn line per unavailable alias")
	assert.Contains(t, logged, "user:***@", "expected the URL to be redacted in the log")
	assert.NotContains(t, logged, password, "raw password must never appear in the log")
	// both aliases must be reported so a single-alias regression fails loudly.
	// slog's text handler emits "alias=<value>" with no surrounding quotes on the
	// value, so count the bare key=value token.
	assert.Equal(t, 1, strings.Count(logged, " alias=primary "), "expected one warn line for primary")
	assert.Equal(t, 1, strings.Count(logged, " alias=secondary "), "expected one warn line for secondary")
}
