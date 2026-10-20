// Package app builds whatnow's parts and runs their background work. The server and the TUI both
// start from it, so a change made in either is cleaned up and replanned the same way.
package app

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/jameynakama/whatnow/internal/agent"
	"github.com/jameynakama/whatnow/internal/cleanup"
	"github.com/jameynakama/whatnow/internal/integrations"
	"github.com/jameynakama/whatnow/internal/integrations/gcal"
	"github.com/jameynakama/whatnow/internal/integrations/jira"
	"github.com/jameynakama/whatnow/internal/integrations/reviewqueue"
	"github.com/jameynakama/whatnow/internal/llm"
	"github.com/jameynakama/whatnow/internal/planner"
	"github.com/jameynakama/whatnow/internal/service"
	"github.com/jameynakama/whatnow/internal/settings"
	"github.com/jameynakama/whatnow/internal/store"
	"github.com/jameynakama/whatnow/internal/stream"
)

// Options says where the database is and how to make the model client. Now and Loc are the
// clock, which tests fix.
type Options struct {
	// DBPath defaults to <user config dir>/whatnow/whatnow.db.
	DBPath    string
	NewClient func(settings.Settings) (llm.Client, settings.ProviderConfig, error)
	Now       func() time.Time
	Loc       *time.Location
}

// App is the running set of parts. Close stops its background work and closes the database.
type App struct {
	DB      *sql.DB
	DBPath  string
	Service *service.Service
	Hub     *stream.Hub
	Cleanup *cleanup.Runner
	Planner *planner.Planner
	Agent   *agent.Agent
	Now     func() time.Time
	Loc     *time.Location
	// Integrations pull outside work in, such as calendar events, when the user asks.
	Integrations *integrations.Runner

	stop    context.CancelFunc
	running sync.WaitGroup
}

// Open opens the database and starts the cleanup runner and the replan watcher.
func Open(o Options) (*App, error) {
	path := o.DBPath
	if path == "" {
		dir, err := os.UserConfigDir()
		if err != nil {
			return nil, err
		}
		path = filepath.Join(dir, "whatnow", "whatnow.db")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	db, err := store.Open(path)
	if err != nil {
		return nil, err
	}

	svc := service.New(db, o.Now)
	hub := stream.NewHub()
	active := func(ctx context.Context) (llm.Client, settings.ProviderConfig, settings.Settings, error) {
		s, err := settings.Load(ctx, db)
		if err != nil {
			return nil, settings.ProviderConfig{}, settings.Settings{}, err
		}
		client, cfg, err := o.NewClient(s)
		return client, cfg, s, err
	}
	plans := planner.New(svc, hub, active, o.Loc, o.Now)
	a := &App{
		DB:      db,
		DBPath:  path,
		Service: svc,
		Hub:     hub,
		Cleanup: cleanup.New(svc, hub, active, o.Loc, o.Now),
		Planner: plans,
		Agent:   agent.New(svc, hub, plans, o.Loc, o.Now),
		Now:     o.Now,
		Loc:     o.Loc,
		Integrations: integrations.NewRunner(db, svc, hub, o.Loc, o.Now,
			gcal.New(nil), jira.New(nil), reviewqueue.New(nil),
		),
	}

	ctx, stop := context.WithCancel(context.Background())
	a.stop = stop
	watched, unsubscribe := hub.Subscribe("batch.applied", "batch.undone")
	a.running.Add(2)
	go func() {
		defer a.running.Done()
		a.Cleanup.Run(ctx)
	}()
	go func() {
		defer a.running.Done()
		defer unsubscribe()
		plans.Run(ctx, watched)
	}()
	return a, nil
}

// Close stops the background work, waits for it, and closes the database.
func (a *App) Close() error {
	a.stop()
	a.running.Wait()
	settings.StopRuntimes()
	return a.DB.Close()
}
