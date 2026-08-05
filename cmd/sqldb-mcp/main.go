// Command sqldb-mcp launches the sqldb-mcp MCP server.
//
// By default the server speaks MCP over stdio, which is the transport used by
// MCP clients that spawn the server as a subprocess. Pass --http <addr> to
// instead serve the streamable-HTTP transport, which is convenient for remote
// or browser-based clients.
package main

import (
	"context"
	"errors"
	"flag"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/dewadg/sqldb-mcp/internal/config"
	"github.com/dewadg/sqldb-mcp/internal/server"
)

func main() {
	httpAddr := flag.String("http", "", "address to serve the streamable-HTTP transport on (e.g. :8080); when empty, serve stdio")
	configPath := flag.String("config", "", "path to the database YAML config file (overrides SQLDB_MCP_CONFIG and auto-loaded sqldb-mcp.yaml)")
	flag.Parse()

	cfg, err := config.Load()
	if err != nil {
		slog.New(slog.NewTextHandler(os.Stderr, nil)).Error("invalid config", "error", err)
		os.Exit(1)
	}
	logger := newLogger(cfg.LogLevel)

	// Resolve and load the database registry config. With no config source the
	// server still starts with an empty registry; ping and list_databases keep
	// working and the other SQL tools return a clear "no databases" error.
	resolvedPath, found := config.ResolveDatabaseConfigPath(*configPath)
	dbCfg, err := config.LoadDatabaseConfig(resolvedPath)
	if err != nil {
		logger.Error("invalid database config", "path", resolvedPath, "error", err)
		os.Exit(1)
	}
	if found {
		logger.Info("loaded database config", "path", resolvedPath, "databases", len(dbCfg.Databases))
	}

	// The transport is chosen by the --http flag alone: when it is set we serve
	// streamable HTTP at the given address; when absent we serve stdio. The
	// env-derived MCP_HTTP_ADDR is used only when the user passes the literal
	// flag value "env", so deployments can keep the address in configuration
	// without forcing HTTP mode by default.
	addr := *httpAddr
	switch addr {
	case "":
		// stdio — see below.
	case "env":
		addr = cfg.HTTPAddr
	default:
		cfg.HTTPAddr = addr
	}

	srv, reg, err := server.New(&cfg, &dbCfg, logger)
	if err != nil {
		logger.Error("failed to build server", "error", err)
		os.Exit(1)
	}
	defer func() {
		if cErr := reg.Close(); cErr != nil {
			logger.Error("database registry close failed", "error", cErr)
		}
	}()

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	if addr != "" {
		runHTTP(ctx, srv, addr, logger)
		return
	}

	// Stdio blocks until the client closes stdin or the context is cancelled.
	// On a normal stdin close the SDK's jsonrpc2 reader sees io.EOF, but the
	// writer side of the connection typically surfaces a wrapped
	// "server is closing: EOF" (jsonrpc2.ErrServerClosing formatted with the
	// read-side io.EOF). Because that wraps ErrServerClosing via %w rather
	// than io.EOF, the SDK's wait() does not suppress it, so Run returns it
	// here. We treat it (along with explicit io.EOF and context.Canceled) as
	// graceful; a signal-driven shutdown surfaces context.Canceled.
	if err := srv.Run(ctx, &mcp.StdioTransport{}); err != nil {
		if isGracefulShutdown(err) {
			return
		}
		logger.Error("stdio server exited with error", "error", err)
		os.Exit(1)
	}
}

// runHTTP serves the streamable-HTTP MCP handler and shuts down gracefully on
// context cancellation (SIGINT/SIGTERM). The listener is bound before the
// "listening" log fires so a bind failure is reported rather than masked.
func runHTTP(ctx context.Context, srv *mcp.Server, addr string, logger *slog.Logger) {
	handler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return srv }, &mcp.StreamableHTTPOptions{
		Logger: logger,
	})

	ln, err := net.Listen("tcp", addr)
	if err != nil {
		logger.Error("http listen failed", "addr", addr, "error", err)
		os.Exit(1)
	}

	httpServer := &http.Server{
		Handler:           handler,
		ReadHeaderTimeout: config.HTTPReadHeaderTimeout,
	}

	errCh := make(chan error, 1)
	go func() {
		logger.Info("streamable-http server listening", "addr", ln.Addr().String())
		errCh <- httpServer.Serve(ln)
	}()

	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), config.HTTPShutdownTimeout)
		defer cancel()
		if err := httpServer.Shutdown(shutdownCtx); err != nil {
			logger.Error("graceful http shutdown failed", "error", err)
		}
	case err := <-errCh:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("http server exited with error", "error", err)
			os.Exit(1)
		}
	}
}

// newLogger builds a leveled text logger writing to stderr so stdio JSON-RPC
// traffic on stdout stays uncontaminated.
func newLogger(level string) *slog.Logger {
	var lvl slog.Level
	switch level {
	case "debug":
		lvl = slog.LevelDebug
	case "warn":
		lvl = slog.LevelWarn
	case "error":
		lvl = slog.LevelError
	default:
		lvl = slog.LevelInfo
	}
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: lvl}))
}

// isGracefulShutdown reports whether err represents an expected end-of-session
// condition: an explicit context cancellation, an io.EOF sentinel (or wrapped
// via %w), or the SDK's "server is closing[: EOF]" string produced when the
// jsonrpc2 layer surfaces ErrServerClosing on stdin close. That last form
// wraps ErrServerClosing (not io.EOF) via %w with the EOF formatted inline,
// so it must be detected by string rather than by errors.Is(err, io.EOF).
func isGracefulShutdown(err error) bool {
	if err == nil {
		return true
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, io.EOF) {
		return true
	}
	msg := err.Error()
	return strings.Contains(msg, io.EOF.Error()) || strings.Contains(msg, "server is closing")
}
