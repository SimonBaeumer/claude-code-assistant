// faketui imitates the parts of Claude Code's TUI the relay depends on: an input box between
// two rules, dim placeholder text, bracketed paste, Enter to submit, and an
// "esc to interrupt" footer while busy. Every submit is appended to $FAKE_LOG as JSON.
package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"
)

type entry struct {
	Text string `json:"text"`
	Busy bool   `json:"busy"` // true if submitted while busy (the relay must never do that)
}

func main() {
	busyFor := 800 * time.Millisecond
	if ms, err := strconv.Atoi(os.Getenv("FAKE_BUSY_MS")); err == nil {
		busyFor = time.Duration(ms) * time.Millisecond
	}
	logPath := os.Getenv("FAKE_LOG")

	stty := exec.Command("stty", "raw", "-echo")
	stty.Stdin = os.Stdin
	_ = stty.Run()
	fmt.Print("\x1b[?2004h")

	var mu sync.Mutex
	var input []rune
	var busyUntil time.Time
	rule := strings.Repeat("─", 60)

	render := func() {
		mu.Lock()
		defer mu.Unlock()
		var b strings.Builder
		b.WriteString("\x1b[H\x1b[2J fake claude\r\n" + rule + "\r\n❯ ")
		if len(input) == 0 {
			b.WriteString("\x1b[2mTry something\x1b[0m")
		} else {
			b.WriteString(strings.ReplaceAll(string(input), "\n", "\r\n  "))
		}
		b.WriteString("\r\n" + rule + "\r\n  ⏵⏵ fake mode")
		if time.Now().Before(busyUntil) {
			b.WriteString(" · esc to interrupt")
		}
		os.Stdout.WriteString(b.String())
	}
	go func() {
		for range time.Tick(50 * time.Millisecond) {
			render()
		}
	}()

	submit := func() {
		mu.Lock()
		defer mu.Unlock()
		if len(input) == 0 {
			return
		}
		e := entry{Text: string(input), Busy: time.Now().Before(busyUntil)}
		input = nil
		busyUntil = time.Now().Add(busyFor)
		if logPath != "" {
			f, err := os.OpenFile(logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
			if err == nil {
				json.NewEncoder(f).Encode(e)
				f.Close()
			}
		}
	}

	r := bufio.NewReader(os.Stdin)
	inPaste := false
	for {
		c, _, err := r.ReadRune()
		if err != nil {
			return
		}
		if c == 0x1b {
			if n, _, _ := r.ReadRune(); n != '[' {
				continue
			}
			var seq strings.Builder
			for {
				x, _, err := r.ReadRune()
				if err != nil {
					return
				}
				seq.WriteRune(x)
				if x >= 0x40 && x <= 0x7e {
					break
				}
			}
			switch seq.String() {
			case "200~":
				inPaste = true
			case "201~":
				inPaste = false
			}
			continue
		}
		mu.Lock()
		switch {
		case inPaste && (c == '\r' || c == '\n'):
			input = append(input, '\n')
		case inPaste:
			input = append(input, c)
		case c == '\r':
			mu.Unlock()
			submit()
			continue
		case c == 0x15: // C-u
			input = nil
		case c == 0x7f:
			if len(input) > 0 {
				input = input[:len(input)-1]
			}
		case c >= 0x20 || c == '\t':
			input = append(input, c)
		}
		mu.Unlock()
	}
}
