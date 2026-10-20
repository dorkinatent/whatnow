package httpapi_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/zalando/go-keyring"

	"github.com/jameynakama/whatnow/internal/httpapi"
	"github.com/jameynakama/whatnow/internal/llm"
	"github.com/jameynakama/whatnow/internal/settings"
)

type modelFunc func(llm.Request) (string, error)

func (f modelFunc) Complete(_ context.Context, req llm.Request) (string, error) { return f(req) }

func (modelFunc) Chat(context.Context, llm.ChatRequest, func(string)) (llm.Reply, error) {
	panic("modelFunc doesn't chat; use chatModel")
}

func TestSettingsRoundTripWithoutExposingKeys(t *testing.T) {
	keyring.MockInit()
	t.Setenv("ANTHROPIC_API_KEY", "")
	t.Setenv("OPENAI_API_KEY", "sk-from-env")
	api := newAPI(t)

	view := decodeAs[httpapi.SettingsView](t, api.do(http.MethodGet, "/api/v1/settings", nil))
	if view.Settings.Provider != "" || view.Keys[settings.Anthropic] != "" || view.Keys[settings.OpenAI] != settings.KeyFromEnv {
		t.Fatalf("fresh settings: %+v", view)
	}

	const secret = "sk-ant-very-secret"
	wantStatus(t, api.do(http.MethodPut, "/api/v1/settings/keys/anthropic", map[string]string{"key": secret}), http.StatusNoContent)
	next := view.Settings
	next.Provider = settings.Anthropic
	next.AboutMe = "Code review drains me."
	rec := api.do(http.MethodPut, "/api/v1/settings", next)
	wantStatus(t, rec, http.StatusOK)
	if strings.Contains(rec.Body.String(), secret) {
		t.Fatal("settings response contains the API key")
	}
	view = decodeAs[httpapi.SettingsView](t, rec)
	if view.Settings.AboutMe != next.AboutMe || view.Keys[settings.Anthropic] != settings.KeyFromKeychain {
		t.Errorf("after save: %+v", view)
	}

	wantStatus(t, api.do(http.MethodDelete, "/api/v1/settings/keys/anthropic", nil), http.StatusNoContent)
	view = decodeAs[httpapi.SettingsView](t, api.do(http.MethodGet, "/api/v1/settings", nil))
	if view.Keys[settings.Anthropic] != "" {
		t.Errorf("key still reported after delete: %+v", view.Keys)
	}
}

func TestSettingsErrors(t *testing.T) {
	keyring.MockInit()
	api := newAPI(t)
	tooLong := settings.Defaults()
	tooLong.Style = strings.Repeat("x", settings.MaxStyleRunes+1)

	wantStatus(t, api.do(http.MethodPut, "/api/v1/settings", tooLong), http.StatusBadRequest)
	wantStatus(t, api.do(http.MethodPut, "/api/v1/settings/keys/gemini", map[string]string{"key": "k"}), http.StatusBadRequest)
	wantStatus(t, api.do(http.MethodPut, "/api/v1/settings/keys/openai", map[string]string{"key": " "}), http.StatusBadRequest)
}

func TestConnectionTest(t *testing.T) {
	keyring.MockInit()
	api := newAPI(t)

	got := decodeAs[httpapi.ConnectionResult](t, api.do(http.MethodPost, "/api/v1/settings/test", nil))
	if got.OK || !strings.Contains(got.Error, "no LLM provider") {
		t.Errorf("unconfigured: %+v", got)
	}

	var sent []string
	api.model = modelFunc(func(req llm.Request) (string, error) {
		sent = append(sent, req.Model)
		return "ok", nil
	})
	got = decodeAs[httpapi.ConnectionResult](t, api.do(http.MethodPost, "/api/v1/settings/test", nil))
	if !got.OK || !slices.Equal(sent, []string{"main", "light"}) {
		t.Errorf("working provider: %+v, models %q", got, sent)
	}

	api.model = modelFunc(func(llm.Request) (string, error) { return "", errors.New("anthropic: 401 invalid x-api-key") })
	rec := api.do(http.MethodPost, "/api/v1/settings/test", nil)
	wantStatus(t, rec, http.StatusOK)
	if got := decodeAs[httpapi.ConnectionResult](t, rec); got.OK || !strings.Contains(got.Error, "401") {
		t.Errorf("failing provider: %+v", got)
	}
}

