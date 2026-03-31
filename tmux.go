package main

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// TmuxPane holds information about a tmux pane.
type TmuxPane struct {
	Session     string
	WindowIndex string
	WindowName  string
	PaneID      string
	PaneTitle   string
	Cwd         string
	Tty         string
}

const tmuxListFormat = "#{session_name}\t#{window_index}\t#{window_name}\t#{pane_id}\t#{pane_title}\t#{pane_current_path}\t#{pane_tty}"

// getCurrentPane returns info about the pane identified by $TMUX_PANE.
func getCurrentPane() (*TmuxPane, error) {
	paneID := os.Getenv("TMUX_PANE")
	if paneID == "" {
		return nil, fmt.Errorf("TMUX_PANE not set (not running inside tmux?)")
	}
	return getPaneByID(paneID)
}

// getPaneByID queries tmux for a specific pane's info.
func getPaneByID(paneID string) (*TmuxPane, error) {
	filter := fmt.Sprintf("#{==:#{pane_id},%s}", paneID)
	out, err := exec.Command("tmux", "list-panes", "-a", "-F", tmuxListFormat, "-f", filter).Output()
	if err != nil {
		return nil, fmt.Errorf("tmux list-panes: %w", err)
	}
	line := strings.TrimSpace(string(out))
	if line == "" {
		return nil, fmt.Errorf("pane %s not found", paneID)
	}
	// Take first line if multiple (shouldn't happen with unique pane_id)
	if idx := strings.IndexByte(line, '\n'); idx >= 0 {
		line = line[:idx]
	}
	return parseTmuxPaneLine(line)
}

func parseTmuxPaneLine(line string) (*TmuxPane, error) {
	parts := strings.SplitN(line, "\t", 7)
	if len(parts) < 7 {
		return nil, fmt.Errorf("unexpected tmux output format: %q", line)
	}
	return &TmuxPane{
		Session:     parts[0],
		WindowIndex: parts[1],
		WindowName:  parts[2],
		PaneID:      parts[3],
		PaneTitle:   parts[4],
		Cwd:         parts[5],
		Tty:         parts[6],
	}, nil
}

// listAllPanes returns all tmux panes across all sessions.
func listAllPanes() ([]TmuxPane, error) {
	out, err := exec.Command("tmux", "list-panes", "-a", "-F", tmuxListFormat).Output()
	if err != nil {
		return nil, fmt.Errorf("tmux list-panes: %w", err)
	}

	var panes []TmuxPane
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line == "" {
			continue
		}
		p, err := parseTmuxPaneLine(line)
		if err != nil {
			continue
		}
		panes = append(panes, *p)
	}
	return panes, nil
}

// jumpToPane switches tmux to the specified pane.
func jumpToPane(session, windowIndex, paneID string) error {
	target := fmt.Sprintf("%s:%s", session, windowIndex)

	// Switch client to target session (may fail if already in same session; that's OK)
	_ = exec.Command("tmux", "switch-client", "-t", session).Run()

	if err := exec.Command("tmux", "select-window", "-t", target).Run(); err != nil {
		return fmt.Errorf("select-window %s: %w", target, err)
	}
	if err := exec.Command("tmux", "select-pane", "-t", paneID).Run(); err != nil {
		return fmt.Errorf("select-pane %s: %w", paneID, err)
	}
	return nil
}

// getPaneContent captures the last N lines of output from a tmux pane.
func getPaneContent(paneID string, lines int) (string, error) {
	start := fmt.Sprintf("-%d", lines)
	out, err := exec.Command("tmux", "capture-pane", "-p", "-t", paneID, "-S", start).Output()
	if err != nil {
		return "", fmt.Errorf("capture-pane %s: %w", paneID, err)
	}
	return strings.TrimRight(string(out), "\n"), nil
}

// getGitBranch returns the current git branch for a directory.
func getGitBranch(dir string) string {
	if dir == "" {
		return ""
	}
	out, err := exec.Command("git", "-C", dir, "rev-parse", "--abbrev-ref", "HEAD").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// notifyApproval sends an OSC 9 notification to the pane's terminal via tmux
// DCS passthrough. This works through SSH with terminal emulators that support
// OSC 9 (iTerm2, WezTerm, Ghostty, etc.) when tmux allow-passthrough is enabled.
// Failures are silently ignored (best-effort notification).
func notifyApproval(pane *TmuxPane) {
	if pane.Tty == "" {
		return
	}
	f, err := os.OpenFile(pane.Tty, os.O_WRONLY, 0)
	if err != nil {
		return
	}
	defer f.Close()
	// DCS passthrough wraps OSC 9 so tmux forwards it to the terminal
	_, _ = f.Write([]byte("\033Ptmux;\033\033]9;🔴 cc-pane: approval needed\a\033\\"))
}

// commandVersion returns the version string of a command.
func commandVersion(name string, args ...string) (string, error) {
	out, err := exec.Command(name, args...).Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}
