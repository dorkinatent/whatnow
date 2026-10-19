package settings

import (
	"errors"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"

	"github.com/zalando/go-keyring"

	"github.com/jameynakama/whatnow/internal/llm"
)

// ErrNotConfigured means no provider is chosen, the chosen one has no key, or its CLI isn't found.
var ErrNotConfigured = errors.New("no LLM provider is set up")

// KeySource says where a provider's key comes from. Empty means there is no key.
type KeySource string

const (
	KeyFromKeychain KeySource = "keychain"
	KeyFromEnv      KeySource = "env"
	// KeyFromLogin means a provider that signs in through its CLI found that CLI. Whether it's
	// signed in shows only when it's used.
	KeyFromLogin KeySource = "login"
)

const keychainService = "whatnow"

var envVars = map[Provider]string{Anthropic: "ANTHROPIC_API_KEY", OpenAI: "OPENAI_API_KEY"}

// Key returns a provider's API key from the keychain, falling back to the provider's usual
// environment variable.
func Key(p Provider) (string, KeySource, error) {
	key, err := keyring.Get(keychainService, string(p))
	if err == nil {
		return key, KeyFromKeychain, nil
	}
	if !errors.Is(err, keyring.ErrNotFound) {
		// An unavailable keychain (no Secret Service, a denied prompt) shouldn't hide an env key.
		log.Printf("read %s key from keychain: %v", p, err)
	}
	if key := os.Getenv(envVars[p]); key != "" {
		return key, KeyFromEnv, nil
	}
	return "", "", nil
}

func SetKey(p Provider, key string) error {
	if p.SignsIn() {
		return fmt.Errorf("%w: provider %q takes no key", ErrInvalid, p)
	}
	if !p.Valid() {
		return fmt.Errorf("%w: provider %q", ErrInvalid, p)
	}
	key = strings.TrimSpace(key)
	if key == "" {
		return fmt.Errorf("%w: key is empty", ErrInvalid)
	}
	return keyring.Set(keychainService, string(p), key)
}

// DeleteKey removes a provider's key from the keychain. Deleting a key that isn't there is fine.
func DeleteKey(p Provider) error {
	if p.SignsIn() {
		return fmt.Errorf("%w: provider %q takes no key", ErrInvalid, p)
	}
	if !p.Valid() {
		return fmt.Errorf("%w: provider %q", ErrInvalid, p)
	}
	if err := keyring.Delete(keychainService, string(p)); err != nil && !errors.Is(err, keyring.ErrNotFound) {
		return err
	}
	return nil
}

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
