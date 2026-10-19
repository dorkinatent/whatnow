package llm

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/github/copilot-sdk/go"
	"github.com/github/copilot-sdk/go/rpc"
	"github.com/google/go-cmp/cmp"
)

// fakeRuntime hands out one scripted session and keeps the config it got.
type fakeRuntime struct {
	sess *fakeSession
	err  error
	cfg  *copilot.SessionConfig
}

func (r *fakeRuntime) session(_ context.Context, cfg *copilot.SessionConfig) (copilotSession, error) {
	r.cfg = cfg
	if r.sess != nil {
		r.sess.cfg = cfg
	}
	return r.sess, r.err
}

// fakeSession plays its script on its own goroutine when sent a prompt, as the runtime would.
type fakeSession struct {
	script       func(s *fakeSession)
	cfg          *copilot.SessionConfig
	handler      copilot.SessionEventHandler
	prompt       string
	mu           sync.Mutex
	aborted      bool
	disconnected bool
}

func (s *fakeSession) On(h copilot.SessionEventHandler) func() { s.handler = h; return func() {} }

func (s *fakeSession) Send(_ context.Context, o copilot.MessageOptions) (string, error) {
	s.prompt = o.Prompt
	go s.script(s)
	return "m1", nil
}

func (s *fakeSession) Abort(context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.aborted = true
	return nil
}

func (s *fakeSession) Disconnect() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.disconnected = true
	return nil
}

func (s *fakeSession) emit(d copilot.SessionEventData) { s.handler(copilot.SessionEvent{Data: d}) }

func (s *fakeSession) message(id, content string, toolRequests int) {
	s.emit(&copilot.AssistantMessageData{MessageID: id, Content: content, ToolRequests: make([]copilot.AssistantMessageToolRequest, toolRequests)})
}

func (s *fakeSession) delta(id, text string) {
	s.emit(&copilot.AssistantMessageDeltaData{MessageID: id, DeltaContent: text})
}

// call runs a tool the way the SDK does, by its handler.
func (s *fakeSession) call(name string, args any) copilot.ToolResult {
	for _, t := range s.cfg.Tools {
		if t.Name == name {
			r, err := t.Handler(copilot.ToolInvocation{ToolName: name, ToolCallID: "tc-" + name, Arguments: args})
			if err != nil {
				return copilot.ToolResult{ResultType: "failure", Error: err.Error()}
			}
			return r
		}
	}
	panic("no tool " + name)
}

func newFakeCopilot(script func(*fakeSession)) (*copilotClient, *fakeRuntime) {
	rt := &fakeRuntime{sess: &fakeSession{script: script}}
	return &copilotClient{rt: rt, dir: "/tmp/whatnow-copilot"}, rt
}

func TestCopilotCompleteAsksForJSONAndUnfences(t *testing.T) {
	c, rt := newFakeCopilot(func(s *fakeSession) {
		s.delta("a", "```json\n")
		s.message("a", "```json\n{\"tier\":\"must\"}\n```", 0)
		s.emit(&copilot.SessionIdleData{})
	})
	schema := map[string]any{"type": "object"}

	got, err := c.Complete(context.Background(), Request{Model: "gpt-5-mini", System: []Block{{Text: "Sort tasks."}}, User: "file taxes", Schema: schema})

	if err != nil || got != `{"tier":"must"}` {
		t.Fatalf("Complete = %q, %v", got, err)
	}
	cfg := rt.cfg
	if cfg.Model != "gpt-5-mini" || cfg.SystemMessage.Mode != "replace" || len(cfg.Tools) != 0 {
		t.Errorf("config = %+v", cfg)
	}
	if !strings.HasPrefix(cfg.SystemMessage.Content, "Sort tasks.") || !strings.Contains(cfg.SystemMessage.Content, `{"type":"object"}`) ||
		!strings.Contains(cfg.SystemMessage.Content, "Reply with only a JSON object") {
		t.Errorf("system = %q", cfg.SystemMessage.Content)
	}
	if !*cfg.Streaming || !*cfg.SkipCustomInstructions || *cfg.EnableConfigDiscovery || *cfg.EnableSessionStore || cfg.WorkingDirectory != "/tmp/whatnow-copilot" {
		t.Errorf("config isn't locked down: %+v", cfg)
	}
	decision, _ := cfg.OnPermissionRequest(nil, copilot.PermissionInvocation{})
	if _, ok := decision.(*rpc.PermissionDecisionReject); !ok {
		t.Errorf("permission decision = %T, want a reject", decision)
	}
	if rt.sess.prompt != "file taxes" || !rt.sess.disconnected {
		t.Errorf("prompt %q, disconnected %v", rt.sess.prompt, rt.sess.disconnected)
	}
}

