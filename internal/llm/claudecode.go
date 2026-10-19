package llm

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"slices"
	"strconv"
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

// Loop runs the whole turn in one claude process, which calls the turn's tools through a
// loopback MCP server.
func (c *claudeCode) Loop(ctx context.Context, req ChatRequest, maxRounds int, run RunTool, onText func(string)) (string, error) {
	tools, err := serveTools(ctx, req.Tools, run)
	if err != nil {
		return "", fmt.Errorf("claude code: serve tools: %w", err)
	}
	defer tools.Close()
	args := append(c.args(req.Model, req.System),
		"--output-format", "stream-json", "--verbose", "--include-partial-messages",
		"--max-turns", strconv.Itoa(maxRounds+1),
		"--mcp-config", tools.config(), "--allowedTools", "mcp__whatnow__*")
	cmd := c.command(ctx, args, transcript(req.Messages))
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.StdoutPipe()
	if err != nil {
		return "", err
	}
	if err := cmd.Start(); err != nil {
		return "", fmt.Errorf("claude code: %w", err)
	}
	text, res, readErr := readStream(out, onText)
	waitErr := cmd.Wait()
	switch {
	case res != nil:
		err := res.err()
		// A reply that streamed nothing still has its text in the result.
		if err == nil && text == "" && res.Result != "" {
			text = res.Result
			onText(text)
		}
		return text, err
	case readErr != nil && ctx.Err() == nil:
		return text, fmt.Errorf("claude code: %w", readErr)
	}
	return text, claudeFailure(ctx, waitErr, stderr.String())
}

// claudeEvent is one line of stream-json. Only text deltas, message starts and the result matter.
type claudeEvent struct {
	claudeResult
	Event struct {
		Type  string `json:"type"`
		Delta struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"delta"`
	} `json:"event"`
}

// readStream reads events until the result. Text deltas go to onText, with a blank line before
// the first text of a message that follows earlier text. A line that isn't JSON is skipped.
func readStream(r io.Reader, onText func(string)) (string, *claudeResult, error) {
	var text strings.Builder
	sep := false
	lines := bufio.NewScanner(r)
	lines.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	for lines.Scan() {
		var e claudeEvent
		if json.Unmarshal(lines.Bytes(), &e) != nil {
			continue
		}
		switch {
		case e.Type == "result":
			return text.String(), &e.claudeResult, nil
		case e.Type != "stream_event":
		case e.Event.Type == "message_start":
			sep = text.Len() > 0
		case e.Event.Type == "content_block_delta" && e.Event.Delta.Type == "text_delta" && e.Event.Delta.Text != "":
			delta := e.Event.Delta.Text
			if sep {
				delta, sep = "\n\n"+delta, false
			}
			text.WriteString(delta)
			onText(delta)
		}
	}
	return text.String(), nil, lines.Err()
}
