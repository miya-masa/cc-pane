package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// State constants representing Claude Code session states.
const (
	StateRunning         = "running"
	StateWaitingInput    = "waiting_input"
	StateApprovalWaiting = "approval_waiting"
	StateIdle            = "idle"
	StateDone            = "done"
	StateUnknown         = "unknown"
)

// PaneState represents the tracked state of a Claude Code session in a tmux pane.
type PaneState struct {
	Session       string `json:"session"`
	WindowIndex   string `json:"window_index"`
	WindowName    string `json:"window_name"`
	PaneID        string `json:"pane_id"`
	PaneTitle     string `json:"pane_title"`
	State         string `json:"state"`
	LastUpdatedAt string `json:"last_updated_at"`
	Cwd           string `json:"cwd,omitempty"`
	Branch        string `json:"branch,omitempty"`
	Preview       string `json:"preview,omitempty"`
}

// StatePriority returns display priority (lower = higher priority).
func StatePriority(state string) int {
	switch state {
	case StateApprovalWaiting:
		return 0
	case StateWaitingInput:
		return 1
	case StateRunning:
		return 2
	case StateUnknown:
		return 3
	case StateIdle:
		return 4
	case StateDone:
		return 5
	default:
		return 6
	}
}

func stateDir() string {
	if dir := os.Getenv("CLAUDE_PANE_STATE_DIR"); dir != "" {
		return dir
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".cache", "claude-pane-state")
}

var (
	ensureStateDirOnce sync.Once
	ensureStateDirErr  error
)

func ensureStateDir() error {
	ensureStateDirOnce.Do(func() {
		ensureStateDirErr = os.MkdirAll(stateDir(), 0o755)
	})
	return ensureStateDirErr
}

// sanitizePaneID strips the % prefix from tmux pane IDs for safe filenames.
func sanitizePaneID(paneID string) string {
	return strings.TrimPrefix(paneID, "%")
}

func stateFilePath(session, windowIndex, paneID string) string {
	name := fmt.Sprintf("%s__%s__%s.json", session, windowIndex, sanitizePaneID(paneID))
	return filepath.Join(stateDir(), name)
}

func writeState(ps *PaneState) error {
	if err := ensureStateDir(); err != nil {
		return fmt.Errorf("create state dir: %w", err)
	}
	ps.LastUpdatedAt = time.Now().Format(time.RFC3339)

	data, err := json.MarshalIndent(ps, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal state: %w", err)
	}
	data = append(data, '\n')

	path := stateFilePath(ps.Session, ps.WindowIndex, ps.PaneID)
	return os.WriteFile(path, data, 0o644)
}

func readState(path string) (*PaneState, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var ps PaneState
	if err := json.Unmarshal(data, &ps); err != nil {
		return nil, fmt.Errorf("unmarshal %s: %w", filepath.Base(path), err)
	}
	return &ps, nil
}

func listStates() ([]*PaneState, error) {
	dir := stateDir()
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read state dir: %w", err)
	}

	var states []*PaneState
	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		ps, err := readState(filepath.Join(dir, entry.Name()))
		if err != nil {
			continue // skip corrupt files
		}
		states = append(states, ps)
	}

	sort.Slice(states, func(i, j int) bool {
		pi := StatePriority(states[i].State)
		pj := StatePriority(states[j].State)
		if pi != pj {
			return pi < pj
		}
		return states[i].LastUpdatedAt > states[j].LastUpdatedAt
	})

	return states, nil
}

// findStateByPaneID finds the existing state file for a specific pane.
func findStateByPaneID(paneID string) *PaneState {
	dir := stateDir()
	pattern := filepath.Join(dir, fmt.Sprintf("*__%s.json", sanitizePaneID(paneID)))
	matches, err := filepath.Glob(pattern)
	if err != nil || len(matches) == 0 {
		return nil
	}
	ps, err := readState(matches[0])
	if err != nil {
		return nil
	}
	return ps
}

