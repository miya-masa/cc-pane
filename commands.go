package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"time"
)

// hooksConfigured checks if cc-pane hooks are present in Claude Code settings.
//
// Deprecated: use claudeHooksConfigured. Retained as an alias for the in-repo
// callers; renamed in cmdDoctor (Task 11) and ui.go.
func hooksConfigured() bool {
	return claudeHooksConfigured()
}

// claudeHooksConfigured reports whether cc-pane hook entries exist in
// ~/.claude/settings.json. Same substring marker strategy as before
// (spec §6.2.2 explicitly preserves Claude-side legacy detection).
func claudeHooksConfigured() bool {
	home, _ := os.UserHomeDir()
	data, err := os.ReadFile(filepath.Join(home, ".claude", "settings.json"))
	if err != nil {
		return false
	}
	return strings.Contains(string(data), "cc-pane")
}

// agentFlag is a flag.Value implementation that detects duplicate --agent
// invocations and tracks whether the flag was actually supplied. Spec §6.1
// distinguishes "absent flag" (legacy claude fallback) from "--agent ”"
// (usage error) so we cannot use the default flag.String API here.
type agentFlag struct {
	set   bool
	value string
}

func (a *agentFlag) String() string { return a.value }
func (a *agentFlag) Set(v string) error {
	if a.set {
		return fmt.Errorf("--agent specified more than once")
	}
	a.set = true
	a.value = v
	return nil
}

// applyAgentSwitchReset performs the agent-change pre-processing described in
// spec §7.3. It returns a *new* PaneState with reset fields applied so callers
// may mutate further before writeState. Background-agent counter and preview
// are cleared because those concepts do not carry across agents.
func applyAgentSwitchReset(prior *PaneState, newAgent string, pane *TmuxPane) *PaneState {
	out := *prior
	out.Agent = newAgent
	out.BackgroundAgents = 0
	out.Preview = ""
	out.LastUpdatedAt = time.Now().Format(time.RFC3339)
	if pane != nil {
		out.Session = pane.Session
		out.WindowIndex = pane.WindowIndex
		out.WindowName = pane.WindowName
		out.PaneID = pane.PaneID
		out.PaneTitle = pane.PaneTitle
		out.Cwd = pane.Cwd
		out.Branch = getGitBranch(pane.Cwd)
	}
	return &out
}

func cmdStatus() error {
	states, err := listStates()
	if err != nil {
		return err
	}
	// No cleanupDeadPanes here — status is read-only and called every few seconds by tmux.
	s := formatStatus(states)
	if s != "" {
		fmt.Print(s)
	}
	return nil
}

func cmdWatch(args []string) error {
	fs := flag.NewFlagSet("watch", flag.ContinueOnError)
	interval := fs.Duration("interval", 2*time.Second, "refresh interval")
	if err := fs.Parse(args); err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	useColor := isColorTerminal()
	timer := time.NewTimer(0) // fire immediately for first render
	defer timer.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-timer.C:
		}

		// Clear screen and move cursor to top-left
		fmt.Print("\033[H\033[2J")

		// Header with timestamp and status summary
		states, err := listStates()
		if err != nil {
			return err
		}
		states = cleanupDeadPanes(states, nil)

		status := formatStatus(states)
		header := fmt.Sprintf("cc-pane watch (updated %s, every %s)", time.Now().Format("15:04:05"), *interval)
		if status != "" {
			header += "  " + status
		}
		fmt.Println(header)
		fmt.Println()

		renderTable(states, useColor)

		timer.Reset(*interval)
	}
}

