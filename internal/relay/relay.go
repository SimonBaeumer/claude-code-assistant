// Package relay is the core: session state, starting sessions, delivery, queueing and hooks.
package relay

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/SimonBaeumer/claude-code-assistent/internal/config"
	"github.com/SimonBaeumer/claude-code-assistent/internal/prompt"
	"github.com/SimonBaeumer/claude-code-assistent/internal/queue"
	"github.com/SimonBaeumer/claude-code-assistent/internal/route"
	"github.com/SimonBaeumer/claude-code-assistent/internal/screen"
	"github.com/SimonBaeumer/claude-code-assistent/internal/state"
	"github.com/SimonBaeumer/claude-code-assistent/internal/tmux"
)

// SessionEnv tells the hook which relay session it runs in.
const SessionEnv = "CLAUDE_RELAY_SESSION"

const (
	optPane = "@claude_relay_pane"
	optDir  = "@claude_relay_dir"
)

type State string

const (
	Missing   State = "missing"
	Idle      State = "idle"
	Busy      State = "busy"
	Attention State = "needs-attention"
	Unknown   State = "unknown"
)

type Status struct {
	State  State
	Reason string
	Pane   string
}

type Relay struct {
	Cfg *config.Config
	T   tmux.Tmux
	Exe string // absolute path of the claude-relay binary, used in hooks

	// Grace is how long a hook-reported "busy" overrides an idle-looking screen, covering the
	// moment between Enter and Claude drawing its spinner. Default 3s.
	Grace time.Duration
	// Poll is the screen polling interval. Default 100ms.
	Poll time.Duration
}

var ErrUnconfirmed = errors.New("submitted, but Claude did not visibly pick it up; check the viewer (it will NOT be resent)")

var shells = map[string]bool{"bash": true, "zsh": true, "sh": true, "fish": true, "dash": true}

func (r *Relay) grace() time.Duration {
	if r.Grace > 0 {
		return r.Grace
	}
	return 3 * time.Second
}

func (r *Relay) poll() time.Duration {
	if r.Poll > 0 {
		return r.Poll
	}
	return 100 * time.Millisecond
}

// Status combines the screen (authoritative for "can I type now?") with hook state
// (covers the instant right after a submit).
func (r *Relay) Status(session string) Status {
	if !r.T.HasSession(session) {
		return Status{State: Missing, Reason: "no tmux session " + session}
	}
	pane := r.T.Option(session, optPane)
	if pane == "" {
		return Status{State: Unknown, Reason: "tmux session " + session + " was not started by claude-relay"}
	}
	st := Status{Pane: pane}
	ps, err := r.T.PaneInfo(pane)
	switch {
	case err != nil:
		st.State, st.Reason = Unknown, err.Error()
		return st
	case ps.Dead:
		st.State, st.Reason = Unknown, "Claude has exited in "+session
		return st
	case shells[ps.Command]:
		st.State, st.Reason = Unknown, "pane runs a shell ("+ps.Command+"), not Claude"
		return st
	case ps.InMode:
		// The capture would show the scrolled-back view, not Claude's live screen, and typed
		// keys would run copy-mode commands. Never read or type until the mode is left.
		st.State, st.Reason = Attention, "the viewer is scrolled back (tmux copy-mode), press q in it to continue"
		return st
	}
	raw, err := r.T.Capture(pane)
	if err != nil {
		st.State, st.Reason = Unknown, err.Error()
		return st
	}
	sc := screen.Parse(raw)
	switch sc.Kind {
	case screen.Busy:
		st.State, st.Reason = Busy, "Claude is working"
		return st
	case screen.Permission:
		st.State, st.Reason = Attention, "Claude is asking something (permission or trust dialog), answer it in the viewer"
		return st
	case screen.Unknown:
		st.State, st.Reason = Unknown, "unrecognised screen (a dialog or menu?)"
		return st
	}
	if !sc.InputEmpty {
		st.State, st.Reason = Attention, "the input box already has text in it"
		return st
	}
	if hs, ok := state.ReadHookState(session); ok && hs.State == state.HookBusy && time.Since(hs.At) < r.grace() {
		st.State, st.Reason = Busy, "a prompt was just submitted"
		return st
	}
	st.State = Idle
	return st
}

// leaveStaleMode takes the pane out of copy-mode when nobody is looking at it. The viewer
// switching away from a scrolled session leaves it in copy-mode; nobody will press q there.
// A session that is on screen is left alone: the user may be reading.
func (r *Relay) leaveStaleMode(session string) {
	pane := r.T.Option(session, optPane)
	if pane == "" {
		return
	}
	if ps, err := r.T.PaneInfo(pane); err == nil && ps.InMode && !ps.Attached {
		_ = r.T.ExitMode(pane)
	}
}

