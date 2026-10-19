package llm

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// toolServer offers a turn's tools over MCP on loopback, for the claude CLI to call. Only a
// request with its bearer token gets through.
type toolServer struct {
	url, token string
	srv        *http.Server
}

// serveTools starts a toolServer. Each call runs through run with ctx, the turn's context, not
// the request's, so a dropped connection can't cut a write short.
func serveTools(ctx context.Context, tools []Tool, run RunTool) (*toolServer, error) {
	server := mcp.NewServer(&mcp.Implementation{Name: "whatnow", Version: "1"}, nil)
	var calls atomic.Int64
	for _, t := range tools {
		server.AddTool(&mcp.Tool{Name: t.Name, Description: t.Description, InputSchema: t.Schema},
			func(_ context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
				input := req.Params.Arguments
				if len(input) == 0 || string(input) == "null" {
					input = json.RawMessage("{}")
				}
				res := run(ctx, ToolCall{ID: fmt.Sprintf("call-%d", calls.Add(1)), Name: t.Name, Input: input})
				return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: res.Content}}, IsError: res.IsError}, nil
			})
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	ts := &toolServer{url: "http://" + ln.Addr().String() + "/mcp", token: rand.Text()}
	handler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, nil)
	ts.srv = &http.Server{
		ReadHeaderTimeout: 10 * time.Second,
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if subtle.ConstantTimeCompare([]byte(r.Header.Get("Authorization")), []byte("Bearer "+ts.token)) != 1 {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
			handler.ServeHTTP(w, r)
		}),
	}
	go ts.srv.Serve(ln)
	return ts, nil
}

func (ts *toolServer) Close() error { return ts.srv.Close() }

// config is the CLI's --mcp-config: this server, named whatnow, so its tools are mcp__whatnow__*.
func (ts *toolServer) config() string {
	b, _ := json.Marshal(map[string]any{"mcpServers": map[string]any{"whatnow": map[string]any{
		"type": "http", "url": ts.url, "headers": map[string]string{"Authorization": "Bearer " + ts.token}}}})
	return string(b)
}
