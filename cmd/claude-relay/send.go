package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/SimonBaeumer/claude-code-assistent/internal/config"
	"github.com/SimonBaeumer/claude-code-assistent/internal/prompt"
	"github.com/SimonBaeumer/claude-code-assistent/internal/route"
)

// sendOpts are the parsed flags of "send".
type sendOpts struct {
	Action, File, Start, End, Line, StartCol, EndCol, Project, Text, Question string
	General                                                                   bool
	Stdin                                                                     string
	HasStdin                                                                  bool
}

// sendRequest is a fully built request, ready for relay.Send.
type sendRequest struct {
	Target route.Target
	Prompt prompt.Prompt
	Label  string
}

// sendError carries the notification title for a failed send.
type sendError struct {
	title string
	err   error
}

func (e sendError) Error() string { return e.title + ": " + e.err.Error() }
func (e sendError) Unwrap() error { return e.err }

func sendErr(title string, err error) error { return sendError{title, err} }

// buildSend turns the flags into a routed, rendered prompt. ask shows the question dialog;
// it is only called after routing succeeded, so a refused action never asks first.
func buildSend(cfg *config.Config, o sendOpts, ask func(label string) (string, error)) (sendRequest, error) {
	act, ok := cfg.Actions[o.Action]
	if !ok {
		return sendRequest{}, sendErr("Unknown action", fmt.Errorf("no action %q in config", o.Action))
	}
	absFile := ""
	if o.File != "" {
		absFile, _ = filepath.Abs(o.File)
	}
	if act.RequiresFile() && absFile == "" && o.Text == "" && !o.HasStdin {
		return sendRequest{}, sendErr("No file", errors.New("this action needs --file, --text or --stdin"))
	}

	var sel, lines string
	wholeFile := false
	switch {
	case !act.RequiresFile():
	case o.Text != "":
		sel = o.Text
	case o.HasStdin:
		sel = o.Stdin
	default:
		s, e := atoi(o.Start), atoi(o.End)
		if s == 0 || (s == e && o.StartCol != "" && o.StartCol == o.EndCol) { // nothing selected
			switch act.NoSelection {
			case config.NoSelectionFile:
				wholeFile = true
			case config.NoSelectionError:
				return sendRequest{}, sendErr("No selection", errors.New("select some code first"))
			}
		}
		if wholeFile {
			if _, err := os.Stat(absFile); err != nil {
				return sendRequest{}, sendErr("No file", err)
			}
			break
		}
		if s == 0 {
			s = atoi(o.Line)
			e = s
		}
		var err error
		sel, err = prompt.ReadLines(absFile, s, e)
		if err != nil {
			return sendRequest{}, sendErr("No selection", err)
		}
		if e > s {
			lines = fmt.Sprintf("%d-%d", s, e)
		} else {
			lines = strconv.Itoa(s)
		}
	}
	if act.RequiresFile() && !wholeFile && strings.TrimSpace(sel) == "" {
		return sendRequest{}, sendErr("No selection", errors.New("select some code first"))
	}

	target := route.Resolve(cfg, absFile, o.Project, o.General)
	if act.Route == config.RouteRepo && target.Kind == route.KindGeneral {
		return sendRequest{}, sendErr("This action needs a git repo",
			fmt.Errorf("%q runs only in a repo session, and this is not inside a git repo or route override", o.Action))
	}

	q := o.Question
	if act.Ask && q == "" {
		var err error
		if q, err = ask(act.Label()); err != nil {
			return sendRequest{}, err // desktop.ErrCancelled is passed through as is
		}
	}

	p, err := prompt.Render(act, prompt.Input{
		File: absFile, RouteDir: target.Dir, Project: o.Project, Lines: lines,
		Selection: sel, Question: q, WholeFile: wholeFile,
	})
	if err != nil {
		return sendRequest{}, sendErr("Prompt template error", err)
	}
	if n := p.Len(); n > cfg.Limits.MaxChars {
		return sendRequest{}, sendErr("Selection too large", fmt.Errorf("%d characters, the limit is %d (limits.max_chars)", n, cfg.Limits.MaxChars))
	}

	label := o.Action
	if absFile != "" {
		label += " " + filepath.Base(absFile)
		if lines != "" {
			label += ":" + lines
		}
	}
	return sendRequest{Target: target, Prompt: p, Label: label}, nil
}
