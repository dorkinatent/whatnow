// Package agent runs the chat. Each message from the user starts one turn: the main model replies,
// calling tools that wrap the service and the planner, for up to maxRounds rounds. Text streams to
// every client over the hub as it arrives, and everything the turn changes shares one batch.
package agent

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/jameynakama/whatnow/internal/domain"
	"github.com/jameynakama/whatnow/internal/facts"
	"github.com/jameynakama/whatnow/internal/llm"
	"github.com/jameynakama/whatnow/internal/planner"
	"github.com/jameynakama/whatnow/internal/service"
	"github.com/jameynakama/whatnow/internal/settings"
	"github.com/jameynakama/whatnow/internal/stream"
)

// ErrBusy means a reply is still being written. Chat runs one turn at a time.
var ErrBusy = errors.New("a reply is still being written")

const (
	// maxRounds is how many rounds of tool calls one turn may run.
	maxRounds = 10
	maxTokens = 8192
	// maxMessageRunes bounds what the user sends in one message.
	maxMessageRunes = 4000
)

// callTimeout bounds one model call, so a hung provider can't hold the chat forever.
var callTimeout = 2 * time.Minute

// loopTimeout bounds a whole turn on a backend that runs the tool loop itself.
var loopTimeout = 5 * time.Minute

type Agent struct {
	svc     *service.Service
	hub     *stream.Hub
	planner *planner.Planner
	loc     *time.Location
	now     func() time.Time
	mu      sync.Mutex
}

func New(svc *service.Service, hub *stream.Hub, p *planner.Planner, loc *time.Location, now func() time.Time) *Agent {
	return &Agent{svc: svc, hub: hub, planner: p, loc: loc, now: now}
}

// Delta is the chat.delta payload: the next piece of the reply being written.
type Delta struct {
	Text string `json:"text"`
}

// History returns today's chat.
func (a *Agent) History(ctx context.Context) ([]domain.Message, error) {
	return a.svc.Messages(ctx, a.today())
}

func (a *Agent) today() domain.Date { return facts.Today(a.now(), a.loc) }

// Send stores the user's message and runs a turn, returning the reply. Both messages are also
// published as chat.message, and the reply's text as chat.delta while it's written. A provider
// error is not returned: it becomes a reply that says what went wrong.
func (a *Agent) Send(ctx context.Context, text string) (domain.Message, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return domain.Message{}, fmt.Errorf("%w: the message is empty", service.ErrInvalid)
	}
	if n := len([]rune(text)); n > maxMessageRunes {
		return domain.Message{}, fmt.Errorf("%w: the message is %d characters; the limit is %d", service.ErrInvalid, n, maxMessageRunes)
	}
	if !a.mu.TryLock() {
		return domain.Message{}, ErrBusy
	}
	defer a.mu.Unlock()

	date := a.today()
	sent, err := a.svc.AddMessage(ctx, domain.Message{Date: date, Role: domain.RoleUser, Text: text})
	if err != nil {
		return domain.Message{}, err
	}
	a.publish(sent)

	t := &turn{agent: a}
	reply := domain.Message{Date: date, Role: domain.RoleAssistant}
	reply.Text, err = t.run(ctx, date)
	if err != nil {
		reply.Text, reply.Error = joinText(reply.Text, errorText(err)), err.Error()
		if !errors.Is(err, settings.ErrNotConfigured) {
			log.Printf("chat: %v", err)
		}
	}
	reply.Tools = t.uses
	if t.batchID != 0 {
		acts, err := a.svc.BatchActivity(ctx, t.batchID)
		if err != nil {
			return domain.Message{}, err
		}
		// The batch starts before its first write runs, so writes that all failed leave it empty.
		if len(acts) > 0 {
			reply.BatchID = t.batchID
			a.hub.PublishBatch(t.batchID, stream.SourceChat, acts)
		}
	}
	reply, err = a.svc.AddMessage(ctx, reply)
	if err != nil {
		return domain.Message{}, err
	}
	a.publish(reply)
	return reply, nil
}

func (a *Agent) publish(m domain.Message) {
	a.hub.Publish(stream.Message{Type: "chat.message", Data: m})
}

// errorText is what the user reads when the model can't reply. The UI links it to settings.
func errorText(err error) string {
	switch {
	case errors.Is(err, settings.ErrNotConfigured):
		return "No AI provider is set up yet, so I can't chat. Add one in settings."
	case errors.Is(err, llm.ErrSignedOut):
		return "The AI provider isn't signed in, so I can't chat. Check settings."
	case errors.Is(err, llm.ErrRefused):
		return "The model declined to answer that."
	case errors.Is(err, llm.ErrTruncated):
		return "My reply ran too long and was cut off. Try asking for less at once."
	}
	return "I couldn't reach the AI provider just now. Check settings, or try again in a bit."
}

func joinText(a, b string) string {
	if a == "" || b == "" {
		return a + b
	}
	return a + "\n\n" + b
}

// turn is one reply in progress. Its writes share a batch, made on the first write.
type turn struct {
	agent   *Agent
	batchID int64
	uses    []domain.ToolUse
}

// run talks to the model until it stops calling tools or runs out of rounds, and returns what it
// said. On an error, the text so far is returned with it.
func (t *turn) run(ctx context.Context, date domain.Date) (string, error) {
	a := t.agent
	sit, err := a.planner.Situation(ctx)
	if err != nil {
		return "", err
	}
	if sit.Model == nil {
		return "", sit.ModelErr
	}
	history, err := a.history(ctx, date, sit)
	if err != nil {
		return "", err
	}
	system, err := a.system(ctx, date, sit, history.fold)
	if err != nil {
		return "", err
	}
	req := llm.ChatRequest{
		Model:     sit.Config.MainModel,
		System:    system,
		Messages:  history.messages,
		Tools:     toolDefs(),
		MaxTokens: maxTokens,
		Fallback:  true,
	}
	if looper, ok := sit.Model.(llm.Looper); ok {
		return t.loop(ctx, looper, req)
	}

	var text string
	for round := 0; ; round++ {
		sep := text != ""
		reply, err := t.call(ctx, sit.Model, req, func(delta string) {
			if sep {
				delta, sep = "\n\n"+delta, false
			}
			a.hub.Publish(stream.Message{Type: "chat.delta", Data: Delta{Text: delta}})
		})
		text = joinText(text, reply.Text)
		if err != nil {
			return text, err
		}
		if len(reply.ToolCalls) == 0 {
			return text, nil
		}
		if round == maxRounds {
			return joinText(text, stopped()), nil
		}
		results := make([]llm.ToolResult, len(reply.ToolCalls))
		for i, c := range reply.ToolCalls {
			results[i] = t.runTool(ctx, c)
		}
		req.Messages = append(req.Messages,
			llm.Message{Role: llm.RoleAssistant, Text: reply.Text, ToolCalls: reply.ToolCalls},
			llm.Message{Role: llm.RoleUser, ToolResults: results})
	}
}

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

func (t *turn) call(ctx context.Context, model llm.Client, req llm.ChatRequest, onText func(string)) (llm.Reply, error) {
	ctx, cancel := context.WithTimeout(ctx, callTimeout)
	defer cancel()
	return model.Chat(ctx, req, onText)
}

// batch returns the turn's batch, starting it on the first write.
func (t *turn) batch(ctx context.Context) (int64, error) {
	if t.batchID == 0 {
		id, err := t.agent.svc.NewBatch(ctx, domain.ActorAgent)
		if err != nil {
			return 0, err
		}
		t.batchID = id
	}
	return t.batchID, nil
}
