# Research: claude-relay

## Problem Statement

While reading code in a JetBrains IDE (GoLand, IntelliJ), the user wants to send the current selection plus a little context to a **persistent, interactive Claude Code session**, with one shortcut and no context switching. Claude's answer appears in the normal Claude Code TUI, where the user can watch, interrupt and keep chatting by hand.

Each git repo has its own session (found from the file's path). A separate **general** session handles requests that are not tied to a repo.

Constraints:
- Linux only for now: TUXEDO OS 24.04, KDE on X11, tmux 3.4, Claude Code 2.1.329, Konsole. Go and Python 3.12 + uv are available.
- Interactive TUI only. No `claude -p`, no API calls, never a new conversation per request.
- Never interrupt a running turn or type into a permission prompt. Exactly one submit per request.
- Prompts and source code are not persisted or logged by default.
- MVP first, simple and lightweight.

This replaces the original Windows/WSL/AutoHotkey plan. Its requirements (session continuity, safety, privacy) carry over, but most of its machinery (clipboard capture, global hotkeys, Windows Terminal) does not.

## Landscape

### Original ChatGPT plan (Windows + WSL2 + AutoHotkey + tmux)

A sound safety analysis (busy state, permission prompts, exactly-once delivery, privacy), but built on the wrong platform for this user and overbuilt: clipboard capture with stale-clipboard detection, a global hotkey daemon, and Windows Terminal orchestration. Its readiness detection was left as "investigate". The PoC below resolves it.

### Claude Code's built-in JetBrains integration (`/ide`)

When connected, Claude Code can see the IDE's current selection and open file. This is passive context, though: the user still has to switch to the terminal and type. It also only exists when the plugin is installed (GoLand has none; IntelliJ has the third-party `idea-claude-code-gui`). It complements the relay rather than replacing it, and the relay must not depend on it.

### Third-party Claude IDE plugins (e.g. `idea-claude-code-gui`)

These embed a Claude chat panel in the IDE. That is a different product: it does not drive a persistent terminal session and is not portable across IDEs. Not pursued.

### `claude -p` / Agent SDK

Ruled out by requirement: there is no persistent visible TUI, and each call starts its own conversation.

## Available Resources

### tmux buffer paste (verified)

`load-buffer` + `paste-buffer -p` (bracketed) + `send-keys Enter` delivers one multiline turn. The `-l` literal `send-keys` mode types a single-line header that stays outside Claude's `<pasted_content>` wrapper. Full evidence: [appendix-tmux-injection-poc.md](appendix-tmux-injection-poc.md) (F1 to F3).

### Claude Code hooks (verified)

`UserPromptSubmit`, `Stop`, `Notification` (with `notification_type: permission_prompt`), `SessionStart` and `PreToolUse` all fire with JSON on stdin, including `session_id` and `transcript_path`. They can be supplied per session with `claude --settings <file>`, so the user's global settings stay untouched. Gaps: Esc interrupt fires no `Stop` (F7). See the [appendix](appendix-tmux-injection-poc.md) (F4 to F7).

### `tmux capture-pane -e -p` (verified)

Shows the screen with SGR attributes. Busy shows `esc to interrupt` in the footer. Dim (SGR 2) text on the `❯` row is placeholder or suggestion text, and normal-intensity text there is real input (F8, F9).

### JetBrains External Tools (verified present in GoLand 2026.3)

Settings > Tools > External Tools runs a program with macro arguments, and a Keymap entry binds it to a shortcut. Macro classes confirmed in GoLand's jars: `SelectedText`, `FilePath`, `FileRelativePath`, `ProjectFileDir`, `SelectionStartLine`, `SelectionEndLine`, `LineNumber`, `ClipboardContent`. Arguments are passed as argv (no shell). A single argv string is capped by Linux `MAX_ARG_STRLEN` (128 KiB), which is well above the planned 30,000-char limit. External Tool configs live in the IDE config dir as XML, so the relay can ship one file per IDE and an install step can copy it.

### Desktop glue on KDE/X11

`notify-send` for notifications, `kdialog` (or `zenity`) for the "Ask" input box, `xdotool` to raise the viewer window, and Konsole as the viewer terminal.

## Approach Options

### Option A: External Tool + stateless relay CLI + hooks + tmpfs queue (recommended)

- The IDE External Tool runs `claude-relay send --action explain --file $FilePath$ --lines $SelectionStartLine$-$SelectionEndLine$ --project $ProjectFileDir$ -- $SelectedText$`.
- The relay resolves the **route**: an override from the YAML file, else the `git rev-parse --show-toplevel` of the file, else the general session.
- It ensures the tmux session exists. If it doesn't, the relay starts `claude --settings <relay-hooks.json>` in a detached tmux session at the repo root.
- It reads the **session state** (from the hook state file plus a screen check). Idle: deliver now. Busy: queue. Needs-attention or unknown: refuse with a notification.
- The **queue** is a set of 0600 files in `$XDG_RUNTIME_DIR/claude-relay/<session>/` (tmpfs, so nothing is written to disk and it is gone on reboot). The `Stop` hook calls `claude-relay drain <session>`, which atomically claims one item and delivers it.
- The **viewer**: one Konsole window attached to tmux. On send, it runs `tmux switch-client` to the target session and raises the window with `xdotool`.

Trade-offs: no daemon and almost no moving parts. The Esc case needs a fallback: because no `Stop` fires there, queued items wait until the next `Stop` or until the next `send` call notices the screen is idle and drains first. Effort: small, roughly a weekend.

### Option B: Long-running daemon with in-memory queue and screen polling

The daemon polls `capture-pane` every ~500 ms, keeps the queue in memory, and the CLI talks to it over a Unix socket. It handles the Esc case naturally through polling. The cost: a process to supervise, a socket protocol, and more code. Overkill for the MVP.

### Option C: Rely on Claude Code's native queue

Always paste and press Enter, and let Claude Code queue. This is the simplest option, but F5 shows the message can be injected mid-turn during tool loops. It violates "never interrupt". Rejected.

### Recommendation

**Option A.** It satisfies every non-negotiable requirement with the fewest parts, and each part was checked against the real CLI. Option B stays as an upgrade path if the Esc gap is annoying in practice.

Implementation language: **Go** (single binary, fast startup on every hook call, matches the user's stack). Python stdlib remains a valid alternative.

## Out of Scope

- Windows, WSL, AutoHotkey, macOS, Wayland.
- Voice input.
- `claude -p`, the Agent SDK, direct API calls.
- A custom IDE plugin. Sending from apps other than JetBrains IDEs (a later global-hotkey path is possible).
- Automatic file or project context beyond the selection, file path, line range and project root (Claude can read files itself).
- Managing Claude sessions the user started by hand without the relay's hooks.

## Open Questions

1. **Prompt suggestion dimness**: the placeholder is verified dim. The post-answer suggestion is assumed dim but not captured with `-e`. Verify during implementation; the safe fallback is to treat it as non-empty and refuse. *Non-blocking.*
2. **Hand-started sessions**: should `claude-relay start [dir]` be the only way to get a relay-aware session? The alternative is installing guarded hooks globally, active only when `CLAUDE_RELAY_SESSION` is set. Recommend `start` plus auto-start only. *Decide in the idea doc.*
3. **Resume after tmux loss** (reboot, crash): auto-start with `claude --continue` to resume the repo's last conversation, or start fresh? Recommend `--continue`. *Decide in the idea doc.*
4. **Permission mode of relay sessions**: the user runs auto mode, and the explain template asks Claude not to modify anything, but that is only an instruction. Should relay sessions start in the user's default mode or a stricter one? Recommend the user's default, so manual work in the same session is unaffected. *Decide in the idea doc.*
5. **Esc-interrupt drain**: is "the next send drains first" good enough, or should the queue get a short-lived waiter process that polls the screen until idle? *Non-blocking. Start simple.*
6. **Tab to spaces** in pasted code (F3): acceptable for explanations, noted only.

## Sources

- Live PoC: [appendix-tmux-injection-poc.md](appendix-tmux-injection-poc.md)
- Environment inspection: `tmux -V`, `claude --version`, `$XDG_SESSION_TYPE`, installed binaries, `~/.config/JetBrains/`, GoLand `lib/*.jar` macro class listing, `getconf ARG_MAX`.
- User's original implementation plan (pasted in the session request).
