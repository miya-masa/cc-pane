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
	"unicode/utf8"
)

// State constants representing Claude Code session states.
const (
	StateRunning         = "running"
	StateWaitingInput    = "waiting_input"
	StateApprovalWaiting = "approval_waiting"
)

// Agent constants identifying which CLI is being tracked.
const (
	AgentClaude  = "claude"
	AgentCodex   = "codex"
	AgentUnknown = "unknown"
)

// PaneState represents the tracked state of an agent session in a tmux pane.
type PaneState struct {
	Agent            string `json:"agent"`
	Session          string `json:"session"`
	WindowIndex      string `json:"window_index"`
	WindowName       string `json:"window_name"`
	PaneID           string `json:"pane_id"`
	PaneTitle        string `json:"pane_title"`
	State            string `json:"state"`
	LastUpdatedAt    string `json:"last_updated_at"`
	Cwd              string `json:"cwd,omitempty"`
	Branch           string `json:"branch,omitempty"`
	Preview          string `json:"preview,omitempty"`
	BackgroundAgents int    `json:"background_agents,omitempty"`
}

// normalizeAgent applies the agent normalization rules.
// flagPresent indicates whether --agent was actually supplied on the command
// line. When the flag is absent the call defaults to claude (legacy behavior).
// An empty value with flagPresent=true is a usage error.
func normalizeAgent(raw string, flagPresent bool) (string, error) {
	if !flagPresent {
		return AgentClaude, nil
	}
	switch raw {
	case AgentClaude, AgentCodex:
		return raw, nil
	case "":
		return "", fmt.Errorf("--agent value cannot be empty")
	default:
		return AgentUnknown, nil
	}
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
	default:
		return 3
	}
}

// waitingInputStaleThreshold is the duration after which a waiting_input session
// is considered stale and sorted below running sessions.
const waitingInputStaleThreshold = 10 * time.Minute

// sortPriority returns display priority considering both state and staleness.
// Stale waiting_input sessions are ranked below running sessions.
func sortPriority(ps *PaneState) int {
	switch ps.State {
	case StateApprovalWaiting:
		return 0
	case StateWaitingInput:
		t, err := time.Parse(time.RFC3339, ps.LastUpdatedAt)
		if err != nil || time.Since(t) > waitingInputStaleThreshold {
			return 3 // stale
		}
		return 1 // recent
	case StateRunning:
		return 2
	default:
		return 4
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
	switch ps.Agent {
	case AgentClaude, AgentCodex, AgentUnknown:
	default:
		return fmt.Errorf("writeState: invalid Agent %q", ps.Agent)
	}
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
	if ps.Agent == "" {
		ps.Agent = AgentClaude // legacy fallback for state files written before the agent field existed
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
		pi := sortPriority(states[i])
		pj := sortPriority(states[j])
		if pi != pj {
			return pi < pj
		}
		return states[i].LastUpdatedAt > states[j].LastUpdatedAt
	})

	return states, nil
}

// findStateByPaneID returns the state for a specific pane_id. When multiple
// state files share the same pane_id (tmux can recycle pane ids across
// sessions), prefer the newest LastUpdatedAt. Used by callers without tmux
// session/window context (cmdShow / cmdRm). update-state should use
// findStateByPaneIDForCurrentTmux instead.
func findStateByPaneID(paneID string) *PaneState {
	dir := stateDir()
	pattern := filepath.Join(dir, fmt.Sprintf("*__%s.json", sanitizePaneID(paneID)))
	matches, err := filepath.Glob(pattern)
	if err != nil || len(matches) == 0 {
		return nil
	}
	var best *PaneState
	for _, m := range matches {
		ps, err := readState(m)
		if err != nil {
			continue
		}
		if best == nil {
			best = ps
			continue
		}
		if ps.LastUpdatedAt > best.LastUpdatedAt {
			best = ps
		}
	}
	return best
}