func cmdLs(args []string) error {
	fs := flag.NewFlagSet("ls", flag.ContinueOnError)
	jsonOutput := fs.Bool("json", false, "output as JSON")
	tsvOutput := fs.Bool("tsv", false, "output as TSV (for piping)")
	if err := fs.Parse(args); err != nil {
		return err
	}

	states, err := listStates()
	if err != nil {
		return err
	}
	states = cleanupDeadPanes(states, nil)

	if *jsonOutput {
		return renderJSON(states)
	}
	if *tsvOutput {
		renderTSV(states)
		return nil
	}
	renderTable(states, isColorTerminal())
	return nil
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
		fmt.Printf("state:   %s %s\n", stateIcon(ps.State), stateLabel(ps))
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

func cmdRm(args []string) error {
	fs := flag.NewFlagSet("rm", flag.ContinueOnError)
	paneID := fs.String("pane", "", "pane ID to remove (e.g., %12)")
	if err := fs.Parse(args); err != nil {
		return err
	}

	if *paneID == "" {
		return fmt.Errorf("--pane is required (e.g., --pane %%12)")
	}

	return removeStateByPaneID(*paneID)
}

func removeStateByPaneID(paneID string) error {
	ps := findStateByPaneID(paneID)
	if ps == nil {
		return fmt.Errorf("no state found for pane %s", paneID)
	}
	path := stateFilePath(ps.Session, ps.WindowIndex, ps.PaneID)
	os.Remove(path)
	fmt.Printf("Removed %s (%s:%s %s)\n", ps.PaneID, ps.Session, ps.WindowIndex, ps.State)
	return nil
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
	// Step 1: parse flags + validate --agent before any side effects.
	fs := flag.NewFlagSet("update-state", flag.ContinueOnError)
	event := fs.String("event", "", "hook event type")
	var af agentFlag
	fs.Var(&af, "agent", "agent name (claude|codex)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *event == "" {
		return fmt.Errorf("--event is required")
	}
	agent, err := normalizeAgent(af.value, af.set)
	if err != nil {
		return err
	}

	// Step 2: hook payload from stdin (best-effort JSON).
	eventData, err := io.ReadAll(os.Stdin)
	if err != nil {
		return fmt.Errorf("read stdin: %w", err)
	}
	var data map[string]any
	if len(eventData) > 0 {
		_ = json.Unmarshal(eventData, &data)
	}

	// Step 3: tmux pane context (failure leaves state untouched).
	pane, err := getCurrentPane()
	if err != nil {
		return fmt.Errorf("get pane info: %w", err)
	}

	// Step 4: SessionEnd is the special case from spec §8 — only delete when
	// the existing state belongs to this agent (or is missing entirely).
	if *event == "SessionEnd" {
		existing := findStateByPaneIDForCurrentTmux(pane)
		if existing == nil || existing.Agent == agent {
			if existing != nil {
				path := stateFilePath(existing.Session, existing.WindowIndex, existing.PaneID)
				if rerr := os.Remove(path); rerr != nil && !os.IsNotExist(rerr) {
					fmt.Fprintf(os.Stderr, "cc-pane: warn: remove %s: %v\n", path, rerr)
				}
			}
		}
		return nil
	}

	// Step 5: prior + agent-switch pre-processing (spec §7.3).
	prior := findStateByPaneIDForCurrentTmux(pane)
	if prior != nil && prior.Agent != agent {
		prior = applyAgentSwitchReset(prior, agent, pane)
	}

	// Step 6: bg counter — only claude maintains it.
	bgCount := 0
	if prior != nil && !shouldResetStaleAgents(prior) {
		bgCount = prior.BackgroundAgents
	}
	if agent == AgentClaude {
		switch {
		case *event == "UserPromptSubmit":
			bgCount = 0
		case *event == "Stop" && isUserInterrupt(data):
			bgCount = 0
		case isBackgroundAgentLaunch(*event, data):
			bgCount++
		case *event == "Notification" && bgCount > 0:
			nt, _ := data["notification_type"].(string)
			if nt != "permission_prompt" && nt != "idle_prompt" {
				bgCount--
			}
		}
		if bgCount < 0 {
			bgCount = 0
		}
	} else {
		bgCount = 0
	}

	// Step 7: state determination + fall-through / approval / preview / branch.
	newState := determineState(*event, data, prior)

	if newState == "" && prior != nil && bgCount != prior.BackgroundAgents {
		newState = prior.State
	}
	if newState == "" {
		return nil
	}

	if bgCount == 0 && hasPendingWork(prior) && newState == StateRunning {
		newState = StateWaitingInput
	}

	if newState == StateApprovalWaiting && (prior == nil || prior.State != StateApprovalWaiting) {
		notifyApproval(pane, agent)
	}

	preview := buildPreview(*event, data)
	if preview == "" && prior != nil {
		preview = prior.Preview
	}
	if *event == "Stop" && bgCount > 0 {
		preview = fmt.Sprintf("bg agents: %d running", bgCount)
	}

	branch := ""
	if prior != nil {
		branch = prior.Branch
	}
	if branch == "" {
		branch = getGitBranch(pane.Cwd)
	}

	// Step 8: write the new state (Agent must be set).
	ps := &PaneState{
		Agent:            agent,
		Session:          pane.Session,
		WindowIndex:      pane.WindowIndex,
		WindowName:       pane.WindowName,
		PaneID:           pane.PaneID,
		PaneTitle:        pane.PaneTitle,
		State:            newState,
		Cwd:              pane.Cwd,
		Branch:           branch,
		Preview:          preview,
		BackgroundAgents: bgCount,
	}
	return writeState(ps)
}

// --- setup / uninstall ---

const ccPaneMarker = "cc-pane"

// requiredHookEvents lists all Claude Code hook events cc-pane needs.
var requiredHookEvents = []string{
	"UserPromptSubmit",
	"SessionStart",
	"PreToolUse",
	"PostToolUse",
	"PermissionRequest",
	"Notification",
	"PreCompact",
	"PostCompact",
	"Stop",
	"SessionEnd",
}

const shellFunctionsContent = `# cc-pane shell functions (generated by cc-pane setup)
# Source this file from your .zshrc or .bashrc:
#   source ~/.config/cc-pane/functions.sh

cc-pick() {
  cc-pane ls --tsv | fzf --no-tmux --delimiter '\t' --with-nth 2.. \
    --preview 'cc-pane show --pane {1}' \
    --preview-window down:60%:wrap:follow | cut -f1 | xargs -r cc-pane jump --pane
}

cc-rm() {
  cc-pane ls --tsv | fzf --no-tmux --multi --delimiter '\t' --with-nth 2.. \
    --preview 'cc-pane show --pane {1}' \
    --preview-window down:60%:wrap:follow | cut -f1 | while read -r pane; do
    cc-pane rm --pane "$pane"
  done
}
`

const tmuxKeybindings = `##### cc-pane #####
bind L display-popup -w 90% -h 50% -E ". ~/.config/cc-pane/functions.sh && cc-pick"
bind R display-popup -w 90% -h 50% -E ". ~/.config/cc-pane/functions.sh && cc-rm"
set -ag status-right " #(cc-pane status)"
set -g status-interval 5
set -g allow-passthrough on`

func tmuxConfPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".tmux.conf")
}

func claudeSettingsPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".claude", "settings.json")
}

func shellFunctionsPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "cc-pane", "functions.sh")
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

	// 2. Shell functions
	shellChanged, err := setupShellFunctions(*dryRun)
	if err != nil {
		return fmt.Errorf("shell functions: %w", err)
	}
	if shellChanged {
		anyChange = true
	}

	// 3. tmux keybindings
	tmuxChanged, err := setupTmuxKeybindings(*dryRun)
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
					"command": fmt.Sprintf("cc-pane update-state --event %s --agent claude", event),
					"async":   true,
				},
			},
		}
		// Notification, PreCompact, PostCompact need a catch-all matcher
		if event == "Notification" || event == "PreCompact" || event == "PostCompact" {
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

const shellRcSourceLine = `source "$HOME/.config/cc-pane/functions.sh" # cc-pane`

func setupShellFunctions(dryRun bool) (bool, error) {
	changed := false

	// 1. Write functions file
	path := shellFunctionsPath()
	existing, err := os.ReadFile(path)
	if err != nil || string(existing) != shellFunctionsContent {
		if dryRun {
			fmt.Println("  ~ Would write shell functions to", path)
		} else {
			dir := filepath.Dir(path)
			if err := os.MkdirAll(dir, 0o755); err != nil {
				return false, fmt.Errorf("mkdir %s: %w", dir, err)
			}
			if err := os.WriteFile(path, []byte(shellFunctionsContent), 0o644); err != nil {
				return false, fmt.Errorf("write %s: %w", path, err)
			}
			fmt.Printf("  ✓ Wrote shell functions to %s\n", path)
		}
		changed = true
	} else {
		fmt.Println("  ✓ Shell functions already up to date")
	}

	// 2. Check shell rc source line
	rcConfigured := false
	for _, rcPath := range shellRcPaths() {
		data, err := os.ReadFile(rcPath)
		if err != nil {
			continue
		}
		if strings.Contains(string(data), ccPaneMarker) {
			rcConfigured = true
			break
		}
	}
	if !rcConfigured {
		fmt.Printf("\n    Add to your shell rc:\n      %s\n", shellRcSourceLine)
	}

	return changed, nil
}

// shellRcPaths returns existing shell rc files to configure.
func shellRcPaths() []string {
	home, _ := os.UserHomeDir()
	candidates := []string{
		filepath.Join(home, ".zshrc"),
		filepath.Join(home, ".bashrc"),
	}
	var paths []string
	for _, p := range candidates {
		if _, err := os.Stat(p); err == nil {
			paths = append(paths, p)
		}
	}
	return paths
}

func setupTmuxKeybindings(dryRun bool) (bool, error) {
	path := tmuxConfPath()
	data, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return false, fmt.Errorf("read %s: %w", path, err)
	}

	if strings.Contains(string(data), ccPaneMarker) {
		fmt.Println("  ✓ tmux keybindings already configured")
		return false, nil
	}

	if dryRun {
		fmt.Println("  ~ Would add keybindings to", path)
		return true, nil
	}

	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return false, fmt.Errorf("open %s: %w", path, err)
	}
	defer f.Close()

	if _, err := fmt.Fprintf(f, "\n%s\n", tmuxKeybindings); err != nil {
		return false, fmt.Errorf("write %s: %w", path, err)
	}

	fmt.Printf("  ✓ Added keybindings to %s (prefix+L: pick, prefix+R: rm)\n", path)
	return true, nil
}

