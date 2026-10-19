package llm

import (
	"os"
	"os/exec"
	"path/filepath"
)

// fallbackDirs are where a CLI is looked for when it isn't on PATH. A launchd service gets only
// /usr/bin:/bin:/usr/sbin:/sbin, which has neither claude's native install nor Homebrew's.
var fallbackDirs = func() []string {
	dirs := []string{"/opt/homebrew/bin", "/usr/local/bin"}
	if home, err := os.UserHomeDir(); err == nil {
		dirs = append([]string{filepath.Join(home, ".local", "bin"), filepath.Join(home, ".claude", "local")}, dirs...)
	}
	return dirs
}

// SetFallbackDirs replaces the directories FindCLI tries after PATH, for tests that must not see
// the machine's real installs. It returns a function that puts the old ones back.
func SetFallbackDirs(dirs ...string) (restore func()) {
	old := fallbackDirs
	fallbackDirs = func() []string { return dirs }
	return func() { fallbackDirs = old }
}

// FindCLI finds the executable name on PATH, then in the usual install directories.
func FindCLI(name string) (string, error) {
	path, err := exec.LookPath(name)
	if err == nil {
		return path, nil
	}
	for _, dir := range fallbackDirs() {
		p := filepath.Join(dir, name)
		if info, statErr := os.Stat(p); statErr == nil && info.Mode().IsRegular() && info.Mode().Perm()&0o111 != 0 {
			return p, nil
		}
	}
	return "", err
}
