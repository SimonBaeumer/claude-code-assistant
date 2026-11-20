// Package route resolves which session a request goes to.
package route

import (
	"crypto/sha1"
	"encoding/hex"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/SimonBaeumer/claude-code-assistant/internal/config"
)

// Prefix marks every tmux session owned by claude-relay.
const Prefix = "cr-"

type Kind string

const (
	KindRepo     Kind = "repo"
	KindOverride Kind = "override"
	KindGeneral  Kind = "general"
)

// Target is the resolved route: a tmux session name and the directory Claude runs in.
type Target struct {
	Session string
	Dir     string
	Kind    Kind
}

// SessionName adds the relay prefix to a configured session name.
func SessionName(name string) string {
	if strings.HasPrefix(name, Prefix) {
		return name
	}
	return Prefix + name
}

// General returns the general session target.
func General(cfg *config.Config) Target {
	return Target{Session: SessionName(cfg.General.Session), Dir: cfg.General.Dir, Kind: KindGeneral}
}

// Resolve picks the target for a file (or, when file is empty, the project dir).
// Order: route override (longest prefix), git root of the path, general session.
func Resolve(cfg *config.Config, file, project string, general bool) Target {
	if general {
		return General(cfg)
	}
	start := file
	if start == "" {
		start = project
	}
	if start == "" {
		return General(cfg)
	}
	abs, err := filepath.Abs(start)
	if err != nil {
		return General(cfg)
	}
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		abs = resolved
	}

	var best *config.Route
	for i := range cfg.Routes {
		r := &cfg.Routes[i]
		if within(abs, r.Path) && (best == nil || len(r.Path) > len(best.Path)) {
			best = r
		}
	}
	if best != nil {
		return Target{Session: SessionName(best.Session), Dir: best.Dir, Kind: KindOverride}
	}

	dir := abs
	if fi, err := os.Stat(abs); err != nil || !fi.IsDir() {
		dir = filepath.Dir(abs)
	}
	out, err := exec.Command("git", "-C", dir, "rev-parse", "--show-toplevel").Output()
	if err != nil {
		return General(cfg)
	}
	root := strings.TrimSpace(string(out))
	if root == "" {
		return General(cfg)
	}
	return Target{Session: RepoSession(root), Dir: root, Kind: KindRepo}
}

var unsafeChars = regexp.MustCompile(`[^A-Za-z0-9_-]+`)

// RepoSession derives a stable, tmux-safe session name from a repo root.
// The hash suffix keeps two repos with the same basename apart.
func RepoSession(root string) string {
	base := unsafeChars.ReplaceAllString(filepath.Base(root), "-")
	base = strings.Trim(base, "-")
	if len(base) > 40 {
		base = base[:40]
	}
	if base == "" {
		base = "repo"
	}
	sum := sha1.Sum([]byte(root))
	return Prefix + base + "-" + hex.EncodeToString(sum[:])[:6]
}

func within(p, base string) bool {
	return p == base || strings.HasPrefix(p, strings.TrimSuffix(base, "/")+"/")
}
