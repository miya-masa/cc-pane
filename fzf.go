package main

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// runFzfPicker shows states in fzf and returns the selected pane ID.
// Returns empty string if user cancelled (Esc or no selection).
func runFzfPicker(states []*PaneState) (string, error) {
	if len(states) == 0 {
		return "", fmt.Errorf("no sessions to pick from")
	}

	// Build fzf input: tab-separated with pane_id as hidden first field
	var lines []string
	for _, ps := range states {
		icon := stateIcon(ps.State)
		cwd := shortenPath(ps.Cwd, 28)
		title := truncate(ps.PaneTitle, 18)
		updated := formatRelativeTime(ps.LastUpdatedAt)
		preview := truncate(ps.Preview, 40)

		// First field (before tab) is the pane_id for extraction; rest is display
		line := fmt.Sprintf("%s\t%s %-18s %-14s %s:%-5s %-20s %-28s %-10s %s",
			ps.PaneID, icon, ps.State, ps.Session,
			ps.WindowIndex, ps.PaneID, title, cwd, updated, preview)
		lines = append(lines, line)
	}

	input := strings.Join(lines, "\n")

	header := fmt.Sprintf("  %-18s %-14s %-8s %-20s %-28s %-10s %s",
		"STATE", "SESSION", "WIN:PANE", "TITLE", "CWD", "UPDATED", "PREVIEW")

	cmd := exec.Command("fzf",
		"--ansi",
		"--no-sort",
		"--delimiter", "\t",
		"--with-nth", "2..", // hide the pane_id extraction field
		"--header", header,
		"--prompt", "cc-pane > ",
		"--reverse",
		"--height", "40%",
		"--min-height", "10",
	)
	cmd.Stdin = strings.NewReader(input)
	cmd.Stderr = os.Stderr

	out, err := cmd.Output()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			code := exitErr.ExitCode()
			if code == 130 || code == 1 {
				return "", nil // Esc or no match
			}
		}
		return "", fmt.Errorf("fzf: %w", err)
	}

	selected := strings.TrimSpace(string(out))
	if selected == "" {
		return "", nil
	}

	// Extract pane_id: first field before tab
	if paneID, _, ok := strings.Cut(selected, "\t"); ok {
		return paneID, nil
	}
	return strings.Fields(selected)[0], nil
}
