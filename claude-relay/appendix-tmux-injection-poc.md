# Appendix: tmux Injection into a Live Claude Code TUI (PoC)

## Summary

A live test on 2026-11-20 against Claude Code v2.1.329 inside tmux 3.4 (KDE/X11, TUXEDO OS 24.04). It answers the plan's "Milestone 2" question: can an external process inject exactly one multiline user turn into an existing interactive session, and can it tell when injection is safe? Short answer: **yes, injection is reliable, but only if it is gated on Claude being idle**. Sending while Claude is working hijacks the running turn.

Setup: two throwaway tmux sessions (`relay-poc` in auto mode, `relay-poc2` in `--permission-mode default`), both started with `claude --model haiku --settings <file>`. The settings file registered `SessionStart`, `UserPromptSubmit`, `PreToolUse`, `Notification` and `Stop` hooks that append their stdin JSON to a log. No user settings were modified.

## Findings

### F1. Bracketed paste + one Enter = exactly one user turn

```
tmux load-buffer -b relay1 msg.txt
tmux paste-buffer -p -d -b relay1 -t relay-poc   # -p = bracketed paste, -d = delete buffer after
tmux send-keys -t relay-poc Enter
```

A 6-line message with a blank line, indentation, umlaut, emoji and escaped quotes arrived as **one** `UserPromptSubmit`, rendered intact in the TUI, and Claude answered "PONG 6". Before Enter, the input box showed `[Pasted text #1 +5 lines]`, so the paste is buffered and not submitted by its own newlines.

### F2. A pure paste is wrapped as `<pasted_content>`

When the whole message is a paste, the prompt Claude receives is:

```
\n\n<pasted_content id="379b">\nReply with exactly the word PONG ...
```

Claude Code treats pasted content as material from elsewhere, not as the user's own instruction. It obeyed here because the message contained nothing else, but a template's instructions ("do not modify files") carry less authority when they sit inside the paste.

### F3. Hybrid delivery: typed header + pasted body

```
tmux send-keys -t relay-poc -l 'Explain ... (from file main.go, lines 3-5): '   # -l = literal, single line
tmux paste-buffer -p -d -b relay2 -t relay-poc                                 # code
tmux send-keys -t relay-poc Enter
```

Result: one turn, **no `<pasted_content>` wrapper**, and the code was inlined into the prompt string. Side effect observed: a leading tab in the code arrived as 4 spaces. That is cosmetic for explanation but means byte-exact code is not guaranteed.

### F4. Sending while busy, without tools: Claude Code queues natively

A second message sent while a plain text answer was streaming showed up above the input box and was submitted after the first turn's `Stop`, as a separate turn. Event order: `UserPromptSubmit, Stop, UserPromptSubmit, Stop`.

### F5. Sending while busy, during a tool loop: the message is injected mid-turn (unsafe)

Prompt: "run `sleep 10 && echo TOOLDONE`, then `echo SECONDTOOL`, then say FINISHED". A second message was sent 4s in, while the first tool was running. Event order:

```
UserPromptSubmit  (first prompt)
PreToolUse        sleep 10 && echo TOOLDONE
UserPromptSubmit  MIDTURN: reply only INJECTED-CHECK     <- before any Stop
Stop
```

Claude answered `INJECTED-CHECK` and **never ran SECONDTOOL or said FINISHED**. The injected message took over the turn. This breaks the "do not interrupt Claude" requirement, so the relay must never deliver while the session is busy.

### F6. Permission prompt raises `Notification` with a type

```
Notification  notification_type=permission_prompt  message="Claude needs your permission"
```

The screen shows `Do you want to proceed?` with a numbered menu. Pasting into this state would select or amend menu items, so this must block delivery.

### F7. Esc interrupt fires no `Stop` and restores the prompt to the input box

After Esc during a long answer, the only event was `UserPromptSubmit`. **No `Stop` fired.** The footer's `esc to interrupt` hint disappeared, and the interrupted prompt text was put back into the input box (non-dim). A hooks-only state machine would believe the session is busy forever.

### F8. The idle input box can contain dim placeholder or suggestion text

With `capture-pane -e`, an empty box with a placeholder renders as:

```
ESC[39m❯ ESC[2mTry ESC[0m ESC[2m"how ESC[0m ...      <- dim (SGR 2): placeholder, not input
ESC[39m❯ abc                                         <- normal intensity: real typed text
ESC[39m❯                                             <- truly empty
```

After an answer, the box also showed a prompt suggestion ("write a test for add"). It is assumed to be dim like the placeholder, but that capture was not made with `-e`, so it is unverified (see Open Questions in research.md).

### F9. Footer is a reliable busy marker

While generating, the footer line contains `esc to interrupt`. When idle, it does not. The status line is user-customised (this user has a multi-column status line), so detection should key on that hint and on the `❯` input row, not on fixed line numbers.

### F10. Trust dialog did not appear in a fresh directory

Possibly because of the user's settings or auto mode. A relay-started session in a new repo may still meet a trust dialog on other setups. Treat any screen without a recognisable idle input row as `unknown`.

## Sample Data

Hook stdin payloads (trimmed):

```json
{"hook_event_name":"SessionStart","session_id":"e38c8759-...","source":"startup","transcript_path":"/home/simon/.claude/projects/-tmp-...-poc-work/e38c8759-....jsonl"}
{"hook_event_name":"UserPromptSubmit","session_id":"e38c8759-...","prompt":"Explain in one short sentence what this Go snippet does (from file main.go, lines 3-5): func add(a, b int) int {\n    return a + b\n}"}
{"hook_event_name":"Notification","session_id":"...","notification_type":"permission_prompt","message":"Claude needs your permission"}
{"hook_event_name":"Stop","session_id":"..."}
```

Busy screen footer:

```
✽ Processing… (1s · ↓ 40 tokens · thinking)
──────────────────────────────
❯
──────────────────────────────
  ⏵⏵ auto mode on (shift+tab to cycle) · esc to interrupt
```

## Implications for Design

1. **Delivery = typed header line (`send-keys -l`) + bracketed paste of the body + exactly one `Enter`.** The header keeps the instruction outside `<pasted_content>`. Never use non-bracketed paste: raw newlines would act as Enter.
2. **Gate on idle; never rely on Claude Code's native queue.** F5 shows the native queue can hijack a tool loop. The relay keeps its own queue.
3. **Session state = hooks + screen check, both required.** Hooks give the transitions (busy on `UserPromptSubmit`, needs-attention on `Notification/permission_prompt`, idle on `Stop`). The screen confirms idle (no `esc to interrupt`, the `❯` row has no normal-intensity text) and catches the Esc case (F7), where the hooks are stale.
4. **Hooks must be injected per session** via `claude --settings <relay-hooks.json>`, so the relay needs no edit to the user's global `~/.claude/settings.json`. Hook payloads carry `session_id` and `transcript_path`, which can link a tmux session to its conversation.
5. **Unknown screen = do not send.** Trust dialogs, menus, or an unrecognised layout all count as `unknown`.

## Sources

- Live commands in scratchpad `poc/` (tmux `new-session`, `load-buffer`, `paste-buffer -p -d`, `send-keys -l`, `capture-pane -e -p`), Claude Code 2.1.329, tmux 3.4.
- Hook payload logs `events.jsonl`, `events2.jsonl` (deleted with the scratchpad).
