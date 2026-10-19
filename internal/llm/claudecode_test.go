package llm_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/jameynakama/whatnow/internal/llm"
)

func TestClaudeCodeCompleteLocksTheCLIDown(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "sk-ant-must-not-be-used")
	t.Setenv("ANTHROPIC_AUTH_TOKEN", "also-not")
	client, dir, read := fakeClaudeCode(t, "text")

	got, err := client.Complete(context.Background(), llm.Request{
		Model:  "haiku",
		System: []llm.Block{{Text: "You plan days.", Cache: true}, {Text: "Today is Monday."}},
		User:   "plan it",
	})
	if err != nil || got != "ok" {
		t.Fatalf("Complete = %q, %v", got, err)
	}
	log := read()
	if log.Stdin != "plan it" {
		t.Errorf("stdin = %q", log.Stdin)
	}
	for name, want := range map[string]string{
		"--model": "haiku", "--system-prompt": "You plan days.\n\nToday is Monday.", "--tools": "",
		"--setting-sources": "", "--permission-mode": "dontAsk", "--output-format": "json",
	} {
		if v, ok := flagValue(log.Args, name); !ok || v != want {
			t.Errorf("%s = %q (present %v), want %q", name, v, ok, want)
		}
	}
	for _, name := range []string{"-p", "--strict-mcp-config", "--no-session-persistence"} {
		if !slices.Contains(log.Args, name) {
			t.Errorf("args lack %s: %q", name, log.Args)
		}
	}
	if slices.Contains(log.Args, "--json-schema") {
		t.Errorf("a schema was sent without one asked: %q", log.Args)
	}
	for _, kv := range log.Env {
		if strings.HasPrefix(kv, "ANTHROPIC_API_KEY=") || strings.HasPrefix(kv, "ANTHROPIC_AUTH_TOKEN=") {
			t.Errorf("the CLI got %s", kv)
		}
	}
	if !slices.Contains(log.Env, "CLAUDE_CODE_DISABLE_AUTO_MEMORY=1") {
		t.Error("auto memory isn't disabled")
	}
	want, _ := filepath.EvalSymlinks(dir)
	if got, _ := filepath.EvalSymlinks(log.Dir); got != want {
		t.Errorf("ran in %q, want %q", got, want)
	}
}

func TestClaudeCodeCompleteReturnsStructuredOutput(t *testing.T) {
	client, _, read := fakeClaudeCode(t, "schema")
	schema := map[string]any{"type": "object", "properties": map[string]any{"tier": map[string]any{"type": "string"}}}

	got, err := client.Complete(context.Background(), llm.Request{Model: "sonnet", User: "sort", Schema: schema, SchemaName: "plan"})
	if err != nil || got != `{"tier":"must"}` {
		t.Fatalf("Complete = %q, %v", got, err)
	}
	sent, _ := flagValue(read().Args, "--json-schema")
	var gotSchema map[string]any
	if err := json.Unmarshal([]byte(sent), &gotSchema); err != nil {
		t.Fatalf("--json-schema %q: %v", sent, err)
	}
	if diff := cmp.Diff(schema, gotSchema); diff != "" {
		t.Errorf("schema (-want +got):\n%s", diff)
	}
}

func TestClaudeCodeCompleteErrors(t *testing.T) {
	schema := map[string]any{"type": "object"}
	cases := []struct {
		scenario string
		schema   map[string]any
		check    func(error) bool
	}{
		{"no-structured-output", schema, func(err error) bool { return strings.Contains(err.Error(), "no structured output") }},
		{"signed-out", nil, func(err error) bool {
			return errors.Is(err, llm.ErrSignedOut) && strings.Contains(err.Error(), "Run `claude` in a terminal and sign in")
		}},
		{"failed", nil, func(err error) bool { return err.Error() == "claude code: API Error: 529 overloaded" }},
		{"crash", nil, func(err error) bool { return err.Error() == "claude code: error: unknown option '--frobnicate'" }},
	}
	for _, c := range cases {
		t.Run(c.scenario, func(t *testing.T) {
			client, _, _ := fakeClaudeCode(t, c.scenario)
			_, err := client.Complete(context.Background(), llm.Request{Model: "haiku", User: "hi", Schema: c.schema})
			if err == nil || !c.check(err) {
				t.Errorf("err = %v", err)
			}
		})
	}
}