// Start launches Claude for the target in a new detached tmux session (if none exists)
// and waits until it is ready or the startup timeout passes.
func (r *Relay) Start(t route.Target) (Status, error) {
	if r.T.HasSession(t.Session) {
		return r.Status(t.Session), nil
	}
	if t.Kind == route.KindGeneral {
		if err := os.MkdirAll(t.Dir, 0o755); err != nil {
			return Status{}, err
		}
	}
	if fi, err := os.Stat(t.Dir); err != nil || !fi.IsDir() {
		return Status{}, fmt.Errorf("session directory %s does not exist", t.Dir)
	}
	hooks, err := r.WriteHooks()
	if err != nil {
		return Status{}, fmt.Errorf("writing hooks: %w", err)
	}
	command, err := exec.LookPath(r.Cfg.Claude.Command)
	if err != nil {
		return Status{}, fmt.Errorf("claude command %q not found in PATH (%s); set claude.command to its absolute path in the config: %w", r.Cfg.Claude.Command, os.Getenv("PATH"), err)
	}
	argv := append([]string{command}, r.Cfg.Claude.Args...)
	argv = append(argv, "--settings", hooks)
	if r.Cfg.Claude.Resume {
		if c, ok := state.ReadConversations()[t.Session]; ok && c.ID != "" && c.Dir == t.Dir && fileExists(c.Transcript) {
			argv = append(argv, "--resume", c.ID)
		}
	}
	pane, err := r.T.NewSession(t.Session, t.Dir, map[string]string{SessionEnv: t.Session, "PATH": os.Getenv("PATH")}, argv)
	if err != nil {
		return Status{}, err
	}
	if err := r.T.SetOption(t.Session, optPane, pane); err != nil {
		return Status{}, err
	}
	_ = r.T.SetOption(t.Session, optDir, t.Dir)

	deadline := time.Now().Add(time.Duration(r.Cfg.Claude.StartupTimeoutMs) * time.Millisecond)
	var st Status
	for {
		st = r.Status(t.Session)
		if st.State == Idle || st.State == Attention {
			return st, nil // Attention: e.g. the trust dialog of a new repo, the user answers it
		}
		if st.State == Missing {
			return st, fmt.Errorf("Claude exited right after starting in %s", t.Dir)
		}
		if time.Now().After(deadline) {
			return st, fmt.Errorf("session %s did not become ready: %s", t.Session, st.Reason)
		}
		time.Sleep(4 * r.poll())
	}
}

// WriteHooks writes the per-session Claude settings file that wires hooks back to the relay.
func (r *Relay) WriteHooks() (string, error) {
	rt, err := state.RuntimeDir()
	if err != nil {
		return "", err
	}
	entry := []any{map[string]any{"hooks": []any{map[string]any{
		"type": "command", "command": shellQuote(r.Exe) + " hook", "timeout": 10,
	}}}}
	settings := map[string]any{"hooks": map[string]any{
		"SessionStart": entry, "UserPromptSubmit": entry, "Stop": entry, "Notification": entry,
	}}
	data, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return "", err
	}
	p := filepath.Join(rt, "hooks.json")
	return p, os.WriteFile(p, data, 0o600)
}

// Outcome describes what Send did.
type Outcome struct {
	Result   string // "sent", "queued"
	Position int    // queue position when queued
	Reason   string // why it was queued
	Started  bool   // a new session was started for this request
}

// DrainWait bounds how long a background drainer waits for a session to become idle.
const DrainWait = 15 * time.Minute

