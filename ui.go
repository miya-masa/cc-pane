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
	colorGray   = "\033[90m"
	colorBold   = "\033[1m"
)

// stateIcon returns a Unicode icon for the state.
func stateIcon(state string) string {
	switch state {
	case StateApprovalWaiting:
		return "🔴"
	case StateWaitingInput:
		return "🟡"
	case StateRunning:
		return "🟢"
	case StateIdle:
		return "⚪"
	case StateDone:
		return "✅"
	default:
		return "❓"
	}
}

func stateColor(state string) string {
	switch state {
	case StateApprovalWaiting:
		return colorRed + colorBold
	case StateWaitingInput:
		return colorYellow
	case StateRunning:
		return colorGreen
	case StateIdle, StateDone:
		return colorGray
	default:
		return ""
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
		fmt.Println("No Claude Code sessions found.")
		if !hooksConfigured() {
			fmt.Println("\nHooks are not configured. Run 'cc-pane doctor' for setup instructions.")
		}
		return
	}

	// Emoji icons are 2 display columns but 4 bytes — avoid %-Ns formatting for them.
	// Use manual padding: icon + space, then fixed-width ASCII columns.
	header := fmt.Sprintf("   %-18s %-22s %-5s %-6s %-40s %s",
		"STATE", "SESSION", "WIN", "PANE", "CWD", "UPDATED")
	if useColor {
		fmt.Printf("%s%s%s\n", colorBold, header, colorReset)
	} else {
		fmt.Println(header)
	}
	fmt.Println(strings.Repeat("─", 100))

	for _, ps := range states {
		icon := stateIcon(ps.State)
		cwd := shortenPath(ps.Cwd, 38)
		updated := formatRelativeTime(ps.LastUpdatedAt)
		session := truncate(ps.Session, 20)

		line := fmt.Sprintf("%s %-18s %-22s %-5s %-6s %-40s %s",
			icon, ps.State, session, ps.WindowIndex, ps.PaneID, cwd, updated)

		if useColor {
			c := stateColor(ps.State)
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
// First field is pane_id (for extraction), remaining fields are padded for display.
func renderTSV(states []*PaneState) {
	for _, ps := range states {
		icon := stateIcon(ps.State)
		cwd := shortenPath(ps.Cwd, 40)
		updated := formatRelativeTime(ps.LastUpdatedAt)
		session := truncate(ps.Session, 22)
		preview := truncate(ps.Preview, 40)

		fmt.Fprintf(os.Stdout, "%s\t%s %-16s\t%-22s\t%-5s\t%-42s\t%-10s\t%s\n",
			ps.PaneID, icon, ps.State, session, ps.WindowIndex, cwd, updated, preview)
	}
}

// renderJSON outputs states as JSON to stdout.
func renderJSON(states []*PaneState) error {
	if states == nil {
		states = []*PaneState{}
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(states)
}
