# Claude Code and GitHub Copilot Providers Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Let whatnow run on the user's Claude plan (through their signed-in `claude` CLI) or their GitHub Copilot plan (through the Copilot Go SDK), with no API key.

**Architecture:** A new optional `llm.Looper` interface lets a backend run the whole chat tool loop itself, as both vendors' agents do. The agent uses it when the client has it, and keeps its own loop for Anthropic and OpenAI. The Claude Code backend runs `claude -p` per call, locked down by flags, and offers the turn's tools through a loopback MCP server. The Copilot backend shares one Copilot runtime per process and makes one locked-down session per call. Settings gain two keyless providers whose "key" is a CLI being found.

**Tech Stack:** Go 1.27, `github.com/modelcontextprotocol/go-sdk` v1.8.0, `github.com/github/copilot-sdk/go` v1.0.22, `go-cmp`; SvelteKit 2 / Svelte 5, vitest.

**Spec:** `docs/superpowers/specs/2026-10-19-whatnow-subscription-providers-design.md`

## Global Constraints

- Provider IDs `claudecode` and `copilot`; JSON config keys `claudecode` and `copilot`.
- Pinned deps: `github.com/modelcontextprotocol/go-sdk@v1.8.0`, `github.com/github/copilot-sdk/go@v1.0.22`. No other new Go modules.
- Claude Code CLI ≥ 2.1.303. Every `claude` call passes `-p --model <m> --system-prompt <s> --tools "" --setting-sources "" --strict-mcp-config --no-session-persistence --permission-mode dontAsk`, runs in whatnow's own empty directory, and has no `ANTHROPIC_API_KEY` or `ANTHROPIC_AUTH_TOKEN` in its environment.
- Copilot runs in `ModeEmpty`, with a permission handler that rejects everything, and whatnow's tools set `SkipPermission`.
- Work directories: `<os.UserCacheDir()>/whatnow/claudecode` and `<os.UserCacheDir()>/whatnow/copilot`, mode `0700`.
- No test calls a real model unless `WHATNOW_LIVE=1`.
- User-facing copy is the spec's, word for word.
- Commits: conventional commits, lowercase after the prefix. Gate: `just test` and `just lint`. Run `go test -race ./internal/agent/ ./internal/llm/` for Tasks 2–5.
- Installing the Copilot CLI and making live calls on the user's plans are the user's to approve. Ask first (Task 1).

## Review Focus

