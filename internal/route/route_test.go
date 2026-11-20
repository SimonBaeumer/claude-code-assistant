package route

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/SimonBaeumer/claude-code-assistent/internal/config"
)

func cfgFor(t *testing.T, routes ...config.Route) *config.Config {
	t.Helper()
	cfg, err := config.Load(filepath.Join(t.TempDir(), "none.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	cfg.Routes = routes
	return cfg
}

func realTemp(t *testing.T) string {
	d, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func TestGitRootRoute(t *testing.T) {
	repo := filepath.Join(realTemp(t), "my.repo")
	sub := filepath.Join(repo, "pkg", "x")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("git", "init", "-q", repo).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v %s", err, out)
	}
	file := filepath.Join(sub, "main.go")
	os.WriteFile(file, []byte("package x"), 0o644)

	tg := Resolve(cfgFor(t), file, "", false)
	if tg.Kind != KindRepo || tg.Dir != repo {
		t.Fatalf("got %+v", tg)
	}
	if !strings.HasPrefix(tg.Session, "cr-my-repo-") || len(tg.Session) != len("cr-my-repo-")+6 {
		t.Fatalf("session name %q", tg.Session)
	}
	if again := Resolve(cfgFor(t), sub, "", false); again.Session != tg.Session {
		t.Fatal("same repo must map to same session")
	}
}

func TestOverrideLongestPrefixWins(t *testing.T) {
	base := realTemp(t)
	a := filepath.Join(base, "mono")
	b := filepath.Join(base, "mono", "svc")
	os.MkdirAll(b, 0o755)
	cfg := cfgFor(t,
		config.Route{Path: a, Session: "mono", Dir: a},
		config.Route{Path: b, Session: "svc", Dir: b},
	)
	if tg := Resolve(cfg, filepath.Join(b, "f.go"), "", false); tg.Session != "cr-svc" || tg.Kind != KindOverride {
		t.Fatalf("got %+v", tg)
	}
	if tg := Resolve(cfg, filepath.Join(a, "f.go"), "", false); tg.Session != "cr-mono" {
		t.Fatalf("got %+v", tg)
	}
	// "monolith" must not match prefix "mono".
	if tg := Resolve(cfg, filepath.Join(base, "monolith", "f.go"), "", false); tg.Kind != KindGeneral {
		t.Fatalf("prefix matched across path boundary: %+v", tg)
	}
}

func TestOutsideRepoFallsBackToGeneral(t *testing.T) {
	cfg := cfgFor(t)
	if tg := Resolve(cfg, filepath.Join(realTemp(t), "loose.txt"), "", false); tg.Kind != KindGeneral || tg.Session != "cr-general" {
		t.Fatalf("got %+v", tg)
	}
	if tg := Resolve(cfg, "", "", false); tg.Kind != KindGeneral {
		t.Fatalf("got %+v", tg)
	}
}

func TestGeneralFlag(t *testing.T) {
	if tg := Resolve(cfgFor(t), "/whatever", "", true); tg.Kind != KindGeneral {
		t.Fatalf("got %+v", tg)
	}
}
