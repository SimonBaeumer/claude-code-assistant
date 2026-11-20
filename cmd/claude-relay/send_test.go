package main

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/SimonBaeumer/claude-code-assistent/internal/config"
	"github.com/SimonBaeumer/claude-code-assistent/internal/desktop"
	"github.com/SimonBaeumer/claude-code-assistent/internal/route"
)

const src = "package main\n\nfunc a() {}\n\nfunc b() {\n\tprintln(\"b\")\n}\n"

// fixture returns a config and a file inside a fresh git repo, plus one outside any repo.
func fixture(t *testing.T) (cfg *config.Config, inRepo, outside string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	repo := t.TempDir()
	if out, err := exec.Command("git", "init", "-q", repo).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v %s", err, out)
	}
	inRepo = filepath.Join(repo, "main.go")
	outside = filepath.Join(t.TempDir(), "loose.go")
	for _, p := range []string{inRepo, outside} {
		if err := os.WriteFile(p, []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	cfg, err := config.Load(filepath.Join(t.TempDir(), "none.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	cfg.General.Dir = t.TempDir()
	return cfg, inRepo, outside
}

func noAsk(t *testing.T) func(string) (string, error) {
	return func(string) (string, error) { t.Fatal("dialog must not be shown"); return "", nil }
}

func TestReviewWithoutSelectionNamesWholeFile(t *testing.T) {
	cfg, f, _ := fixture(t)
	// Nothing selected: start and end line and column are the caret position.
	req, err := buildSend(cfg, sendOpts{Action: "review", File: f, Start: "5", End: "5", StartCol: "3", EndCol: "3", Line: "5"}, noAsk(t))
	if err != nil {
		t.Fatal(err)
	}
	if req.Prompt.Body != "" {
		t.Fatalf("whole-file review must not paste, body %q", req.Prompt.Body)
	}
	if !strings.Contains(req.Prompt.Instruction, "main.go (whole file)") || req.Target.Kind != route.KindRepo {
		t.Fatalf("%+v", req)
	}
}

func TestReviewSelectionPastesCode(t *testing.T) {
	cfg, f, _ := fixture(t)
	req, err := buildSend(cfg, sendOpts{Action: "review", File: f, Start: "5", End: "7", StartCol: "1", EndCol: "2"}, noAsk(t))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(req.Prompt.Instruction, "main.go, lines 5-7") || !strings.Contains(req.Prompt.Body, "println(\"b\")") {
		t.Fatalf("%+v", req.Prompt)
	}
}

func TestExplainWithoutSelectionSendsCaretLine(t *testing.T) {
	cfg, f, _ := fixture(t)
	req, err := buildSend(cfg, sendOpts{Action: "explain", File: f, Start: "3", End: "3", StartCol: "4", EndCol: "4", Line: "3"}, noAsk(t))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(req.Prompt.Body, "func a() {}") || strings.Contains(req.Prompt.Body, "func b") {
		t.Fatalf("body %q", req.Prompt.Body)
	}
}

func TestReviewChangesNeedsNoSelection(t *testing.T) {
	cfg, f, _ := fixture(t)
	req, err := buildSend(cfg, sendOpts{Action: "review-changes", File: f, Project: filepath.Dir(f)}, noAsk(t))
	if err != nil {
		t.Fatal(err)
	}
	if req.Prompt.Body != "" || !strings.Contains(req.Prompt.Instruction, "git diff HEAD") || req.Target.Kind != route.KindRepo {
		t.Fatalf("%+v", req)
	}
}

func TestEditAsksAndPutsRequestInInstruction(t *testing.T) {
	cfg, f, _ := fixture(t)
	var label string
	ask := func(l string) (string, error) { label = l; return "rename a to start\nand add a doc comment", nil }
	req, err := buildSend(cfg, sendOpts{Action: "edit", File: f, Start: "3", End: "3", StartCol: "1", EndCol: "12"}, ask)
	if err != nil {
		t.Fatal(err)
	}
	if label != "Describe the change:" {
		t.Fatalf("label %q", label)
	}
	if !strings.Contains(req.Prompt.Instruction, "as follows: rename a to start and add a doc comment") {
		t.Fatalf("instruction %q", req.Prompt.Instruction)
	}
	if !strings.Contains(req.Prompt.Body, "func a() {}") {
		t.Fatalf("body %q", req.Prompt.Body)
	}
}

func TestRepoOnlyActionsRefuseGeneralBeforeAsking(t *testing.T) {
	cfg, inRepo, outside := fixture(t)
	for _, o := range []sendOpts{
		{Action: "edit", File: outside, Start: "3", End: "3"},
		{Action: "review", File: outside, Start: "3", End: "3"},
		{Action: "edit", File: inRepo, Start: "3", End: "3", General: true},
	} {
		_, err := buildSend(cfg, o, noAsk(t))
		var se sendError
		if !errors.As(err, &se) || se.title != "This action needs a git repo" {
			t.Fatalf("%+v: want repo refusal, got %v", o, err)
		}
	}
}

func TestCancelledDialogSendsNothing(t *testing.T) {
	cfg, f, _ := fixture(t)
	ask := func(string) (string, error) { return "", desktop.ErrCancelled }
	if _, err := buildSend(cfg, sendOpts{Action: "edit", File: f, Start: "3", End: "3"}, ask); !errors.Is(err, desktop.ErrCancelled) {
		t.Fatalf("want ErrCancelled, got %v", err)
	}
}
