package llm

import (
	"context"
	"net/http"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type bearer struct{ token string }

func (b bearer) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header.Set("Authorization", "Bearer "+b.token)
	return http.DefaultTransport.RoundTrip(r)
}

func TestClosingTheToolServerEndsItsSessions(t *testing.T) {
	ts, err := serveTools(context.Background(), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "1"}, nil)
	cs, err := client.Connect(context.Background(), &mcp.StreamableClientTransport{
		Endpoint: ts.url, HTTPClient: &http.Client{Transport: bearer{ts.token}}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()
	var open int
	for range ts.server.Sessions() {
		open++
	}
	if open != 1 {
		t.Fatalf("%d sessions open before Close, want 1", open)
	}

	ts.Close()

	for range ts.server.Sessions() {
		t.Error("a session is still open after Close")
	}
}
