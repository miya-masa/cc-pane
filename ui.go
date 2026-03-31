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

	header := fmt.Sprintf("%-3s %-18s %-14s %-5s %-6s %-20s %-30s %s",
		"", "STATE", "SESSION", "WIN", "PANE", "TITLE", "CWD", "UPDATED")
	if useColor {
		fmt.Printf("%s%s%s\n", colorBold, header, colorReset)
	} else {
		fmt.Println(header)
	}
	fmt.Println(strings.Repeat("─", 110))

	for _, ps := range states {
		icon := stateIcon(ps.State)
		cwd := shortenPath(ps.Cwd, 28)
		title := truncate(ps.PaneTitle, 18)
		updated := formatRelativeTime(ps.LastUpdatedAt)

		line := fmt.Sprintf("%-3s %-18s %-14s %-5s %-6s %-20s %-30s %s",
			icon, ps.State, ps.Session, ps.WindowIndex, ps.PaneID, title, cwd, updated)

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

// renderJSON outputs states as JSON to stdout.
func renderJSON(states []*PaneState) error {
	if states == nil {
		states = []*PaneState{}
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(states)
}
