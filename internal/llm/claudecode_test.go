package llm_test

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"slices"
	"strings"
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
