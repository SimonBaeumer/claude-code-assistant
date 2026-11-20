package tmux

import (
	"crypto/rand"
	"encoding/hex"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// A blocked tmux client (in real use: keys typed into copy-mode opening a jump-to prompt on
// the viewer) must not hang the relay. wait-for blocks until signalled, so it stands in.
func TestRunTimesOut(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not installed")
	}
	b := make([]byte, 4)
	rand.Read(b)
	tm := Tmux{Socket: "cr-test-" + hex.EncodeToString(b), Config: "/dev/null", Timeout: 500 * time.Millisecond}
	t.Cleanup(func() { exec.Command("tmux", "-L", tm.Socket, "kill-server").Run() })
	if _, err := tm.NewSession("s", t.TempDir(), nil, []string{"sleep", "30"}); err != nil {
		t.Fatal(err)
	}
	begin := time.Now()
	_, err := tm.run(nil, "wait-for", "never-signalled")
	if el := time.Since(begin); err == nil || !strings.Contains(err.Error(), "no answer within") || el > 2*time.Second {
		t.Fatalf("want timeout error after ~0.5s, got %v after %s", err, el)
	}
}