func TestConnectionTestNamesAMissingLightModel(t *testing.T) {
	keyring.MockInit()
	api := newAPI(t)
	api.model = modelFunc(func(req llm.Request) (string, error) {
		if req.Model == "light" {
			return "", errors.New("openai: 404 The model 'light' does not exist")
		}
		return "ok", nil
	})
	got := decodeAs[httpapi.ConnectionResult](t, api.do(http.MethodPost, "/api/v1/settings/test", nil))
	if got.OK || !strings.HasPrefix(got.Error, "light: ") {
		t.Errorf("got %+v, want an error naming the light model", got)
	}
}

func TestConnectionTestChecksASharedModelOnce(t *testing.T) {
	keyring.MockInit()
	api := newAPI(t)
	api.models = settings.ProviderConfig{MainModel: "gpt-5.5", LightModel: "gpt-5.5"}
	calls := 0
	api.model = modelFunc(func(llm.Request) (string, error) {
		calls++
		return "ok", nil
	})
	got := decodeAs[httpapi.ConnectionResult](t, api.do(http.MethodPost, "/api/v1/settings/test", nil))
	if !got.OK || calls != 1 {
		t.Errorf("got %+v after %d calls, want ok after 1", got, calls)
	}
}

func TestDeleteEverything(t *testing.T) {
	keyring.MockInit()
	t.Setenv("ANTHROPIC_API_KEY", "")
	t.Setenv("OPENAI_API_KEY", "")
	api := newAPI(t)
	wantStatus(t, api.do(http.MethodPost, "/api/v1/tasks", map[string]string{"title": "buy oat milk"}), http.StatusCreated)
	wantStatus(t, api.do(http.MethodPut, "/api/v1/settings/keys/anthropic", map[string]string{"key": "sk-ant"}), http.StatusNoContent)
	wantStatus(t, api.do(http.MethodPut, "/api/v1/settings/keys/openai", map[string]string{"key": "sk-oai"}), http.StatusNoContent)
	set := settings.Defaults()
	set.Provider = settings.Anthropic
	set.Onboarded = true
	wantStatus(t, api.do(http.MethodPut, "/api/v1/settings", set), http.StatusOK)
	msgs, unsub := api.hub.Subscribe()
	defer unsub()

	wantStatus(t, api.do(http.MethodDelete, "/api/v1/data", nil), http.StatusNoContent)

	if tasks := decodeAs[[]json.RawMessage](t, api.do(http.MethodGet, "/api/v1/tasks", nil)); len(tasks) != 0 {
		t.Errorf("%d tasks left", len(tasks))
	}
	view := decodeAs[httpapi.SettingsView](t, api.do(http.MethodGet, "/api/v1/settings", nil))
	if diff := cmp.Diff(settings.Defaults(), view.Settings); diff != "" {
		t.Errorf("settings aren't back to the defaults (-want +got):\n%s", diff)
	}
	if view.Keys[settings.Anthropic] != "" || view.Keys[settings.OpenAI] != "" {
		t.Errorf("keys left: %+v", view.Keys)
	}
	select {
	case m := <-msgs:
		if m.Type != "data.reset" {
			t.Errorf("published %q, want data.reset", m.Type)
		}
	default:
		t.Error("data.reset wasn't published")
	}
}

func TestDeleteEverythingWithoutAKeychain(t *testing.T) {
	keyring.MockInitWithError(errors.New("no Secret Service"))
	t.Cleanup(keyring.MockInit)
	api := newAPI(t)
	msgs, unsub := api.hub.Subscribe()
	defer unsub()

	// The rows are gone either way, so an unreachable keychain mustn't turn that into a failure.
	wantStatus(t, api.do(http.MethodDelete, "/api/v1/data", nil), http.StatusNoContent)
	select {
	case m := <-msgs:
		if m.Type != "data.reset" {
			t.Errorf("published %q, want data.reset", m.Type)
		}
	default:
		t.Error("data.reset wasn't published")
	}
}

func TestSettingsShowWhetherSignInCLIsAreFound(t *testing.T) {
	keyring.MockInit()
	t.Setenv("PATH", t.TempDir())
	t.Setenv("COPILOT_CLI_PATH", "")
	defer llm.SetFallbackDirs(t.TempDir())()
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
