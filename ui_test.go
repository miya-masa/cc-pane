package main

import "testing"

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
	for _, state := range []string{StateApprovalWaiting, StateWaitingInput, StateRunning, StateIdle, StateDone, StateUnknown} {
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
