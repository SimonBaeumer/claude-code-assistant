package envpath

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFixAddsMissingUserBin(t *testing.T) {
	home := t.TempDir()
	os.MkdirAll(filepath.Join(home, ".local", "bin"), 0o755)
	t.Setenv("HOME", home)
	t.Setenv("PATH", "/usr/bin:/bin")
	p := Fix()
	if !strings.HasPrefix(p, "/usr/bin:/bin:") || !strings.Contains(p, filepath.Join(home, ".local", "bin")) {
		t.Fatalf("PATH %q", p)
	}
	if strings.Contains(p, filepath.Join(home, "bin")+":") || os.Getenv("PATH") != p {
		t.Fatalf("PATH %q", p)
	}
	if Fix() != p {
		t.Fatal("Fix must be idempotent")
	}
}
