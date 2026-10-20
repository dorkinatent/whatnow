package httpapi

import (
	"fmt"
	"log"
	"net/http"
	"slices"

	"github.com/jameynakama/whatnow/internal/integrations"
	"github.com/jameynakama/whatnow/internal/llm"
	"github.com/jameynakama/whatnow/internal/settings"
	"github.com/jameynakama/whatnow/internal/store"
	"github.com/jameynakama/whatnow/internal/stream"
)

// SettingsView is what the settings screen gets: the settings, and where each provider's
// access comes from: a key's source, or `login` for a provider whose CLI is found. Keys themselves
// never leave the server.
type SettingsView struct {
	Settings settings.Settings                        `json:"settings"`
	Keys     map[settings.Provider]settings.KeySource `json:"keys"`
}

func (s *Server) settingsView(w http.ResponseWriter, r *http.Request) {
	saved, err := settings.Load(r.Context(), s.DB)
	if err != nil {
		writeError(w, err)
		return
	}
	view := SettingsView{Settings: saved, Keys: map[settings.Provider]settings.KeySource{}}
	for _, p := range settings.Providers {
		source, err := settings.Source(p)
		if err != nil {
			writeError(w, err)
			return
		}
		view.Keys[p] = source
	}
	writeJSON(w, http.StatusOK, view)
}

func (s *Server) getSettings(w http.ResponseWriter, r *http.Request) {
	s.settingsView(w, r)
}

func (s *Server) putSettings(w http.ResponseWriter, r *http.Request) {
	var body settings.Settings
	if err := decode(r, &body); err != nil {
		writeError(w, err)
		return
	}
	if err := settings.Save(r.Context(), s.DB, body); err != nil {
		writeError(w, err)
		return
	}
	s.settingsView(w, r)
}

func (s *Server) putKey(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Key string `json:"key"`
	}
	if err := decode(r, &body); err != nil {
		writeError(w, err)
		return
	}
	if err := settings.SetKey(settings.Provider(r.PathValue("provider")), body.Key); err != nil {
		writeError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) deleteKey(w http.ResponseWriter, r *http.Request) {
	if err := settings.DeleteKey(settings.Provider(r.PathValue("provider"))); err != nil {
		writeError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ConnectionResult reports whether the saved provider answered a tiny request.
type ConnectionResult struct {
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
}

// testConnection sends one tiny request to each model, main then light, so a mistyped light
// model shows here rather than as cleanups that quietly fail. A failure is a normal result for
// the settings screen to show, so it comes back as 200 with ok false.
func (s *Server) testConnection(w http.ResponseWriter, r *http.Request) {
	saved, err := settings.Load(r.Context(), s.DB)
	if err != nil {
		writeError(w, err)
		return
	}
	client, cfg, err := s.NewClient(saved)
	if err != nil {
		writeJSON(w, http.StatusOK, ConnectionResult{Error: err.Error()})
		return
	}
	for _, model := range slices.Compact([]string{cfg.MainModel, cfg.LightModel}) {
		_, err := client.Complete(r.Context(), llm.Request{
			Model:     model,
			User:      "Reply with the single word: ok",
			MaxTokens: 1024, // room for models that think before answering
		})
		if err != nil {
			writeJSON(w, http.StatusOK, ConnectionResult{Error: fmt.Sprintf("%s: %v", model, err)})
			return
		}
	}
	writeJSON(w, http.StatusOK, ConnectionResult{OK: true})
}

// deleteData wipes every row, both saved keys, and the integrations' secrets, then tells every
// open tab to start over. Environment keys can't be removed and are found again on the next setup.
// An unreachable keychain is logged rather than failed, as in settings.Key: the rows are already
// gone. The secrets are listed first, since the rows say which there are.
func (s *Server) deleteData(w http.ResponseWriter, r *http.Request) {
	secrets, err := s.Integrations.Secrets(r.Context())
	if err != nil {
		writeError(w, err)
		return
	}
	if err := store.Wipe(r.Context(), s.DB); err != nil {
		writeError(w, err)
		return
	}
	for _, p := range []settings.Provider{settings.Anthropic, settings.OpenAI} {
		if err := settings.DeleteKey(p); err != nil {
			log.Printf("delete %s key: %v", p, err)
		}
	}
	if err := integrations.DeleteSecrets(secrets); err != nil {
		log.Printf("delete integration secrets: %v", err)
	}
	s.Hub.Publish(stream.Message{Type: "data.reset"})
	w.WriteHeader(http.StatusNoContent)
}