// Send delivers the prompt now if the session is idle, otherwise queues it.
func (r *Relay) Send(t route.Target, p prompt.Prompt, label string) (Outcome, error) {
	var out Outcome
	if r.Status(t.Session).State == Missing {
		if _, err := r.Start(t); err != nil {
			return out, err
		}
		out.Started = true
	}

	unlock, err := state.Lock(t.Session)
	if err != nil {
		return out, err
	}
	defer unlock()

	r.leaveStaleMode(t.Session)
	st := r.Status(t.Session)
	sd, err := state.SessionDir(t.Session)
	if err != nil {
		return out, err
	}
	q := queue.New(sd)
	item := queue.Item{Prompt: p, Label: label}

	switch st.State {
	case Idle:
		if q.Len() == 0 {
			if _, err := r.deliver(st.Pane, t.Session, p); err != nil {
				return out, err
			}
			out.Result = "sent"
			return out, nil
		}
		// Older items first: queue this one behind them and deliver the oldest.
		pos, err := q.Push(item, r.Cfg.Queue.MaxItems)
		if err != nil {
			return out, r.queueErr(err)
		}
		if _, err := r.drainOneLocked(q, st.Pane, t.Session); err != nil {
			return out, err
		}
		out.Result, out.Position, out.Reason = "queued", pos-1, "older queued requests go first"
		return out, nil
	case Busy, Attention:
		if r.Cfg.Queue.MaxItems == 0 {
			return out, fmt.Errorf("not sent: %s (queueing is disabled)", st.Reason)
		}
		pos, err := q.Push(item, r.Cfg.Queue.MaxItems)
		if err != nil {
			return out, r.queueErr(err)
		}
		out.Result, out.Position, out.Reason = "queued", pos, st.Reason
		// The Stop hook drains too, but a drainer also covers turns that end without Stop
		// (Esc interrupt) and dialogs answered by hand (trust, permission).
		if r.Exe != "" {
			_ = r.spawnDrain(t.Session)
		}
		return out, nil
	default:
		return out, fmt.Errorf("not sent: %s", st.Reason)
	}
}

func (r *Relay) queueErr(err error) error {
	if errors.Is(err, queue.ErrFull) {
		return fmt.Errorf("not sent: queue is full (%d items); run `claude-relay queue clear`", r.Cfg.Queue.MaxItems)
	}
	return err
}

// Drain delivers queued prompts in FIFO order, one per idle period, until the queue is empty
// or wait passes. Only one drainer runs per session; a second call returns at once.
// onDeliver (optional) is called after each delivered prompt.
func (r *Relay) Drain(session string, wait time.Duration, onDeliver func(left int)) (int, error) {
	sd, err := state.SessionDir(session)
	if err != nil {
		return 0, err
	}
	q := queue.New(sd)
	deadline := time.Now().Add(wait)
	delivered := 0
	for q.Len() > 0 && time.Now().Before(deadline) {
		release, ok, err := state.TryLock(session, "drainer")
		if err != nil || !ok {
			return delivered, err // another drainer owns this session
		}
		n, err := r.drainLoop(q, session, deadline, onDeliver)
		release()
		delivered += n
		if err != nil {
			return delivered, err
		}
		// Re-check after releasing: an item pushed while we were exiting must not be stranded.
	}
	return delivered, nil
}

func (r *Relay) drainLoop(q queue.Queue, session string, deadline time.Time, onDeliver func(int)) (int, error) {
	delivered := 0
	for q.Len() > 0 && time.Now().Before(deadline) {
		unlock, err := state.Lock(session)
		if err != nil {
			return delivered, err
		}
		r.leaveStaleMode(session)
		st := r.Status(session)
		if st.State == Idle {
			ok, err := r.drainOneLocked(q, st.Pane, session)
			unlock()
			if err != nil {
				return delivered, err
			}
			if ok {
				delivered++
				if onDeliver != nil {
					onDeliver(q.Len())
				}
			}
			continue
		}
		unlock()
		if st.State == Missing {
			return delivered, fmt.Errorf("cannot drain: %s", st.Reason)
		}
		// Busy, Attention, or a transient Unknown: keep waiting.
		time.Sleep(5 * r.poll())
	}
	return delivered, nil
}

func (r *Relay) drainOneLocked(q queue.Queue, pane, session string) (bool, error) {
	c, err := q.Claim()
	if err != nil || c == nil {
		return false, err
	}
	submitted, err := r.deliver(pane, session, c.Prompt)
	if submitted {
		_ = c.Done()
	} else {
		_ = c.Release()
	}
	return submitted, err
}

