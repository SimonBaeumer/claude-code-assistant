package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func write(t *testing.T, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestDefaultsWhenFileMissing(t *testing.T) {
	cfg, err := Load(filepath.Join(t.TempDir(), "nope.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.General.Session != "general" || !filepath.IsAbs(cfg.General.Dir) {
		t.Fatalf("bad general: %+v", cfg.General)
	}
	if _, ok := cfg.Actions["explain"]; !ok {
		t.Fatal("explain action missing")
	}
	if cfg.Limits.MaxChars != 30000 || cfg.Queue.MaxItems != 5 {
		t.Fatalf("bad limits: %+v %+v", cfg.Limits, cfg.Queue)
	}
}

func TestOverlayKeepsDefaultsAndAddsAction(t *testing.T) {
	p := write(t, `
queue:
  max_items: 2
routes:
  - path: ~/mono/a
    session: a
actions:
  raw:
    instruction: "Look at this from {file}:"
    body: "\n{selection}"
`)
	cfg, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Queue.MaxItems != 2 {
		t.Fatal("override not applied")
	}
	if _, ok := cfg.Actions["explain"]; !ok {
		t.Fatal("default action lost when adding a new one")
	}
	if cfg.Routes[0].Dir != cfg.Routes[0].Path || !strings.HasSuffix(cfg.Routes[0].Path, "/mono/a") {
		t.Fatalf("route not expanded: %+v", cfg.Routes[0])
	}
}

func TestOverriddenActionKeepsDefaultBody(t *testing.T) {
	cfg, err := Load(write(t, "actions:\n  explain:\n    instruction: Explain {file}\n"))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Actions["explain"].Body != "\n{code_block}" {
		t.Fatalf("body = %q", cfg.Actions["explain"].Body)
	}
}

func TestValidationErrors(t *testing.T) {
	cases := map[string]string{
		"unknown key":         "queue:\n  maxitems: 3\n",
		"bad session":         "general:\n  session: \"a b\"\n",
		"relative route":      "routes:\n  - path: rel/path\n    session: x\n",
		"unknown placeholder": "actions:\n  x:\n    instruction: \"hi {nope}\"\n",
		"question w/o ask":    "actions:\n  x:\n    instruction: \"hi {question}\"\n",
		"zero chars":          "limits:\n  max_chars: 0\n",
	}
	for name, content := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := Load(write(t, content)); err == nil {
				t.Fatal("expected error")
			}
		})
	}
}

func TestNewActionFields(t *testing.T) {
	cfg, err := Load(write(t, "actions:\n  review:\n    instruction: Shorter review of {target}\n"))
	if err != nil {
		t.Fatal(err)
	}
	r := cfg.Actions["review"]
	if r.NoSelection != NoSelectionFile || r.Route != RouteRepo || r.Body != "\n{code_block}" {
		t.Fatalf("override must inherit unset fields: %+v", r)
	}
	rc := cfg.Actions["review-changes"]
	if rc.RequiresFile() || rc.Body != "" {
		t.Fatalf("review-changes: %+v", rc)
	}
	if e := cfg.Actions["edit"]; !e.Ask || e.Label() != "Describe the change:" {
		t.Fatalf("edit: %+v", e)
	}
	if l := cfg.Actions["ask"].Label(); l != "Question about the selection:" {
		t.Fatalf("default label %q", l)
	}
}

func TestNewActionFieldsValidation(t *testing.T) {
	for name, y := range map[string]string{
		"bad no_selection": "actions:\n  x:\n    instruction: hi\n    no_selection: all\n",
		"bad route":        "actions:\n  x:\n    instruction: hi\n    route: general\n",
		"no file but file": "actions:\n  x:\n    instruction: hi {file}\n    needs_file: false\n",
	} {
		if _, err := Load(write(t, y)); err == nil {
			t.Errorf("%s: expected an error", name)
		}
	}
}
