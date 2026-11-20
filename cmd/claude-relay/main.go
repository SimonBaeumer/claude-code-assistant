// claude-relay sends a code selection from a JetBrains IDE into a persistent interactive
// Claude Code session running in tmux, picked by the file's git repo.
package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/SimonBaeumer/claude-code-assistent/internal/config"
	"github.com/SimonBaeumer/claude-code-assistent/internal/desktop"
	"github.com/SimonBaeumer/claude-code-assistent/internal/envpath"
	"github.com/SimonBaeumer/claude-code-assistent/internal/jetbrains"
	"github.com/SimonBaeumer/claude-code-assistent/internal/prompt"
	"github.com/SimonBaeumer/claude-code-assistent/internal/queue"
	"github.com/SimonBaeumer/claude-code-assistent/internal/relay"
	"github.com/SimonBaeumer/claude-code-assistent/internal/route"
	"github.com/SimonBaeumer/claude-code-assistent/internal/state"
	"github.com/SimonBaeumer/claude-code-assistent/internal/tmux"
)

const usage = `claude-relay: send IDE selections into persistent Claude Code sessions

Usage:
  claude-relay send [--action explain|ask|...] [--general] --file F [--start N --end M] [--project P]
  claude-relay send --text "..." | --stdin  [--general] [--file F]
  claude-relay start [--general] [dir]       start (or reuse) the session for dir
  claude-relay view  [--general] [dir]       show that session in the viewer window
  claude-relay status                        list relay sessions, their state and queues
  claude-relay queue list|clear <session>    inspect or discard queued requests
  claude-relay stop <session>                kill a session (ends that Claude process)
  claude-relay install-jetbrains             add External Tools to every JetBrains IDE
  claude-relay config path|init|check        manage the config file
  claude-relay hook | drain <session>        internal: called by Claude Code hooks
`

