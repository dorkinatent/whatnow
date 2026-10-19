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

// lateLooper returns at once, leaving a goroutine that calls run once released, as a backend
// does when a tool call is still in flight when the turn ends.
type lateLooper struct {
	model
	release chan struct{}
	late    chan llm.ToolResult
}

func (l *lateLooper) Loop(ctx context.Context, _ llm.ChatRequest, _ int, run llm.RunTool, _ func(string)) (string, error) {
	go func() {
		<-l.release
		l.late <- run(ctx, call("late", "create_task", map[string]any{"title": "Too late", "notes": "", "size": "",
			"energy": "", "feeling": "", "due_on": "", "not_before": ""}))
	}()
	return "Done.", nil
}

func TestAToolCallAfterTheTurnEndsDoesNothing(t *testing.T) {
	f := newFixture(t)
	l := &lateLooper{model: model{t: t}, release: make(chan struct{}), late: make(chan llm.ToolResult, 1)}
	f.client = l

	reply := f.send(t, "hi")
	close(l.release)
	res := <-l.late

	if !res.IsError || res.CallID != "late" || res.Content != "the turn is over" {
		t.Errorf("late result = %+v", res)
	}
	if len(reply.Tools) != 0 || reply.BatchID != 0 {
		t.Errorf("reply = %+v", reply)
	}
	tasks, err := f.svc.ListTasks(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 0 {
		t.Errorf("the late call created %d tasks", len(tasks))
	}
}