// findStateByPaneIDForCurrentTmux is the update-state-aware variant: it
// prefers state files whose session/window matches the supplied tmux pane,
// falling back to the newest LastUpdatedAt when no exact match exists.
func findStateByPaneIDForCurrentTmux(pane *TmuxPane) *PaneState {
	if pane == nil {
		return nil
	}
	dir := stateDir()
	pattern := filepath.Join(dir, fmt.Sprintf("*__%s.json", sanitizePaneID(pane.PaneID)))
	matches, err := filepath.Glob(pattern)
	if err != nil || len(matches) == 0 {
		return nil
	}
	var best *PaneState
	var bestExact bool
	for _, m := range matches {
		ps, err := readState(m)
		if err != nil {
			continue
		}
		exact := ps.Session == pane.Session && ps.WindowIndex == pane.WindowIndex
		if best == nil {
			best = ps
			bestExact = exact
			continue
		}
		if exact && !bestExact {
			best = ps
			bestExact = true
			continue
		}
		if exact == bestExact && ps.LastUpdatedAt > best.LastUpdatedAt {
			best = ps
		}
	}
	return best
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
//
// When existing is non-nil and has pending background agents, Stop keeps the
// state as running instead of transitioning to waiting_input.
func determineState(event string, data map[string]any, existing *PaneState) string {
	switch event {
	case "SessionStart":
		return StateWaitingInput
	case "UserPromptSubmit":
		return StateRunning
	case "PreToolUse", "PostToolUse":
		if event == "PostToolUse" {
			if toolName, _ := data["tool_name"].(string); toolName == "ExitPlanMode" {
				return StateApprovalWaiting
			}
		}
		return StateRunning
	case "PreCompact", "PostCompact":
		return StateRunning
	case "PermissionRequest":
		return StateApprovalWaiting
	case "Stop":
		if isUserInterrupt(data) {
			return StateWaitingInput
		}
		if hasPendingWork(existing) {
			return StateRunning
		}
		return StateWaitingInput
	case "SessionEnd":
		return "" // handled specially in cmdUpdateState (removes state file)
	case "Notification":
		// Check notification type field (matches hook matcher values)
		if t, ok := data["notification_type"].(string); ok {
			switch t {
			case "permission_prompt":
				return StateApprovalWaiting
			case "idle_prompt":
				if hasPendingWork(existing) {
					return StateRunning
				}
				return StateWaitingInput
			}
		}
		return "" // no state change for other notification types
	default:
		return "" // unknown events are ignored
	}
}

// backgroundAgentTimeout is the maximum duration to keep pending background agent
// counts before resetting. Prevents stuck "running" state if an agent crashes
// without sending a completion notification.
const backgroundAgentTimeout = 30 * time.Minute

// isBackgroundAgentLaunch checks if a PostToolUse event represents a background
// agent dispatch. It looks for tool_name "Agent" with tool_input.run_in_background
// set to true.
func isBackgroundAgentLaunch(event string, data map[string]any) bool {
	if event != "PostToolUse" {
		return false
	}
	toolName, _ := data["tool_name"].(string)
	if toolName != "Agent" {
		return false
	}
	toolInput, ok := data["tool_input"].(map[string]any)
	if !ok {
		return false
	}
	bg, _ := toolInput["run_in_background"].(bool)
	return bg
}

// isUserInterrupt checks if a Stop event was triggered by the user (Escape key).
func isUserInterrupt(data map[string]any) bool {
	reason, _ := data["stop_reason"].(string)
	return reason == "user_interrupt"
}

// hasPendingWork reports whether the pane has outstanding background work.
func hasPendingWork(ps *PaneState) bool {
	return ps != nil && ps.BackgroundAgents > 0
}

// shouldResetStaleAgents returns true if background agent tracking has been
// stale for longer than backgroundAgentTimeout. Background agents are a
// claude-only concept (codex / unknown never accumulate the counter), so
// stale detection only fires on claude states.
func shouldResetStaleAgents(ps *PaneState) bool {
	if ps == nil || ps.BackgroundAgents <= 0 {
		return false
	}
	if ps.Agent != AgentClaude {
		return false
	}
	t, err := time.Parse(time.RFC3339, ps.LastUpdatedAt)
	if err != nil {
		return true // can't parse timestamp, reset to be safe
	}
	return time.Since(t) > backgroundAgentTimeout
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

func overlayLiveCodexPanes(states []*PaneState, panes []TmuxPane, now time.Time) []*PaneState {
	if len(panes) == 0 {
		return states
	}

	byPaneID := make(map[string]int, len(states))
	for i, ps := range states {
		byPaneID[ps.PaneID] = i
	}

	for _, pane := range panes {
		if !isCodexPane(pane) {
			continue
		}

		ps := newLiveCodexState(pane, now)

		if idx, ok := byPaneID[pane.PaneID]; ok {
			mergeExistingLiveCodexState(ps, states[idx], pane)
			states[idx] = ps
			continue
		}

		byPaneID[pane.PaneID] = len(states)
		states = append(states, ps)
	}

	sort.Slice(states, func(i, j int) bool {
		pi := sortPriority(states[i])
		pj := sortPriority(states[j])
		if pi != pj {
			return pi < pj
		}
		return states[i].LastUpdatedAt > states[j].LastUpdatedAt
	})

	return states
}

func newLiveCodexState(pane TmuxPane, now time.Time) *PaneState {
	return &PaneState{
		Agent:         AgentCodex,
		Session:       pane.Session,
		WindowIndex:   pane.WindowIndex,
		WindowName:    pane.WindowName,
		PaneID:        pane.PaneID,
		PaneTitle:     pane.PaneTitle,
		State:         codexLiveState(pane),
		LastUpdatedAt: now.Format(time.RFC3339),
		Cwd:           pane.Cwd,
		Branch:        getGitBranch(pane.Cwd),
	}
}

func mergeExistingLiveCodexState(ps, existing *PaneState, pane TmuxPane) {
	ps.Branch = existing.Branch
	ps.Preview = existing.Preview
	if existing.Agent == AgentCodex && existing.State == ps.State {
		ps.LastUpdatedAt = existing.LastUpdatedAt
		return
	}
	ps.Preview = ""
	if ps.Branch == "" {
		ps.Branch = getGitBranch(pane.Cwd)
	}
}

func isCodexPane(pane TmuxPane) bool {
	if pane.CurrentCommand == "codex" {
		return true
	}
	if pane.CurrentCommand == "node" || pane.CurrentCommand == "node-MainThread" {
		return paneHasCodexProcess(pane.Tty)
	}
	return false
}

func codexLiveState(pane TmuxPane) string {
	if paneHasCodexApprovalPrompt(pane.PaneID) {
		return StateApprovalWaiting
	}
	if hasCodexRunningTitle(pane.PaneTitle) {
		return StateRunning
	}
	return StateWaitingInput
}

func hasCodexRunningTitle(title string) bool {
	r, _ := utf8.DecodeRuneInString(strings.TrimSpace(title))
	return r >= '\u2801' && r <= '\u28ff'
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
	case "PreCompact":
		return "compacting context"
	case "PostCompact":
		return "compaction complete"
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