func uninstallTmuxKeybindings() error {
	path := tmuxConfPath()
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
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
		fmt.Println("  ✓ No tmux keybindings found")
		return nil
	}

	if err := os.WriteFile(path, []byte(strings.Join(filtered, "\n")), 0o644); err != nil {
		return err
	}
	fmt.Printf("  ✓ Removed cc-pane keybindings from %s\n", path)
	return nil
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

	// 2. Remove shell functions
	if err := uninstallShellFunctions(); err != nil {
		fmt.Fprintf(os.Stderr, "  ! shell functions: %v\n", err)
	}

	// 3. Remove tmux keybindings
	if err := uninstallTmuxKeybindings(); err != nil {
		fmt.Fprintf(os.Stderr, "  ! tmux config: %v\n", err)
	}

	// 4. Optionally remove state directory
	if *purge {
		dir := stateDir()
		if err := os.RemoveAll(dir); err != nil {
			fmt.Fprintf(os.Stderr, "  ! remove %s: %v\n", dir, err)
		} else {
			fmt.Printf("  ✓ Removed state directory %s\n", dir)
		}
	}

	// Check if source line remains in shell rc
	for _, rcPath := range shellRcPaths() {
		data, err := os.ReadFile(rcPath)
		if err != nil {
			continue
		}
		if strings.Contains(string(data), ccPaneMarker) {
			fmt.Printf("\n    Remove from %s:\n      %s\n", rcPath, shellRcSourceLine)
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

func uninstallShellFunctions() error {
	path := shellFunctionsPath()
	if err := os.Remove(path); err != nil {
		if os.IsNotExist(err) {
			fmt.Println("  ✓ No shell functions file found")
			return nil
		}
		return err
	}
	fmt.Printf("  ✓ Removed %s\n", path)
	return nil
}
