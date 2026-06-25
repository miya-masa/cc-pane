package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"
)

// ANSI color codes.
const (
	colorReset  = "\033[0m"
	colorRed    = "\033[31m"
	colorGreen  = "\033[32m"
	colorYellow = "\033[33m"
	colorDim    = "\033[2m"
	colorBold   = "\033[1m"
)

// isStaleWaiting reports whether the pane is a stale waiting_input session.
func isStaleWaiting(ps *PaneState) bool {
	if ps.State != StateWaitingInput {
		return false
	}
	t, err := time.Parse(time.RFC3339, ps.LastUpdatedAt)
	if err != nil {
		return true
	}
	return time.Since(t) > waitingInputStaleThreshold
}

// isStaleApproval reports whether the pane is a stale approval_waiting session.
// An approval_waiting pane becomes stale when it has not been updated for
// approvalWaitingStaleThreshold. This handles the case where a user cancels an
// approval dialog via Esc/Ctrl-C: Claude does not fire a Stop hook on user
// interrupts, so the state would otherwise remain approval_waiting indefinitely.
func isStaleApproval(ps *PaneState) bool {
	if ps.State != StateApprovalWaiting {
		return false
	}
	t, err := time.Parse(time.RFC3339, ps.LastUpdatedAt)
	if err != nil {
		return true
	}
	return time.Since(t) > approvalWaitingStaleThreshold
}

// effectiveState returns the display-layer state for a pane.
// Stale approval_waiting is degraded to waiting_input for all display/output/sorting
// surfaces so that the user sees a consistent picture.
// This function must ONLY be used for display, output, and sorting — never for
// writing state files or determining raw state.
func effectiveState(ps *PaneState) string {
	if isStaleApproval(ps) {
		return StateWaitingInput
	}
	return ps.State
}

// stateIcon returns a Unicode icon for the state.
func stateIcon(state string) string {
	switch state {
	case StateApprovalWaiting:
		return "🔴"
	case StateWaitingInput:
		return "🟡"
	case StateRunning:
		return "🟢"
	default:
		return "❓"
	}
}

// paneIcon returns a Unicode icon considering staleness.
func paneIcon(ps *PaneState) string {
	if isStaleWaiting(ps) {
		return "⚪"
	}
	return stateIcon(effectiveState(ps))
}

func stateColor(state string) string {
	switch state {
	case StateApprovalWaiting:
		return colorRed + colorBold
	case StateWaitingInput:
		return colorYellow
	case StateRunning:
		return colorGreen
	default:
		return ""
	}
}

// paneColor returns the ANSI color considering staleness.
func paneColor(ps *PaneState) string {
	if isStaleWaiting(ps) {
		return colorDim
	}
	return stateColor(effectiveState(ps))
}

// stateLabel returns the state string with a background agent suffix if applicable.
// Uses effectiveState so that stale approval_waiting displays as waiting_input.
func stateLabel(ps *PaneState) string {
	label := effectiveState(ps)
	if ps.BackgroundAgents > 0 {
		return fmt.Sprintf("%s (+%d bg)", label, ps.BackgroundAgents)
	}
	return label
}

// agentLabel returns the short 2-char display label for an agent.
func agentLabel(agent string) string {
	switch agent {
	case AgentClaude:
		return "CC"
	case AgentCodex:
		return "CX"
	default:
		return "??"
	}
}

func isColorTerminal() bool {
	if os.Getenv("NO_COLOR") != "" {
		return false
	}
	term := os.Getenv("TERM")
	return term != "" && term != "dumb"
}

// formatRelativeTime formats a RFC3339 timestamp as a human-readable relative time.
func formatRelativeTime(ts string) string {
	t, err := time.Parse(time.RFC3339, ts)
	if err != nil {
		return ts
	}
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds ago", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	}
}

func shortenPath(path string, maxLen int) string {
	home, _ := os.UserHomeDir()
	if home != "" {
		path = strings.Replace(path, home, "~", 1)
	}
	if len(path) <= maxLen {
		return path
	}
	return "..." + path[len(path)-maxLen+3:]
}

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	if maxLen <= 3 {
		return s[:maxLen]
	}
	return s[:maxLen-3] + "..."
}

