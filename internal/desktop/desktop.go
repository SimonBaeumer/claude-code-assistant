// Package desktop holds the KDE/X11 glue: notifications, the question dialog and the viewer window.
package desktop

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	"github.com/SimonBaeumer/claude-code-assistent/internal/config"
	"github.com/SimonBaeumer/claude-code-assistent/internal/state"
	"github.com/SimonBaeumer/claude-code-assistent/internal/tmux"
)

// Notify shows a short desktop notification. Never pass prompt or code content here:
// notification daemons keep a history.
func Notify(enabled bool, title, body string) {
	fmt.Fprintf(os.Stderr, "claude-relay: %s: %s\n", title, body)
	if enabled {
		_ = exec.Command("notify-send", "-a", "claude-relay", "-t", "4000", title, body).Run()
	}
}

// ErrCancelled means the user closed the question dialog.
var ErrCancelled = errors.New("cancelled")

// Ask shows a single-line input dialog (kdialog, falling back to zenity).
func Ask(label string) (string, error) {
	var cmd *exec.Cmd
	if _, err := exec.LookPath("kdialog"); err == nil {
		cmd = exec.Command("kdialog", "--title", "claude-relay", "--inputbox", label)
	} else if _, err := exec.LookPath("zenity"); err == nil {
		cmd = exec.Command("zenity", "--entry", "--title", "claude-relay", "--text", label)
	} else {
		return "", errors.New("neither kdialog nor zenity is installed")
	}
	out, err := cmd.Output()
	if err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			return "", ErrCancelled
		}
		return "", err
	}
	q := strings.TrimSpace(string(out))
	if q == "" {
		return "", ErrCancelled
	}
	return q, nil
}

// Viewer is the single terminal window that shows whichever session got the last request.
type Viewer struct {
	Cfg config.Viewer
	T   tmux.Tmux
}

func pidFile() (string, error) {
	rt, err := state.RuntimeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(rt, "viewer.pid"), nil
}

// Show switches the viewer to session, opening a viewer window if there is none.
func (v Viewer) Show(session string) error {
	pf, err := pidFile()
	if err != nil {
		return err
	}
	pid := 0
	if data, err := os.ReadFile(pf); err == nil {
		pid, _ = strconv.Atoi(strings.TrimSpace(string(data)))
	}
	clients := v.T.ListClients()
	if pid > 0 && alive(pid) {
		for _, c := range clients {
			if descendantOf(c.PID, pid) {
				if c.Session != session {
					if err := v.T.SwitchClient(c.TTY, session); err != nil {
						return err
					}
				}
				if v.Cfg.Focus {
					focus(pid)
				}
				return nil
			}
		}
	}
	for _, c := range clients {
		if c.Session == session {
			return nil // already visible in a terminal the user attached themselves
		}
	}
	if !v.Cfg.OpenIfMissing {
		return nil
	}
	argv := append(append([]string{}, v.Cfg.Terminal...), "tmux")
	argv = append(argv, v.T.BaseArgs()...)
	argv = append(argv, "attach-session", "-t", "="+session)
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("opening viewer (%s): %w", argv[0], err)
	}
	_ = os.WriteFile(pf, []byte(strconv.Itoa(cmd.Process.Pid)), 0o600)
	return cmd.Process.Release()
}

func alive(pid int) bool { return syscall.Kill(pid, 0) == nil }

func ppid(pid int) int {
	data, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return 0
	}
	s := string(data)
	i := strings.LastIndexByte(s, ')')
	if i < 0 {
		return 0
	}
	f := strings.Fields(s[i+1:])
	if len(f) < 2 {
		return 0
	}
	p, _ := strconv.Atoi(f[1])
	return p
}

func descendantOf(pid, ancestor int) bool {
	for i := 0; i < 16 && pid > 1; i++ {
		if pid == ancestor {
			return true
		}
		pid = ppid(pid)
	}
	return false
}

func focus(pid int) {
	out, err := exec.Command("xdotool", "search", "--pid", strconv.Itoa(pid)).Output()
	if err != nil {
		return
	}
	ids := strings.Fields(string(out))
	if len(ids) == 0 {
		return
	}
	_ = exec.Command("xdotool", "windowactivate", ids[len(ids)-1]).Run()
}
