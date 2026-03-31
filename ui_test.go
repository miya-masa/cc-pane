package main

import (
	"io"
	"os"
	"strings"
	"testing"
)

func TestFormatRelativeTime(t *testing.T) {
	// Valid timestamp should return something meaningful
	result := formatRelativeTime("2025-01-01T00:00:00Z")
	if result == "" {
		t.Error("formatRelativeTime returned empty string")
	}

	// Invalid timestamp should return as-is
	result = formatRelativeTime("invalid")
	if result != "invalid" {
		t.Errorf("expected 'invalid' for bad timestamp, got %q", result)
	}
}

func TestShortenPath(t *testing.T) {
	tests := []struct {
		name   string
		path   string
		maxLen int
		check  func(string) bool
	}{
		{
			name:   "short path unchanged",
			path:   "/short",
			maxLen: 20,
			check:  func(s string) bool { return s == "/short" },
		},
		{
			name:   "long path truncated",
			path:   "/very/long/path/that/exceeds/limit",
			maxLen: 15,
			check:  func(s string) bool { return len(s) <= 15 && s[:3] == "..." },
		},
		{
			name:   "exact length",
			path:   "/exact",
			maxLen: 6,
			check:  func(s string) bool { return s == "/exact" },
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := shortenPath(tt.path, tt.maxLen)
			if !tt.check(got) {
				t.Errorf("shortenPath(%q, %d) = %q", tt.path, tt.maxLen, got)
			}
		})
	}
}

func TestTruncate(t *testing.T) {
	if got := truncate("short", 10); got != "short" {
		t.Errorf("truncate short = %q", got)
	}
	if got := truncate("this is long text", 10); len(got) > 10 {
		t.Errorf("truncate long = %q (len %d)", got, len(got))
	}
}

func TestStateIcon(t *testing.T) {
	// Each state should have a distinct non-empty icon
	icons := map[string]string{}
	for _, state := range []string{StateApprovalWaiting, StateWaitingInput, StateRunning} {
		icon := stateIcon(state)
		if icon == "" {
			t.Errorf("stateIcon(%q) returned empty", state)
		}
		if prev, exists := icons[icon]; exists {
			t.Errorf("stateIcon(%q) = stateIcon(%q) = %q (should be distinct)", state, prev, icon)
		}
		icons[icon] = state
	}
}

func TestRenderTSV(t *testing.T) {
	states := []*PaneState{
		{PaneID: "%1", State: StateRunning, Session: "main", WindowIndex: "0", Cwd: "/tmp", LastUpdatedAt: "2025-01-01T00:00:00Z", Preview: "tool: Bash"},
		{PaneID: "%2", State: StateWaitingInput, Session: "dev", WindowIndex: "1", Cwd: "/home", LastUpdatedAt: "2025-01-01T00:00:00Z", Preview: "waiting for input"},
	}

	// Capture output by redirecting stdout
	old := captureStdout(func() { renderTSV(states) })

	lines := strings.Split(strings.TrimRight(old, "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("expected 2 lines, got %d: %v", len(lines), lines)
	}

	// First field should be pane_id
	fields := strings.Split(lines[0], "\t")
	if len(fields) != 7 {
		t.Fatalf("expected 7 tab-separated fields, got %d: %v", len(fields), fields)
	}
	if fields[0] != "%1" {
		t.Errorf("field[0] = %q, want %%1", fields[0])
	}
	if !strings.Contains(fields[1], StateRunning) {
		t.Errorf("field[1] = %q, should contain %q", fields[1], StateRunning)
	}
}

func captureStdout(fn func()) string {
	r, w, _ := os.Pipe()
	old := os.Stdout
	os.Stdout = w
	fn()
	w.Close()
	os.Stdout = old
	out, _ := io.ReadAll(r)
	return string(out)
}

func TestStateLabel(t *testing.T) {
	tests := []struct {
		name     string
		ps       *PaneState
		expected string
	}{
		{
			name:     "no bg agents",
			ps:       &PaneState{State: StateRunning, BackgroundAgents: 0},
			expected: StateRunning,
		},
		{
			name:     "with bg agents",
			ps:       &PaneState{State: StateRunning, BackgroundAgents: 2},
			expected: "running (+2 bg)",
		},
		{
			name:     "single bg agent",
			ps:       &PaneState{State: StateRunning, BackgroundAgents: 1},
			expected: "running (+1 bg)",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := stateLabel(tt.ps)
			if got != tt.expected {
				t.Errorf("stateLabel() = %q, want %q", got, tt.expected)
			}
		})
	}
}

func TestRenderTSV_WithBackgroundAgents(t *testing.T) {
	states := []*PaneState{
		{PaneID: "%1", State: StateRunning, Session: "main", WindowIndex: "0", Cwd: "/tmp", LastUpdatedAt: "2025-01-01T00:00:00Z", BackgroundAgents: 2},
	}

	output := captureStdout(func() { renderTSV(states) })
	if !strings.Contains(output, "(+2 bg)") {
		t.Errorf("expected TSV output to contain '(+2 bg)', got: %s", output)
	}
}

func TestStateColor(t *testing.T) {
	// approval_waiting should have red+bold
	c := stateColor(StateApprovalWaiting)
	if c == "" {
		t.Error("approval_waiting should have color")
	}

	// running should have green
	c = stateColor(StateRunning)
	if c == "" {
		t.Error("running should have color")
	}
}
