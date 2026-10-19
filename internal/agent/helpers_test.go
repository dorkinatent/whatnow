package agent_test

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jameynakama/whatnow/internal/agent"
	"github.com/jameynakama/whatnow/internal/domain"
	"github.com/jameynakama/whatnow/internal/llm"
	"github.com/jameynakama/whatnow/internal/planner"
	"github.com/jameynakama/whatnow/internal/service"
	"github.com/jameynakama/whatnow/internal/settings"
	"github.com/jameynakama/whatnow/internal/store"
	"github.com/jameynakama/whatnow/internal/stream"
)

// loc is UTC-5. The clock reads 09:00 local on Monday 2026-10-05.
var loc = time.FixedZone("UTC-5", -5*3600)

const today = domain.Date("2026-10-05")

// model is a fake provider. Chat answers with the next scripted reply, streaming its text in two
// pieces; Complete answers with the next scripted completion. A scripted error is returned as one.
// It records every request.
type model struct {
	t           *testing.T
	mu          sync.Mutex
	replies     []any
	completions []any
	chats       []llm.ChatRequest
	completes   []llm.Request
}

func (m *model) Chat(_ context.Context, req llm.ChatRequest, onText func(string)) (llm.Reply, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.chats = append(m.chats, req)
	if len(m.replies) == 0 {
		m.t.Fatalf("unexpected chat call %d", len(m.chats))
	}
	r := m.replies[0]
	m.replies = m.replies[1:]
	if err, ok := r.(error); ok {
		return llm.Reply{}, err
	}
	reply := r.(llm.Reply)
	if half := len(reply.Text) / 2; half > 0 {
		onText(reply.Text[:half])
		onText(reply.Text[half:])
	}
	return reply, nil
}

func (m *model) Complete(_ context.Context, req llm.Request) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.completes = append(m.completes, req)
	if len(m.completions) == 0 {
		m.t.Fatalf("unexpected completion %d", len(m.completes))
	}
	r := m.completions[0]
	m.completions = m.completions[1:]
	if err, ok := r.(error); ok {
		return "", err
	}
	if s, ok := r.(string); ok {
		return s, nil
	}
	b, _ := json.Marshal(r)
	return string(b), nil
}

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

type fixture struct {
	svc   *service.Service
	hub   *stream.Hub
	agent *agent.Agent
	model *model
	// configured false means no provider is set up.
	configured bool
	// client, when set, answers instead of model.
	client llm.Client
}

func newFixture(t *testing.T, replies ...any) *fixture {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	now := func() time.Time { return time.Date(2026, 10, 5, 14, 0, 0, 0, time.UTC) }
	f := &fixture{svc: service.New(db, now), hub: stream.NewHub(), model: &model{t: t, replies: replies}, configured: true}
	s := settings.Defaults()
	s.Provider = settings.Anthropic
	s.Style = "dry humor"
	client := func(context.Context) (llm.Client, settings.ProviderConfig, settings.Settings, error) {
		if !f.configured {
			return nil, settings.ProviderConfig{}, settings.Defaults(), settings.ErrNotConfigured
		}
		if f.client != nil {
			return f.client, s.Config(), s, nil
		}
		return f.model, s.Config(), s, nil
	}
	f.agent = agent.New(f.svc, f.hub, planner.New(f.svc, f.hub, client, loc, now), loc, now)
	return f
}

func (f *fixture) task(t *testing.T, fields service.TaskFields) domain.Task {
	t.Helper()
	ctx := context.Background()
	batch, err := f.svc.NewBatch(ctx, domain.ActorUser)
	if err != nil {
		t.Fatal(err)
	}
	task, err := f.svc.CreateTask(ctx, batch, fields)
	if err != nil {
		t.Fatal(err)
	}
	return task
}

func (f *fixture) send(t *testing.T, text string) domain.Message {
	t.Helper()
	reply, err := f.agent.Send(context.Background(), text)
	if err != nil {
		t.Fatal(err)
	}
	return reply
}

// call is a tool call with its input given as a value.
func call(id, name string, input any) llm.ToolCall {
	b, _ := json.Marshal(input)
	return llm.ToolCall{ID: id, Name: name, Input: b}
}

func calls(cs ...llm.ToolCall) llm.Reply { return llm.Reply{ToolCalls: cs} }

func text(s string) llm.Reply { return llm.Reply{Text: s} }

// drain returns every message waiting on a subscription.
func drain(msgs <-chan stream.Message) []stream.Message {
	var got []stream.Message
	for {
		select {
		case m := <-msgs:
			got = append(got, m)
		default:
			return got
		}
	}
}

func system(blocks []llm.Block) string {
	var b strings.Builder
	for _, blk := range blocks {
		b.WriteString(blk.Text)
	}
	return b.String()
}

func date(d string) *domain.Date { v := domain.Date(d); return &v }
