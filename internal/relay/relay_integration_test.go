package relay

import (
	"bufio"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/SimonBaeumer/claude-code-assistent/internal/config"
	"github.com/SimonBaeumer/claude-code-assistent/internal/prompt"
	"github.com/SimonBaeumer/claude-code-assistent/internal/route"
	"github.com/SimonBaeumer/claude-code-assistent/internal/state"
	"github.com/SimonBaeumer/claude-code-assistent/internal/tmux"
)

var fakeBin string

func stateTryLock(session string) (func(), bool, error) { return state.TryLock(session, "drainer") }

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "faketui")
	if err != nil {
		panic(err)
	}
	fakeBin = filepath.Join(dir, "faketui")
	if out, err := exec.Command("go", "build", "-o", fakeBin, "./testdata/faketui").CombinedOutput(); err != nil {
		panic(fmt.Sprintf("building faketui: %v\n%s", err, out))
	}
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

type env struct {
	r      *Relay
	target route.Target
	log    string
}

// setup starts nothing yet; it gives each test an isolated tmux server and runtime dir.
func setup(t *testing.T, busyMs int) env {
	t.Helper()
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not installed")
	}
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	log := filepath.Join(t.TempDir(), "submits.jsonl")
	t.Setenv("FAKE_LOG", log)
	t.Setenv("FAKE_BUSY_MS", fmt.Sprint(busyMs))
	t.Setenv("TMUX", "") // never talk to the user's server

	b := make([]byte, 4)
	rand.Read(b)
	tm := tmux.Tmux{Socket: "cr-test-" + hex.EncodeToString(b), Config: "/dev/null"}
	t.Cleanup(func() { exec.Command("tmux", "-L", tm.Socket, "kill-server").Run() })

	cfg, err := config.Load(filepath.Join(t.TempDir(), "none.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	cfg.Claude.Command = fakeBin
	cfg.Claude.Resume = false
	cfg.Claude.StartupTimeoutMs = 5000
	return env{
		r:      &Relay{Cfg: cfg, T: tm, Exe: "/bin/true", Grace: 300 * time.Millisecond, Poll: 50 * time.Millisecond},
		target: route.Target{Session: "cr-test", Dir: t.TempDir(), Kind: route.KindRepo},
		log:    log,
	}
}

type submit struct {
	Text string `json:"text"`
	Busy bool   `json:"busy"`
}

func submits(t *testing.T, path string) []submit {
	t.Helper()
	f, err := os.Open(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var out []submit
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		var s submit
		if err := json.Unmarshal(sc.Bytes(), &s); err != nil {
			t.Fatal(err)
		}
		if s.Busy {
			t.Fatalf("a prompt was submitted while the session was busy: %q", s.Text)
		}
		out = append(out, s)
	}
	return out
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func TestSendStartsSessionAndDeliversMultilineExactlyOnce(t *testing.T) {
	e := setup(t, 300)
	p := prompt.Prompt{
		Instruction: "Explain this Go code from main.go, lines 3-5:",
		Body:        "\n```go\nfunc ü() string {\n\treturn \"🚀 \\\"quoted\\\"\"\n}\n\n// after a blank line\n```",
	}
	out, err := e.r.Send(e.target, p, "explain")
	if err != nil {
		t.Fatal(err)
	}
	if out.Result != "sent" || !out.Started {
		t.Fatalf("outcome %+v", out)
	}
	var got []submit
	waitFor(t, "one submit", func() bool { got = submits(t, e.log); return len(got) >= 1 })
	time.Sleep(300 * time.Millisecond)
	got = submits(t, e.log)
	if len(got) != 1 {
		t.Fatalf("want exactly 1 submit, got %d: %+v", len(got), got)
	}
	if got[0].Text != p.Instruction+p.Body {
		t.Fatalf("submitted text differs\nwant %q\ngot  %q", p.Instruction+p.Body, got[0].Text)
	}

	// Starting again must reuse the session, never create a second one.
	if _, err := e.r.Start(e.target); err != nil {
		t.Fatal(err)
	}
	if n := len(e.r.T.ListSessions()); n != 1 {
		t.Fatalf("want 1 tmux session, got %d", n)
	}
}

func TestBusySessionQueuesFIFOAndDrainsOneAtATime(t *testing.T) {
	e := setup(t, 1200)
	if _, err := e.r.Start(e.target); err != nil {
		t.Fatal(err)
	}
	for i, name := range []string{"A", "B", "C"} {
		out, err := e.r.Send(e.target, prompt.Prompt{Instruction: "Prompt " + name}, name)
		if err != nil {
			t.Fatal(err)
		}
		if i == 0 && out.Result != "sent" {
			t.Fatalf("A: %+v", out)
		}
		if i > 0 && (out.Result != "queued" || out.Position != i) {
			t.Fatalf("%s: %+v", name, out)
		}
	}
	if n := len(submits(t, e.log)); n != 1 {
		t.Fatalf("only A may be submitted while busy, got %d", n)
	}
	// One drainer delivers B and C, each only after the previous turn finished.
	n, err := e.r.Drain(e.target.Session, 10*time.Second, nil)
	if err != nil || n != 2 {
		t.Fatalf("drain: delivered %d, err %v", n, err)
	}
	waitFor(t, "3 submits", func() bool { return len(submits(t, e.log)) == 3 })
	got := submits(t, e.log)
	for i, name := range []string{"A", "B", "C"} {
		if got[i].Text != "Prompt "+name {
			t.Fatalf("order broken: %+v", got)
		}
	}
	if n, _ := e.r.Drain(e.target.Session, time.Second, nil); n != 0 {
		t.Fatal("empty queue must not deliver")
	}
}

func TestOnlyOneDrainerPerSession(t *testing.T) {
	e := setup(t, 200)
	release, ok, err := stateTryLock(e.target.Session)
	if err != nil || !ok {
		t.Fatalf("lock: %v %v", ok, err)
	}
	defer release()
	e.r.Start(e.target)
	e.r.Cfg.Queue.MaxItems = 5
	// Make it busy so the next prompt is queued, then try draining while "another drainer" holds the lock.
	e.r.Send(e.target, prompt.Prompt{Instruction: "one"}, "1")
	e.r.Send(e.target, prompt.Prompt{Instruction: "two"}, "2")
	if n, err := e.r.Drain(e.target.Session, 3*time.Second, nil); n != 0 || err != nil {
		t.Fatalf("second drainer must back off, delivered %d err %v", n, err)
	}
}

func TestQueueLimit(t *testing.T) {
	e := setup(t, 3000)
	e.r.Cfg.Queue.MaxItems = 1
	e.r.Start(e.target)
	e.r.Send(e.target, prompt.Prompt{Instruction: "first"}, "1")
	if out, err := e.r.Send(e.target, prompt.Prompt{Instruction: "second"}, "2"); err != nil || out.Result != "queued" {
		t.Fatalf("%+v %v", out, err)
	}
	if _, err := e.r.Send(e.target, prompt.Prompt{Instruction: "third"}, "3"); err == nil || !strings.Contains(err.Error(), "queue is full") {
		t.Fatalf("want queue full, got %v", err)
	}
}

func TestTextInInputBoxBlocksDelivery(t *testing.T) {
	e := setup(t, 200)
	st, err := e.r.Start(e.target)
	if err != nil {
		t.Fatal(err)
	}
	e.r.T.SendLiteral(st.Pane, "half typed by the user")
	waitFor(t, "typed text visible", func() bool { return e.r.Status(e.target.Session).State == Attention })
	out, err := e.r.Send(e.target, prompt.Prompt{Instruction: "relay prompt"}, "x")
	if err != nil || out.Result != "queued" {
		t.Fatalf("want queued, got %+v %v", out, err)
	}
	time.Sleep(300 * time.Millisecond)
	if n := len(submits(t, e.log)); n != 0 {
		t.Fatalf("nothing may be submitted, got %d", n)
	}
	// The user finishes typing and submits; once idle again the queued prompt goes out.
	e.r.T.SendKey(st.Pane, "Enter")
	n, err := e.r.Drain(e.target.Session, 5*time.Second, nil)
	if err != nil || n != 1 {
		t.Fatalf("drain: delivered %d, err %v", n, err)
	}
	waitFor(t, "2 submits", func() bool { return len(submits(t, e.log)) == 2 })
	if got := submits(t, e.log); got[0].Text != "half typed by the user" || got[1].Text != "relay prompt" {
		t.Fatalf("got %+v", got)
	}
}

func TestRefusesSessionsItDidNotStart(t *testing.T) {
	e := setup(t, 200)
	if _, err := e.r.T.NewSession(e.target.Session, e.target.Dir, nil, []string{fakeBin}); err != nil {
		t.Fatal(err)
	}
	if st := e.r.Status(e.target.Session); st.State != Unknown {
		t.Fatalf("want unknown, got %+v", st)
	}
	if _, err := e.r.Send(e.target, prompt.Prompt{Instruction: "x"}, "x"); err == nil {
		t.Fatal("must refuse a session the relay did not start")
	}
}

func TestShellPaneIsNotClaude(t *testing.T) {
	e := setup(t, 200)
	pane, err := e.r.T.NewSession(e.target.Session, e.target.Dir, nil, []string{"sh"})
	if err != nil {
		t.Fatal(err)
	}
	e.r.T.SetOption(e.target.Session, optPane, pane)
	if st := e.r.Status(e.target.Session); st.State != Unknown {
		t.Fatalf("want unknown for a shell pane, got %+v", st)
	}
}

func TestInstructionOnlyPromptIsOneSubmitWithoutPaste(t *testing.T) {
	e := setup(t, 200)
	p := prompt.Prompt{Instruction: "Review main.go (whole file) (Go) for correctness."}
	if out, err := e.r.Send(e.target, p, "review"); err != nil || out.Result != "sent" {
		t.Fatalf("%+v %v", out, err)
	}
	waitFor(t, "one submit", func() bool { return len(submits(t, e.log)) >= 1 })
	time.Sleep(300 * time.Millisecond)
	if got := submits(t, e.log); len(got) != 1 || got[0].Text != p.Instruction {
		t.Fatalf("got %+v", got)
	}
}

func TestStaleCopyModeIsLeftAndPromptDeliveredOnce(t *testing.T) {
	e := setup(t, 200)
	st, err := e.r.Start(e.target)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := exec.Command("tmux", append(e.r.T.BaseArgs(), "copy-mode", "-t", st.Pane)...).CombinedOutput(); err != nil {
		t.Fatal(err)
	}
	if s := e.r.Status(e.target.Session); s.State != Attention || !strings.Contains(s.Reason, "copy-mode") {
		t.Fatalf("a scrolled pane must not look idle: %+v", s)
	}
	p := prompt.Prompt{Instruction: "Explain the selected code that follows", Body: "\nfunc f() {}"}
	if out, err := e.r.Send(e.target, p, "x"); err != nil || out.Result != "sent" {
		t.Fatalf("detached scrolled session must be un-scrolled and sent: %+v %v", out, err)
	}
	waitFor(t, "one submit", func() bool { return len(submits(t, e.log)) >= 1 })
	time.Sleep(300 * time.Millisecond)
	if got := submits(t, e.log); len(got) != 1 || got[0].Text != p.Instruction+p.Body {
		t.Fatalf("got %+v", got)
	}
}
