// Package settings holds the user's configuration and API keys. Settings live in SQLite as one
// JSON document. API keys live in the OS keychain, never in the database.
package settings

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"slices"
	"strings"
	"time"
	"unicode/utf8"
)

var ErrInvalid = errors.New("invalid settings")

type Provider string

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

// Theme is the web UI's look. Empty is the default sticker look.
type Theme string

const (
	ThemeSticker   Theme = ""
	ThemePlain     Theme = "plain"
	ThemeNeon      Theme = "neon"
	ThemeTerminal  Theme = "terminal"
	ThemePaper     Theme = "paper"
	ThemeVaporwave Theme = "vaporwave"
)

func (t Theme) Valid() bool {
	switch t {
	case ThemeSticker, ThemePlain, ThemeNeon, ThemeTerminal, ThemePaper, ThemeVaporwave:
		return true
	}
	return false
}

// ProviderConfig picks the models for one provider. Main runs the agent, plans, and "what now?";
// light runs cleanup and day summaries.
type ProviderConfig struct {
	MainModel  string `json:"main_model"`
	LightModel string `json:"light_model"`
	BaseURL    string `json:"base_url"`
}

type Settings struct {
	// Provider is empty until the user sets one up.
	Provider   Provider       `json:"provider"`
	Anthropic  ProviderConfig `json:"anthropic"`
	OpenAI     ProviderConfig `json:"openai"`
	ClaudeCode ProviderConfig `json:"claudecode"`
	Copilot    ProviderConfig `json:"copilot"`
	Style      string         `json:"style"`
	AboutMe    string         `json:"about_me"`
	// EndOfDay is when the working day ends, as local HH:MM. Free time is counted up to it.
	EndOfDay string `json:"end_of_day"`
	Nudges   Nudges `json:"nudges"`
	// AlertLeadMinutes is how long before an event its nudge comes.
	AlertLeadMinutes int   `json:"alert_lead_minutes"`
	Theme            Theme `json:"theme"`
	// Onboarded is set once first-run setup is finished or skipped.
	Onboarded bool `json:"onboarded"`
	// AllowedHosts are hostnames the server answers besides loopback, such as the machine's
	// Tailscale name behind `tailscale serve`. Whoever reaches one gets full access.
	AllowedHosts []string `json:"allowed_hosts"`
}

// Nudges turns each kind of nudge on or off. The web client sends them; the server only keeps
// the choice.
type Nudges struct {
	BeforeEvent bool `json:"before_event"`
	EventEnd    bool `json:"event_end"`
	LateDay     bool `json:"late_day"`
}

const (
	MaxStyleRunes   = 500
	MaxAboutMeRunes = 1000
	MaxAlertLead    = 60
)

// Defaults has Claude, Claude Code and Copilot models filled in. OpenAI-compatible models are left
// for the user, since they depend on the server.
func Defaults() Settings {
	return Settings{
		Anthropic:        ProviderConfig{MainModel: "claude-sonnet-5-5", LightModel: "claude-haiku-4-5"},
		ClaudeCode:       ProviderConfig{MainModel: "sonnet", LightModel: "haiku"},
		Copilot:          ProviderConfig{MainModel: "claude-sonnet-4.5", LightModel: "gpt-5-mini"},
		EndOfDay:         "17:30",
		Nudges:           Nudges{BeforeEvent: true, EventEnd: true, LateDay: true},
		AlertLeadMinutes: 10,
		AllowedHosts:     []string{},
	}
}

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

func (s Settings) Validate() error {
	if s.Provider != "" && !s.Provider.Valid() {
		return fmt.Errorf("%w: provider %q", ErrInvalid, s.Provider)
	}
	if s.Provider != "" {
		if cfg := s.Config(); cfg.MainModel == "" || cfg.LightModel == "" {
			return fmt.Errorf("%w: %s needs a main and a light model", ErrInvalid, s.Provider)
		}
	}
	for _, cfg := range []ProviderConfig{s.Anthropic, s.OpenAI} {
		if cfg.BaseURL == "" {
			continue
		}
		u, err := url.Parse(cfg.BaseURL)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return fmt.Errorf("%w: base URL %q must be an http or https URL", ErrInvalid, cfg.BaseURL)
		}
	}
	if n := utf8.RuneCountInString(s.Style); n > MaxStyleRunes {
		return fmt.Errorf("%w: style is %d characters, the limit is %d", ErrInvalid, n, MaxStyleRunes)
	}
	if n := utf8.RuneCountInString(s.AboutMe); n > MaxAboutMeRunes {
		return fmt.Errorf("%w: about me is %d characters, the limit is %d", ErrInvalid, n, MaxAboutMeRunes)
	}
	if _, err := time.Parse(ClockLayout, s.EndOfDay); err != nil {
		return fmt.Errorf("%w: end of day %q must be HH:MM", ErrInvalid, s.EndOfDay)
	}
	if !s.Theme.Valid() {
		return fmt.Errorf("%w: theme %q", ErrInvalid, s.Theme)
	}
	if s.AlertLeadMinutes < 1 || s.AlertLeadMinutes > MaxAlertLead {
		return fmt.Errorf("%w: alert lead time is %d minutes, it must be 1 to %d", ErrInvalid, s.AlertLeadMinutes, MaxAlertLead)
	}
	for _, h := range s.AllowedHosts {
		if h == "" || strings.ContainsAny(h, ":/ ") {
			return fmt.Errorf("%w: allowed host %q must be a bare hostname, with no scheme or port", ErrInvalid, h)
		}
	}
	return nil
}

// ClockLayout is the form of a local time of day, such as EndOfDay.
const ClockLayout = "15:04"

// Load returns the saved settings, or the defaults if nothing has been saved.
func Load(ctx context.Context, db *sql.DB) (Settings, error) {
	s := Defaults()
	var body string
	err := db.QueryRowContext(ctx, `SELECT body FROM settings WHERE id = 1`).Scan(&body)
	if errors.Is(err, sql.ErrNoRows) {
		return s, nil
	}
	if err != nil {
		return Settings{}, err
	}
	err = json.Unmarshal([]byte(body), &s)
	return s, err
}

func Save(ctx context.Context, db *sql.DB, s Settings) error {
	if err := s.Validate(); err != nil {
		return err
	}
	body, err := json.Marshal(s)
	if err != nil {
		return err
	}
	_, err = db.ExecContext(ctx,
		`INSERT INTO settings (id, body) VALUES (1, ?) ON CONFLICT (id) DO UPDATE SET body = excluded.body`, string(body))
	return err
}
