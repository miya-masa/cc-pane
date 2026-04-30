package main

import (
	"encoding/json"
	"io"
	"os"
	"strings"
	"testing"
	"time"
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
		{Agent: AgentClaude, PaneID: "%1", State: StateRunning, Session: "main", WindowIndex: "0", Cwd: "/tmp", LastUpdatedAt: "2025-01-01T00:00:00Z", Preview: "tool: Bash"},
		{Agent: AgentClaude, PaneID: "%2", State: StateWaitingInput, Session: "dev", WindowIndex: "1", Cwd: "/home", LastUpdatedAt: "2025-01-01T00:00:00Z", Preview: "waiting for input"},
	}

	// Capture output by redirecting stdout
	old := captureStdout(func() { renderTSV(states) })

	lines := strings.Split(strings.TrimRight(old, "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("expected 2 lines, got %d: %v", len(lines), lines)
	}

	// Field 1 = pane_id, field 2 = agent label, field 3 = icon + state.
	fields := strings.Split(lines[0], "\t")
	if len(fields) != 8 {
		t.Fatalf("expected 8 tab-separated fields, got %d: %v", len(fields), fields)
	}
	if fields[0] != "%1" {
		t.Errorf("field[0] = %q, want %%1", fields[0])
	}
	if fields[1] != "CC" {
		t.Errorf("field[1] = %q, want CC (agent label)", fields[1])
	}
	if !strings.Contains(fields[2], StateRunning) {
		t.Errorf("field[2] = %q, should contain %q", fields[2], StateRunning)
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
		{Agent: AgentClaude, PaneID: "%1", State: StateRunning, Session: "main", WindowIndex: "0", Cwd: "/tmp", LastUpdatedAt: "2025-01-01T00:00:00Z", BackgroundAgents: 2},
	}

	output := captureStdout(func() { renderTSV(states) })
	if !strings.Contains(output, "(+2 bg)") {
		t.Errorf("expected TSV output to contain '(+2 bg)', got: %s", output)
	}
}

func TestAgentLabel(t *testing.T) {
	cases := []struct {
		agent string
		want  string
	}{
		{AgentClaude, "CC"},
		{AgentCodex, "CX"},
		{AgentUnknown, "??"},
		{"", "??"},
		{"gemini", "??"},
	}
	for _, c := range cases {
		got := agentLabel(c.agent)
		if got != c.want {
			t.Errorf("agentLabel(%q) = %q, want %q", c.agent, got, c.want)
		}
	}
}

func TestRenderTSVIncludesAgent(t *testing.T) {
	states := []*PaneState{
		{Agent: AgentCodex, Session: "s", WindowIndex: "0", WindowName: "w", PaneID: "%1", State: StateRunning, LastUpdatedAt: time.Now().Format(time.RFC3339)},
	}
	out := captureStdout(func() { renderTSV(states) })
	if !strings.HasPrefix(out, "%1\t") {
		t.Errorf("pane_id should be first field: %q", out)
	}
	fields := strings.SplitN(strings.TrimRight(out, "\n"), "\t", 3)
	if len(fields) < 2 {
		t.Fatalf("expected at least 2 fields, got %d: %q", len(fields), out)
	}
	if fields[1] != "CX" {
		t.Errorf("2nd field should be 'CX' (agentLabel), got %q", fields[1])
	}
}

func TestRenderTableHasAgentColumn(t *testing.T) {
	states := []*PaneState{
		{Agent: AgentClaude, Session: "s", WindowIndex: "0", WindowName: "w", PaneID: "%1", State: StateRunning, LastUpdatedAt: time.Now().Format(time.RFC3339)},
	}
	out := captureStdout(func() { renderTable(states, false) })
	if !strings.Contains(out, "AGENT") {
		t.Errorf("table header should contain 'AGENT': %q", out)
	}
	if !strings.Contains(out, "CC") {
		t.Errorf("Claude row should display 'CC' label: %q", out)
	}
}

func TestJSONOutputIncludesAgent(t *testing.T) {
	ps := &PaneState{Agent: AgentCodex, Session: "s", WindowIndex: "0", PaneID: "%1", State: StateRunning, LastUpdatedAt: "2026-04-30T00:00:00Z"}
	data, err := json.Marshal(ps)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"agent":"codex"`) {
		t.Errorf("agent field missing or wrong: %s", data)
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

func TestFormatStatus(t *testing.T) {
	now := time.Now().Format(time.RFC3339)
	stale := time.Now().Add(-11 * time.Minute).Format(time.RFC3339)

	tests := []struct {
		name     string
		states   []*PaneState
		expected string
	}{
		{
			name:     "empty",
			states:   nil,
			expected: "",
		},
		{
			name: "all types",
			states: []*PaneState{
				{State: StateApprovalWaiting, LastUpdatedAt: now},
				{State: StateRunning, LastUpdatedAt: now},
				{State: StateWaitingInput, LastUpdatedAt: now},
				{State: StateWaitingInput, LastUpdatedAt: stale},
			},
			expected: "🔴1 🟡1 🟢1 ⚪1",
		},
		{
			name: "only running",
			states: []*PaneState{
				{State: StateRunning, LastUpdatedAt: now},
				{State: StateRunning, LastUpdatedAt: now},
			},
			expected: "🟢2",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := formatStatus(tt.states)
			if got != tt.expected {
				t.Errorf("formatStatus() = %q, want %q", got, tt.expected)
			}
		})
	}
}
