package llm_test

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/jameynakama/whatnow/internal/llm"
)

func stubCLI(t *testing.T, dir, name string, mode os.FileMode) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("shell stubs")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte("#!/bin/sh\n"), mode); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestFindCLIPrefersPATH(t *testing.T) {
	onPath := t.TempDir()
	want := stubCLI(t, onPath, "whatnow-fake-cli", 0o755)
	stubCLI(t, filepath.Join(t.TempDir(), "bin"), "whatnow-fake-cli", 0o755)
	t.Setenv("PATH", onPath)
	defer llm.SetFallbackDirs(t.TempDir())()

	if got, err := llm.FindCLI("whatnow-fake-cli"); err != nil || got != want {
		t.Errorf("FindCLI = %q, %v; want %q", got, err, want)
	}
}

func TestFindCLIFallsBackToTheInstallDirs(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	empty, other := t.TempDir(), t.TempDir()
	stubCLI(t, other, "whatnow-fake-cli", 0o644) // not executable
	second := t.TempDir()
	want := stubCLI(t, second, "whatnow-fake-cli", 0o755)
	defer llm.SetFallbackDirs(empty, other, second)()

	if got, err := llm.FindCLI("whatnow-fake-cli"); err != nil || got != want {
		t.Errorf("FindCLI = %q, %v; want %q", got, err, want)
	}
}

func TestFindCLINotFound(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	defer llm.SetFallbackDirs(t.TempDir())()

	if got, err := llm.FindCLI("whatnow-fake-cli"); err == nil {
		t.Errorf("FindCLI = %q, want an error", got)
	}
}
