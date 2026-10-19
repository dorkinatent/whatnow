package settings_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/zalando/go-keyring"

	"github.com/jameynakama/whatnow/internal/llm"
	"github.com/jameynakama/whatnow/internal/settings"
	"github.com/jameynakama/whatnow/internal/store"
)

func TestLoadDefaultsThenSaveRoundTrip(t *testing.T) {
	ctx := context.Background()
	db, err := store.Open(filepath.Join(t.TempDir(), "s.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	got, err := settings.Load(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	if diff := cmp.Diff(settings.Defaults(), got); diff != "" {
		t.Errorf("fresh load differs from defaults:\n%s", diff)
	}

	want := settings.Defaults()
	want.Provider = settings.Anthropic
	want.Style = "gentle, like Thich Nhat Hanh"
	want.AboutMe = "Code review drains me. Writing is easy."
	if err := settings.Save(ctx, db, want); err != nil {
		t.Fatal(err)
	}
	got, err = settings.Load(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("saved settings differ (-want +got):\n%s", diff)
	}
}

func TestLoadFillsFieldsAddedSinceTheSave(t *testing.T) {
	ctx := context.Background()
	db, err := store.Open(filepath.Join(t.TempDir(), "s.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	// Saved before nudges, the alert lead time, and onboarding existed.
	_, err = db.Exec(`INSERT INTO settings (id, body) VALUES (1, '{"provider":"anthropic","end_of_day":"18:00"}')`)
	if err != nil {
		t.Fatal(err)
	}

	got, err := settings.Load(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	want := settings.Defaults()
	want.Provider = settings.Anthropic
	want.EndOfDay = "18:00"
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("old settings didn't pick up the new defaults (-want +got):\n%s", diff)
	}
}

func TestValidate(t *testing.T) {
	valid := settings.Defaults()
	valid.Provider = settings.Anthropic
	cases := map[string]func(*settings.Settings){
		"unknown provider":              func(s *settings.Settings) { s.Provider = "gemini" },
		"openai without models":         func(s *settings.Settings) { s.Provider = settings.OpenAI },
		"claude code without models":    func(s *settings.Settings) { s.Provider = settings.ClaudeCode; s.ClaudeCode = settings.ProviderConfig{} },
		"copilot without models":        func(s *settings.Settings) { s.Provider = settings.Copilot; s.Copilot = settings.ProviderConfig{} },
		"base URL without scheme":       func(s *settings.Settings) { s.OpenAI.BaseURL = "localhost:11434" },
		"style over 500 characters":     func(s *settings.Settings) { s.Style = strings.Repeat("é", 501) },
		"about me over 1000 characters": func(s *settings.Settings) { s.AboutMe = strings.Repeat("🧠", 1001) },
		"end of day not HH:MM":          func(s *settings.Settings) { s.EndOfDay = "5pm" },
		"no alert lead time":            func(s *settings.Settings) { s.AlertLeadMinutes = 0 },
		"alert lead over an hour":       func(s *settings.Settings) { s.AlertLeadMinutes = 61 },
		"unknown theme":                 func(s *settings.Settings) { s.Theme = "geocities" },
		"allowed host with a port":      func(s *settings.Settings) { s.AllowedHosts = []string{"mac.example.ts.net:443"} },
		"allowed host with a scheme":    func(s *settings.Settings) { s.AllowedHosts = []string{"https://mac.example.ts.net"} },
		"blank allowed host":            func(s *settings.Settings) { s.AllowedHosts = []string{" "} },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			s := valid
			mutate(&s)
			if err := s.Validate(); !errors.Is(err, settings.ErrInvalid) {
				t.Errorf("err = %v, want ErrInvalid", err)
			}
		})
	}

	atLimit := valid
	atLimit.Style = strings.Repeat("é", 500) // 1000 bytes, 500 characters
	atLimit.OpenAI.BaseURL = "http://localhost:11434/v1"
	atLimit.AlertLeadMinutes = 60
	atLimit.Theme = settings.ThemeVaporwave
	atLimit.AllowedHosts = []string{"mac.example.ts.net", "100.64.0.1"}
	if err := atLimit.Validate(); err != nil {
		t.Errorf("settings at the limits rejected: %v", err)
	}
}

func TestKeysPreferKeychainOverEnv(t *testing.T) {
	keyring.MockInit()
	t.Setenv("ANTHROPIC_API_KEY", "from-env")

	key, source, err := settings.Key(settings.Anthropic)
	if err != nil || key != "from-env" || source != settings.KeyFromEnv {
		t.Fatalf("env fallback: %q %q %v", key, source, err)
	}

	if err := settings.SetKey(settings.Anthropic, "  from-keychain \n"); err != nil {
		t.Fatal(err)
	}
	key, source, err = settings.Key(settings.Anthropic)
	if err != nil || key != "from-keychain" || source != settings.KeyFromKeychain {
		t.Fatalf("keychain: %q %q %v", key, source, err)
	}

	if err := settings.DeleteKey(settings.Anthropic); err != nil {
		t.Fatal(err)
	}
	if err := settings.DeleteKey(settings.Anthropic); err != nil {
		t.Errorf("deleting a missing key: %v", err)
	}
	if _, source, _ := settings.Key(settings.Anthropic); source != settings.KeyFromEnv {
		t.Errorf("after delete, source = %q, want env", source)
	}
	if err := settings.SetKey(settings.Anthropic, "   "); !errors.Is(err, settings.ErrInvalid) {
		t.Errorf("blank key: err = %v, want ErrInvalid", err)
	}
}

func TestNewClientNeedsProviderAndKey(t *testing.T) {
	keyring.MockInit()
	t.Setenv("OPENAI_API_KEY", "")
	s := settings.Defaults()

	if _, _, err := settings.NewClient(s); !errors.Is(err, settings.ErrNotConfigured) {
		t.Errorf("no provider: err = %v, want ErrNotConfigured", err)
	}
	s.Provider = settings.OpenAI
	s.OpenAI = settings.ProviderConfig{MainModel: "big", LightModel: "small"}
	if _, _, err := settings.NewClient(s); !errors.Is(err, settings.ErrNotConfigured) {
		t.Errorf("no key: err = %v, want ErrNotConfigured", err)
	}
	if err := settings.SetKey(settings.OpenAI, "sk-test"); err != nil {
		t.Fatal(err)
	}
	client, cfg, err := settings.NewClient(s)
	if err != nil || client == nil || cfg.LightModel != "small" {
		t.Errorf("configured: client %v, cfg %+v, err %v", client, cfg, err)
	}
}

func TestKeychainFailureFallsBackToEnv(t *testing.T) {
	keyring.MockInitWithError(errors.New("no secret service"))
	t.Setenv("ANTHROPIC_API_KEY", "from-env")

	key, source, err := settings.Key(settings.Anthropic)
	if err != nil || key != "from-env" || source != settings.KeyFromEnv {
		t.Errorf("got %q %q %v, want the env key", key, source, err)
	}
}

func TestSettingsSavedBeforeEndOfDayExistedGetTheDefault(t *testing.T) {
	ctx := context.Background()
	db, err := store.Open(filepath.Join(t.TempDir(), "s.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`INSERT INTO settings (id, body) VALUES (1, '{"provider":"anthropic","style":"terse"}')`); err != nil {
		t.Fatal(err)
	}

	got, err := settings.Load(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	if got.EndOfDay != "17:30" || got.Style != "terse" {
		t.Errorf("loaded %+v", got)
	}
}

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

// A launchd service's PATH has neither ~/.local/bin nor Homebrew's, so the CLI is looked for there.
func TestClaudeIsFoundInItsInstallDirWhenNotOnPATH(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell stubs")
	}
	t.Setenv("PATH", t.TempDir())
	home := t.TempDir()
	t.Setenv("HOME", home)
	bin := filepath.Join(home, ".local", "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bin, "claude"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	if src, err := settings.Source(settings.ClaudeCode); err != nil || src != settings.KeyFromLogin {
		t.Errorf("claude in ~/.local/bin: source %q, %v", src, err)
	}
}

func TestSignInProvidersNeedTheirCLI(t *testing.T) {
	keyring.MockInit()
	t.Setenv("PATH", t.TempDir())
	t.Setenv("COPILOT_CLI_PATH", "")
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	t.Setenv("HOME", t.TempDir())
	defer llm.SetFallbackDirs(t.TempDir())()

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
