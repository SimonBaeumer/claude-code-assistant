# claude-relay: Decision Tree

The shape of the design and how it got there. Each node is a decision reached during the interview (round 1, 2026-11-20): the question, the options weighed, the answer, and why.

## How to read this

- Nodes are numbered in the order they were settled. Child nodes depend on their parent.
- **Options** lists what was on the table. **Chosen** is the answer. **Why** is the trade-off that decided it.

## Tree

### 1. Platform and scope

- **Options**: A: the original Windows + WSL2 + AutoHotkey plan; B: Linux-only MVP
- **Chosen**: B
- **Why**: the user only needs Linux (KDE/X11) now. It drops WSL bridging, AutoHotkey and Windows Terminal.

#### 1.1 Name

- **Chosen**: `claude-relay`

### 2. Trigger mechanism

- **Options**: A: JetBrains External Tool + keymap shortcut with IDE macros; B: KDE global shortcut + `xdotool` copy + clipboard capture; C: custom IDE plugin
- **Chosen**: A
- **Why**: the IDE hands over the selection, path and lines as argv, so there is no clipboard juggling, stale-clipboard risk or daemon. It works in GoLand and IntelliJ alike. B stays available later for "send from anywhere".
- **Evidence**: [research.md](research.md#jetbrains-external-tools-verified-present-in-goland-20263)

### 3. Routing a request to a session

- **Options**: A: automatic from git root; B: explicit YAML path mapping; C: hybrid, automatic with YAML route overrides
- **Chosen**: C
- **Why**: automatic covers almost every case with zero config. Overrides handle monorepo subfolders or several repos sharing one session.

#### 3.1 Missing session

- **Options**: A: auto-start Claude in a detached tmux session at the repo root; B: notify and do nothing
- **Chosen**: A
- **Why**: low effort is the core value. It never creates a second session for the same route.

#### 3.2 General session

- **Options**: A: one session in a configurable dir, reached by its own shortcuts and also catching files outside any repo; B: the same but explicit-only
- **Chosen**: A (accepted recommendation)
- **Why**: a scratch file or a file outside a repo still gets an answer instead of an error.

### 4. Viewer

- **Options**: A: one dedicated Konsole window, switched to the target session via `tmux switch-client` and raised; B: one window per session; C: notification only
- **Chosen**: A (accepted recommendation)
- **Why**: you always look in one place, with no window sprawl. Closing it never kills a session.

### 5. Session state detection

- **Options**: A: screen-scraping only; B: Claude Code hooks only; C: hooks as the source of truth plus a screen check
- **Chosen**: C (accepted recommendation)
- **Why**: confirmed by the PoC. Hooks give clean transitions, but Esc fires no `Stop` (F7). The screen check catches that, and also catches half-typed user input in the box (F8).
- **Evidence**: [appendix-tmux-injection-poc.md](appendix-tmux-injection-poc.md)

#### 5.1 Delivery method

- **Options**: A: bracketed paste of the whole prompt; B: typed header line + bracketed paste of the body; C: Claude Code's native queue (paste whenever)
- **Chosen**: B
- **Why**: A wraps the instructions in `<pasted_content>`, which lowers their authority (F2). C hijacks running tool loops (F5). B gives one turn with the instructions as the user's own words (F3).

### 6. Queue when busy

- **Options**: A: no daemon, FIFO files in `$XDG_RUNTIME_DIR` drained by the `Stop` hook; B: long-running daemon with an in-memory queue; C: reject when busy
- **Chosen**: A (accepted recommendation)
- **Why**: tmpfs means no disk persistence. An atomic rename to claim an item gives exactly-once delivery. B is the upgrade path if the Esc gap hurts.

### 7. MVP actions

- **Options**: explain, ask (`kdialog` question), raw, each with a general-session variant
- **Chosen**: explain + ask, each with a general variant (four shortcuts). Templates are configurable, so raw is just one more template.
- **Why**: covers "understand this" and "question about this" without shipping UI.

### 8. Implementation language

- **Options**: A: Go single binary; B: Python stdlib
- **Chosen**: A (accepted recommendation)
- **Why**: fast startup on every hook call, a single file to install, and it matches the user's stack.

### 9. Non-goals

- **Chosen**: no Windows/WSL/AHK, no macOS/Wayland, no voice, no `claude -p` or API, no IDE plugin, no automatic file context beyond selection + path + lines + project root, no management of hand-started sessions.

### 10. Validation before design

- **Chosen**: run a live PoC in throwaway tmux sessions (accepted).
- **Evidence**: [appendix-tmux-injection-poc.md](appendix-tmux-injection-poc.md)

### 11. Deliverables

- **Chosen**: research, idea, spec, plan, beads export. The work is tracked in a new git repo in the project dir.

## Open questions

- Hand-started sessions: `claude-relay start` only, or global guarded hooks? Deferred to the idea doc.
- Resume with `claude --continue` after tmux loss? Deferred to the idea doc.
- Permission mode for relay-started sessions? Deferred to the idea doc.
- Esc-interrupt drain: is "next send drains first" enough? Deferred until real usage.
