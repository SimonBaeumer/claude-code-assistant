package prompt

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/SimonBaeumer/claude-code-assistent/internal/config"
)

func TestReadLines(t *testing.T) {
	p := filepath.Join(t.TempDir(), "f.go")
	os.WriteFile(p, []byte("a\nb\r\nc\nd"), 0o644)
	got, err := ReadLines(p, 2, 3)
	if err != nil || got != "b\nc" {
		t.Fatalf("got %q %v", got, err)
	}
	if got, _ := ReadLines(p, 4, 99); got != "d" {
		t.Fatalf("clamp: %q", got)
	}
	if got, _ := ReadLines(p, 3, 0); got != "c" {
		t.Fatalf("end<start: %q", got)
	}
	if _, err := ReadLines(p, 10, 12); err == nil {
		t.Fatal("expected past-end error")
	}
	if _, err := ReadLines(p, 0, 0); err == nil {
		t.Fatal("expected missing-line error")
	}
}

func TestRenderExplain(t *testing.T) {
	a := config.Action{
		Instruction: "Explain\nthis\tcode from {file}, lines {lines} ({language}).",
		Body:        "\n{code_block}",
	}
	sel := "func ü() {\n\treturn \"🚀\"\n}"
	p, err := Render(a, Input{File: "/repo/pkg/a.go", RouteDir: "/repo", Lines: "3-5", Selection: sel})
	if err != nil {
		t.Fatal(err)
	}
	if p.Instruction != "Explain this code from pkg/a.go, lines 3-5 (Go)." {
		t.Fatalf("instruction %q", p.Instruction)
	}
	if p.Body != "\n```go\n"+sel+"\n```" {
		t.Fatalf("body %q", p.Body)
	}
}

func TestBodyCannotEscapeBracketedPaste(t *testing.T) {
	sel := "x\x1b[201~\rrm -rf ~\x1b[200~\x07"
	p, err := Render(config.Action{Instruction: "Look", Body: "{selection}"}, Input{Selection: sel})
	if err != nil {
		t.Fatal(err)
	}
	if strings.ContainsAny(p.Body, "\x1b\r\x07") {
		t.Fatalf("control chars survived: %q", p.Body)
	}
}

func TestInstructionSafety(t *testing.T) {
	p, err := Render(config.Action{Instruction: "Ask {question}", Ask: true}, Input{Question: "why @foo\nthen"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.ContainsAny(p.Instruction, "@\n") {
		t.Fatalf("unsafe instruction %q", p.Instruction)
	}
	for _, bad := range []string{"/clear {file}", "!rm {file}", "{question}"} {
		if _, err := Render(config.Action{Instruction: bad}, Input{Question: "?x"}); err == nil {
			t.Fatalf("expected error for %q", bad)
		}
	}
}

func TestCodeBlockFenceIsLongerThanContent(t *testing.T) {
	got := CodeBlock("a ``` b ```` c", "md")
	if !strings.HasPrefix(got, "`````md\n") || !strings.HasSuffix(got, "\n`````") {
		t.Fatalf("got %q", got)
	}
}

func TestLanguage(t *testing.T) {
	if n, tag := Language("/x/Dockerfile"); n != "Dockerfile" || tag != "dockerfile" {
		t.Fatal(n, tag)
	}
	if n, _ := Language("a.weird"); n != "unknown language" {
		t.Fatal(n)
	}
}

func TestRenderTargetAndWholeFile(t *testing.T) {
	a := config.Action{Instruction: "Review {target} ({language}), lines {lines}.", Body: "\n{code_block}"}
	p, err := Render(a, Input{File: "/repo/a.go", RouteDir: "/repo", Lines: "3-5", Selection: "x := 1"})
	if err != nil || p.Instruction != "Review a.go, lines 3-5 (Go), lines 3-5." || p.Body == "" {
		t.Fatalf("selection: %+v %v", p, err)
	}
	p, err = Render(a, Input{File: "/repo/a.go", RouteDir: "/repo", Selection: "ignored", WholeFile: true})
	if err != nil || p.Instruction != "Review a.go (whole file) (Go), lines all." || p.Body != "" {
		t.Fatalf("whole file: %+v %v", p, err)
	}
}