func TestClaudeCodeCancelStopsTheProcess(t *testing.T) {
	client, _, _ := fakeClaudeCode(t, "hang")
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	start := time.Now()

	_, err := client.Complete(ctx, llm.Request{Model: "haiku", User: "hi"})

	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("err = %v, want the deadline", err)
	}
	if took := time.Since(start); took > 6*time.Second {
		t.Errorf("took %v to stop", took)
	}
}

func looperOf(t *testing.T, c llm.Client) llm.Looper {
	t.Helper()
	l, ok := c.(llm.Looper)
	if !ok {
		t.Fatal("the Claude Code client doesn't run its own loop")
	}
	return l
}

var echoTools = []llm.Tool{
	{Name: "echo", Description: "Says it back.", Schema: map[string]any{"type": "object",
		"properties": map[string]any{"text": map[string]any{"type": "string"}}, "required": []string{"text"}, "additionalProperties": false}},
	{Name: "broken", Description: "Always fails.", Schema: map[string]any{"type": "object",
		"properties": map[string]any{}, "required": []string{}, "additionalProperties": false}},
}

func TestClaudeCodeLoopStreamsWithABlankLineBetweenMessages(t *testing.T) {
	client, _, read := fakeClaudeCode(t, "stream")
	var pieces []string

	got, err := looperOf(t, client).Loop(context.Background(), llm.ChatRequest{
		Model:    "sonnet",
		Messages: []llm.Message{{Role: llm.RoleUser, Text: "earlier"}, {Role: llm.RoleAssistant, Text: "sure"}, {Role: llm.RoleUser, Text: "now"}},
	}, 10, nil, func(s string) { pieces = append(pieces, s) })

	if err != nil || got != "Hello\n\nAgain" {
		t.Fatalf("Loop = %q, %v", got, err)
	}
	if diff := cmp.Diff([]string{"Hel", "lo", "\n\nAgain"}, pieces); diff != "" {
		t.Errorf("streamed (-want +got):\n%s", diff)
	}
	log := read()
	if !strings.Contains(log.Stdin, "<assistant>\nsure\n</assistant>") || !strings.HasSuffix(log.Stdin, "now") {
		t.Errorf("stdin = %q", log.Stdin)
	}
	for name, want := range map[string]string{"--output-format": "stream-json", "--max-turns": "11", "--allowedTools": "mcp__whatnow__*"} {
		if v, _ := flagValue(log.Args, name); v != want {
			t.Errorf("%s = %q, want %q", name, v, want)
		}
	}
	for _, name := range []string{"--verbose", "--include-partial-messages", "--strict-mcp-config"} {
		if !slices.Contains(log.Args, name) {
			t.Errorf("args lack %s", name)
		}
	}
}

func TestClaudeCodeLoopServesToolsOverMCP(t *testing.T) {
	client, _, _ := fakeClaudeCode(t, "tools")
	var mu sync.Mutex
	var calls []llm.ToolCall
	run := func(_ context.Context, c llm.ToolCall) llm.ToolResult {
		mu.Lock()
		calls = append(calls, c)
		mu.Unlock()
		if c.Name == "broken" {
			return llm.ToolResult{CallID: c.ID, Content: "it broke", IsError: true}
		}
		var in struct{ Text string }
		json.Unmarshal(c.Input, &in)
		return llm.ToolResult{CallID: c.ID, Content: "you said " + in.Text}
	}

	got, err := looperOf(t, client).Loop(context.Background(), llm.ChatRequest{
		Model: "sonnet", Tools: echoTools, Messages: []llm.Message{{Role: llm.RoleUser, Text: "go"}},
	}, 10, run, func(string) {})

	want := "type http; no token: 401 Unauthorized; tools: broken,echo; echo: you said hi; broken: true it broke"
	if err != nil || got != want {
		t.Fatalf("Loop = %q, %v\nwant %q", got, err, want)
	}
	if len(calls) != 2 || string(calls[1].Input) != "{}" {
		t.Errorf("calls = %+v; a call with no arguments must arrive as {}", calls)
	}
}

