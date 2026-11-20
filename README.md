# claude-relay

Send the code you have selected in GoLand or IntelliJ into a **persistent, interactive Claude Code session** with one shortcut. The answer appears in Claude Code's normal terminal UI, where you can watch it, interrupt it and keep chatting by hand. Each git repo gets its own session, picked from the file's path, and a separate **general** session takes everything else.

- No `claude -p`, no API calls, never a new conversation per shortcut.
- Never types into Claude while it is working, while it shows a permission or trust dialog, or while you have text in its input box. Those requests are queued and sent when the session is idle.
- Exactly one submit per request. Queued prompts live only in tmpfs (`$XDG_RUNTIME_DIR`) and are never logged.

Linux only (tested on KDE/X11 with Konsole, tmux 3.4, Claude Code 2.1.329).

## Install

```sh
make install-jetbrains     # builds, installs ~/.local/bin/claude-relay, adds IDE External Tools
```

Then restart the IDE and bind shortcuts in **Settings > Keymap > External Tools > claude-relay**:

| Tool | Suggested key |
|---|---|
| Explain selection | Alt+E |
| Ask about selection | Alt+Q |
| Explain selection (general) | Alt+Shift+E |
| Ask about selection (general) | Alt+Shift+Q |

Requirements: `tmux`, `claude`, `git`, `notify-send`, `kdialog` (or `zenity`) for Ask, and `xdotool` to raise the viewer.

## Use

Select code, press the shortcut. The first press in a repo starts `claude` for that repo in a detached tmux session and opens the **viewer**, a single Konsole window that always switches to the session that just got a request. In a repo Claude has never seen, it first asks whether you trust the folder. Answer it in the viewer and the request goes out automatically.

```sh
claude-relay status                  # sessions, state, queue length
claude-relay start [dir] | --general # start a session without sending anything
claude-relay view  [dir] | --general # show a session in the viewer
claude-relay queue list|clear <session>
claude-relay stop <session>          # ends that Claude process
claude-relay send --text "..." [--general] [--file F]   # from scripts or other tools
claude-relay config init|check|path
```

Closing the viewer only detaches it: the Claude sessions keep running. You can also attach yourself with `tmux attach -t cr-<name>`.

## Configure

`claude-relay config init` writes `~/.config/claude-relay/config.yaml`, which documents every key. The essentials:

```yaml
general:
  dir: ~/claude-general          # where the general session runs
routes:                          # optional; default is one session per git root
  - path: ~/src/monorepo/service-a
    session: service-a
claude:
  args: ["--model", "opus"]      # extra flags for every relay-started claude
actions:                         # prompt templates; add your own
  review:
    instruction: "Review this code from {file}, lines {lines}, for bugs. Do not edit files."
    body: "\n{code_block}"
```

Run a custom action from an External Tool with `send --action review ...`. Placeholders: `{file} {abs_file} {lines} {language} {project} {question} {selection} {code_block}`.

## How it works

```
IDE External Tool ──> claude-relay send ──> route (override > git root > general)
                                         ──> session state (screen + hooks)
                         idle: type instruction, bracketed-paste code, one Enter
                         busy / dialog / your text in the box: queue in tmpfs
Claude Code hooks ──> claude-relay hook ──> Stop: drain the queue
background drainer ──> waits for idle, delivers queued prompts one turn at a time
```

- **Delivery.** The instruction is typed as one line (`tmux send-keys -l`) and the code is pasted with bracketed paste (`load-buffer` from stdin, then `paste-buffer -p`), followed by exactly one Enter. Claude Code sees your instruction as your own words and the code as pasted material. Control characters are stripped from the code, so a selection cannot end the paste early.
- **State.** The screen is the source of truth: busy while the footer says `esc to interrupt`, a dialog when there is no input box, and "your text" when the input box holds non-dim characters (placeholder text is dim). Hooks (`UserPromptSubmit`, `Stop`, `Notification`, `SessionStart`) are injected per session with `claude --settings`, so your global settings are untouched.
- **Queue.** Claiming an item is an atomic rename, so it can never be delivered twice. If anything fails after Enter was pressed, the prompt is **not** resent.
- **Resume.** The relay remembers each session's conversation id (only the id, in `~/.local/state/claude-relay/`). If the tmux session is lost (reboot), the next request restarts it with `--resume`.

The research behind these choices, including a live test against the real TUI, is in [`claude-relay/`](claude-relay/research.md).

## Selection handling

The IDE passes `$FilePath$` and the selected line range, and the relay reads those lines from the file. This avoids quoting problems with `$SelectedText$`, but it means:

- the selection is widened to whole lines, and with no selection the caret line is sent;
- the file content on disk is used. JetBrains IDEs save documents before running an External Tool, but if you see stale code, check autosave.

## Troubleshooting

| Symptom | Cause and fix |
|---|---|
| "not started by claude-relay" | A tmux session with that name exists but was not created by the relay. Stop or rename it. |
| Request stays queued | `claude-relay status` shows why (busy, dialog, text in the input box). `queue clear <session>` discards it. |
| "+N stuck in flight" in status | A delivery crashed midway. Check the viewer and `queue clear` the session. |
| "did not visibly pick it up" | Enter was pressed but no busy state appeared. Look at the viewer; the prompt is never resent. |
| Viewer does not come to front | KDE focus-stealing prevention. Set `viewer.focus: false` and raise it yourself, or lower the prevention level for Konsole. |
| Hand-started `claude` sessions | Not managed. Start sessions with `claude-relay start` so they get the hooks. |

## Development

```sh
make test   # unit tests plus tmux integration tests against a fake Claude TUI (internal/relay/testdata/faketui)
```

The integration tests use an isolated tmux server (`-L`) and never touch your real sessions.