- `ANTHROPIC_API_KEY` set in the shell while Claude Code is the provider: the call must run on the sign-in, not bill the key. Pinned in Task 3 (`TestClaudeCodeCompleteLocksTheCLIDown` checks the child's environment).
- Copilot running two tool calls of one round at once, both writing: they must land in one batch, with no data race. Pinned in Task 2 (`TestALooperRunsTheTurnInOneBatch`, run with `-race`).
- A chat whose CLI hangs, or a user who gives up: the `claude` process must die, the MCP server must close, and the error must be the context's. Pinned in Task 4 (`TestClaudeCodeLoopCancelStopsEverything`).
- A signed-out CLI: the connection test and the chat must say so plainly, not "couldn't reach". Pinned in Task 3 (`signed out` case) and Task 2 (`TestASignedOutProviderSaysSo`).
- A tool the model calls with no arguments (`what_now`, `undo_last`): it must reach the tool as `{}`, not as empty input that fails to parse. Pinned in Task 4 (the `broken` call's input) and Task 5 (`nil` arguments).

---

### Task 1: Spike: confirm the CLIs on this machine

No code. This checks the assumptions the later tasks' fakes encode. Fix a fake in a later task if this finds different output.

**Files:**
- Modify: `docs/superpowers/specs/2026-10-19-whatnow-subscription-providers-design.md` (append a "Checked on <date>" section)

- [ ] **Step 1: Check Claude Code's version and sign-in**

```bash
claude --version          # want ≥ 2.1.303
claude auth status        # want "loggedIn": true
```

- [ ] **Step 2: A Complete-shaped call.** It runs on the user's plan (one Haiku call).

```bash
mkdir -p "$HOME/Library/Caches/whatnow/claudecode" && cd "$HOME/Library/Caches/whatnow/claudecode"
echo 'Reply with the single word: ok' | env -u ANTHROPIC_API_KEY -u ANTHROPIC_AUTH_TOKEN CLAUDE_CODE_DISABLE_AUTO_MEMORY=1 \
  claude -p --model haiku --system-prompt 'You are terse.' --tools "" --setting-sources "" \
  --strict-mcp-config --no-session-persistence --permission-mode dontAsk --output-format json
```

Expected: one JSON object with `"type":"result"`, `"subtype":"success"`, `"is_error":false`, `"result":"ok"`. If `--setting-sources ""` is rejected, retry with `--setting-sources local` and use that everywhere instead (the directory is empty, so `local` loads nothing).

- [ ] **Step 3: A schema call**

```bash
echo 'Pick a tier for: file taxes' | env -u ANTHROPIC_API_KEY claude -p --model haiku --system-prompt 'Sort tasks.' \
  --tools "" --setting-sources "" --strict-mcp-config --no-session-persistence --permission-mode dontAsk \
  --output-format json --json-schema '{"type":"object","properties":{"tier":{"type":"string","enum":["must","should","could"]}},"required":["tier"],"additionalProperties":false}'
```

Expected: `structured_output` is an object like `{"tier":"must"}`.

- [ ] **Step 4: The stream shape**

```bash
echo 'Say hello in five words.' | env -u ANTHROPIC_API_KEY claude -p --model haiku --system-prompt 'Be brief.' \
  --tools "" --setting-sources "" --strict-mcp-config --no-session-persistence --permission-mode dontAsk \
  --output-format stream-json --verbose --include-partial-messages | head -c 4000
```

Expected: lines with `"type":"stream_event"` whose `event.type` is `message_start` and `content_block_delta` (with `delta.type` `text_delta` and `delta.text`), and a final `"type":"result"` line. Task 4's fake prints exactly these shapes.

- [ ] **Step 5: The signed-out text**

```bash
echo hi | env -u ANTHROPIC_API_KEY CLAUDE_CONFIG_DIR="$(mktemp -d)" claude -p --output-format json --tools ""
```

Expected: a result with `is_error: true` and text containing "Not logged in" or "/login". Task 3's `signed-out` fake must contain whichever appears. If it doesn't sign out this way, keep the fake as written.

- [ ] **Step 6: Copilot CLI.** Ask the user before installing anything. Then:

```bash
brew install copilot-cli || npm install -g @github/copilot
copilot --version
gh auth status            # signed in, with a gho_ token
```

If the Homebrew formula has another name, use what installs `copilot`, and put that command in the spec's install note and Task 7's copy.

- [ ] **Step 7: Record and commit.** Append to the spec:

```markdown
## Checked on 2026-10-19

- claude <version>: flags accepted as listed; `--setting-sources ""` <accepted | replaced by `local`>.
- Signed-out result text: "<text>".
- copilot <version>, installed with `<command>`.
```

```bash
git add docs/superpowers/specs/2026-10-19-whatnow-subscription-providers-design.md docs/superpowers/plans/2026-10-19-whatnow-subscription-providers.md
git commit -m "docs: spec and plan the claude code and copilot providers"
```

---

### Task 2: `llm.Looper`, and the agent hands it the turn

**Files:**
- Modify: `internal/llm/llm.go`
- Create: `internal/llm/fold.go`
- Create: `internal/llm/fold_test.go` (package `llm`, internal)
- Modify: `internal/agent/agent.go`
- Modify: `internal/agent/helpers_test.go` (add the `looper` fake)
- Create: `internal/agent/looper_test.go`

**Interfaces:**
- Produces:
  - `llm.ErrTooManyRounds`, `llm.ErrSignedOut` (errors)
  - `type llm.RunTool func(ctx context.Context, c llm.ToolCall) llm.ToolResult`
  - `type llm.Looper interface { Loop(ctx context.Context, req llm.ChatRequest, maxRounds int, run llm.RunTool, onText func(string)) (string, error) }`
  - unexported in `llm`: `flatSystem([]Block) string`, `transcript([]Message) string`, `joinText(a, b string) string`

- [ ] **Step 1: Write the failing fold tests** in `internal/llm/fold_test.go`:

```go
package llm

import "testing"

func TestTranscriptFoldsEarlierMessagesAboveTheNewOne(t *testing.T) {
	got := transcript([]Message{
		{Role: RoleUser, Text: "move the dentist"},
		{Role: RoleAssistant, Text: "Moved to Thursday."},
		{Role: RoleUser, Text: "and the RFC?"},
	})
	want := "Earlier in this conversation:\n\n" +
		"<user>\nmove the dentist\n</user>\n\n" +
		"<assistant>\nMoved to Thursday.\n</assistant>\n\n" +
		"The user's new message:\n\nand the RFC?"
	if got != want {
		t.Errorf("transcript =\n%s\nwant\n%s", got, want)
	}
}

func TestTranscriptOfOneMessageIsJustIt(t *testing.T) {
	if got := transcript([]Message{{Role: RoleUser, Text: "hi"}}); got != "hi" {
		t.Errorf("transcript = %q", got)
	}
	if got := transcript(nil); got != "" {
		t.Errorf("empty transcript = %q", got)
	}
}

func TestFlatSystemJoinsBlocks(t *testing.T) {
	got := flatSystem([]Block{{Text: "You plan days.", Cache: true}, {Text: "Today is Monday."}})
	if got != "You plan days.\n\nToday is Monday." {
		t.Errorf("flatSystem = %q", got)
	}
}
```

- [ ] **Step 2: Run them to see them fail**

Run: `go test ./internal/llm/ -run 'Transcript|FlatSystem'`
Expected: FAIL, `undefined: transcript`.

- [ ] **Step 3: Add `internal/llm/fold.go`**

```go
package llm

import (
	"fmt"
	"strings"
)

// flatSystem joins a system prompt's blocks, for backends that take it as one string. Their
// cache marks are lost.
func flatSystem(blocks []Block) string {
	texts := make([]string, len(blocks))
	for i, b := range blocks {
		texts[i] = b.Text
	}
	return strings.Join(texts, "\n\n")
}

// transcript folds a chat into one prompt, for backends that can't be handed earlier turns: the
// earlier messages as a tagged record, then the last message, which is the user's.
func transcript(msgs []Message) string {
	if len(msgs) == 0 {
		return ""
	}
	last := msgs[len(msgs)-1].Text
	if len(msgs) == 1 {
		return last
	}
	var b strings.Builder
	b.WriteString("Earlier in this conversation:\n\n")
	for _, m := range msgs[:len(msgs)-1] {
		fmt.Fprintf(&b, "<%s>\n%s\n</%s>\n\n", m.Role, m.Text, m.Role)
	}
	b.WriteString("The user's new message:\n\n")
	b.WriteString(last)
	return b.String()
}

func joinText(a, b string) string {
	if a == "" || b == "" {
		return a + b
	}
	return a + "\n\n" + b
}
```

- [ ] **Step 4: Add the interface and errors to `internal/llm/llm.go`.** Extend the `var` block:

```go
	// ErrTooManyRounds means a Looper's model kept calling tools past the round limit.
	ErrTooManyRounds = errors.New("model kept calling tools past the round limit")
	// ErrSignedOut means a provider that runs on the user's own sign-in isn't signed in.
	ErrSignedOut = errors.New("not signed in")
```

After `type Reply`:

```go
// RunTool runs one tool call and answers it. A Looper may call it from several goroutines.
type RunTool func(ctx context.Context, c ToolCall) ToolResult

// Looper is a Client whose backend runs the tool loop itself, as agent SDKs do. Loop sends the
// conversation and runs the model's tool calls through run, for up to maxRounds rounds, until the
// model stops. It streams text to onText, with a blank line between separate model messages, and
// returns all of it, even alongside an error.
type Looper interface {
	Loop(ctx context.Context, req ChatRequest, maxRounds int, run RunTool, onText func(string)) (string, error)
}
```

- [ ] **Step 5: Run the fold tests**

Run: `go test ./internal/llm/`
Expected: PASS.

- [ ] **Step 6: Add the `looper` fake** to `internal/agent/helpers_test.go`, after `model`:

```go
// looper is a fake backend that runs the tool loop itself. Each round's calls run at once, as
// Copilot runs them; then it says its text and returns its error. It records what it was asked.
type looper struct {
	model
	rounds  [][]llm.ToolCall
	say     string
	err     error
	req     llm.ChatRequest
	max     int
	results [][]llm.ToolResult
}

func (l *looper) Loop(ctx context.Context, req llm.ChatRequest, maxRounds int, run llm.RunTool, onText func(string)) (string, error) {
	l.req, l.max = req, maxRounds
	for i, round := range l.rounds {
		if i == maxRounds {
			return "", llm.ErrTooManyRounds
		}
		results := make([]llm.ToolResult, len(round))
		var wg sync.WaitGroup
		for j, c := range round {
			wg.Go(func() { results[j] = run(ctx, c) })
		}
		wg.Wait()
		l.results = append(l.results, results)
	}
	if l.say != "" {
		onText(l.say)
	}
	return l.say, l.err
}
```

- [ ] **Step 7: Write the failing agent tests** in `internal/agent/looper_test.go`:

```go
package agent_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/jameynakama/whatnow/internal/llm"
	"github.com/jameynakama/whatnow/internal/service"
)

func TestALooperRunsTheTurnInOneBatch(t *testing.T) {
	f := newFixture(t)
	rfc := f.task(t, service.TaskFields{Title: "Write the RFC"})
	l := &looper{model: model{t: t}, say: "Booked in, and the RFC's done.", rounds: [][]llm.ToolCall{{
		call("c1", "create_task", map[string]any{"title": "Call the dentist", "notes": "", "size": "S", "energy": "",
			"feeling": "", "due_on": "2026-10-06", "not_before": ""}),
		call("c2", "complete_task", map[string]any{"task_id": rfc.ID}),
	}}}
	f.client = l
	msgs, unsub := f.hub.Subscribe()
	defer unsub()

	reply := f.send(t, "finished the RFC, and remind me to call the dentist tomorrow")

	if reply.Text != "Booked in, and the RFC's done." || reply.Error != "" || len(reply.Tools) != 2 {
		t.Fatalf("reply = %+v", reply)
	}
	if l.max != 10 || len(l.req.Tools) == 0 || len(l.req.Messages) == 0 {
		t.Errorf("asked with max %d, %d tools, %d messages", l.max, len(l.req.Tools), len(l.req.Messages))
	}
	for _, r := range l.results[0] {
		if r.IsError {
			t.Errorf("result %s failed: %s", r.CallID, r.Content)
		}
	}
	acts, err := f.svc.BatchActivity(context.Background(), reply.BatchID)
	if err != nil {
		t.Fatal(err)
	}
	if len(acts) != 2 {
		t.Errorf("batch %d has %d changes, want both", reply.BatchID, len(acts))
	}
	var deltas int
	for _, m := range drain(msgs) {
		if m.Type == "chat.delta" {
			deltas++
		}
	}
	if deltas == 0 {
		t.Error("no chat.delta published")
	}
	if len(f.model.chats) != 0 {
		t.Errorf("the agent's own loop ran %d chats", len(f.model.chats))
	}
}

func TestALooperPastTheRoundLimitSaysItStopped(t *testing.T) {
	f := newFixture(t)
	look := []llm.ToolCall{call("c", "list_tasks", map[string]any{"status": "open"})}
	l := &looper{model: model{t: t}}
	for range 11 {
		l.rounds = append(l.rounds, look)
	}
	f.client = l

	reply := f.send(t, "keep looking")

	if len(reply.Tools) != 10 || !strings.Contains(reply.Text, "stopped after 10 rounds") || reply.Error != "" {
		t.Errorf("reply = %+v", reply)
	}
}

func TestALooperErrorKeepsItsTextAndWrites(t *testing.T) {
	f := newFixture(t)
	f.client = &looper{model: model{t: t}, say: "Adding it.", err: errors.New("boom"), rounds: [][]llm.ToolCall{{
		call("c1", "create_task", map[string]any{"title": "Call the dentist", "notes": "", "size": "", "energy": "",
			"feeling": "", "due_on": "", "not_before": ""}),
	}}}

	reply := f.send(t, "remind me to call the dentist")

	if !strings.HasPrefix(reply.Text, "Adding it.") || !strings.Contains(reply.Text, "couldn't reach") || reply.BatchID == 0 {
		t.Errorf("reply = %+v", reply)
	}
}

func TestASignedOutProviderSaysSo(t *testing.T) {
	f := newFixture(t)
	f.client = &looper{model: model{t: t}, err: llm.ErrSignedOut}

	reply := f.send(t, "hi")

	if !strings.Contains(reply.Text, "isn't signed in") {
		t.Errorf("text = %q", reply.Text)
	}
}
```

- [ ] **Step 8: Run them to see them fail**

Run: `go test ./internal/agent/ -run 'Looper|SignedOut'`
Expected: FAIL. The looper's `Loop` is never called, so the fixture's `model` fails with "unexpected chat call 1".

- [ ] **Step 9: Hand the turn to a looper** in `internal/agent/agent.go`.

Next to `callTimeout`:

```go
// loopTimeout bounds a whole turn on a backend that runs the tool loop itself.
var loopTimeout = 5 * time.Minute
```

In `errorText`, add a case before `ErrRefused`:

```go
	case errors.Is(err, llm.ErrSignedOut):
		return "The AI provider isn't signed in, so I can't chat. Check settings."
```

In `turn.run`, right after `req` is built:

```go
	if looper, ok := sit.Model.(llm.Looper); ok {
		return t.loop(ctx, looper, req)
	}
```

Replace the round-limit line with `return joinText(text, stopped()), nil`, and add after `turn.run`:

```go
func stopped() string {
	return fmt.Sprintf("(I stopped after %d rounds of changes. Ask me to keep going if there's more.)", maxRounds)
}

// loop hands the whole turn to a backend that runs tools itself. Its tool calls may arrive at
// once, but a turn's writes share one batch, so they run one at a time.
func (t *turn) loop(ctx context.Context, model llm.Looper, req llm.ChatRequest) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, loopTimeout)
	defer cancel()
	var mu sync.Mutex
	run := func(ctx context.Context, c llm.ToolCall) llm.ToolResult {
		mu.Lock()
		defer mu.Unlock()
		return t.runTool(ctx, c)
	}
	text, err := model.Loop(ctx, req, maxRounds, run, func(delta string) {
		t.agent.hub.Publish(stream.Message{Type: "chat.delta", Data: Delta{Text: delta}})
	})
	if errors.Is(err, llm.ErrTooManyRounds) {
		return joinText(text, stopped()), nil
	}
	return text, err
}
```

- [ ] **Step 10: Run the agent suite with the race detector**

Run: `go test -race ./internal/agent/ ./internal/llm/`
Expected: PASS, including the old `TestTheLoopStopsAfterTenRounds`.

- [ ] **Step 11: Commit**

```bash
git add internal/llm/llm.go internal/llm/fold.go internal/llm/fold_test.go internal/agent/
git commit -m "feat: let a backend that runs its own tool loop take the whole chat turn"
```

---

### Task 3: Claude Code `Complete`

**Files:**
- Create: `internal/llm/claudecode.go`
- Create: `internal/llm/claudefake_test.go` (package `llm_test`: `TestMain` and the fake CLI)
- Create: `internal/llm/claudecode_test.go` (package `llm_test`)

**Interfaces:**
- Consumes: `flatSystem`, `ErrSignedOut`, `ErrTooManyRounds` (Task 2)
- Produces: `func llm.NewClaudeCode(path, dir string) llm.Client`; unexported `(*claudeCode).args`, `(*claudeCode).command`, `claudeResult`, `claudeFailure` for Task 4.

- [ ] **Step 1: Write the fake CLI** in `internal/llm/claudefake_test.go`. The test binary plays `claude`. When `WHATNOW_FAKE_CLAUDE` names a scenario, `TestMain` runs the fake instead of the tests.

```go
package llm_test

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/jameynakama/whatnow/internal/llm"
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
```

Add a placeholder `fakeClaudeLoop` at the end of the file so it compiles until Task 4 replaces it:

```go
// fakeClaudeLoop plays the stream-json scenarios. Task 4 fills it in.
func fakeClaudeLoop(scenario string, _ []string, _ func(any), _ func(string, bool, string) map[string]any) int {
	fmt.Fprintln(os.Stderr, "no scenario", scenario)
	return 2
}
```

- [ ] **Step 2: Write the failing tests** in `internal/llm/claudecode_test.go`:

```go
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
```

- [ ] **Step 3: Run them to see them fail**

Run: `go test ./internal/llm/ -run ClaudeCode`
Expected: FAIL, `undefined: llm.NewClaudeCode`.

- [ ] **Step 4: Implement** `internal/llm/claudecode.go`:

```go
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
```

`claudeCode` doesn't satisfy `Client` until Task 4 adds `Chat`. Add this stub to compile now; Task 4 replaces it:

```go
// Chat is not how Claude Code chats: the agent uses Loop, since the CLI runs the tool loop.
func (c *claudeCode) Chat(context.Context, ChatRequest, func(string)) (Reply, error) {
	return Reply{}, errors.New("claude code: use Loop")
}
```

- [ ] **Step 5: Run the tests**

Run: `go test ./internal/llm/ -run ClaudeCode -v`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/llm/claudecode.go internal/llm/claudecode_test.go internal/llm/claudefake_test.go
git commit -m "feat: complete through the user's signed-in claude cli"
```

---

### Task 4: Claude Code `Loop`, with the turn's tools over MCP

**Files:**
- Modify: `go.mod`, `go.sum` (`go get github.com/modelcontextprotocol/go-sdk@v1.8.0`)
- Create: `internal/llm/mcp.go`
- Modify: `internal/llm/claudecode.go` (add `Loop`, `readStream`; `Chat` stays as Task 3 left it)
- Modify: `internal/llm/claudefake_test.go` (real `fakeClaudeLoop`)
- Modify: `internal/llm/claudecode_test.go` (Loop tests, live test)

**Interfaces:**
- Consumes: `RunTool`, `transcript`, `ErrTooManyRounds` (Task 2); `args`, `command`, `claudeResult`, `claudeFailure` (Task 3)
- Produces: `(*claudeCode).Loop`, so `NewClaudeCode`'s client is an `llm.Looper`; unexported `serveTools(ctx, tools, run) (*toolServer, error)`

- [ ] **Step 1: Add the dependency**

```bash
go get github.com/modelcontextprotocol/go-sdk@v1.8.0
```

- [ ] **Step 2: Replace the placeholder `fakeClaudeLoop`** in `claudefake_test.go`. Add imports `context`, `net/http`, `sort`, `strings`, and `github.com/modelcontextprotocol/go-sdk/mcp`.

```go
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
```

- [ ] **Step 3: Write the failing Loop tests**, appended to `claudecode_test.go`. Add imports `net/http`, `os`, `os/exec`, `sync`.

```go
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
```

- [ ] **Step 4: Run them to see them fail**

Run: `go test ./internal/llm/ -run ClaudeCodeLoop`
Expected: FAIL, "the Claude Code client doesn't run its own loop".

- [ ] **Step 5: Add `internal/llm/mcp.go`**

```go
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
```

- [ ] **Step 6: Add `Loop` and `readStream`** to `claudecode.go`. Add imports `bufio`, `io`, `strconv`.

```go
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
		return text, res.err()
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
```

- [ ] **Step 7: Run the tests**

Run: `go test -race ./internal/llm/ -run ClaudeCode -v`
Expected: PASS. `TestLiveClaudeCode` is skipped.

- [ ] **Step 8: Run the live test once.** It uses the user's plan, so ask first.

Run: `WHATNOW_LIVE=1 go test ./internal/llm/ -run TestLiveClaudeCode -v`
Expected: PASS, with the reply logged. If the CLI refuses the MCP tools under `dontAsk`, check `--allowedTools` against the tool name the init event lists (`--output-format stream-json` prints it) and fix the pattern.

- [ ] **Step 9: Commit**

```bash
git add go.mod go.sum internal/llm/
git commit -m "feat: chat through claude code, serving the turn's tools over loopback mcp"
```

---

### Task 5: Copilot backend

**Files:**
- Modify: `go.mod`, `go.sum` (`go get github.com/github/copilot-sdk/go@v1.0.22`)
- Create: `internal/llm/copilot.go`
- Create: `internal/llm/copilot_test.go` (package `llm`, internal, so the fake can satisfy unexported interfaces)

**Interfaces:**
- Consumes: `RunTool`, `transcript`, `flatSystem`, `joinText`, `ErrTooManyRounds`, `ErrSignedOut` (Task 2)
- Produces:
  - `type llm.CopilotRuntime`; `func llm.NewCopilotRuntime(path, dir string) *llm.CopilotRuntime`; `(*CopilotRuntime).Path() string`; `(*CopilotRuntime).Stop() error`
  - `func llm.NewCopilot(rt *llm.CopilotRuntime, dir string) llm.Client`; the client is also an `llm.Looper`

- [ ] **Step 1: Add the dependency**

```bash
go get github.com/github/copilot-sdk/go@v1.0.22
```

- [ ] **Step 2: Write the failing tests** in `internal/llm/copilot_test.go`:

```go
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
	run := func(_ context.Context, call ToolCall) ToolResult { runs++; return ToolResult{CallID: call.ID, Content: "ok"} }

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
```

- [ ] **Step 3: Run them to see them fail**

Run: `go test ./internal/llm/ -run Copilot`
Expected: FAIL, `undefined: copilotClient`.

- [ ] **Step 4: Implement** `internal/llm/copilot.go`:

```go
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
```

`TestCopilotLoopStopsPastMaxRounds` checks that the second round's tool doesn't run. For that to hold, the round count must go up before the handler is called. In the fake, `message` comes before `call`. The live test confirms the real runtime sends `assistant.message` before it runs that message's tools.

- [ ] **Step 5: Run the tests with the race detector**

Run: `go test -race ./internal/llm/ -run Copilot -v`
Expected: PASS. `TestLiveCopilot` is skipped.

- [ ] **Step 6: Run the live test once.** This uses the user's Copilot plan, so ask first. It needs the CLI from Task 1.

Run: `WHATNOW_LIVE=1 go test ./internal/llm/ -run TestLiveCopilot -v`
Expected: PASS, with models and the reply logged. If a default model isn't listed, pick the closest main and light models from the log, and put them in Task 6's `Defaults` and the spec.

- [ ] **Step 7: Commit**

```bash
git add go.mod go.sum internal/llm/copilot.go internal/llm/copilot_test.go
git commit -m "feat: run on github copilot through its go sdk, one locked-down session per call"
```

---

### Task 6: Settings, client wiring, and the settings API

**Files:**
- Modify: `internal/settings/settings.go`
- Modify: `internal/settings/keys.go`
- Modify: `internal/settings/settings_test.go`
- Modify: `internal/httpapi/settings.go`
- Modify: `internal/httpapi/settings_test.go`
- Modify: `internal/app/app.go` (`Close` stops the runtime)

**Interfaces:**
- Consumes: `llm.NewClaudeCode`, `llm.NewCopilotRuntime`, `llm.NewCopilot`, `(*CopilotRuntime).Path`, `(*CopilotRuntime).Stop` (Tasks 3–5)
- Produces:
  - `settings.ClaudeCode`, `settings.Copilot` (Providers); `settings.Providers []Provider`; `Provider.SignsIn() bool`
  - `Settings.ClaudeCode`, `Settings.Copilot` (`ProviderConfig`, JSON `claudecode`, `copilot`)
  - `settings.KeyFromLogin` (`"login"`); `settings.Source(p Provider) (KeySource, error)`; `settings.StopRuntimes()`
  - `SettingsView.Keys` gains `claudecode` and `copilot`, which the web reads in Task 7

- [ ] **Step 1: Write the failing settings tests**, appended to `internal/settings/settings_test.go`. Add imports `os` and `runtime`.

```go
// onPath puts an executable named name in a fresh directory and makes that directory the PATH.
func onPath(t *testing.T, name string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("shell stubs")
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, name), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
}

func TestSignInProvidersNeedTheirCLI(t *testing.T) {
	keyring.MockInit()
	t.Setenv("PATH", t.TempDir())
	t.Setenv("COPILOT_CLI_PATH", "")
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	t.Setenv("HOME", t.TempDir())

	for _, p := range []settings.Provider{settings.ClaudeCode, settings.Copilot} {
		s := settings.Defaults()
		s.Provider = p
		if src, err := settings.Source(p); err != nil || src != "" {
			t.Errorf("%s with no CLI: source %q, %v", p, src, err)
		}
		if _, _, err := settings.NewClient(s); !errors.Is(err, settings.ErrNotConfigured) {
			t.Errorf("%s with no CLI: err = %v, want ErrNotConfigured", p, err)
		}
		if err := settings.SetKey(p, "k"); !errors.Is(err, settings.ErrInvalid) {
			t.Errorf("%s took a key: %v", p, err)
		}
	}

	onPath(t, "claude")
	s := settings.Defaults()
	s.Provider = settings.ClaudeCode
	if src, _ := settings.Source(settings.ClaudeCode); src != settings.KeyFromLogin {
		t.Errorf("claude on PATH: source %q", src)
	}
	client, cfg, err := settings.NewClient(s)
	if err != nil || cfg.MainModel != "sonnet" || cfg.LightModel != "haiku" {
		t.Fatalf("claude code: cfg %+v, err %v", cfg, err)
	}
	if _, ok := client.(llm.Looper); !ok {
		t.Error("the Claude Code client doesn't run its own loop")
	}

	t.Setenv("COPILOT_CLI_PATH", "/opt/copilot/bin/copilot")
	s.Provider = settings.Copilot
	if src, _ := settings.Source(settings.Copilot); src != settings.KeyFromLogin {
		t.Errorf("COPILOT_CLI_PATH set: source %q", src)
	}
	if _, cfg, err := settings.NewClient(s); err != nil || cfg.MainModel != "claude-sonnet-4.5" {
		t.Errorf("copilot: cfg %+v, err %v", cfg, err)
	}
	settings.StopRuntimes()
}
```

This needs the `llm` import (`github.com/jameynakama/whatnow/internal/llm`). In `TestValidate`, add two cases to the map:

```go
		"claude code without models": func(s *settings.Settings) { s.Provider = settings.ClaudeCode; s.ClaudeCode = settings.ProviderConfig{} },
		"copilot without models":     func(s *settings.Settings) { s.Provider = settings.Copilot; s.Copilot = settings.ProviderConfig{} },
```

- [ ] **Step 2: Run them to see them fail**

Run: `go test ./internal/settings/`
Expected: FAIL, `undefined: settings.ClaudeCode`.

- [ ] **Step 3: Extend `internal/settings/settings.go`.** Add `slices` to the imports.

```go
const (
	Anthropic  Provider = "anthropic"
	OpenAI     Provider = "openai"
	ClaudeCode Provider = "claudecode"
	Copilot    Provider = "copilot"
)

// Providers is every provider, in the order settings shows them.
var Providers = []Provider{Anthropic, OpenAI, ClaudeCode, Copilot}

func (p Provider) Valid() bool { return slices.Contains(Providers, p) }

// SignsIn says whether the provider runs on the user's own sign-in to its vendor's CLI. Such a
// provider takes no key.
func (p Provider) SignsIn() bool { return p == ClaudeCode || p == Copilot }
```

In `Settings`, after `OpenAI`:

```go
	ClaudeCode ProviderConfig `json:"claudecode"`
	Copilot    ProviderConfig `json:"copilot"`
```

In `Defaults`, after `Anthropic: …`:

```go
		ClaudeCode:       ProviderConfig{MainModel: "sonnet", LightModel: "haiku"},
		Copilot:          ProviderConfig{MainModel: "claude-sonnet-4.5", LightModel: "gpt-5-mini"},
```

Replace `Config`:

```go
// Config returns the active provider's model settings.
func (s Settings) Config() ProviderConfig {
	switch s.Provider {
	case OpenAI:
		return s.OpenAI
	case ClaudeCode:
		return s.ClaudeCode
	case Copilot:
		return s.Copilot
	}
	return s.Anthropic
}
```

Update the `Defaults` doc comment: "Defaults has Claude, Claude Code and Copilot models filled in. OpenAI-compatible models are left for the user, since they depend on the server."

- [ ] **Step 4: Extend `internal/settings/keys.go`.** Add imports `os/exec`, `path/filepath`, `sync`.

```go
const (
	KeyFromKeychain KeySource = "keychain"
	KeyFromEnv      KeySource = "env"
	// KeyFromLogin means a provider that signs in through its CLI found that CLI. Whether it's
	// signed in shows only when it's used.
	KeyFromLogin KeySource = "login"
)
```

In `SetKey` and `DeleteKey`, make the check `if !p.Valid() || p.SignsIn()`, with the message `"%w: provider %q takes no key"` when `p.SignsIn()`:

```go
	if p.SignsIn() {
		return fmt.Errorf("%w: provider %q takes no key", ErrInvalid, p)
	}
```

That goes before the existing `!p.Valid()` check in both functions. Then add:

```go
// Source says where a provider's access comes from: its key's source, or KeyFromLogin for a
// provider whose CLI is found. Empty means there is none.
func Source(p Provider) (KeySource, error) {
	if p.SignsIn() {
		if _, err := cliPath(p); err != nil {
			return "", nil
		}
		return KeyFromLogin, nil
	}
	_, source, err := Key(p)
	return source, err
}

// cliPath finds the CLI a sign-in provider runs: claude on PATH, or Copilot's runtime at
// COPILOT_CLI_PATH or on PATH. The Copilot SDK does no PATH lookup itself.
func cliPath(p Provider) (string, error) {
	if p == Copilot {
		if path := os.Getenv("COPILOT_CLI_PATH"); path != "" {
			return path, nil
		}
		return exec.LookPath("copilot")
	}
	return exec.LookPath("claude")
}

// workDir is a sign-in provider's own empty directory, so no project's instructions apply.
func workDir(p Provider) (string, error) {
	cache, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(cache, "whatnow", string(p))
	return dir, os.MkdirAll(dir, 0o700)
}

var (
	copilotMu sync.Mutex
	copilotRT *llm.CopilotRuntime
)

// copilotRuntime returns the shared Copilot runtime, replacing it if the CLI's path changed.
func copilotRuntime(path, dir string) *llm.CopilotRuntime {
	copilotMu.Lock()
	defer copilotMu.Unlock()
	if copilotRT != nil && copilotRT.Path() != path {
		if err := copilotRT.Stop(); err != nil {
			log.Printf("stop copilot: %v", err)
		}
		copilotRT = nil
	}
	if copilotRT == nil {
		copilotRT = llm.NewCopilotRuntime(path, dir)
	}
	return copilotRT
}

// StopRuntimes stops the Copilot runtime, if one started. The app calls it on close.
func StopRuntimes() {
	copilotMu.Lock()
	defer copilotMu.Unlock()
	if copilotRT == nil {
		return
	}
	if err := copilotRT.Stop(); err != nil {
		log.Printf("stop copilot: %v", err)
	}
	copilotRT = nil
}
```

Replace `NewClient`:

```go
// NewClient returns a client for the active provider, along with its model settings.
func NewClient(s Settings) (llm.Client, ProviderConfig, error) {
	if s.Provider == "" {
		return nil, ProviderConfig{}, ErrNotConfigured
	}
	cfg := s.Config()
	if s.Provider.SignsIn() {
		path, err := cliPath(s.Provider)
		if err != nil {
			return nil, ProviderConfig{}, ErrNotConfigured
		}
		dir, err := workDir(s.Provider)
		if err != nil {
			return nil, ProviderConfig{}, err
		}
		if s.Provider == Copilot {
			return llm.NewCopilot(copilotRuntime(path, dir), dir), cfg, nil
		}
		return llm.NewClaudeCode(path, dir), cfg, nil
	}
	key, _, err := Key(s.Provider)
	if err != nil {
		return nil, ProviderConfig{}, err
	}
	if key == "" {
		return nil, ProviderConfig{}, ErrNotConfigured
	}
	if s.Provider == OpenAI {
		return llm.NewOpenAI(key, cfg.BaseURL), cfg, nil
	}
	return llm.NewAnthropic(key, cfg.BaseURL), cfg, nil
}
```

Update the `ErrNotConfigured` comment: "no provider is chosen, the chosen one has no key, or its CLI isn't found."

- [ ] **Step 5: Run the settings tests**

Run: `go test ./internal/settings/`
Expected: PASS.

- [ ] **Step 6: Write the failing API test**, appended to `internal/httpapi/settings_test.go`. Add imports `os` and `path/filepath`.

```go
func TestSettingsShowWhetherSignInCLIsAreFound(t *testing.T) {
	keyring.MockInit()
	t.Setenv("PATH", t.TempDir())
	t.Setenv("COPILOT_CLI_PATH", "")
	api := newAPI(t)

	view := decodeAs[httpapi.SettingsView](t, api.do(http.MethodGet, "/api/v1/settings", nil))
	if src, ok := view.Keys[settings.ClaudeCode]; !ok || src != "" {
		t.Errorf("no claude: keys = %+v", view.Keys)
	}
	if view.Settings.ClaudeCode.MainModel != "sonnet" {
		t.Errorf("claude code models = %+v", view.Settings.ClaudeCode)
	}

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "claude"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	t.Setenv("COPILOT_CLI_PATH", "/opt/copilot/bin/copilot")
	view = decodeAs[httpapi.SettingsView](t, api.do(http.MethodGet, "/api/v1/settings", nil))
	if view.Keys[settings.ClaudeCode] != settings.KeyFromLogin || view.Keys[settings.Copilot] != settings.KeyFromLogin {
		t.Errorf("CLIs found: keys = %+v", view.Keys)
	}
	wantStatus(t, api.do(http.MethodPut, "/api/v1/settings/keys/claudecode", map[string]string{"key": "k"}), http.StatusBadRequest)
}
```

- [ ] **Step 7: Run it to see it fail**

Run: `go test ./internal/httpapi/ -run SignInCLIs`
Expected: FAIL. `keys` has no `claudecode` entry.

- [ ] **Step 8: Report every provider** in `internal/httpapi/settings.go`'s `settingsView`:

```go
	view := SettingsView{Settings: saved, Keys: map[settings.Provider]settings.KeySource{}}
	for _, p := range settings.Providers {
		source, err := settings.Source(p)
		if err != nil {
			writeError(w, err)
			return
		}
		view.Keys[p] = source
	}
```

Update the `SettingsView` comment: "…and where each provider's access comes from: a key's source, or `login` for a provider whose CLI is found. Keys themselves never leave the server." `deleteData` keeps its explicit `Anthropic, OpenAI` list, since only those have keys.

- [ ] **Step 9: Stop the runtime on close** in `internal/app/app.go`:

```go
func (a *App) Close() error {
	a.stop()
	a.running.Wait()
	settings.StopRuntimes()
	return a.DB.Close()
}
```

- [ ] **Step 10: Run the whole Go suite**

Run: `go test ./... && go vet ./...`
Expected: PASS.

- [ ] **Step 11: Commit**

```bash
git add internal/settings/ internal/httpapi/settings.go internal/httpapi/settings_test.go internal/app/app.go
git commit -m "feat: offer claude code and copilot as providers that sign in through their cli"
```

---

### Task 7: Web settings and welcome

**Files:**
- Modify: `web/src/lib/api.ts`
- Modify: `web/src/lib/api.test.ts`
- Create: `web/src/lib/components/SignInNote.svelte`
- Modify: `web/src/routes/settings/+page.svelte`
- Modify: `web/src/routes/welcome/+page.svelte`

**Interfaces:**
- Consumes: `SettingsView.keys.claudecode|copilot` = `'' | 'login'`; `settings.claudecode`, `settings.copilot` (Task 6)
- Produces: `signsIn(p: Provider): boolean`; `<SignInNote provider source />`

- [ ] **Step 1: Write the failing tests** in `web/src/lib/api.test.ts`. Change the `view` helper to take any provider and source, and fill every provider's keys:

```ts
	const view = (provider: '' | Provider, key: KeySource): SettingsView => ({
		settings: {
			provider,
			anthropic: { main_model: '', light_model: '', base_url: '' },
			openai: { main_model: '', light_model: '', base_url: '' },
			claudecode: { main_model: 'sonnet', light_model: 'haiku', base_url: '' },
			copilot: { main_model: 'claude-sonnet-4.5', light_model: 'gpt-5-mini', base_url: '' },
			style: '',
			about_me: '',
			end_of_day: '17:30',
			nudges: { before_event: true, event_end: true, late_day: true },
			alert_lead_minutes: 10,
			theme: '',
			onboarded: true,
			allowed_hosts: []
		},
		keys: { anthropic: '', openai: '', claudecode: '', copilot: '', ...(provider ? { [provider]: key } : {}) }
	});
```

Change the import line to `import { ApiError, api, configured, signsIn, type KeySource, type Provider, type SettingsView } from './api';`. The existing case `view('', 'env')` now has no key anywhere, which still counts as not set up. Add these cases to the `configured` table:

```ts
		['a sign-in provider whose CLI is found', true, view('claudecode', 'login')],
		['a sign-in provider whose CLI is missing', false, view('copilot', '')]
```

And a new block:

```ts
describe('signsIn', () => {
	it.each([
		['anthropic', false],
		['openai', false],
		['claudecode', true],
		['copilot', true]
	] as const)('Should say whether %s signs in through its CLI', (p, want) => {
		expect(signsIn(p)).toBe(want);
	});
});
```

- [ ] **Step 2: Run them to see them fail**

Run: `cd web && npx vitest run src/lib/api.test.ts`
Expected: FAIL, `signsIn` is not exported.

- [ ] **Step 3: Extend `web/src/lib/api.ts`**

```ts
export type Provider = 'anthropic' | 'openai' | 'claudecode' | 'copilot';
/** Where a provider's access comes from. Empty means there is none; login means its CLI was found. */
export type KeySource = '' | 'keychain' | 'env' | 'login';

/** Whether a provider runs on the user's own sign-in to its vendor's CLI, and takes no key. */
export function signsIn(p: Provider): boolean {
	return p === 'claudecode' || p === 'copilot';
}
```

In `Settings`, after `openai`:

```ts
	claudecode: ProviderConfig;
	copilot: ProviderConfig;
```

- [ ] **Step 4: Run the unit tests**

Run: `cd web && npx vitest run src/lib/api.test.ts`
Expected: PASS.

- [ ] **Step 5: Add `web/src/lib/components/SignInNote.svelte`**

```svelte
<script lang="ts">
	import type { KeySource, Provider } from '#lib/api.ts';

	let { provider, source }: { provider: Provider; source: KeySource } = $props();
</script>

{#if provider === 'claudecode'}
	<p>
		Uses the Claude Code you're signed in to, so your Claude plan's limits apply. For your own use on your own
		machine.
	</p>
	{#if source === 'login'}
		<p class="ok">Found <code>claude</code>.</p>
	{:else}
		<p class="bad">
			Install Claude Code first: <a href="https://code.claude.com" target="_blank" rel="noreferrer">https://code.claude.com</a>
		</p>
	{/if}
{:else}
	<p>Uses your GitHub Copilot plan. Each message counts toward your Copilot usage.</p>
	{#if source === 'login'}
		<p class="ok">Found the Copilot CLI.</p>
	{:else}
		<p class="bad">Install the Copilot CLI first: <code>brew install copilot-cli</code></p>
	{/if}
{/if}

<style>
	.ok {
		font-weight: var(--heavy);
	}
	.bad {
		color: var(--danger);
		font-weight: var(--heavy);
	}
</style>
```

Use the install command Task 1 recorded, if it differed.

- [ ] **Step 6: Settings page.** In `web/src/routes/settings/+page.svelte`, import `signsIn` with `api`, and import `SignInNote from '#lib/components/SignInNote.svelte'`. Add the options:

```svelte
					<option value="anthropic">Anthropic</option>
					<option value="openai">OpenAI-compatible</option>
					<option value="claudecode">Claude Code (your Claude plan)</option>
					<option value="copilot">GitHub Copilot</option>
```

Replace the API key `<label>` with:

```svelte
			{#if signsIn(provider)}
				<SignInNote {provider} source={view.keys[provider]} />
			{:else}
				<label>
					<span class="label">API key <span class="muted">({keyNote})</span></span>
					<input class="field" type="password" bind:value={key} autocomplete="off" placeholder="Leave blank to keep" />
				</label>
			{/if}
```

In `save`, change `if (key.trim())` to `if (!signsIn(provider) && key.trim())`.

- [ ] **Step 7: Welcome page.** In `web/src/routes/welcome/+page.svelte`, import `signsIn` and `SignInNote` the same way. After the OpenAI-compatible choice button, add:

```svelte
					<button
						class="sticker choice"
						class:on={provider === 'claudecode'}
						onclick={() => view && (view.settings.provider = 'claudecode')}
					>
						<strong>Claude Code</strong>
						<span class="muted">Your Claude plan, through the claude you're signed in to</span>
					</button>
					<button
						class="sticker choice"
						class:on={provider === 'copilot'}
						onclick={() => view && (view.settings.provider = 'copilot')}
					>
						<strong>GitHub Copilot</strong>
						<span class="muted">Your Copilot plan, through the Copilot CLI</span>
					</button>
```

Make `keyReady` account for sign-in providers:

```ts
	const keyReady = $derived(
		signsIn(provider)
			? view?.keys[provider] === 'login'
			: fromEnv || key.trim() !== '' || view?.keys[provider] === 'keychain'
	);
```

In `saveKey`, change `if (!fromEnv && key.trim())` to `if (!signsIn(provider) && !fromEnv && key.trim())`. In the `key` step, replace `<h1>Your API key</h1>` and the block under it with:

```svelte
			{:else if step === 'key'}
				{#if signsIn(provider)}
					<h1>Your sign-in</h1>
					<SignInNote {provider} source={view.keys[provider]} />
				{:else}
					<h1>Your API key</h1>
					{#if fromEnv}
						<p>Found your key in <code>{envVar}</code>.</p>
						<button class="link" onclick={() => (ownKey = true)}>Use a different key</button>
					{:else}
						<p>It stays in your system keychain, never in whatnow's database.</p>
						<input
							class="field"
							type="password"
							bind:value={key}
							autocomplete="off"
							aria-label="API key"
							placeholder={view.keys[provider] === 'keychain' ? 'Saved. Leave blank to keep' : ''}
						/>
					{/if}
				{/if}
```

- [ ] **Step 8: Check and test the web client**

Run: `cd web && npm run check && npm test`
Expected: no type errors; tests PASS.

- [ ] **Step 9: Look at it running.** Use the `run` skill, or `just dev`. Open `/settings` and `/welcome`. Pick Claude Code: the key field gives way to the note, which reads "Found `claude`." Click Test connection: "Connected". Pick Copilot with the CLI missing: the install line shows, and welcome's Next is disabled.

- [ ] **Step 10: Commit**

```bash
git add web/src/
git commit -m "feat: pick claude code or copilot in settings and setup, with no key"
```

---

### Task 8: README, and one real day on each

**Files:**
- Modify: `README.md` (the Install lead-in and `## Models`)

- [ ] **Step 1: Rewrite `## Models`** in `README.md`:

```markdown
## Models

whatnow works with Anthropic (the default), any OpenAI-compatible API (OpenAI, OpenRouter, Ollama,
LM Studio), or a plan you already have:

- **Claude Code** runs on your Claude plan through the `claude` CLI you're signed in to. whatnow
  starts it with no tools of its own, none of your settings, and nothing saved, and hands it
  only whatnow's planner tools. This is for your own use on your own machine: Anthropic doesn't
  allow third-party apps to offer Claude plan sign-in to other people.
- **GitHub Copilot** runs on your Copilot plan through the Copilot CLI (`brew install
  copilot-cli`), signed in with `gh auth login`. Each message counts toward your Copilot usage.

Set it up on the first run, or later in settings. API keys are stored in your OS keychain, never in
the database. `ANTHROPIC_API_KEY` or `OPENAI_API_KEY` in the environment works too.
```

This replaces the paragraph that says consumer subscriptions can't power whatnow. In `## Install`, change "You'll need an API key for Anthropic, OpenAI, or an OpenAI-compatible provider" to "You'll need an API key for Anthropic, OpenAI, or an OpenAI-compatible provider, or a signed-in Claude Code or GitHub Copilot".

- [ ] **Step 2: Gate**

Run: `just test && just lint`
Expected: PASS.

- [ ] **Step 3: One real morning on each.** This uses the user's plans, so ask first. Run `just demo`. In settings, pick Claude Code. Do a morning brain dump and get a plan. Then in chat: "push the first task to tomorrow". Check that the task moves, Undo works, and the reply streams. Do the same with GitHub Copilot. Note anything wrong as a fix before merging.

- [ ] **Step 4: Commit**

```bash
git add README.md
git commit -m "docs: describe the claude code and copilot providers"
```
