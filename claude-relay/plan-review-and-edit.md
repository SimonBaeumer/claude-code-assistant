# Plan: Review and Edit actions

Two new shortcuts on top of the existing Explain and Ask:

| Shortcut | Tool | What happens |
|---|---|---|
| Alt+R | Review | Best practice and security review of the selection, or of the whole file if nothing is selected. Read only. |
| Alt+Shift+R | Review changes | The same review over the repo's uncommitted changes (`git diff HEAD` plus untracked files). Read only. |
| Alt+I | Edit | A dialog opens, you type or dictate the change, Claude edits the selected code in place. |

Both go to the **repo session**, never the general one. A review needs the surrounding code to judge it, and an edit must run where the file lives.

## Facts this plan builds on

- `$SelectionStartColumn$` and `$SelectionEndColumn$` exist in GoLand 2026.3 (`com/intellij/ide/macro/Selection{Start,End}ColumnMacro`). Equal start and end line and column means "nothing selected". (To verify: that both report the caret position when there is no selection.)
- Handy 0.9.8 is running. It types dictated text into the focused widget, so dictation needs **no code in the relay**. The dialog only has to have focus, which `desktop.Ask` now does (it attaches to the IDE window and raises it).
- The relay already waits through permission prompts (state `needs-attention`). An edit that needs approval simply shows the prompt in the viewer, which comes to the front.
- Claude Code's built-in `/security-review` and `/code-review` exist, but the relay refuses instructions that start with `/`. That is deliberate, so a template cannot run slash commands.

## Feature A: Review

### Prompt (default template)

```
Review the code below from {target} ({language}) for correctness, best practices and
security, as a senior engineer would in a code review. Report at most 10 findings,
most severe first. For each: severity (critical, high, medium, low), file:line,
the problem in one sentence, why it matters, and the concrete fix in one or two lines.
Check in particular: input validation and injection, authn/authz, secrets in code,
unsafe deserialization, path traversal, error handling, resource leaks, concurrency,
and non-idiomatic patterns for this language. Skip pure style nits. If there is
nothing significant, say so in one line. Read the surrounding code where needed to be
sure. Review only, do not modify files. I can ask you to fix a finding afterwards.
```

`{target}` is a new placeholder: `main.go, lines 3-5` for a selection, `main.go (whole file)` for the file.

**Follow-up:** the review lands in the repo session. Typing `fix 2` in the viewer, or pressing Alt+I and saying "fix finding 2", applies one finding. No extra feature is needed for that.

### Scopes

| Scope | Body | Why |
|---|---|---|
| selection | pasted code block, as today | Claude sees exactly what you selected. |
| file (nothing selected) | **no paste**, the instruction names the file and Claude reads it | No size limit, always the saved version, keeps the prompt small. |
| changes | no paste, the instruction tells Claude to run `git diff HEAD` and `git status` | Same reasons, and Claude can read untouched context around each hunk. |

## Feature B: Edit

### Flow

1. Select code (or put the caret on a line), press Alt+I.
2. Dialog "Describe the change:" opens with focus. Type it, or hold Handy's key and dictate.
3. Enter sends it. The relay routes to the repo session, waits if it is busy (as now).
4. Claude edits the file. If the session is not in auto mode, the permission prompt appears in the viewer.
5. The IDE reloads the file from disk (fsnotifier picks it up).

### Prompt (default template)

```
Change the code below in {target} ({language}) as follows: {question}
Edit the file directly. Keep the change minimal and limited to this code unless the
request needs more, follow the file's existing style, and do not touch unrelated code.
When done, summarise what you changed in two or three lines.
```

Body: the selected code as a code block. It is the **anchor**: Claude locates the code by content, not by line number, so a request that waited in the queue while the file changed fails visibly instead of editing the wrong lines.

### Risks and mitigations