// deliver types the instruction, pastes the body and presses Enter exactly once.
// Caller holds the session lock and has seen the session idle.
// submitted reports whether Enter was sent; when true the prompt must never be retried.
func (r *Relay) deliver(pane, session string, p prompt.Prompt) (submitted bool, err error) {
	instr := prompt.SanitizeLine(p.Instruction)
	body := prompt.SanitizeBody(p.Body)
	if instr == "" {
		return false, errors.New("empty instruction")
	}
	buf := "cr-" + randHex(6)
	if body != "" {
		if err := r.T.LoadBuffer(buf, body); err != nil {
			return false, err
		}
		defer r.T.DeleteBuffer(buf) // no-op after a successful paste -d
	}
	// The user may have entered copy-mode since the status check; keys would then run copy-mode
	// commands instead of reaching Claude.
	if ps, err := r.T.PaneInfo(pane); err != nil || ps.InMode {
		if err == nil {
			err = errors.New("the viewer was scrolled back (tmux copy-mode) right before typing")
		}
		return false, err
	}
	if err := r.T.SendLiteral(pane, instr); err != nil {
		return false, fmt.Errorf("typing into Claude failed, check its input box: %w", err)
	}
	if body != "" {
		if err := r.T.PasteBuffer(buf, pane); err != nil {
			return false, fmt.Errorf("pasting into Claude failed, check its input box: %w", err)
		}
	}
	if err := r.waitInputSettled(pane, 3*time.Second); err != nil {
		return false, err
	}
	if err := r.T.SendKey(pane, "Enter"); err != nil {
		return false, fmt.Errorf("pressing Enter failed, the prompt is still in the input box: %w", err)
	}
	_ = state.WriteHookState(session, state.HookState{State: state.HookBusy, Event: "relay-submit", At: time.Now()})

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		time.Sleep(r.poll())
		raw, err := r.T.Capture(pane)
		if err != nil {
			continue
		}
		sc := screen.Parse(raw)
		if sc.Kind == screen.Busy || (sc.Kind == screen.Idle && sc.InputEmpty) {
			return true, nil
		}
	}
	return true, ErrUnconfirmed
}

// waitInputSettled waits until the input box shows content that stopped changing,
// so Enter never races a paste that is still being processed.
func (r *Relay) waitInputSettled(pane string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	prev := ""
	for time.Now().Before(deadline) {
		time.Sleep(r.poll())
		raw, err := r.T.Capture(pane)
		if err != nil {
			continue
		}
		sc := screen.Parse(raw)
		if sc.Kind == screen.Idle && !sc.InputEmpty {
			if raw == prev {
				return nil
			}
			prev = raw
		}
	}
	return errors.New("the prompt did not show up in Claude's input box; nothing was submitted, check the viewer")
}

// hookEvent is the subset of Claude Code's hook payload the relay reads.
type hookEvent struct {
	Event            string `json:"hook_event_name"`
	SessionID        string `json:"session_id"`
	Transcript       string `json:"transcript_path"`
	Cwd              string `json:"cwd"`
	NotificationType string `json:"notification_type"`
}

// Hook handles one Claude Code hook invocation. It must be fast, print nothing to stdout
// (UserPromptSubmit stdout would be added to Claude's context) and never fail Claude.
func (r *Relay) Hook(in io.Reader) error {
	session := os.Getenv(SessionEnv)
	if session == "" {
		return nil
	}
	var ev hookEvent
	if err := json.NewDecoder(io.LimitReader(in, 1<<20)).Decode(&ev); err != nil {
		return err
	}
	set := func(s string) error {
		return state.WriteHookState(session, state.HookState{State: s, Event: ev.Event, At: time.Now()})
	}
	switch ev.Event {
	case "SessionStart":
		if ev.SessionID != "" {
			_ = state.SaveConversation(session, state.Conversation{ID: ev.SessionID, Transcript: ev.Transcript, Dir: ev.Cwd, Updated: time.Now()})
		}
		return set(state.HookIdle)
	case "UserPromptSubmit":
		return set(state.HookBusy)
	case "Notification":
		if ev.NotificationType == "permission_prompt" {
			return set(state.HookAttention)
		}
	case "Stop":
		if err := set(state.HookIdle); err != nil {
			return err
		}
		if sd, err := state.SessionDir(session); err == nil && queue.New(sd).Len() > 0 {
			return r.spawnDrain(session)
		}
	}
	return nil
}

// spawnDrain starts a detached `claude-relay drain`. It must not run inside the hook itself:
// Claude only redraws its idle screen after the hook returns.
func (r *Relay) spawnDrain(session string) error {
	cmd := exec.Command(r.Exe, "drain", "--wait", DrainWait.String(), session)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return err
	}
	return cmd.Process.Release()
}

// Sessions lists the tmux sessions started by the relay.
func (r *Relay) Sessions() []string {
	var out []string
	for _, s := range r.T.ListSessions() {
		if strings.HasPrefix(s, route.Prefix) && r.T.Option(s, optPane) != "" {
			out = append(out, s)
		}
	}
	return out
}

// SessionDir is the working directory recorded for a relay session.
func (r *Relay) SessionDir(session string) string { return r.T.Option(session, optDir) }

func shellQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

func fileExists(p string) bool {
	if p == "" {
		return false
	}
	_, err := os.Stat(p)
	return err == nil
}

func randHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
