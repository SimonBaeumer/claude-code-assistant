// Package envpath repairs PATH for processes launched from a desktop session (an IDE started
// from the app menu does not read ~/.zshrc or ~/.profile, so ~/.local/bin is often missing).
package envpath

import (
	"os"
	"path/filepath"
	"strings"
)

// UserDirs are added to PATH, in this order, when they exist and are missing.
var UserDirs = []string{"~/.local/bin", "~/bin", "~/go/bin", "~/.cargo/bin", "/usr/local/bin"}

// Fix appends missing existing dirs (and the running executable's dir) to PATH and returns it.
func Fix() string {
	home, _ := os.UserHomeDir()
	dirs := filepath.SplitList(os.Getenv("PATH"))
	have := map[string]bool{}
	for _, d := range dirs {
		have[filepath.Clean(d)] = true
	}
	cands := append([]string{}, UserDirs...)
	if exe, err := os.Executable(); err == nil {
		cands = append(cands, filepath.Dir(exe))
	}
	for _, d := range cands {
		if strings.HasPrefix(d, "~/") {
			if home == "" {
				continue
			}
			d = filepath.Join(home, d[2:])
		}
		d = filepath.Clean(d)
		if have[d] {
			continue
		}
		if fi, err := os.Stat(d); err == nil && fi.IsDir() {
			dirs = append(dirs, d)
			have[d] = true
		}
	}
	p := strings.Join(dirs, string(os.PathListSeparator))
	os.Setenv("PATH", p)
	return p
}
