# cc-pane

A lightweight CLI tool to list Claude Code session states across tmux panes, pick one with fzf, and jump to it.

## Overview

When running Claude Code in multiple tmux panes simultaneously, it's hard to tell which pane needs your attention — is it waiting for approval, waiting for input, or still running? cc-pane uses Claude Code hooks to record per-pane state as JSON files, then provides a CLI to list, filter, and jump to any session.

## Features

- **Lightweight**: No daemon process. State files are updated only when hooks fire.
- **Priority sorting**: `approval_waiting` > `waiting_input` > `running` — urgent items surface first.
- **fzf integration**: `cc-pane pick` opens an interactive picker and jumps to the selected pane.
- **Single Go binary**: Zero external library dependencies.
- **JSON output**: `cc-pane ls --json` for scripting and automation.

## Dependencies

| Tool | Required | Purpose |
|------|----------|---------|
| tmux | Yes      | Pane management |
| fzf  | Yes      | Interactive picker (`pick` command) |
| jq   | No       | Optional (fzf preview, scripting) |
| git  | No       | Branch name detection |

## Installation

### Build & Install

```bash
cd cc-pane
make install
```

This installs the binary to `$GOPATH/bin/cc-pane`.

### go install

```bash
go install github.com/miya-masa/cc-pane@latest
```

## Claude Code Hook Setup

Add the following to `~/.claude/settings.json`:

```json
{
  "hooks": {
    "PreToolUse": [
      {
        "matcher": "",
        "hooks": [
          {
            "type": "command",
            "command": "cc-pane update-state --event PreToolUse",
            "async": true
          }
        ]
      }
    ],
    "PostToolUse": [
      {
        "matcher": "",
        "hooks": [
          {
            "type": "command",
            "command": "cc-pane update-state --event PostToolUse",
            "async": true
          }
        ]
      }
    ],
    "PermissionRequest": [
      {
        "hooks": [
          {
            "type": "command",
            "command": "cc-pane update-state --event PermissionRequest",
            "async": true
          }
        ]
      }
    ],
    "Notification": [
      {
        "matcher": "",
        "hooks": [
          {
            "type": "command",
            "command": "cc-pane update-state --event Notification",
            "async": true
          }
        ]
      }
    ],
    "Stop": [
      {
        "matcher": "",
        "hooks": [
          {
            "type": "command",
            "command": "cc-pane update-state --event Stop",
            "async": true
          }
        ]
      }
    ],
    "UserPromptSubmit": [
      {
        "matcher": "",
        "hooks": [
          {
            "type": "command",
            "command": "cc-pane update-state --event UserPromptSubmit",
            "async": true
          }
        ]
      }
    ]
  }
}
```

> **Note**: If you already have hooks configured, add the cc-pane entries to each event's array.

### Verify Setup

```bash
cc-pane doctor
```

## Usage

### List Sessions

```bash
cc-pane ls
```

Example output:

```
   STATE              SESSION        WIN   PANE   TITLE                CWD                            UPDATED
──────────────────────────────────────────────────────────────────────────────────────────────────────────────────
🔴 approval_waiting   work           1     %5     claude-code          ~/project                      3s ago
🟡 waiting_input      main           0     %12    claude               ~/src/api                      1m ago
🟢 running            main           2     %8     claude               ~/src/frontend                 5s ago
⚪ idle               dev            0     %3     shell                ~/dotfiles                     2h ago
```

### JSON Output

```bash
cc-pane ls --json
```

### Pick and Jump with fzf

```bash
cc-pane pick
```

Sessions needing attention (approval waiting, input waiting) appear at the top. Press Enter to jump, Esc to cancel.

### Jump to a Specific Pane

```bash
cc-pane jump --pane %12
```

### Clean Up Stale State Files

```bash
cc-pane refresh
```

Removes state files for panes that no longer exist.

### Diagnostics

```bash
cc-pane doctor
```

## State Transitions

| Hook Event | State | Description |
|------------|-------|-------------|
| UserPromptSubmit | `running` | User submitted a prompt |
| PreToolUse | `running` | Tool is about to execute |
| PostToolUse | `running` | Tool completed |
| PermissionRequest | `approval_waiting` | Claude is waiting for user to approve a tool |
| Stop | `waiting_input` | Claude stopped responding, waiting for user |
| Notification (`permission_prompt`) | `approval_waiting` | Permission prompt notification |
| Notification (`idle_prompt`) | `waiting_input` | Idle prompt notification |

## State Priority

Display order in listings (highest priority first):

1. `approval_waiting` 🔴 — Needs immediate user action
2. `waiting_input` 🟡 — Waiting for user input
3. `running` 🟢 — Actively working
4. `unknown` ❓ — State could not be determined
5. `idle` ⚪ — Idle
6. `done` ✅ — Completed

## State Files

State is persisted as JSON files in `~/.cache/claude-pane-state/`.

```
~/.cache/claude-pane-state/
  main__0__12.json
  work__1__5.json
```

Filename format: `{session}__{window_index}__{pane_id}.json`

Example file contents:

```json
{
  "session": "main",
  "window_index": "0",
  "window_name": "dev",
  "pane_id": "%12",
  "pane_title": "claude-code",
  "state": "running",
  "last_updated_at": "2026-03-31T10:30:00+09:00",
  "cwd": "/home/user/project",
  "branch": "feature/auth",
  "preview": "tool: Bash"
}
```

## Environment Variables

| Variable | Description | Default |
|----------|-------------|---------|
| `CLAUDE_PANE_STATE_DIR` | State file directory | `~/.cache/claude-pane-state/` |
| `NO_COLOR` | Disable color output when set | — |
| `TMUX_PANE` | Set automatically by tmux (used by hooks) | — |

## tmux Keybinding Examples

Add to `.tmux.conf`:

```tmux
# Ctrl-b C to launch cc-pane pick
bind C run-shell -b "cc-pane pick"

# Ctrl-b L to open cc-pane pick in a popup
bind L display-popup -E "cc-pane pick"
```

## Design Notes

### Why Hook-Based?

- Parsing tmux pane output is fragile and breaks easily
- Claude Code hooks are an official extension point with clear event semantics
- No daemon process required

### Detecting approval_waiting

Detected definitively via two mechanisms:
- `PermissionRequest` event — fires when Claude Code asks the user to approve a tool
- `Notification` with type `permission_prompt` — notification-level signal for the same

Both set the state to `approval_waiting`. Any subsequent `PreToolUse` or `PostToolUse` transitions back to `running`, confirming the tool was approved.

### Future Improvements

- [ ] Configurable tool list for `approval_waiting` detection
- [ ] tmux status-line integration
- [ ] Pane output preview in fzf preview window
- [ ] State expiration (auto-transition to `unknown` after timeout)
- [ ] `watch` mode (periodic refresh and display)
- [ ] Worktree information display
