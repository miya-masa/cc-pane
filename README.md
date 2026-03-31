# cc-pane

A lightweight CLI tool to monitor Claude Code session states across tmux panes.

## Overview

When running Claude Code in multiple tmux panes simultaneously, it's hard to tell which pane needs your attention — is it waiting for approval, waiting for input, or still running? cc-pane uses Claude Code hooks to record per-pane state as JSON files, then provides a CLI to list, show, and jump to any session.

## Features

- **Lightweight**: No daemon process. State files are updated only when hooks fire.
- **Priority sorting**: `approval_waiting` > `waiting_input` > `running` — urgent items surface first.
- **Pipe-friendly**: `cc-pane ls --tsv` outputs tab-separated values for use with fzf, grep, awk, etc.
- **Single Go binary**: Zero external library dependencies. Only requires tmux.
- **JSON output**: `cc-pane ls --json` for scripting and automation.
- **Clean lifecycle**: `SessionStart` creates state, `SessionEnd` removes it automatically.

## Dependencies

| Tool | Required | Purpose             |
| ---- | -------- | ------------------- |
| tmux | Yes      | Pane management     |
| fzf  | No       | Interactive picker (via shell functions) |
| git  | No       | Branch name detection |

## Installation

### go install

```bash
go install github.com/miya-masa/cc-pane@latest
```

### Build from source

```bash
cd cc-pane
make install
```

## Setup

After installing the binary, run:

```bash
cc-pane setup
```

This automatically:

1. Adds cc-pane hooks to `~/.claude/settings.json` (existing hooks are preserved)
2. Writes shell functions (`cc-pick`, `cc-rm`) to `~/.config/cc-pane/functions.sh`
3. Adds tmux keybindings (`prefix+L`: pick, `prefix+R`: rm) to `~/.tmux.conf`

Then add the following to your `.zshrc` or `.bashrc`:

```bash
source "$HOME/.config/cc-pane/functions.sh" # cc-pane
```

Preview changes before applying:

```bash
cc-pane setup --dry-run
```

### Verify Setup

```bash
cc-pane doctor
```

### Uninstall

```bash
cc-pane uninstall          # remove hooks, shell functions, tmux keybindings
cc-pane uninstall --purge  # also remove state files
```

## Usage

### List Sessions

```bash
cc-pane ls
```

Example output:

```
   STATE              SESSION                WIN   PANE   CWD                                      UPDATED
────────────────────────────────────────────────────────────────────────────────────────────────────
🔴 approval_waiting   work                   1     %5     ~/project                                3s ago
🟡 waiting_input      main                   0     %12    ~/src/api                                1m ago
🟢 running            main                   2     %8     ~/src/frontend                           5s ago
```

### TSV Output (for piping)

```bash
cc-pane ls --tsv
```

Tab-separated output with pane ID as the first field. Designed for piping to fzf, grep, awk, etc.

### JSON Output

```bash
cc-pane ls --json
```

### Pick and Jump (via shell function)

```bash
cc-pick
```

Or press `prefix+L` in tmux. Uses fzf with preview to select and jump to a session.

### Remove State Entries (via shell function)

```bash
cc-rm
```

Or press `prefix+R` in tmux. Uses fzf with multi-select (TAB) to remove stale entries.

### Direct Commands

```bash
cc-pane jump --pane %12       # jump to a specific pane
cc-pane rm --pane %12         # remove a specific state entry
cc-pane show --pane %12       # show state and pane output
cc-pane refresh               # clean up state files for closed panes
```

### Custom Pipelines

Combine `cc-pane ls --tsv` with any tools:

```bash
# Pick with fzf and jump
cc-pane ls --tsv | fzf --delimiter '\t' --with-nth 2.. \
  --preview 'cc-pane show --pane {1}' | cut -f1 | xargs -r cc-pane jump --pane

# List only running sessions
cc-pane ls --tsv | grep running

# Count sessions by state
cc-pane ls --tsv | cut -f2 | sort | uniq -c
```

## State Transitions

| Hook Event                         | State              | Description                              |
| ---------------------------------- | ------------------ | ---------------------------------------- |
| SessionStart                       | `waiting_input`    | Session started, waiting for first input |
| UserPromptSubmit                   | `running`          | User submitted a prompt                  |
| PreToolUse                         | `running`          | Tool is about to execute                 |
| PostToolUse                        | `running`          | Tool completed                           |
| PermissionRequest                  | `approval_waiting` | Waiting for user to approve a tool       |
| Stop                               | `waiting_input`    | Claude stopped, waiting for next input   |
| SessionEnd                         | *(file removed)*   | Session ended, state file deleted        |
| Notification (`permission_prompt`) | `approval_waiting` | Permission prompt notification           |
| Notification (`idle_prompt`)       | `waiting_input`    | Idle prompt notification                 |

## State Priority

Display order in listings (highest priority first):

1. `approval_waiting` 🔴 — Needs immediate user action
2. `waiting_input` 🟡 — Waiting for user input
3. `running` 🟢 — Actively working

## State Files

State is persisted as JSON files in `~/.cache/claude-pane-state/`.

```
~/.cache/claude-pane-state/
  main__0__12.json
  work__1__5.json
```

Filename format: `{session}__{window_index}__{pane_id}.json`

Files are created on `SessionStart` and removed on `SessionEnd`. The `refresh` command cleans up any orphaned files for panes that no longer exist in tmux.

## Environment Variables

| Variable                | Description                               | Default                       |
| ----------------------- | ----------------------------------------- | ----------------------------- |
| `CLAUDE_PANE_STATE_DIR` | State file directory                      | `~/.cache/claude-pane-state/` |
| `NO_COLOR`              | Disable color output when set             | —                             |
| `TMUX_PANE`             | Set automatically by tmux (used by hooks) | —                             |

## Design Notes

### Why Hook-Based?

- Parsing tmux pane output is fragile and breaks easily
- Claude Code hooks are an official extension point with clear event semantics
- No daemon process required

### Unix Philosophy

cc-pane outputs data (`ls --tsv`, `ls --json`, `show`), and users compose with their preferred tools (fzf, grep, jq). Shell functions (`cc-pick`, `cc-rm`) in `~/.config/cc-pane/functions.sh` are provided as convenient defaults but are fully customizable.