// cleanStaleStates removes state files for panes that no longer exist.
func cleanStaleStates(activePaneIDs map[string]bool) (int, error) {
	dir := stateDir()
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, err
	}

	removed := 0
	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		ps, err := readState(path)
		if err != nil {
			os.Remove(path)
			removed++
			continue
		}
		if !activePaneIDs[ps.PaneID] {
			os.Remove(path)
			removed++
		}
	}
	return removed, nil
}

// determineState maps a hook event to a pane state.
//
// State detection relies on PermissionRequest for definitive approval_waiting,
// and Notification types (permission_prompt, idle_prompt) for additional signals.
// PreToolUse always maps to running since the actual permission check
// is handled by PermissionRequest.
func determineState(event string, data map[string]any) string {
	switch event {
	case "SessionStart":
		return StateWaitingInput
	case "UserPromptSubmit":
		return StateRunning
	case "PreToolUse", "PostToolUse":
		return StateRunning
	case "PermissionRequest":
		return StateApprovalWaiting
	case "Stop":
		return StateDone
	case "SessionEnd":
		return "" // handled specially in cmdUpdateState (removes state file)
	case "Notification":
		// Check notification type field (matches hook matcher values)
		if t, ok := data["type"].(string); ok {
			switch t {
			case "permission_prompt":
				return StateApprovalWaiting
			case "idle_prompt":
				return StateWaitingInput
			}
		}
		return "" // no state change for other notification types
	default:
		return StateUnknown
	}
}

// cleanupDeadPanes removes state files for panes that no longer exist in tmux.
// If panes is nil, it queries tmux for the current pane list.
func cleanupDeadPanes(states []*PaneState, panes []TmuxPane) []*PaneState {
	if panes == nil {
		var err error
		panes, err = listAllPanes()
		if err != nil {
			return states
		}
	}

	activeIDs := make(map[string]bool, len(panes))
	for _, p := range panes {
		activeIDs[p.PaneID] = true
	}

	var result []*PaneState
	for _, ps := range states {
		if !activeIDs[ps.PaneID] {
			path := stateFilePath(ps.Session, ps.WindowIndex, ps.PaneID)
			os.Remove(path)
			continue
		}
		result = append(result, ps)
	}
	return result
}

// looksLikeQuestion checks if pane content ends with a question.
// Used to distinguish "waiting for user answer" from "task completed".
func looksLikeQuestion(content string) bool {
	for _, line := range strings.Split(content, "\n") {
		trimmed := strings.TrimRight(line, " \t")
		if trimmed != "" && strings.HasSuffix(trimmed, "?") {
			return true
		}
	}
	return false
}

// previewMaxLen is the maximum length of preview text before truncation.
const previewMaxLen = 80

// buildPreview extracts a short preview string from hook event data.
func buildPreview(event string, data map[string]any) string {
	const maxLen = previewMaxLen

	switch event {
	case "UserPromptSubmit":
		for _, key := range []string{"prompt", "message"} {
			if v, ok := data[key].(string); ok && v != "" {
				v = strings.ReplaceAll(v, "\n", " ")
				if len(v) > maxLen {
					return v[:maxLen] + "..."
				}
				return v
			}
		}
	case "PreToolUse", "PostToolUse":
		if toolName, ok := data["tool_name"].(string); ok {
			return "tool: " + toolName
		}
	case "PermissionRequest":
		if toolName, ok := data["tool_name"].(string); ok {
			return "approval: " + toolName
		}
		return "approval requested"
	case "Stop":
		for _, key := range []string{"stop_reason", "reason"} {
			if v, ok := data[key].(string); ok && v != "" {
				return "stopped: " + v
			}
		}
		return "waiting for input"
	case "Notification":
		if msg, ok := data["message"].(string); ok && msg != "" {
			msg = strings.ReplaceAll(msg, "\n", " ")
			if len(msg) > maxLen {
				return msg[:maxLen] + "..."
			}
			return msg
		}
	}
	return ""
}
