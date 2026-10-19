package llm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/github/copilot-sdk/go"
	"github.com/github/copilot-sdk/go/rpc"
)

// copilotRuntime starts Copilot sessions. CopilotRuntime is the real one; tests use a fake.
type copilotRuntime interface {
	session(ctx context.Context, cfg *copilot.SessionConfig) (copilotSession, error)
}

// copilotSession is the part of a Copilot session whatnow uses.
type copilotSession interface {
	On(handler copilot.SessionEventHandler) func()
	Send(ctx context.Context, opts copilot.MessageOptions) (string, error)
	Abort(ctx context.Context) error
	Disconnect() error
}

// CopilotRuntime is one Copilot runtime process, shared by every Copilot call. It starts on first
// use, and again on the next call if it died.
type CopilotRuntime struct {
	path, dir string
	mu        sync.Mutex
	client    *copilot.Client
}

// NewCopilotRuntime returns a runtime that runs the Copilot CLI at path, keeping its state in dir.
func NewCopilotRuntime(path, dir string) *CopilotRuntime {
	return &CopilotRuntime{path: path, dir: dir}
}

func (r *CopilotRuntime) Path() string { return r.path }

func (r *CopilotRuntime) start(ctx context.Context) (*copilot.Client, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.client != nil {
		if _, err := r.client.GetStatus(ctx); err == nil {
			return r.client, nil
		}
		r.client.ForceStop()
		r.client = nil
	}
	client := copilot.NewClient(&copilot.ClientOptions{
		Connection:       copilot.StdioConnection{Path: r.path},
		Mode:             copilot.ModeEmpty,
		BaseDirectory:    r.dir,
		WorkingDirectory: r.dir,
		GitHubToken:      githubToken(),
	})
	if err := client.Start(ctx); err != nil {
		return nil, fmt.Errorf("copilot: %w", err)
	}
	r.client = client
	return client, nil
}

func (r *CopilotRuntime) session(ctx context.Context, cfg *copilot.SessionConfig) (copilotSession, error) {
	client, err := r.start(ctx)
	if err != nil {
		return nil, err
	}
	s, err := client.CreateSession(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("copilot: %w", err)
	}
	return &sdkSession{Session: s, client: client}, nil
}

// Stop ends the runtime process, if it started.
func (r *CopilotRuntime) Stop() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.client == nil {
		return nil
	}
	err := r.client.Stop()
	r.client = nil
	return err
}

// sdkSession deletes its saved state when it disconnects, so sessions don't pile up on disk.
type sdkSession struct {
	*copilot.Session
	client *copilot.Client
}

func (s *sdkSession) Disconnect() error {
	err := s.Session.Disconnect()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return errors.Join(err, s.client.DeleteSession(ctx, s.SessionID))
}

