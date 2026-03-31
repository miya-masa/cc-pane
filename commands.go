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
	states = reconcileStates(states, nil)

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
	states = reconcileStates(states, nil)

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

	// State info (reconcile with live tmux data)
	ps := findStateByPaneID(*paneID)
	if ps != nil {
		if pane, err := getPaneByID(*paneID); err == nil {
			if reconcileSingleState(ps, pane.CurrentCommand) {
				if wErr := writeState(ps); wErr != nil {
					fmt.Fprintf(os.Stderr, "warning: failed to update state for %s: %v\n", ps.PaneID, wErr)
				}
			}
		}
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

	// Also reconcile live panes (detect exited Claude Code)
	states, err := listStates()
	if err != nil {
		return err
	}
	reconcileStates(states, panes)

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

// --- setup / uninstall ---

const ccPaneMarker = "cc-pane"

// requiredHookEvents lists all Claude Code hook events cc-pane needs.
var requiredHookEvents = []string{
	"UserPromptSubmit",
	"PreToolUse",
	"PostToolUse",
	"PermissionRequest",
	"Notification",
	"Stop",
}

const tmuxKeybinding = `bind L display-popup -w 90% -h 50% -E "cc-pane pick"`

func claudeSettingsPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".claude", "settings.json")
}

func tmuxConfPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".tmux.conf")
}

func cmdSetup(args []string) error {
	fs := flag.NewFlagSet("setup", flag.ContinueOnError)
	dryRun := fs.Bool("dry-run", false, "show what would be changed without writing")
	if err := fs.Parse(args); err != nil {
		return err
	}

	anyChange := false

	// 1. Claude Code hooks
	hooksChanged, err := setupClaudeHooks(*dryRun)
	if err != nil {
		return fmt.Errorf("claude hooks: %w", err)
	}
	if hooksChanged {
		anyChange = true
	}

	// 2. tmux keybinding
	tmuxChanged, err := setupTmuxKeybinding(*dryRun)
	if err != nil {
		return fmt.Errorf("tmux config: %w", err)
	}
	if tmuxChanged {
		anyChange = true
	}

	if !anyChange {
		fmt.Println("Everything is already configured.")
	} else if *dryRun {
		fmt.Println("\nRun 'cc-pane setup' (without --dry-run) to apply.")
	} else {
		fmt.Println("\nSetup complete. Restart Claude Code sessions for hooks to take effect.")
	}
	return nil
}

func setupClaudeHooks(dryRun bool) (bool, error) {
	path := claudeSettingsPath()
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			// Create minimal settings with hooks
			data = []byte("{}")
		} else {
			return false, fmt.Errorf("read %s: %w", path, err)
		}
	}

	var settings map[string]any
	if err := json.Unmarshal(data, &settings); err != nil {
		return false, fmt.Errorf("parse %s: %w", path, err)
	}

	changed := mergeHooks(settings)
	if !changed {
		fmt.Println("  ✓ Claude Code hooks already configured")
		return false, nil
	}

	if dryRun {
		fmt.Println("  ~ Would add cc-pane hooks to", path)
		return true, nil
	}

	// Backup before writing
	backupPath := path + ".bak"
	if err := os.WriteFile(backupPath, data, 0o644); err != nil {
		return false, fmt.Errorf("backup %s: %w", backupPath, err)
	}

	out, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return false, fmt.Errorf("marshal settings: %w", err)
	}
	out = append(out, '\n')

	if err := os.WriteFile(path, out, 0o644); err != nil {
		return false, fmt.Errorf("write %s: %w", path, err)
	}

	fmt.Printf("  ✓ Added cc-pane hooks to %s (backup: %s)\n", path, backupPath)
	return true, nil
}

// mergeHooks adds cc-pane hook entries to the settings hooks map.
// Returns true if any changes were made.
func mergeHooks(settings map[string]any) bool {
	hooks, ok := settings["hooks"].(map[string]any)
	if !ok {
		hooks = map[string]any{}
		settings["hooks"] = hooks
	}

	changed := removeNullHooks(hooks)

	for _, event := range requiredHookEvents {
		entries := toSlice(hooks[event])
		if containsCCPane(entries) {
			continue
		}

		hook := map[string]any{
			"hooks": []any{
				map[string]any{
					"type":    "command",
					"command": fmt.Sprintf("cc-pane update-state --event %s", event),
					"async":   true,
				},
			},
		}
		// Notification needs a catch-all matcher
		if event == "Notification" {
			hook["matcher"] = ""
		}

		entries = append(entries, hook)
		hooks[event] = entries
		changed = true
	}
	return changed
}

