// Package jetbrains installs claude-relay as External Tools in every JetBrains IDE config dir.
package jetbrains

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"os"
	"path/filepath"
)

type tool struct {
	Name, Description, Params string
}

var tools = []tool{
	{"Explain selection", "Explain the selection in this repo's Claude session", "--action explain"},
	{"Ask about selection", "Ask a question about the selection in this repo's Claude session", "--action ask"},
	{"Explain selection (general)", "Explain the selection in the general Claude session", "--action explain --general"},
	{"Ask about selection (general)", "Ask about the selection in the general Claude session", "--action ask --general"},
}

// Context passed from the IDE. The selection is read from the saved file by line range
// instead of $SelectedText$, so code containing quotes cannot break argument parsing.
const ideArgs = `--file "$FilePath$" --start=$SelectionStartLine$ --end=$SelectionEndLine$ --line=$LineNumber$ --project "$ProjectFileDir$"`

func attr(s string) string {
	var b bytes.Buffer
	_ = xml.EscapeText(&b, []byte(s))
	return b.String()
}

// ToolsXML renders the External Tools file for the given binary.
func ToolsXML(exe string) string {
	var b bytes.Buffer
	b.WriteString("<toolSet name=\"claude-relay\">\n")
	for _, t := range tools {
		fmt.Fprintf(&b, `  <tool name="%s" description="%s" showInMainMenu="false" showInEditor="true" showInProject="false" showInSearchPopup="true" disabled="false" useConsole="false" showConsoleOnStdOut="false" showConsoleOnStdErr="false" synchronizeAfterRun="false">
    <exec>
      <option name="COMMAND" value="%s" />
      <option name="PARAMETERS" value="%s" />
      <option name="WORKING_DIRECTORY" value="$ProjectFileDir$" />
    </exec>
  </tool>
`, attr(t.Name), attr(t.Description), attr(exe), attr("send "+t.Params+" "+ideArgs))
	}
	b.WriteString("</toolSet>\n")
	return b.String()
}

// Install writes tools/claude-relay.xml into every JetBrains config dir and returns the paths.
func Install(exe string) ([]string, error) {
	cfgDir, err := os.UserConfigDir()
	if err != nil {
		return nil, err
	}
	dirs, _ := filepath.Glob(filepath.Join(cfgDir, "JetBrains", "*"))
	var written []string
	for _, d := range dirs {
		if fi, err := os.Stat(filepath.Join(d, "options")); err != nil || !fi.IsDir() {
			continue // not an IDE config dir
		}
		toolsDir := filepath.Join(d, "tools")
		if err := os.MkdirAll(toolsDir, 0o755); err != nil {
			return written, err
		}
		p := filepath.Join(toolsDir, "claude-relay.xml")
		if err := os.WriteFile(p, []byte(ToolsXML(exe)), 0o644); err != nil {
			return written, err
		}
		written = append(written, p)
	}
	if len(written) == 0 {
		return nil, fmt.Errorf("no JetBrains IDE config found under %s", filepath.Join(cfgDir, "JetBrains"))
	}
	return written, nil
}