func main() {
	if len(os.Args) < 2 {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
	envpath.Fix() // IDEs started from the desktop menu lack ~/.local/bin, where claude usually lives
	cmd, args := os.Args[1], os.Args[2:]
	var code int
	switch cmd {
	case "send":
		code = cmdSend(args)
	case "start":
		code = cmdStart(args)
	case "view":
		code = cmdView(args)
	case "status":
		code = cmdStatus()
	case "queue":
		code = cmdQueue(args)
	case "stop":
		code = cmdStop(args)
	case "drain":
		code = cmdDrain(args)
	case "hook":
		code = cmdHook()
	case "install-jetbrains":
		code = cmdInstallJetBrains()
	case "config":
		code = cmdConfig(args)
	case "-h", "--help", "help":
		fmt.Print(usage)
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n%s", cmd, usage)
		code = 2
	}
	os.Exit(code)
}

func exePath() string {
	exe, err := os.Executable()
	if err != nil {
		return "claude-relay"
	}
	if r, err := filepath.EvalSymlinks(exe); err == nil {
		return r
	}
	return exe
}

func load() (*config.Config, *relay.Relay, error) {
	cfg, err := config.Load(config.Path())
	if err != nil {
		return nil, nil, err
	}
	return cfg, &relay.Relay{Cfg: cfg, T: tmux.Tmux{}, Exe: exePath()}, nil
}

func fail(cfg *config.Config, title string, err error) int {
	notify := cfg == nil || cfg.Notify
	msg := err.Error()
	if i := strings.IndexByte(msg, '\n'); i > 0 && notify {
		fmt.Fprintln(os.Stderr, msg) // full multi-line error on stderr, first line in the bubble
		msg = msg[:i]
	}
	desktop.Notify(notify, title, msg)
	return 1
}

func cmdSend(args []string) int {
	fs := flag.NewFlagSet("send", flag.ContinueOnError)
	action := fs.String("action", "explain", "action (prompt template) name")
	general := fs.Bool("general", false, "send to the general session")
	file := fs.String("file", "", "source file")
	start := fs.String("start", "", "first selected line (1-based)")
	end := fs.String("end", "", "last selected line")
	line := fs.String("line", "", "caret line, used when there is no selection")
	project := fs.String("project", "", "project root (routing fallback)")
	text := fs.String("text", "", "selection text instead of reading --file")
	fromStdin := fs.Bool("stdin", false, "read the selection from stdin")
	question := fs.String("question", "", "question for ask actions (skips the dialog)")
	noView := fs.Bool("no-view", false, "do not switch or open the viewer")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	cfg, rl, err := load()
	if err != nil {
		return fail(nil, "claude-relay config error", err)
	}
	act, ok := cfg.Actions[*action]
	if !ok {
		return fail(cfg, "Unknown action", fmt.Errorf("no action %q in config", *action))
	}

	absFile := ""
	if *file != "" {
		absFile, _ = filepath.Abs(*file)
	}
	var sel, lines string
	switch {
	case *text != "":
		sel = *text
	case *fromStdin:
		data, err := io.ReadAll(io.LimitReader(os.Stdin, int64(cfg.Limits.MaxChars)*4+1))
		if err != nil {
			return fail(cfg, "Reading stdin failed", err)
		}
		sel = string(data)
	case absFile != "":
		s, e := atoi(*start), atoi(*end)
		if s == 0 {
			s = atoi(*line)
			e = s
		}
		sel, err = prompt.ReadLines(absFile, s, e)
		if err != nil {
			return fail(cfg, "No selection", err)
		}
		if e > s {
			lines = fmt.Sprintf("%d-%d", s, e)
		} else {
			lines = strconv.Itoa(s)
		}
	}
	if strings.TrimSpace(sel) == "" {
		return fail(cfg, "No selection", errors.New("select some code first"))
	}

	q := *question
	if act.Ask && q == "" {
		q, err = desktop.Ask("Question about the selection:")
		if errors.Is(err, desktop.ErrCancelled) {
			return 0
		}
		if err != nil {
			return fail(cfg, "Question dialog failed", err)
		}
	}

	target := route.Resolve(cfg, absFile, *project, *general)
	p, err := prompt.Render(act, prompt.Input{
		File: absFile, RouteDir: target.Dir, Project: *project, Lines: lines, Selection: sel, Question: q,
	})
	if err != nil {
		return fail(cfg, "Prompt template error", err)
	}
	if n := p.Len(); n > cfg.Limits.MaxChars {
		return fail(cfg, "Selection too large", fmt.Errorf("%d characters, the limit is %d (limits.max_chars)", n, cfg.Limits.MaxChars))
	}

	label := *action
	if absFile != "" {
		label += " " + filepath.Base(absFile)
		if lines != "" {
			label += ":" + lines
		}
	}
	out, err := rl.Send(target, p, label)
	if !*noView && rl.Status(target.Session).State != relay.Missing {
		if verr := (desktop.Viewer{Cfg: cfg.Viewer, T: rl.T}).Show(target.Session); verr != nil {
			desktop.Notify(cfg.Notify, "Viewer failed", verr.Error())
		}
	}
	if err != nil {
		return fail(cfg, "Not sent to "+target.Session, err)
	}
	switch out.Result {
	case "sent":
		title := "Sent to " + target.Session
		if out.Started {
			title = "Started and sent to " + target.Session
		}
		desktop.Notify(cfg.Notify, title, label)
	case "queued":
		desktop.Notify(cfg.Notify, fmt.Sprintf("Queued (#%d) for %s", out.Position, target.Session), out.Reason)
	}
	return 0
}

func targetFromArgs(cfg *config.Config, fs *flag.FlagSet, general bool) route.Target {
	if general {
		return route.General(cfg)
	}
	dir := fs.Arg(0)
	if dir == "" {
		dir, _ = os.Getwd()
	}
	return route.Resolve(cfg, dir, "", false)
}

func cmdStart(args []string) int {
	fs := flag.NewFlagSet("start", flag.ContinueOnError)
	general := fs.Bool("general", false, "start the general session")
	noView := fs.Bool("no-view", false, "do not open the viewer")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	cfg, rl, err := load()
	if err != nil {
		return fail(nil, "claude-relay config error", err)
	}
	t := targetFromArgs(cfg, fs, *general)
	st, err := rl.Start(t)
	fmt.Printf("%s (%s) in %s: %s %s\n", t.Session, t.Kind, t.Dir, st.State, st.Reason)
	if !*noView && rl.Status(t.Session).State != relay.Missing {
		if verr := (desktop.Viewer{Cfg: cfg.Viewer, T: rl.T}).Show(t.Session); verr != nil {
			fmt.Fprintln(os.Stderr, "viewer:", verr)
		}
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	return 0
}

func cmdView(args []string) int {
	fs := flag.NewFlagSet("view", flag.ContinueOnError)
	general := fs.Bool("general", false, "show the general session")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	cfg, rl, err := load()
	if err != nil {
		return fail(nil, "claude-relay config error", err)
	}
	t := targetFromArgs(cfg, fs, *general)
	if rl.Status(t.Session).State == relay.Missing {
		fmt.Fprintf(os.Stderr, "no session %s yet; run `claude-relay start`\n", t.Session)
		return 1
	}
	if err := (desktop.Viewer{Cfg: cfg.Viewer, T: rl.T}).Show(t.Session); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	return 0
}

func sessionQueue(session string) (queue.Queue, error) {
	sd, err := state.SessionDir(session)
	if err != nil {
		return queue.Queue{}, err
	}
	return queue.New(sd), nil
}

func cmdStatus() int {
	_, rl, err := load()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	sessions := rl.Sessions()
	if len(sessions) == 0 {
		fmt.Println("no relay sessions running")
		return 0
	}
	w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(w, "SESSION\tSTATE\tQUEUED\tDIR\tDETAIL")
	for _, s := range sessions {
		st := rl.Status(s)
		q, _ := sessionQueue(s)
		queued := strconv.Itoa(q.Len())
		if n := q.Inflight(); n > 0 {
			queued += fmt.Sprintf(" (+%d stuck in flight)", n)
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n", s, st.State, queued, rl.SessionDir(s), st.Reason)
	}
	w.Flush()
	return 0
}

func cmdQueue(args []string) int {
	if len(args) != 2 || (args[0] != "list" && args[0] != "clear") {
		fmt.Fprintln(os.Stderr, "usage: claude-relay queue list|clear <session>")
		return 2
	}
	q, err := sessionQueue(route.SessionName(args[1]))
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	if args[0] == "clear" {
		fmt.Printf("discarded %d item(s)\n", q.Clear())
		return 0
	}
	for i, it := range q.List() {
		fmt.Printf("%d. %s (queued %s ago, %d chars)\n", i+1, it.Label, time.Since(it.Created).Round(time.Second), it.Prompt.Len())
	}
	return 0
}

func cmdStop(args []string) int {
	if len(args) != 1 {
		fmt.Fprintln(os.Stderr, "usage: claude-relay stop <session>")
		return 2
	}
	_, rl, err := load()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	s := route.SessionName(args[0])
	if err := rl.T.KillSession(s); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	if q, err := sessionQueue(s); err == nil {
		q.Clear()
	}
	fmt.Println("stopped", s)
	return 0
}

func cmdDrain(args []string) int {
	fs := flag.NewFlagSet("drain", flag.ContinueOnError)
	wait := fs.Duration("wait", relay.DrainWait, "how long to wait for the session to become idle")
	if err := fs.Parse(args); err != nil || fs.NArg() != 1 {
		fmt.Fprintln(os.Stderr, "usage: claude-relay drain [--wait 15m] <session>")
		return 2
	}
	cfg, rl, err := load()
	if err != nil {
		return fail(nil, "claude-relay config error", err)
	}
	s := route.SessionName(fs.Arg(0))
	_, err = rl.Drain(s, *wait, func(left int) {
		desktop.Notify(cfg.Notify, "Sent queued request to "+s, fmt.Sprintf("%d still queued", left))
	})
	if err != nil {
		return fail(cfg, "Queued request not sent to "+s, err)
	}
	return 0
}

// cmdHook always exits 0: a failing hook must never disturb the Claude session.
func cmdHook() int {
	cfg, rl, err := load()
	if err != nil {
		cfg, _ = config.Load(os.DevNull)
		rl = &relay.Relay{Cfg: cfg, T: tmux.Tmux{}, Exe: exePath()}
	}
	if err := rl.Hook(os.Stdin); err != nil {
		fmt.Fprintln(os.Stderr, "claude-relay hook:", err)
	}
	return 0
}

func cmdInstallJetBrains() int {
	paths, err := jetbrains.Install(exePath())
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	for _, p := range paths {
		fmt.Println("wrote", p)
	}
	fmt.Println(`
Restart the IDE, then bind shortcuts in Settings > Keymap > External Tools > claude-relay
(for example Alt+E explain, Alt+Q ask, Alt+Shift+E / Alt+Shift+Q for the general session).`)
	return 0
}

func cmdConfig(args []string) int {
	sub := "path"
	if len(args) > 0 {
		sub = args[0]
	}
	p := config.Path()
	switch sub {
	case "path":
		fmt.Println(p)
	case "init":
		if _, err := os.Stat(p); err == nil {
			fmt.Fprintln(os.Stderr, "config already exists:", p)
			return 1
		}
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		if err := os.WriteFile(p, config.DefaultYAML, 0o644); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		fmt.Println("wrote", p)
	case "check":
		if _, err := config.Load(p); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		fmt.Println("ok:", p)
	default:
		fmt.Fprintln(os.Stderr, "usage: claude-relay config path|init|check")
		return 2
	}
	return 0
}

func atoi(s string) int {
	n, _ := strconv.Atoi(strings.TrimSpace(s))
	return n
}
