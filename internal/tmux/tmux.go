// Package tmux is a thin wrapper around the tmux CLI.
package tmux

import (
	"bytes"
	"fmt"
	"io"
	"os/exec"
	"strconv"
	"strings"
)

// Tmux talks to one tmux server. Socket and Config are empty for the user's default server;
// tests set them to get an isolated server.
type Tmux struct {
	Socket string // -L
	Config string // -f
}

// BaseArgs are the global flags that select the server, for building attach commands.
func (t Tmux) BaseArgs() []string {
	a := []string{"-u"} // force UTF-8 even without a locale (desktop-launched IDEs), or tmux mangles ❯ and tabs
	if t.Socket != "" {
		a = append(a, "-L", t.Socket)
	}
	if t.Config != "" {
		a = append(a, "-f", t.Config)
	}
	return a
}

func (t Tmux) run(stdin io.Reader, args ...string) (string, error) {
	c := exec.Command("tmux", append(t.BaseArgs(), args...)...)
	c.Stdin = stdin
	var out, errb bytes.Buffer
	c.Stdout, c.Stderr = &out, &errb
	if err := c.Run(); err != nil {
		return out.String(), fmt.Errorf("tmux %s: %v: %s", args[0], err, strings.TrimSpace(errb.String()))
	}
	return out.String(), nil
}

func (t Tmux) HasSession(name string) bool {
	_, err := t.run(nil, "has-session", "-t", "="+name)
	return err == nil
}

// NewSession starts a detached session running argv directly (no shell) and returns its pane id.
func (t Tmux) NewSession(name, dir string, env map[string]string, argv []string) (string, error) {
	args := []string{"new-session", "-d", "-s", name, "-c", dir, "-x", "200", "-y", "50", "-P", "-F", "#{pane_id}"}
	for k, v := range env {
		args = append(args, "-e", k+"="+v)
	}
	args = append(args, argv...)
	out, err := t.run(nil, args...)
	return strings.TrimSpace(out), err
}

func (t Tmux) KillSession(name string) error {
	_, err := t.run(nil, "kill-session", "-t", "="+name)
	return err
}

func (t Tmux) SetOption(session, key, value string) error {
	_, err := t.run(nil, "set-option", "-t", "="+session+":", key, value)
	return err
}

// Option returns a session option's value, or "" if unset.
func (t Tmux) Option(session, key string) string {
	out, err := t.run(nil, "show-options", "-q", "-v", "-t", "="+session+":", key)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(out)
}

// PaneInfo returns the pane's foreground command and whether it is dead.
func (t Tmux) PaneInfo(pane string) (cmd string, dead bool, err error) {
	out, err := t.run(nil, "display-message", "-p", "-t", pane, "#{pane_dead} #{pane_current_command}")
	if err != nil {
		return "", false, err
	}
	parts := strings.SplitN(strings.TrimSpace(out), " ", 2)
	if len(parts) != 2 {
		return "", false, fmt.Errorf("unexpected pane info %q", out)
	}
	return parts[1], parts[0] == "1", nil
}

// Capture returns the visible pane content including SGR attributes.
func (t Tmux) Capture(pane string) (string, error) {
	return t.run(nil, "capture-pane", "-p", "-e", "-t", pane)
}

// SendLiteral types text as literal keys (no key-name interpretation).
func (t Tmux) SendLiteral(pane, text string) error {
	_, err := t.run(nil, "send-keys", "-t", pane, "-l", "--", text)
	return err
}

func (t Tmux) SendKey(pane, key string) error {
	_, err := t.run(nil, "send-keys", "-t", pane, key)
	return err
}

// LoadBuffer fills a named paste buffer from memory (stdin), so nothing touches the disk.
func (t Tmux) LoadBuffer(name, data string) error {
	_, err := t.run(strings.NewReader(data), "load-buffer", "-b", name, "-")
	return err
}

// PasteBuffer pastes with bracketed-paste markers and deletes the buffer afterwards.
func (t Tmux) PasteBuffer(name, pane string) error {
	_, err := t.run(nil, "paste-buffer", "-p", "-d", "-b", name, "-t", pane)
	return err
}

func (t Tmux) DeleteBuffer(name string) {
	_, _ = t.run(nil, "delete-buffer", "-b", name)
}

func (t Tmux) ListSessions() []string {
	out, err := t.run(nil, "list-sessions", "-F", "#{session_name}")
	if err != nil {
		return nil
	}
	return strings.Fields(out)
}

type Client struct {
	TTY     string
	PID     int
	Session string
}

func (t Tmux) ListClients() []Client {
	out, err := t.run(nil, "list-clients", "-F", "#{client_tty}\t#{client_pid}\t#{client_session}")
	if err != nil {
		return nil
	}
	var cs []Client
	for _, l := range strings.Split(strings.TrimSpace(out), "\n") {
		p := strings.Split(l, "\t")
		if len(p) != 3 {
			continue
		}
		pid, _ := strconv.Atoi(p[1])
		cs = append(cs, Client{TTY: p[0], PID: pid, Session: p[2]})
	}
	return cs
}

func (t Tmux) SwitchClient(tty, session string) error {
	_, err := t.run(nil, "switch-client", "-c", tty, "-t", "="+session)
	return err
}