// containsCCPane checks if any entry in a hooks array references cc-pane.
func containsCCPane(entries []any) bool {
	for _, entry := range entries {
		data, _ := json.Marshal(entry)
		if strings.Contains(string(data), ccPaneMarker) {
			return true
		}
	}
	return false
}

// toSlice safely converts an any value to []any.
func toSlice(v any) []any {
	if v == nil {
		return nil
	}
	if s, ok := v.([]any); ok {
		return s
	}
	return nil
}

func setupTmuxKeybinding(dryRun bool) (bool, error) {
	path := tmuxConfPath()
	data, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return false, fmt.Errorf("read %s: %w", path, err)
	}

	content := string(data)
	if strings.Contains(content, ccPaneMarker) {
		fmt.Println("  ✓ tmux keybinding already configured")
		return false, nil
	}

	if dryRun {
		fmt.Println("  ~ Would add keybinding to", path)
		return true, nil
	}

	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return false, fmt.Errorf("open %s: %w", path, err)
	}
	defer f.Close()

	line := fmt.Sprintf("\n##### cc-pane #####\n%s\n", tmuxKeybinding)
	if _, err := f.WriteString(line); err != nil {
		return false, fmt.Errorf("write %s: %w", path, err)
	}

	fmt.Printf("  ✓ Added keybinding to %s (prefix+L)\n", path)
	return true, nil
}

func cmdUninstall(args []string) error {
	fs := flag.NewFlagSet("uninstall", flag.ContinueOnError)
	purge := fs.Bool("purge", false, "also remove state directory")
	if err := fs.Parse(args); err != nil {
		return err
	}

	// 1. Remove hooks from settings.json
	if err := uninstallClaudeHooks(); err != nil {
		fmt.Fprintf(os.Stderr, "  ! claude hooks: %v\n", err)
	}

	// 2. Remove tmux keybinding
	if err := uninstallTmuxKeybinding(); err != nil {
		fmt.Fprintf(os.Stderr, "  ! tmux config: %v\n", err)
	}

	// 3. Optionally remove state directory
	if *purge {
		dir := stateDir()
		if err := os.RemoveAll(dir); err != nil {
			fmt.Fprintf(os.Stderr, "  ! remove %s: %v\n", dir, err)
		} else {
			fmt.Printf("  ✓ Removed state directory %s\n", dir)
		}
	}

	fmt.Println("\nUninstall complete.")
	return nil
}

// removeNullHooks deletes null entries from the hooks map.
func removeNullHooks(hooks map[string]any) bool {
	changed := false
	for event, val := range hooks {
		if val == nil {
			delete(hooks, event)
			changed = true
		}
	}
	return changed
}

// removeHookEntries removes all cc-pane hook entries from the hooks map.
// It also deletes keys that become empty or were already null.
// Returns true if any changes were made.
func removeHookEntries(hooks map[string]any) bool {
	changed := removeNullHooks(hooks)
	for event, val := range hooks {
		entries := toSlice(val)
		var filtered []any
		for _, entry := range entries {
			entryJSON, _ := json.Marshal(entry)
			if !strings.Contains(string(entryJSON), ccPaneMarker) {
				filtered = append(filtered, entry)
			} else {
				changed = true
			}
		}
		if len(filtered) == 0 {
			delete(hooks, event)
		} else {
			hooks[event] = filtered
		}
	}
	return changed
}

func uninstallClaudeHooks() error {
	path := claudeSettingsPath()
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}

	var settings map[string]any
	if err := json.Unmarshal(data, &settings); err != nil {
		return err
	}

	hooks, ok := settings["hooks"].(map[string]any)
	if !ok {
		fmt.Println("  ✓ No hooks to remove")
		return nil
	}

	changed := removeHookEntries(hooks)

	if !changed {
		fmt.Println("  ✓ No cc-pane hooks found")
		return nil
	}

	out, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return err
	}
	out = append(out, '\n')

	if err := os.WriteFile(path, out, 0o644); err != nil {
		return err
	}
	fmt.Printf("  ✓ Removed cc-pane hooks from %s\n", path)
	return nil
}

func uninstallTmuxKeybinding() error {
	path := tmuxConfPath()
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}

	lines := strings.Split(string(data), "\n")
	var filtered []string
	changed := false
	for _, line := range lines {
		if strings.Contains(line, ccPaneMarker) {
			changed = true
			continue
		}
		filtered = append(filtered, line)
	}

	if !changed {
		fmt.Println("  ✓ No tmux keybinding found")
		return nil
	}

	if err := os.WriteFile(path, []byte(strings.Join(filtered, "\n")), 0o644); err != nil {
		return err
	}
	fmt.Printf("  ✓ Removed cc-pane keybinding from %s\n", path)
	return nil
}
