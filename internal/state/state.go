// Package state holds the relay's on-machine state.
//
// Runtime state (hook state, queue, locks) lives under $XDG_RUNTIME_DIR, which is a tmpfs:
// prompts never reach the disk and vanish on reboot. The only persistent file maps a relay
// session to its Claude conversation id (no prompt content), so a lost session can resume.
package state

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

// RuntimeDir returns (and creates, mode 0700) the relay's runtime directory.
func RuntimeDir() (string, error) {
	base := os.Getenv("XDG_RUNTIME_DIR")
	var dir string
	if base != "" {
		dir = filepath.Join(base, "claude-relay")
	} else {
		dir = filepath.Join(os.TempDir(), fmt.Sprintf("claude-relay-%d", os.Getuid()))
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	return dir, nil
}

// SessionDir returns (and creates) the runtime directory of one session.
func SessionDir(session string) (string, error) {
	rt, err := RuntimeDir()
	if err != nil {
		return "", err
	}
	d := filepath.Join(rt, session)
	return d, os.MkdirAll(d, 0o700)
}

// Hook states as reported by Claude Code hooks.
const (
	HookIdle      = "idle"
	HookBusy      = "busy"
	HookAttention = "attention"
)

type HookState struct {
	State string    `json:"state"`
	Event string    `json:"event"`
	At    time.Time `json:"at"`
}

func WriteHookState(session string, hs HookState) error {
	d, err := SessionDir(session)
	if err != nil {
		return err
	}
	return writeJSON(filepath.Join(d, "hook-state.json"), hs)
}

func ReadHookState(session string) (HookState, bool) {
	var hs HookState
	d, err := SessionDir(session)
	if err != nil {
		return hs, false
	}
	data, err := os.ReadFile(filepath.Join(d, "hook-state.json"))
	if err != nil || json.Unmarshal(data, &hs) != nil {
		return hs, false
	}
	return hs, true
}

// Lock takes an exclusive per-session lock. Every check-then-deliver runs under it,
// so two shortcut presses can never paste into the same session at once.
func Lock(session string) (func(), error) {
	d, err := SessionDir(session)
	if err != nil {
		return nil, err
	}
	f, err := os.OpenFile(filepath.Join(d, "lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		f.Close()
		return nil, err
	}
	return func() {
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		f.Close()
	}, nil
}

// TryLock takes a named, non-blocking per-session lock. ok is false if someone else holds it.
func TryLock(session, name string) (release func(), ok bool, err error) {
	d, err := SessionDir(session)
	if err != nil {
		return nil, false, err
	}
	f, err := os.OpenFile(filepath.Join(d, name+".lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, false, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		if err == syscall.EWOULDBLOCK {
			return nil, false, nil
		}
		return nil, false, err
	}
	return func() {
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		f.Close()
	}, true, nil
}

// Conversation links a relay session to the Claude conversation running in it.
type Conversation struct {
	ID         string    `json:"id"`
	Transcript string    `json:"transcript"`
	Dir        string    `json:"dir"`
	Updated    time.Time `json:"updated"`
}

func conversationsPath() (string, error) {
	base := os.Getenv("XDG_STATE_HOME")
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		base = filepath.Join(home, ".local", "state")
	}
	dir := filepath.Join(base, "claude-relay")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	return filepath.Join(dir, "conversations.json"), nil
}

func ReadConversations() map[string]Conversation {
	m := map[string]Conversation{}
	p, err := conversationsPath()
	if err != nil {
		return m
	}
	if data, err := os.ReadFile(p); err == nil {
		_ = json.Unmarshal(data, &m)
	}
	return m
}

// SaveConversation records the conversation for a session (read-modify-write under a lock).
func SaveConversation(session string, c Conversation) error {
	p, err := conversationsPath()
	if err != nil {
		return err
	}
	f, err := os.OpenFile(p+".lock", os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		return err
	}
	defer syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
	m := ReadConversations()
	m[session] = c
	return writeJSON(p, m)
}

func writeJSON(path string, v any) error {
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	tmp := fmt.Sprintf("%s.tmp-%d", path, os.Getpid())
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
