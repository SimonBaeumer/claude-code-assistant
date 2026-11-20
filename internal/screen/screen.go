// Package screen classifies a `tmux capture-pane -e -p` snapshot of the Claude Code TUI.
//
// Layout it relies on (verified against Claude Code 2.1.329):
//
//	────────────────   rule
//	❯ <input>          input box, may continue on following lines
//	────────────────   rule
//	  footer ... · esc to interrupt     <- present only while Claude is working
//
// Placeholder and suggestion text in an empty box is drawn dim (SGR 2), real input is not.
package screen

import (
	"regexp"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

type Kind string

const (
	Unknown    Kind = "unknown"
	Idle       Kind = "idle"
	Busy       Kind = "busy"
	Permission Kind = "permission"
)

type Screen struct {
	Kind       Kind
	InputEmpty bool
	Input      string // plain text of the input box (non-dim characters only)
}

type cell struct {
	r   rune
	dim bool
}

var permissionRe = regexp.MustCompile(`Do you want to |Esc to cancel`)

// Parse classifies a capture.
func Parse(raw string) Screen {
	rawLines := strings.Split(strings.TrimRight(raw, "\n"), "\n")
	cells := make([][]cell, len(rawLines))
	plain := make([]string, len(rawLines))
	dim := false
	for i, l := range rawLines {
		cells[i], dim = parseLine(l, dim)
		var b strings.Builder
		for _, c := range cells[i] {
			b.WriteRune(c.r)
		}
		plain[i] = b.String()
	}

	top, bottom := findInputBox(plain)
	if top < 0 {
		tail := plain
		if len(tail) > 30 {
			tail = tail[len(tail)-30:]
		}
		if permissionRe.MatchString(strings.Join(tail, "\n")) {
			return Screen{Kind: Permission}
		}
		return Screen{Kind: Unknown}
	}

	var input strings.Builder
	for i := top + 1; i < bottom; i++ {
		line := cells[i]
		if i == top+1 {
			line = stripPrompt(line)
		}
		for _, c := range line {
			if !c.dim {
				input.WriteRune(c.r)
			}
		}
		input.WriteRune('\n')
	}
	text := strings.TrimSpace(input.String())
	s := Screen{Kind: Idle, InputEmpty: text == "", Input: text}

	footer := strings.Join(plain[bottom+1:], "\n")
	if strings.Contains(footer, "esc to interrupt") {
		s.Kind = Busy
	}
	return s
}

// findInputBox returns the indices of the two rules around the "❯" input box (bottom-most one).
func findInputBox(plain []string) (int, int) {
	for bottom := len(plain) - 1; bottom > 0; bottom-- {
		if !isRule(plain[bottom]) {
			continue
		}
		for top := bottom - 1; top >= 0; top-- {
			if isRule(plain[top]) {
				if top+1 < bottom && strings.HasPrefix(strings.TrimLeftFunc(plain[top+1], unicode.IsSpace), "❯") {
					return top, bottom
				}
				break
			}
		}
	}
	return -1, -1
}

func isRule(s string) bool {
	t := strings.TrimSpace(s)
	if utf8.RuneCountInString(t) < 10 {
		return false
	}
	for _, r := range t {
		if r != '─' {
			return false
		}
	}
	return true
}

func stripPrompt(line []cell) []cell {
	for i, c := range line {
		if c.r == '❯' {
			return line[i+1:]
		}
		if !unicode.IsSpace(c.r) {
			return line
		}
	}
	return line
}

// parseLine splits a line into cells, tracking the SGR dim attribute across escape sequences.
func parseLine(s string, dim bool) ([]cell, bool) {
	var out []cell
	for i := 0; i < len(s); {
		if s[i] == 0x1b {
			if i+1 < len(s) && s[i+1] == '[' {
				j := i + 2
				for j < len(s) && (s[j] < 0x40 || s[j] > 0x7e) {
					j++
				}
				if j < len(s) && s[j] == 'm' {
					dim = applySGR(s[i+2:j], dim)
				}
				i = j + 1
				continue
			}
			if i+1 < len(s) && s[i+1] == ']' { // OSC: skip to BEL or ST
				j := i + 2
				for j < len(s) && s[j] != 0x07 && !(s[j] == 0x1b && j+1 < len(s) && s[j+1] == '\\') {
					j++
				}
				if j < len(s) && s[j] == 0x1b {
					j++
				}
				i = j + 1
				continue
			}
			i += 2
			continue
		}
		r, size := utf8.DecodeRuneInString(s[i:])
		i += size
		if r == '\r' {
			continue
		}
		out = append(out, cell{r: r, dim: dim})
	}
	return out, dim
}

func applySGR(params string, dim bool) bool {
	if params == "" {
		return false
	}
	parts := strings.FieldsFunc(params, func(r rune) bool { return r == ';' || r == ':' })
	for k := 0; k < len(parts); k++ {
		n, err := strconv.Atoi(parts[k])
		if err != nil {
			continue
		}
		switch n {
		case 0, 22:
			dim = false
		case 2:
			dim = true
		case 38, 48, 58: // extended colour: skip its sub-parameters
			if k+1 < len(parts) {
				if parts[k+1] == "5" {
					k += 2
				} else if parts[k+1] == "2" {
					k += 4
				}
			}
		}
	}
	return dim
}
