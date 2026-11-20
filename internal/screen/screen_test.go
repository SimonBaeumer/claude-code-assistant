package screen

import (
	"strings"
	"testing"
)

const rule = "────────────────────────────────────────"

func frame(input, footer string) string {
	return strings.Join([]string{
		" ▐▛███▛█   Claude Code v2.1.329",
		"● PONG 6",
		rule,
		input,
		rule,
		"  5h Usage: │ 7d Usage:  │ Context:",
		footer,
	}, "\n") + "\n"
}

func TestIdleEmpty(t *testing.T) {
	s := Parse(frame("\x1b[39m❯ ", "  ⏵⏵ auto mode on (shift+tab to cycle)"))
	if s.Kind != Idle || !s.InputEmpty {
		t.Fatalf("%+v", s)
	}
}

func TestIdleWithDimPlaceholder(t *testing.T) {
	s := Parse(frame("\x1b[39m❯ \x1b[2mTry\x1b[0m \x1b[2m\"how\x1b[0m \x1b[2mdoes\x1b[0m", "  ⏸ manual mode on"))
	if s.Kind != Idle || !s.InputEmpty {
		t.Fatalf("%+v", s)
	}
}

func TestIdleWithTypedText(t *testing.T) {
	s := Parse(frame("\x1b[39m❯ abc", "  ⏵⏵ auto mode on"))
	if s.Kind != Idle || s.InputEmpty || s.Input != "abc" {
		t.Fatalf("%+v", s)
	}
}

func TestExtendedColourIsNotDim(t *testing.T) {
	// "38;2;..." contains a literal 2 parameter that must not be read as dim.
	s := Parse(frame("\x1b[38;2;10;20;30m❯ typed", ""))
	if s.InputEmpty {
		t.Fatalf("%+v", s)
	}
}

func TestMultilineInputBox(t *testing.T) {
	raw := strings.Join([]string{"x", rule, "❯ first line", "  second line", rule, "footer"}, "\n")
	s := Parse(raw)
	if s.Kind != Idle || s.InputEmpty || !strings.Contains(s.Input, "second line") {
		t.Fatalf("%+v", s)
	}
}

func TestBusy(t *testing.T) {
	s := Parse(frame("❯ ", "  ⏵⏵ auto mode on (shift+tab to cycle) · esc to interrupt"))
	if s.Kind != Busy {
		t.Fatalf("%+v", s)
	}
}

func TestPermissionPrompt(t *testing.T) {
	raw := strings.Join([]string{
		"● Bash(touch created.txt)",
		"  ⎿  Waiting…",
		rule,
		" Bash command",
		" touch created.txt",
		" Do you want to proceed?",
		" ❯ 1. Yes",
		"   2. No",
		" Esc to cancel · Tab to amend",
	}, "\n")
	if s := Parse(raw); s.Kind != Permission {
		t.Fatalf("%+v", s)
	}
}

func TestUnknownScreen(t *testing.T) {
	if s := Parse("simon@host:~$ \n"); s.Kind != Unknown {
		t.Fatalf("%+v", s)
	}
}

func TestPastedPlaceholderCountsAsInput(t *testing.T) {
	s := Parse(frame("❯ [Pasted text #1 +5 lines]", ""))
	if s.InputEmpty {
		t.Fatalf("%+v", s)
	}
}
