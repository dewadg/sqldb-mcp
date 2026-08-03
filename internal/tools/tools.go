// Package tools owns the MCP tool surface for sqldb-mcp.
//
// Each tool is a handler function together with its typed input/output structs.
// Register wires them all onto a *mcp.Server, so adding a new tool means
// writing one handler + structs and adding a single mcp.AddTool line here.
package tools

import (
	"context"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Register attaches every tool exposed by this package to the given server.
// name and version are the server implementation identity reported by the
// ping tool; they should match the values passed to mcp.NewServer so the
// initialize response and the ping tool never disagree.
func Register(s *mcp.Server, name, version string) {
	mcp.AddTool(s,
		&mcp.Tool{
			Name:        "ping",
			Description: "health check; returns server build info and the current time",
		},
		pingHandler(name, version),
	)
}

// PingInput is the (empty) input schema for the ping tool. Its presence keeps
// input validation consistent with richer tools added later.
type PingInput struct{}

// PingOutput is the structured result returned by the ping tool.
type PingOutput struct {
	Server  string `json:"server" jsonschema:"the server implementation name"`
	Version string `json:"version" jsonschema:"the server implementation version"`
	At      string `json:"at" jsonschema:"RFC3339 UTC timestamp at which the server responded"`
}

// pingHandler returns a handler closure for the ping health-check tool. It
// captures the server identity so the tool's output stays consistent with the
// initialize response. The tool carries no side effects and returns build info
// plus the response timestamp.
func pingHandler(name, version string) func(context.Context, *mcp.CallToolRequest, PingInput) (*mcp.CallToolResult, PingOutput, error) {
	return func(_ context.Context, _ *mcp.CallToolRequest, _ PingInput) (*mcp.CallToolResult, PingOutput, error) {
		return nil, PingOutput{
			Server:  name,
			Version: version,
			At:      time.Now().UTC().Format(time.RFC3339),
		}, nil
	}
}