// githubToken is the token to hand the runtime. It's empty when the environment has one, since the
// runtime reads that itself. Otherwise it's gh's, since ModeEmpty may keep the runtime from its
// own stored sign-in.
func githubToken() string {
	for _, v := range []string{"COPILOT_GITHUB_TOKEN", "GH_TOKEN", "GITHUB_TOKEN"} {
		if os.Getenv(v) != "" {
			return ""
		}
	}
	gh, err := exec.LookPath("gh")
	if err != nil {
		return ""
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, gh, "auth", "token").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

type copilotClient struct {
	rt  copilotRuntime
	dir string
}

// NewCopilot returns a Client that runs each call in a fresh session on rt, working in dir.
func NewCopilot(rt *CopilotRuntime, dir string) Client {
	return &copilotClient{rt: rt, dir: dir}
}

func (c *copilotClient) Complete(ctx context.Context, req Request) (string, error) {
	system := flatSystem(req.System)
	if req.Schema != nil {
		schema, err := json.Marshal(req.Schema)
		if err != nil {
			return "", err
		}
		system += "\n\nReply with only a JSON object that matches this JSON schema, with no other text:\n" + string(schema)
	}
	turn := newCopilotTurn(0, func(string) {})
	text, err := c.send(ctx, &copilot.SessionConfig{Model: req.Model, SystemMessage: replace(system)}, req.User, turn)
	if err != nil || req.Schema == nil {
		return text, err
	}
	return unfence(text), nil
}

// Chat is not how Copilot chats: the agent uses Loop, since the runtime runs the tool loop.
func (c *copilotClient) Chat(context.Context, ChatRequest, func(string)) (Reply, error) {
	return Reply{}, errors.New("copilot: use Loop")
}

func (c *copilotClient) Loop(ctx context.Context, req ChatRequest, maxRounds int, run RunTool, onText func(string)) (string, error) {
	turn := newCopilotTurn(maxRounds, onText)
	cfg := &copilot.SessionConfig{Model: req.Model, SystemMessage: replace(flatSystem(req.System))}
	for _, t := range req.Tools {
		cfg.AvailableTools = append(cfg.AvailableTools, "custom:"+t.Name)
		cfg.Tools = append(cfg.Tools, copilot.Tool{
			Name: t.Name, Description: t.Description, Parameters: t.Schema, SkipPermission: true,
			Handler: func(inv copilot.ToolInvocation) (copilot.ToolResult, error) {
				if turn.over() {
					return copilot.ToolResult{TextResultForLLM: "Stopped: too many rounds of tool calls.", ResultType: "failure"}, nil
				}
				input, err := json.Marshal(inv.Arguments)
				if err != nil || string(input) == "null" {
					input = []byte("{}")
				}
				res := run(ctx, ToolCall{ID: inv.ToolCallID, Name: t.Name, Input: input})
				if res.IsError {
					return copilot.ToolResult{TextResultForLLM: res.Content, ResultType: "failure", Error: res.Content}, nil
				}
				return copilot.ToolResult{TextResultForLLM: res.Content, ResultType: "success"}, nil
			},
		})
	}
	return c.send(ctx, cfg, transcript(req.Messages), turn)
}

func replace(system string) *copilot.SystemMessageConfig {
	return &copilot.SystemMessageConfig{Mode: "replace", Content: system}
}

// send runs one prompt in a fresh, locked-down session and waits until the model is done, it
// fails, the turn goes past its rounds, or ctx ends. Anything but done aborts the session.
func (c *copilotClient) send(ctx context.Context, cfg *copilot.SessionConfig, prompt string, turn *copilotTurn) (string, error) {
	cfg.Streaming = copilot.Bool(true)
	cfg.SkipCustomInstructions = copilot.Bool(true)
	cfg.EnableConfigDiscovery = copilot.Bool(false)
	cfg.EnableSessionStore = copilot.Bool(false)
	cfg.WorkingDirectory = c.dir
	cfg.OnPermissionRequest = rejectPermission
	s, err := c.rt.session(ctx, cfg)
	if err != nil {
		return "", err
	}
	defer s.Disconnect()
	defer s.On(turn.handle)()
	if _, err := s.Send(ctx, copilot.MessageOptions{Prompt: prompt}); err != nil {
		return "", fmt.Errorf("copilot: %w", err)
	}
	select {
	case err = <-turn.done:
	case <-ctx.Done():
		err = ctx.Err()
	}
	if err != nil {
		abort, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		s.Abort(abort)
		cancel()
	}
	return turn.result(), err
}

// rejectPermission refuses every permission request: whatnow's own tools skip permission, and
// nothing else should run.
func rejectPermission(copilot.PermissionRequest, copilot.PermissionInvocation) (rpc.PermissionDecision, error) {
	return &rpc.PermissionDecisionReject{}, nil
}

// copilotTurn follows one send's events: it streams text, joins each message's text, counts
// rounds of tool requests, and says when the turn ends and why.
type copilotTurn struct {
	maxRounds int
	onText    func(string)
	done      chan error
	mu        sync.Mutex
	text      string
	lastMsg   string
	streamed  bool
	rounds    int
}

func newCopilotTurn(maxRounds int, onText func(string)) *copilotTurn {
	return &copilotTurn{maxRounds: maxRounds, onText: onText, done: make(chan error, 1)}
}

func (t *copilotTurn) handle(e copilot.SessionEvent) {
	t.mu.Lock()
	defer t.mu.Unlock()
	switch d := e.Data.(type) {
	case *copilot.AssistantMessageDeltaData:
		if d.ParentToolCallID != nil || d.DeltaContent == "" {
			return
		}
		delta := d.DeltaContent
		if d.MessageID != t.lastMsg {
			if t.streamed {
				delta = "\n\n" + delta
			}
			t.lastMsg = d.MessageID
		}
		t.streamed = true
		t.onText(delta)
	case *copilot.AssistantMessageData:
		if d.ParentToolCallID != nil {
			return
		}
		t.text = joinText(t.text, d.Content)
		if len(d.ToolRequests) > 0 {
			if t.rounds++; t.rounds > t.maxRounds {
				t.finish(ErrTooManyRounds)
			}
		}
	case *copilot.SessionIdleData:
		t.finish(nil)
	case *copilot.SessionErrorData:
		t.finish(copilotError(d))
	}
}

// finish reports the first way the turn ended. Later ones are dropped.
func (t *copilotTurn) finish(err error) {
	select {
	case t.done <- err:
	default:
	}
}

func (t *copilotTurn) over() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.rounds > t.maxRounds
}

func (t *copilotTurn) result() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.text
}

func copilotError(d *copilot.SessionErrorData) error {
	switch d.ErrorType {
	case "authentication", "authorization":
		return fmt.Errorf("GitHub Copilot is %w. Run `gh auth login`, or `copilot` and sign in", ErrSignedOut)
	case "quota":
		return errors.New("GitHub Copilot's usage limit is reached")
	}
	return fmt.Errorf("copilot: %s", d.Message)
}

// unfence strips a Markdown code fence from a JSON reply, which models add though told not to.
func unfence(s string) string {
	s = strings.TrimSpace(s)
	if !strings.HasPrefix(s, "```") {
		return s
	}
	s = strings.TrimPrefix(s, "```")
	s = strings.TrimPrefix(s, "json")
	return strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(s), "```"))
}
