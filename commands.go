package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// hooksConfigured checks if cc-pane hooks are present in Claude Code settings.
func hooksConfigured() bool {
	home, _ := os.UserHomeDir()
	data, err := os.ReadFile(filepath.Join(home, ".claude", "settings.json"))
	if err != nil {
		return false
	}
	return strings.Contains(string(data), "cc-pane")
}

func cmdLs(args []string) error {
	fs := flag.NewFlagSet("ls", flag.ContinueOnError)
	jsonOutput := fs.Bool("json", false, "output as JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}

	states, err := listStates()
	if err != nil {
		return err
	}

	if *jsonOutput {
		return renderJSON(states)
	}
	renderTable(states, isColorTerminal())
	return nil
}

func cmdPick(_ []string) error {
	states, err := listStates()
	if err != nil {
		return err
	}

	paneID, err := runFzfPicker(states)
	if err != nil {
		return err
	}
	if paneID == "" {
		return nil // user cancelled
	}

	// Find state for this pane to get session/window info
	for _, ps := range states {
		if ps.PaneID == paneID {
			return jumpToPane(ps.Session, ps.WindowIndex, ps.PaneID)
		}
	}

	// Fallback: query tmux directly
	return jumpToPaneByID(paneID)
}

func cmdJump(args []string) error {
	fs := flag.NewFlagSet("jump", flag.ContinueOnError)
	paneID := fs.String("pane", "", "pane ID to jump to (e.g., %12)")
	if err := fs.Parse(args); err != nil {
		return err
	}

	if *paneID == "" {
		return fmt.Errorf("--pane is required (e.g., --pane %%12)")
	}
	return jumpToPaneByID(*paneID)
}

func cmdShow(args []string) error {
	fs := flag.NewFlagSet("show", flag.ContinueOnError)
	paneID := fs.String("pane", "", "pane ID to show (e.g., %12)")
	lines := fs.Int("lines", 15, "number of pane output lines to capture")
	if err := fs.Parse(args); err != nil {
		return err
	}

	if *paneID == "" {
		return fmt.Errorf("--pane is required (e.g., --pane %%12)")
	}

	// State info
	ps := findStateByPaneID(*paneID)
	if ps != nil {
		fmt.Println("--- State ---")
		fmt.Printf("state:   %s %s\n", stateIcon(ps.State), ps.State)
		fmt.Printf("session: %s:%s\n", ps.Session, ps.WindowIndex)
		fmt.Printf("pane:    %s\n", ps.PaneID)
		if ps.Cwd != "" {
			fmt.Printf("cwd:     %s\n", shortenPath(ps.Cwd, 60))
		}
		if ps.Branch != "" {
			fmt.Printf("branch:  %s\n", ps.Branch)
		}
		fmt.Printf("updated: %s\n", formatRelativeTime(ps.LastUpdatedAt))
		if ps.Preview != "" {
			fmt.Printf("preview: %s\n", ps.Preview)
		}
		fmt.Println()
	}

	// Pane output
	content, err := getPaneContent(*paneID, *lines)
	if err != nil {
		fmt.Fprintf(os.Stderr, "(could not capture pane: %v)\n", err)
		return nil
	}
	fmt.Println("--- Pane Output ---")
	fmt.Println(content)
	return nil
}

func jumpToPaneByID(paneID string) error {
	pane, err := getPaneByID(paneID)
	if err != nil {
		return err
	}
	return jumpToPane(pane.Session, pane.WindowIndex, pane.PaneID)
}

func cmdRefresh() error {
	panes, err := listAllPanes()
	if err != nil {
		return err
	}

	activeIDs := make(map[string]bool, len(panes))
	for _, p := range panes {
		activeIDs[p.PaneID] = true
	}

	removed, err := cleanStaleStates(activeIDs)
	if err != nil {
		return err
	}

	fmt.Printf("Cleaned up %d stale state file(s)\n", removed)
	return nil
}

func cmdDoctor() error {
	allOK := true

	check := func(name string, fn func() (string, bool)) {
		msg, ok := fn()
		if ok {
			fmt.Printf("  ✓ %-20s %s\n", name, msg)
		} else {
			fmt.Printf("  ✗ %-20s %s\n", name, msg)
			allOK = false
		}
	}

	fmt.Println("cc-pane doctor")
	fmt.Println(strings.Repeat("─", 50))

	check("tmux", func() (string, bool) {
		v, err := commandVersion("tmux", "-V")
		if err != nil {
			return "not found", false
		}
		return v, true
	})

	check("fzf", func() (string, bool) {
		v, err := commandVersion("fzf", "--version")
		if err != nil {
			return "not found", false
		}
		return v, true
	})

	check("jq (optional)", func() (string, bool) {
		v, err := commandVersion("jq", "--version")
		if err != nil {
			return "not found (optional, used for preview)", true
		}
		return v, true
	})

	check("tmux session", func() (string, bool) {
		if os.Getenv("TMUX") == "" {
			return "not running inside tmux", false
		}
		return "active", true
	})

	check("state directory", func() (string, bool) {
		dir := stateDir()
		info, err := os.Stat(dir)
		if err != nil {
			if os.IsNotExist(err) {
				return dir + " (will be created on first use)", true
			}
			return fmt.Sprintf("%s: %v", dir, err), false
		}
		if !info.IsDir() {
			return dir + " exists but is not a directory", false
		}
		entries, _ := os.ReadDir(dir)
		jsonCount := 0
		for _, e := range entries {
			if strings.HasSuffix(e.Name(), ".json") {
				jsonCount++
			}
		}
		return fmt.Sprintf("%s (%d state files)", dir, jsonCount), true
	})

	check("hooks", func() (string, bool) {
		if hooksConfigured() {
			return "configured in ~/.claude/settings.json", true
		}
		return "not configured (see README for hook setup)", false
	})

	fmt.Println(strings.Repeat("─", 50))
	if allOK {
		fmt.Println("All checks passed!")
	} else {
		fmt.Println("Some checks failed. See README.md for setup instructions.")
	}
	return nil
}

func cmdUpdateState(args []string) error {
	fs := flag.NewFlagSet("update-state", flag.ContinueOnError)
	event := fs.String("event", "", "hook event type")
	if err := fs.Parse(args); err != nil {
		return err
	}

	if *event == "" {
		return fmt.Errorf("--event is required")
	}

	// Read event data from stdin (piped by Claude Code hooks)
	eventData, err := io.ReadAll(os.Stdin)
	if err != nil {
		return fmt.Errorf("read stdin: %w", err)
	}

	var data map[string]any
	if len(eventData) > 0 {
		_ = json.Unmarshal(eventData, &data) // best-effort; stdin may be empty
	}

	// Get current tmux pane context
	pane, err := getCurrentPane()
	if err != nil {
		return fmt.Errorf("get pane info: %w", err)
	}

	// Determine new state from event
	newState := determineState(*event, data)
	if newState == "" {
		return nil // no state change (e.g., unrecognized Notification)
	}

	// For Stop events, check pane content to distinguish done vs waiting_input
	if *event == "Stop" && newState == StateDone {
		if content, err := getPaneContent(pane.PaneID, 3); err == nil && looksLikeQuestion(content) {
			newState = StateWaitingInput
		}
	}

	// Build preview from event data
	preview := buildPreview(*event, data)

	// Read existing state once for fallback values (branch cache, preview)
	existing := findStateByPaneID(pane.PaneID)

	if preview == "" && existing != nil {
		preview = existing.Preview
	}

	// Reuse cached branch to avoid spawning git on every hook event
	branch := ""
	if existing != nil {
		branch = existing.Branch
	}
	if branch == "" {
		branch = getGitBranch(pane.Cwd)
	}

	ps := &PaneState{
		Session:     pane.Session,
		WindowIndex: pane.WindowIndex,
		WindowName:  pane.WindowName,
		PaneID:      pane.PaneID,
		PaneTitle:   pane.PaneTitle,
		State:       newState,
		Cwd:         pane.Cwd,
		Branch:      branch,
		Preview:     preview,
	}

	return writeState(ps)
}
