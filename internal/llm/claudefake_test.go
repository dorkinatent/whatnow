package llm_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/jameynakama/whatnow/internal/llm"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestMain(m *testing.M) {
	if scenario := os.Getenv("WHATNOW_FAKE_CLAUDE"); scenario != "" {
		os.Exit(fakeClaude(scenario))
	}
	os.Exit(m.Run())
}

// fakeLog is what the fake was run with.
type fakeLog struct {
	Args  []string `json:"args"`
	Stdin string   `json:"stdin"`
	Env   []string `json:"env"`
	Dir   string   `json:"dir"`
}

// fakeClaude logs how it was run to WHATNOW_FAKE_CLAUDE_LOG, then prints what the scenario says,
// in the shapes Task 1 checked.
func fakeClaude(scenario string) int {
	stdin, _ := io.ReadAll(os.Stdin)
	dir, _ := os.Getwd()
	b, _ := json.Marshal(fakeLog{Args: os.Args[1:], Stdin: string(stdin), Env: os.Environ(), Dir: dir})
	if err := os.WriteFile(os.Getenv("WHATNOW_FAKE_CLAUDE_LOG"), b, 0o600); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	emit := func(v any) {
		b, _ := json.Marshal(v)
		fmt.Println(string(b))
	}
	result := func(subtype string, isError bool, text string) map[string]any {
		return map[string]any{"type": "result", "subtype": subtype, "is_error": isError, "result": text, "session_id": "s1"}
	}
	switch scenario {
	case "text":
		emit(result("success", false, "ok"))
	case "schema":
		r := result("success", false, "")
		r["structured_output"] = map[string]any{"tier": "must"}
		emit(r)
	case "no-structured-output":
		emit(result("success", false, "here you go"))
	case "signed-out":
		emit(result("success", true, "Not logged in · Please run /login"))
	case "failed":
		r := result("error_during_execution", true, "")
		r["errors"] = []any{"API Error: 529 overloaded"}
		emit(r)
	case "crash":
		fmt.Fprintln(os.Stderr, "starting up\nerror: unknown option '--frobnicate'")
		return 1
	case "hang":
		time.Sleep(time.Minute)
	default:
		return fakeClaudeLoop(scenario, os.Args[1:], emit, result)
	}
	return 0
}

// fakeClaudeCode returns a client that runs the fake for scenario in a fresh directory, and a
// function that reads what the fake was run with.
func fakeClaudeCode(t *testing.T, scenario string) (llm.Client, string, func() fakeLog) {
	t.Helper()
	logPath := filepath.Join(t.TempDir(), "log.json")
	t.Setenv("WHATNOW_FAKE_CLAUDE", scenario)
	t.Setenv("WHATNOW_FAKE_CLAUDE_LOG", logPath)
	dir := t.TempDir()
	read := func() fakeLog {
		t.Helper()
		var l fakeLog
		b, err := os.ReadFile(logPath)
		if err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(b, &l); err != nil {
			t.Fatal(err)
		}
		return l
	}
	return llm.NewClaudeCode(os.Args[0], dir), dir, read
}

// flagValue returns the argument after name, and whether name is there.
func flagValue(args []string, name string) (string, bool) {
	i := slices.Index(args, name)
	if i < 0 || i+1 >= len(args) {
		return "", false
	}
	return args[i+1], true
}

// fakeClaudeLoop plays the stream-json scenarios.
func fakeClaudeLoop(scenario string, args []string, emit func(any), result func(string, bool, string) map[string]any) int {
	messageStart := func() { emit(map[string]any{"type": "stream_event", "event": map[string]any{"type": "message_start"}}) }
	delta := func(text string) {
		emit(map[string]any{"type": "stream_event", "parent_tool_use_id": nil, "event": map[string]any{
			"type": "content_block_delta", "index": 0, "delta": map[string]any{"type": "text_delta", "text": text}}})
	}
	emit(map[string]any{"type": "system", "subtype": "init", "session_id": "s1"})
	switch scenario {
	case "stream":
		messageStart()
		delta("Hel")
		delta("lo")
		fmt.Println("not json: a stray warning")
		messageStart()
		delta("Again")
		emit(result("success", false, "Hello\n\nAgain"))
	case "tools":
		messageStart()
		delta(fakeUseTools(args))
		emit(result("success", false, ""))
	case "max-turns":
		messageStart()
		delta("Still going")
		emit(result("error_max_turns", true, ""))
	case "loop-hang":
		time.Sleep(time.Minute)
	default:
		fmt.Fprintln(os.Stderr, "no scenario", scenario)
		return 2
	}
	return 0
}

// fakeUseTools does what the CLI does with --mcp-config: connects to whatnow's server, lists its
// tools, and calls them. It reports what happened, and first tries the server with no token.
func fakeUseTools(args []string) string {
	raw, _ := flagValue(args, "--mcp-config")
	var cfg struct {
		MCPServers map[string]struct {
			Type    string            `json:"type"`
			URL     string            `json:"url"`
			Headers map[string]string `json:"headers"`
		} `json:"mcpServers"`
	}
	if err := json.Unmarshal([]byte(raw), &cfg); err != nil {
		return "bad config: " + err.Error()
	}
	srv := cfg.MCPServers["whatnow"]
	noToken := "unreachable"
	if resp, err := http.Post(srv.URL, "application/json", strings.NewReader("{}")); err == nil {
		noToken = resp.Status
		resp.Body.Close()
	}
	ctx := context.Background()
	hc := &http.Client{Transport: headers(srv.Headers)}
	session, err := mcp.NewClient(&mcp.Implementation{Name: "fake-claude", Version: "1"}, nil).
		Connect(ctx, &mcp.StreamableClientTransport{Endpoint: srv.URL, HTTPClient: hc}, nil)
	if err != nil {
		return "connect: " + err.Error()
	}
	defer session.Close()
	list, err := session.ListTools(ctx, nil)
	if err != nil {
		return "list: " + err.Error()
	}
	var names []string
	for _, tool := range list.Tools {
		names = append(names, tool.Name)
	}
	sort.Strings(names)
	echo, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "echo", Arguments: map[string]any{"text": "hi"}})
	if err != nil {
		return "echo: " + err.Error()
	}
	broken, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "broken"})
	if err != nil {
		return "broken: " + err.Error()
	}
	return fmt.Sprintf("type %s; no token: %s; tools: %s; echo: %s; broken: %v %s", srv.Type, noToken,
		strings.Join(names, ","), echo.Content[0].(*mcp.TextContent).Text,
		broken.IsError, broken.Content[0].(*mcp.TextContent).Text)
}

// headers sets fixed headers on every request.
type headers map[string]string

func (h headers) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	for k, v := range h {
		r.Header.Set(k, v)
	}
	return http.DefaultTransport.RoundTrip(r)
}
