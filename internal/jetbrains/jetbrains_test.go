package jetbrains

import (
	"strings"
	"testing"
)

func TestToolsXML(t *testing.T) {
	x := ToolsXML("/home/u/.local/bin/claude-relay")
	for _, want := range []string{
		`name="Review selection or file"`, `send --action review --file`,
		`name="Review uncommitted changes"`, `send --action review-changes --file`,
		`name="Edit selection"`, `send --action edit --file`,
		`--start-col=$SelectionStartColumn$ --end-col=$SelectionEndColumn$`,
		`name="Explain selection"`, // existing names stay, so keymap bindings survive
	} {
		if !strings.Contains(x, want) {
			t.Errorf("missing %s", want)
		}
	}
}
