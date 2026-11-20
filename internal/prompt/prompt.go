// Package prompt turns a selection plus an action template into a deliverable prompt.
package prompt

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/SimonBaeumer/claude-code-assistent/internal/config"
)

// Prompt is what gets delivered: Instruction is typed as one line, Body is pasted.
type Prompt struct {
	Instruction string `json:"instruction"`
	Body        string `json:"body"`
}

// Len is the number of characters that will be delivered.
func (p Prompt) Len() int {
	return utf8.RuneCountInString(p.Instruction) + utf8.RuneCountInString(p.Body)
}

// Input is the request context that fills a template.
type Input struct {
	File      string // absolute path, may be empty
	RouteDir  string // session dir, used to shorten File
	Project   string
	Lines     string // "12-30", "7" or ""
	Selection string
	Question  string
}

// ReadLines returns lines start..end (1-based, inclusive) of file.
func ReadLines(file string, start, end int) (string, error) {
	if start < 1 {
		return "", fmt.Errorf("no line number for the selection")
	}
	if end < start {
		end = start
	}
	data, err := os.ReadFile(file)
	if err != nil {
		return "", err
	}
	lines := strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n")
	if start > len(lines) {
		return "", fmt.Errorf("line %d is past the end of %s (%d lines); save the file first?", start, filepath.Base(file), len(lines))
	}
	if end > len(lines) {
		end = len(lines)
	}
	return strings.Join(lines[start-1:end], "\n"), nil
}

// Render fills the action's templates. The instruction is flattened to a single safe line,
// the body is stripped of control characters so the paste cannot break out of bracketed paste.
func Render(a config.Action, in Input) (Prompt, error) {
	langName, langTag := Language(in.File)
	file := in.File
	if in.RouteDir != "" && in.File != "" {
		if rel, err := filepath.Rel(in.RouteDir, in.File); err == nil && !strings.HasPrefix(rel, "..") {
			file = rel
		}
	}
	if file == "" {
		file = "(no file)"
	}
	lines := in.Lines
	if lines == "" {
		lines = "?"
	}
	sel := SanitizeBody(in.Selection)
	r := strings.NewReplacer(
		"{file}", file,
		"{abs_file}", in.File,
		"{lines}", lines,
		"{language}", langName,
		"{project}", in.Project,
		"{question}", in.Question,
		"{selection}", sel,
		"{code_block}", CodeBlock(sel, langTag),
	)
	p := Prompt{
		Instruction: SanitizeLine(r.Replace(a.Instruction)),
		Body:        SanitizeBody(r.Replace(a.Body)),
	}
	if p.Instruction == "" {
		return p, fmt.Errorf("rendered instruction is empty")
	}
	first, _ := utf8.DecodeRuneInString(p.Instruction)
	if !unicode.IsLetter(first) && !unicode.IsDigit(first) {
		// "/" opens the slash-command menu, "!" bash mode, "?" the help overlay.
		return p, fmt.Errorf("rendered instruction must start with a letter or digit, got %q", string(first))
	}
	return p, nil
}

// SanitizeLine makes text safe to type into Claude Code's input box as one line:
// no newlines (Enter would submit), no control characters, no "@" (opens file completion,
// where the final Enter would pick a completion instead of submitting).
func SanitizeLine(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r == '@':
			b.WriteString("(at)")
		case r == '\n' || r == '\r' || r == '\t':
			b.WriteRune(' ')
		case unicode.IsControl(r):
		default:
			b.WriteRune(r)
		}
	}
	return strings.Join(strings.Fields(b.String()), " ")
}

// SanitizeBody removes every control character except newline and tab. Dropping ESC is what
// stops a selection containing "ESC[201~" from ending the bracketed paste early.
func SanitizeBody(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	return strings.Map(func(r rune) rune {
		if r == '\n' || r == '\t' {
			return r
		}
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, s)
}

// CodeBlock fences code with enough backticks that the code cannot close the fence.
func CodeBlock(code, tag string) string {
	longest, run := 0, 0
	for _, r := range code {
		if r == '`' {
			run++
			if run > longest {
				longest = run
			}
		} else {
			run = 0
		}
	}
	n := 3
	if longest >= n {
		n = longest + 1
	}
	fence := strings.Repeat("`", n)
	return fence + tag + "\n" + code + "\n" + fence
}

var languages = map[string][2]string{
	".go": {"Go", "go"}, ".py": {"Python", "python"}, ".java": {"Java", "java"},
	".kt": {"Kotlin", "kotlin"}, ".kts": {"Kotlin", "kotlin"}, ".scala": {"Scala", "scala"},
	".groovy": {"Groovy", "groovy"}, ".gradle": {"Groovy", "groovy"},
	".js": {"JavaScript", "javascript"}, ".mjs": {"JavaScript", "javascript"}, ".jsx": {"JavaScript", "jsx"},
	".ts": {"TypeScript", "typescript"}, ".tsx": {"TypeScript", "tsx"},
	".rs": {"Rust", "rust"}, ".rb": {"Ruby", "ruby"}, ".php": {"PHP", "php"},
	".c": {"C", "c"}, ".h": {"C", "c"}, ".cc": {"C++", "cpp"}, ".cpp": {"C++", "cpp"}, ".hpp": {"C++", "cpp"},
	".cs": {"C#", "csharp"}, ".swift": {"Swift", "swift"}, ".lua": {"Lua", "lua"},
	".sh": {"Shell", "bash"}, ".bash": {"Shell", "bash"}, ".zsh": {"Shell", "zsh"},
	".sql": {"SQL", "sql"}, ".yaml": {"YAML", "yaml"}, ".yml": {"YAML", "yaml"},
	".json": {"JSON", "json"}, ".xml": {"XML", "xml"}, ".html": {"HTML", "html"},
	".css": {"CSS", "css"}, ".scss": {"SCSS", "scss"}, ".md": {"Markdown", "markdown"},
	".tf": {"Terraform", "hcl"}, ".proto": {"Protocol Buffers", "protobuf"}, ".toml": {"TOML", "toml"},
}

// Language guesses a display name and a code-fence tag from the file name.
func Language(file string) (name, tag string) {
	base := filepath.Base(file)
	switch strings.ToLower(base) {
	case "dockerfile":
		return "Dockerfile", "dockerfile"
	case "makefile":
		return "Makefile", "makefile"
	}
	if l, ok := languages[strings.ToLower(filepath.Ext(base))]; ok {
		return l[0], l[1]
	}
	return "unknown language", ""
}
