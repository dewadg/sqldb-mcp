package tools

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// TestPingHandler exercises the ping tool handler in table-driven form. The
// "At" timestamp is non-deterministic, so the table only asserts on the stable
// fields and a separate check parses the timestamp as RFC3339.
func TestPingHandler(t *testing.T) {
	type args struct {
		in PingInput
	}
	tests := []struct {
		name        string
		serverName  string
		serverVer   string
		args        args
		wantResult  *mcp.CallToolResult
		wantServer  string
		wantVersion string
		wantErr     error
	}{
		{
			name:        "defaults reflect configured server identity",
			serverName:  "sqldb-mcp",
			serverVer:   "v0.1.0",
			args:        args{in: PingInput{}},
			wantResult:  nil,
			wantServer:  "sqldb-mcp",
			wantVersion: "v0.1.0",
			wantErr:     nil,
		},
		{
			name:        "honors overridden name and version",
			serverName:  "custom-srv",
			serverVer:   "9.9.9",
			args:        args{in: PingInput{}},
			wantResult:  nil,
			wantServer:  "custom-srv",
			wantVersion: "9.9.9",
			wantErr:     nil,
		},
	}

	ctx := context.Background()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := &mcp.CallToolRequest{}
			handler := pingHandler(tt.serverName, tt.serverVer)

			result, output, err := handler(ctx, req, tt.args.in)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("pingHandler() error = %v, wantErr %v", err, tt.wantErr)
			}
			if result != tt.wantResult {
				t.Errorf("pingHandler() result = %v, want %v", result, tt.wantResult)
			}
			if output.Server != tt.wantServer {
				t.Errorf("pingHandler() output.Server = %q, want %q", output.Server, tt.wantServer)
			}
			if output.Version != tt.wantVersion {
				t.Errorf("pingHandler() output.Version = %q, want %q", output.Version, tt.wantVersion)
			}
			if _, err := time.Parse(time.RFC3339, output.At); err != nil {
				t.Errorf("pingHandler() output.At = %q, not a valid RFC3339 timestamp: %v", output.At, err)
			}
		})
	}
}