| Risk | Mitigation |
|---|---|
| Unsaved IDE buffer for the same file when Claude edits it, so the IDE shows a "file changed on disk" conflict | JetBrains saves all documents before running an External Tool (to verify). README: don't keep typing in that file while Claude works on it. |
| Queued edit runs after the file changed | Content anchor (above). Claude's Edit tool fails when the old text is gone, and the summary says so. |
| Dictated text with newlines | The request goes in the typed instruction, which is one line (newlines become spaces). That keeps it outside `<pasted_content>`, so Claude treats it as your own words. |
| Edit sent to the general session | Refused: `edit` requires a repo route. |

## Changes

### 1. Config: per-action fields

```yaml
actions:
  review:
    instruction: "..."          # template above
    body: "\n{code_block}"
    no_selection: file          # caret (default, today's behaviour) | file | error
    route: repo                 # any (default) | repo: refuse the general session
  review-changes:
    instruction: "Review the uncommitted changes in this repo (run git diff HEAD and git status) ..."
    body: ""
    needs_file: false           # default true; false = no file or selection needed
    route: repo
  edit:
    ask: true
    ask_label: "Describe the change:"   # default "Question about the selection:"
    instruction: "..."          # template above, uses {question}
    body: "\n{code_block}"
    route: repo
```

Validation additions: `no_selection` and `route` enums; `needs_file: false` must not use `{file} {lines} {target} {selection} {code_block}`.

**Acceptance:** `config check` rejects bad enums and placeholder misuse. Existing configs load unchanged (all new fields default to today's behaviour).

### 2. Prompt rendering

- New placeholder `{target}`: `<file>, lines <lines>` or `<file> (whole file)`.
- When nothing is selected and `no_selection: file`: `{selection}` and `{code_block}` render empty and the body is dropped if it is only whitespace.

**Acceptance:** unit tests for selection, whole file, and no-file actions.

### 3. CLI: `send`

- New flags `--start-col` and `--end-col`. "No selection" = equal start and end line and column. Without the flags the old behaviour holds (the caret line).
- `--action review-changes` works with only `--project` (routes by the project dir).
- `route: repo` with a general target fails with the notification "This action needs a git repo".

**Acceptance:** CLI tests for each path. An integration test with the fake TUI that a file-scope review sends one line and no paste.

### 4. JetBrains tools

Add "Review", "Review changes" and "Edit" to `ToolsXML`, passing the column macros. Re-run `install-jetbrains`, restart the IDE, bind the keys.

**Acceptance:** the generated XML contains the three tools. Manual check in GoLand.

### 5. Dialog

`desktop.Ask(label)` already takes a label. Pass `ask_label`. Keep the single-line input box: Enter submits, which is the lowest effort with dictation.

### 6. Docs

README: the new shortcuts, review scopes, the edit flow with Handy, and the unsaved-buffer caveat.

### 7. Manual end-to-end (real Claude, scratch repo)

- Review a selection with a planted SQL injection, then the whole file, then the uncommitted changes. Each gives ranked findings and no file is modified.
- Edit by dictation: "rename x to count and add a doc comment". The file changes, the IDE reloads it, the summary appears.
- An edit while Claude is busy queues and applies after the turn ends.
- An edit in default permission mode shows the prompt in the viewer and goes through after approval.

## Decisions (recommendation first)

1. **Review prompt in the relay, not `/security-review`.** It works on a selection or a file, not just a branch diff, stays short, and avoids the costly multi-agent slash commands. Alternative: allow a slash-command instruction for `review-changes` only.
2. **No selection means whole file for Review, caret line for Explain and Edit.** Reviewing one line is rarely useful, while explaining or editing the caret line is.
3. **Single-line dialog for Edit.** Enter submits. Alternative: `kdialog --textinputbox` (multiline, but Enter inserts a newline, so you would need to click OK).
4. **No separate "Edit (general)"**: edits must run in the repo.

## Out of scope

- Applying review findings automatically.
- Built-in speech to text (Handy covers it).
- Per-hunk diff review UI in the IDE.