// renderTable prints pane states as a formatted table.
func renderTable(states []*PaneState, useColor bool) {
	if len(states) == 0 {
		fmt.Println("No agent sessions found.")
		if !claudeHooksConfigured() {
			fmt.Println("\nHooks are not configured. Run 'cc-pane doctor' for setup instructions.")
		}
		return
	}

	// Emoji icons are 2 display columns but 4 bytes — avoid %-Ns formatting for them.
	// Use manual padding: icon + space, then fixed-width ASCII columns.
	header := fmt.Sprintf("   %-18s %-6s %-22s %-14s %-6s %-40s %s",
		"STATE", "AGENT", "SESSION", "WINDOW", "PANE", "CWD", "UPDATED")
	if useColor {
		fmt.Printf("%s%s%s\n", colorBold, header, colorReset)
	} else {
		fmt.Println(header)
	}
	fmt.Println(strings.Repeat("─", len(header)))

	for _, ps := range states {
		icon := paneIcon(ps)
		cwd := shortenPath(ps.Cwd, 38)
		updated := formatRelativeTime(ps.LastUpdatedAt)
		session := truncate(ps.Session, 20)
		label := stateLabel(ps)
		agent := agentLabel(ps.Agent)

		win := truncate(ps.WindowIndex+":"+ps.WindowName, 12)
		line := fmt.Sprintf("%s %-18s %-6s %-22s %-14s %-6s %-40s %s",
			icon, label, agent, session, win, ps.PaneID, cwd, updated)

		if useColor {
			c := paneColor(ps)
			if c != "" {
				fmt.Printf("%s%s%s\n", c, line, colorReset)
			} else {
				fmt.Println(line)
			}
		} else {
			fmt.Println(line)
		}
	}
}

// renderTSV outputs states as tab-separated values for piping to other tools.
// Field 1 is pane_id (for extraction), field 2 is the short agent label
// (CC / CX / ??), remaining fields are padded for display. Downstream
// pipelines that need the canonical agent name (claude / codex / unknown)
// should use --json which preserves it via the JSON tag.
func renderTSV(states []*PaneState) {
	for _, ps := range states {
		icon := paneIcon(ps)
		cwd := shortenPath(ps.Cwd, 40)
		updated := formatRelativeTime(ps.LastUpdatedAt)
		session := truncate(ps.Session, 22)
		preview := truncate(ps.Preview, 40)
		label := stateLabel(ps)
		agent := agentLabel(ps.Agent)

		win := truncate(ps.WindowIndex+":"+ps.WindowName, 14)
		fmt.Fprintf(os.Stdout, "%s\t%s\t%s %-16s\t%-22s\t%-14s\t%-42s\t%-10s\t%s\n",
			ps.PaneID, agent, icon, label, session, win, cwd, updated, preview)
	}
}

// formatStatus returns a compact summary string for tmux status-right.
// Example: "🔴1 🟢3 🟡2 ⚪1"
func formatStatus(states []*PaneState) string {
	var approval, running, waiting, stale int
	for _, ps := range states {
		switch effectiveState(ps) {
		case StateApprovalWaiting:
			approval++
		case StateRunning:
			running++
		case StateWaitingInput:
			if isStaleWaiting(ps) {
				stale++
			} else {
				waiting++
			}
		}
	}

	var parts []string
	if approval > 0 {
		parts = append(parts, fmt.Sprintf("🔴%d", approval))
	}
	if waiting > 0 {
		parts = append(parts, fmt.Sprintf("🟡%d", waiting))
	}
	if running > 0 {
		parts = append(parts, fmt.Sprintf("🟢%d", running))
	}
	if stale > 0 {
		parts = append(parts, fmt.Sprintf("⚪%d", stale))
	}
	return strings.Join(parts, " ")
}

// renderJSON outputs states as JSON to stdout.
// Applies effectiveState to each pane so that stale approval_waiting is output
// as waiting_input. Original PaneState values are never mutated.
func renderJSON(states []*PaneState) error {
	if states == nil {
		states = []*PaneState{}
	}
	out := make([]*PaneState, len(states))
	for i, ps := range states {
		copy := *ps
		copy.State = effectiveState(ps)
		out[i] = &copy
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(out)
}