func TestCopilotLoopRunsToolsAndStreams(t *testing.T) {
	var result copilot.ToolResult
	var empty copilot.ToolResult
	c, rt := newFakeCopilot(func(s *fakeSession) {
		s.delta("a", "Looking.")
		s.message("a", "Looking.", 2)
		result = s.call("echo", map[string]any{"text": "hi"})
		empty = s.call("broken", nil)
		s.delta("b", "Done")
		s.delta("b", " now")
		s.message("b", "Done now", 0)
		s.emit(&copilot.SessionIdleData{})
	})
	var inputs []string
	run := func(_ context.Context, call ToolCall) ToolResult {
		inputs = append(inputs, string(call.Input))
		if call.Name == "broken" {
			return ToolResult{CallID: call.ID, Content: "it broke", IsError: true}
		}
		return ToolResult{CallID: call.ID, Content: "you said hi"}
	}
	tools := []Tool{{Name: "echo", Schema: map[string]any{"type": "object"}}, {Name: "broken", Schema: map[string]any{"type": "object"}}}
	var pieces []string

	got, err := c.Loop(context.Background(), ChatRequest{Model: "claude-sonnet-4.5", Tools: tools,
		Messages: []Message{{Role: RoleUser, Text: "earlier"}, {Role: RoleAssistant, Text: "ok"}, {Role: RoleUser, Text: "now"}}},
		10, run, func(s string) { pieces = append(pieces, s) })

	if err != nil || got != "Looking.\n\nDone now" {
		t.Fatalf("Loop = %q, %v", got, err)
	}
	if diff := cmp.Diff([]string{"Looking.", "\n\nDone", " now"}, pieces); diff != "" {
		t.Errorf("streamed (-want +got):\n%s", diff)
	}
	if result.ResultType != "success" || result.TextResultForLLM != "you said hi" {
		t.Errorf("echo result = %+v", result)
	}
	if empty.ResultType != "failure" || empty.TextResultForLLM != "it broke" {
		t.Errorf("broken result = %+v", empty)
	}
	if diff := cmp.Diff([]string{`{"text":"hi"}`, `{}`}, inputs); diff != "" {
		t.Errorf("tool inputs (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff([]string{"custom:echo", "custom:broken"}, rt.cfg.AvailableTools); diff != "" {
		t.Errorf("available tools (-want +got):\n%s", diff)
	}
	if !rt.cfg.Tools[0].SkipPermission || !strings.Contains(rt.sess.prompt, "<assistant>\nok\n</assistant>") {
		t.Errorf("tool %+v, prompt %q", rt.cfg.Tools[0], rt.sess.prompt)
	}
}

func TestCopilotLoopStopsPastMaxRounds(t *testing.T) {
	var second copilot.ToolResult
	// Loop returns as soon as the second round starts, while the script plays on.
	finished := make(chan struct{})
	c, rt := newFakeCopilot(func(s *fakeSession) {
		defer close(finished)
		s.message("a", "", 1)
		s.call("echo", map[string]any{})
		s.message("b", "", 1)
		second = s.call("echo", map[string]any{})
		s.emit(&copilot.SessionIdleData{})
	})
	runs := 0
	run := func(_ context.Context, call ToolCall) ToolResult {
		runs++
		return ToolResult{CallID: call.ID, Content: "ok"}
	}

	_, err := c.Loop(context.Background(), ChatRequest{Model: "m", Tools: []Tool{{Name: "echo", Schema: map[string]any{"type": "object"}}},
		Messages: []Message{{Role: RoleUser, Text: "go"}}}, 1, run, func(string) {})
	<-finished

	if !errors.Is(err, ErrTooManyRounds) || runs != 1 || second.ResultType != "failure" {
		t.Errorf("err %v, %d runs, second result %+v", err, runs, second)
	}
	if !rt.sess.aborted {
		t.Error("the session wasn't aborted")
	}
}

func TestCopilotErrors(t *testing.T) {
	sessionError := func(kind, msg string) func(*fakeSession) {
		return func(s *fakeSession) { s.emit(&copilot.SessionErrorData{ErrorType: kind, Message: msg}) }
	}
	cases := map[string]struct {
		script func(*fakeSession)
		check  func(error) bool
	}{
		"signed out": {sessionError("authentication", "no token"), func(err error) bool {
			return errors.Is(err, ErrSignedOut) && strings.Contains(err.Error(), "Run `gh auth login`")
		}},
		"no access": {sessionError("authorization", "no seat"), func(err error) bool { return errors.Is(err, ErrSignedOut) }},
		"quota":     {sessionError("quota", "out"), func(err error) bool { return err.Error() == "GitHub Copilot's usage limit is reached" }},
		"other":     {sessionError("query", "boom"), func(err error) bool { return err.Error() == "copilot: boom" }},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			c, _ := newFakeCopilot(tc.script)
			if _, err := c.Complete(context.Background(), Request{Model: "m", User: "hi"}); err == nil || !tc.check(err) {
				t.Errorf("err = %v", err)
			}
		})
	}
	c := &copilotClient{rt: &fakeRuntime{err: errors.New("copilot: runtime unavailable")}}
	if _, err := c.Complete(context.Background(), Request{Model: "m", User: "hi"}); err == nil || err.Error() != "copilot: runtime unavailable" {
		t.Errorf("runtime error = %v", err)
	}
}