func TestClaudeCodeLoopFallsBackToTheResultText(t *testing.T) {
	client, _, _ := fakeClaudeCode(t, "result-only")
	var streamed []string

	got, err := looperOf(t, client).Loop(context.Background(), llm.ChatRequest{Model: "sonnet",
		Messages: []llm.Message{{Role: llm.RoleUser, Text: "go"}}}, 10, nil, func(d string) { streamed = append(streamed, d) })

	if err != nil || got != "Just this" || !slices.Equal(streamed, []string{"Just this"}) {
		t.Errorf("Loop = %q, %v; streamed %q", got, err, streamed)
	}
}

func TestClaudeCodeLoopPastMaxTurnsKeepsItsText(t *testing.T) {
	client, _, _ := fakeClaudeCode(t, "max-turns")

	got, err := looperOf(t, client).Loop(context.Background(), llm.ChatRequest{Model: "sonnet",
		Messages: []llm.Message{{Role: llm.RoleUser, Text: "go"}}}, 10, nil, func(string) {})

	if !errors.Is(err, llm.ErrTooManyRounds) || got != "Still going" {
		t.Errorf("Loop = %q, %v", got, err)
	}
}

func TestClaudeCodeLoopCancelStopsEverything(t *testing.T) {
	client, _, read := fakeClaudeCode(t, "loop-hang")
	// Long enough for the fake to start and log its arguments.
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	_, err := looperOf(t, client).Loop(ctx, llm.ChatRequest{Model: "sonnet", Tools: echoTools,
		Messages: []llm.Message{{Role: llm.RoleUser, Text: "go"}}}, 10, nil, func(string) {})

	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("err = %v, want the deadline", err)
	}
	raw, _ := flagValue(read().Args, "--mcp-config")
	var cfg struct {
		MCPServers map[string]struct{ URL string } `json:"mcpServers"`
	}
	json.Unmarshal([]byte(raw), &cfg)
	if resp, err := http.Post(cfg.MCPServers["whatnow"].URL, "application/json", strings.NewReader("{}")); err == nil {
		resp.Body.Close()
		t.Error("the tool server is still up after the turn")
	}
}

// TestLiveClaudeCode runs the real CLI on the user's plan. WHATNOW_LIVE=1 turns it on.
func TestLiveClaudeCode(t *testing.T) {
	if os.Getenv("WHATNOW_LIVE") != "1" {
		t.Skip("set WHATNOW_LIVE=1 to call the real claude")
	}
	path, err := exec.LookPath("claude")
	if err != nil {
		t.Fatal(err)
	}
	client := llm.NewClaudeCode(path, t.TempDir())
	ctx := context.Background()
	got, err := client.Complete(ctx, llm.Request{Model: "haiku", User: "Reply with the single word: ok"})
	if err != nil || !strings.Contains(strings.ToLower(got), "ok") {
		t.Fatalf("Complete = %q, %v", got, err)
	}
	var said []string
	run := func(_ context.Context, c llm.ToolCall) llm.ToolResult {
		said = append(said, string(c.Input))
		return llm.ToolResult{CallID: c.ID, Content: "echoed"}
	}
	text, err := looperOf(t, client).Loop(ctx, llm.ChatRequest{Model: "haiku", Tools: echoTools[:1],
		Messages: []llm.Message{{Role: llm.RoleUser, Text: "Call the echo tool with text hi, then tell me what it returned."}}},
		10, run, func(string) {})
	if err != nil || len(said) == 0 {
		t.Fatalf("Loop = %q, %v; tool inputs %q", text, err, said)
	}
	t.Logf("reply: %s", text)
}
