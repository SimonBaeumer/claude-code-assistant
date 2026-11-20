# claude-relay: Glossary

claude-relay sends a code selection from a JetBrains IDE into a persistent, interactive Claude Code session running in tmux, picked by the file's git repo, without the user leaving the editor.

## Language

### Sessions

**Session**:
One long-lived interactive Claude Code instance running in its own tmux session, started by the relay with the relay's hooks.
_Avoid_: harness, instance, agent, terminal

**Repo session**:
The session bound to one git repository's root directory.
_Avoid_: project session, workspace

**General session**:
The single session for requests not tied to a repo, running in a configurable directory.
_Avoid_: general assistant, default session, fallback session

**Session state**:
The relay's view of whether a session can take input: `idle`, `busy`, `needs-attention` or `unknown`.
_Avoid_: status, readiness, mode

**Viewer**:
The one terminal window attached to tmux that the relay switches to whichever session just received a request.
_Avoid_: visible terminal, console, window

### Requests

**Request**:
One shortcut press: an action plus the selection and its metadata (file, line range, project root).
_Avoid_: message, prompt, job

**Action**:
A named prompt template a request is built from, e.g. `explain` or `ask`.
_Avoid_: command, mode, hotkey

**Prompt**:
The final text that one request becomes after its action's template is filled in.
_Avoid_: message, payload

**Route**:
The session a request is sent to, resolved from overrides, then the file's git root, then the general session.
_Avoid_: mapping, target, binding

**Route override**:
A YAML rule that maps a path prefix to a specific session, beating the automatic git-root route.
_Avoid_: mapping, alias

### Delivery

**Delivery**:
Putting one prompt into a session's input box and submitting it exactly once.
_Avoid_: injection, send, paste

**Queue**:
A session's FIFO of prompts waiting for it to become idle, held only in tmpfs.
_Avoid_: backlog, buffer, pending list

**Drain**:
Claiming the oldest queued prompt of a session and delivering it, if the session is idle.
_Avoid_: flush, process, dequeue