func TestCopilotCancelAborts(t *testing.T) {
	c, rt := newFakeCopilot(func(*fakeSession) {})
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	_, err := c.Complete(ctx, Request{Model: "m", User: "hi"})

	if !errors.Is(err, context.DeadlineExceeded) || !rt.sess.aborted || !rt.sess.disconnected {
		t.Errorf("err %v, aborted %v, disconnected %v", err, rt.sess.aborted, rt.sess.disconnected)
	}
}

// TestLiveCopilot runs the real runtime on the user's plan. WHATNOW_LIVE=1 turns it on.
// WHATNOW_LIVE_COPILOT_MODEL picks the model; it lists the ones available.
func TestLiveCopilot(t *testing.T) {
	if os.Getenv("WHATNOW_LIVE") != "1" {
		t.Skip("set WHATNOW_LIVE=1 to call the real Copilot")
	}
	path, err := exec.LookPath("copilot")
	if err != nil {
		t.Fatal(err)
	}
	rt := NewCopilotRuntime(path, t.TempDir())
	defer rt.Stop()
	ctx := context.Background()
	client, err := rt.start(ctx)
	if err != nil {
		t.Fatal(err)
	}
	models, err := client.ListModels(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, m := range models {
		ids = append(ids, m.ID)
	}
	t.Logf("models: %s", strings.Join(ids, ", "))
	for _, want := range []string{"claude-sonnet-4.5", "gpt-5-mini"} {
		if !slices.Contains(ids, want) {
			t.Errorf("default model %s isn't offered; change settings.Defaults", want)
		}
	}
	model := os.Getenv("WHATNOW_LIVE_COPILOT_MODEL")
	if model == "" {
		model = "gpt-5-mini"
	}
	c := NewCopilot(rt, t.TempDir())
	got, err := c.Complete(ctx, Request{Model: model, User: "Reply with the single word: ok"})
	if err != nil || !strings.Contains(strings.ToLower(got), "ok") {
		t.Fatalf("Complete = %q, %v", got, err)
	}
	var said []string
	run := func(_ context.Context, call ToolCall) ToolResult {
		said = append(said, string(call.Input))
		return ToolResult{CallID: call.ID, Content: "echoed"}
	}
	text, err := c.(Looper).Loop(ctx, ChatRequest{Model: model,
		Tools: []Tool{{Name: "echo", Description: "Says it back.", Schema: map[string]any{"type": "object",
			"properties": map[string]any{"text": map[string]any{"type": "string"}}, "required": []string{"text"}, "additionalProperties": false}}},
		Messages: []Message{{Role: RoleUser, Text: "Call the echo tool with text hi, then tell me what it returned."}}},
		10, run, func(string) {})
	if err != nil || len(said) == 0 {
		t.Fatalf("Loop = %q, %v; tool inputs %q", text, err, said)
	}
	t.Logf("reply: %s", text)
}
