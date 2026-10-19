package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"slices"
	"strings"
	"time"
)

// claudeCode runs the user's own claude CLI headless, one process per call, so whatnow runs on
// their Claude plan rather than an API key.
type claudeCode struct {
	path string
	dir  string
}

// NewClaudeCode returns a Client that runs the claude executable at path. It runs in dir, an
// empty directory of whatnow's own, so no project's CLAUDE.md or settings apply.
func NewClaudeCode(path, dir string) Client {
	return &claudeCode{path: path, dir: dir}
}

// args are what every call passes: no built-in tools, no user or project settings, no MCP
// servers but whatnow's own, nothing saved, and nothing that asks permission.
func (c *claudeCode) args(model string, system []Block) []string {
	return []string{"-p", "--model", model, "--system-prompt", flatSystem(system),
		"--tools", "", "--setting-sources", "", "--strict-mcp-config",
		"--no-session-persistence", "--permission-mode", "dontAsk"}
}

// command makes a claude process that reads input on stdin. It drops Anthropic API credentials
// from the environment: in -p mode a key there wins over the user's sign-in.
func (c *claudeCode) command(ctx context.Context, args []string, input string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, c.path, args...)
	cmd.Dir = c.dir
	cmd.Stdin = strings.NewReader(input)
	cmd.Env = append(slices.DeleteFunc(os.Environ(), func(kv string) bool {
		return strings.HasPrefix(kv, "ANTHROPIC_API_KEY=") || strings.HasPrefix(kv, "ANTHROPIC_AUTH_TOKEN=")
	}), "CLAUDE_CODE_DISABLE_AUTO_MEMORY=1")
	cmd.Cancel = func() error { return cmd.Process.Signal(os.Interrupt) }
	cmd.WaitDelay = 5 * time.Second
	return cmd
}

func (c *claudeCode) Complete(ctx context.Context, req Request) (string, error) {
	args := append(c.args(req.Model, req.System), "--output-format", "json")
	if req.Schema != nil {
		schema, err := json.Marshal(req.Schema)
		if err != nil {
			return "", err
		}
		args = append(args, "--json-schema", string(schema))
	}
	cmd := c.command(ctx, args, req.User)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, runErr := cmd.Output()
	var res claudeResult
	if err := json.Unmarshal(out, &res); err != nil || res.Type != "result" {
		return "", claudeFailure(ctx, runErr, stderr.String())
	}
	if err := res.err(); err != nil {
		return "", err
	}
	if req.Schema == nil {
		return res.Result, nil
	}
	if len(res.StructuredOutput) == 0 || string(res.StructuredOutput) == "null" {
		return "", errors.New("claude code: no structured output")
	}
	return string(res.StructuredOutput), nil
}

// Chat is not how Claude Code chats: the agent uses Loop, since the CLI runs the tool loop.
func (c *claudeCode) Chat(context.Context, ChatRequest, func(string)) (Reply, error) {
	return Reply{}, errors.New("claude code: use Loop")
}

// claudeResult is the CLI's final event: the reply, or why there isn't one.
type claudeResult struct {
	Type             string            `json:"type"`
	Subtype          string            `json:"subtype"`
	IsError          bool              `json:"is_error"`
	Result           string            `json:"result"`
	StructuredOutput json.RawMessage   `json:"structured_output"`
	Errors           []json.RawMessage `json:"errors"`
}

func (r claudeResult) err() error {
	if r.Subtype == "error_max_turns" {
		return ErrTooManyRounds
	}
	if !r.IsError && r.Subtype == "success" {
		return nil
	}
	msg := r.Result
	if msg == "" {
		var parts []string
		for _, e := range r.Errors {
			var s string
			if json.Unmarshal(e, &s) != nil {
				s = string(e)
			}
			parts = append(parts, s)
		}
		msg = strings.Join(parts, "; ")
	}
	if strings.Contains(msg, "Not logged in") || strings.Contains(msg, "/login") {
		return fmt.Errorf("Claude Code is %w. Run `claude` in a terminal and sign in", ErrSignedOut)
	}
	if msg == "" {
		msg = r.Subtype
	}
	return fmt.Errorf("claude code: %s", msg)
}

// claudeFailure explains a process that ended without a result: the context's error if it was
// cancelled, else the last thing it said on stderr.
func claudeFailure(ctx context.Context, runErr error, stderr string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	lines := strings.Split(strings.TrimSpace(stderr), "\n")
	if last := strings.TrimSpace(lines[len(lines)-1]); last != "" {
		return fmt.Errorf("claude code: %s", last)
	}
	if runErr != nil {
		return fmt.Errorf("claude code: %w", runErr)
	}
	return errors.New("claude code: no result")
}
