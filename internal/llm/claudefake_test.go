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

// fakeClaudeLoop plays the stream-json scenarios. Task 4 fills it in.
func fakeClaudeLoop(scenario string, _ []string, _ func(any), _ func(string, bool, string) map[string]any) int {
	fmt.Fprintln(os.Stderr, "no scenario", scenario)
	return 2
}
