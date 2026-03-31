package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestStatePriority(t *testing.T) {
	tests := []struct {
		state    string
		expected int
	}{
		{StateApprovalWaiting, 0},
		{StateWaitingInput, 1},
		{StateRunning, 2},
		{StateUnknown, 3},
		{StateIdle, 4},
		{StateDone, 5},
	}

	for _, tt := range tests {
		if got := StatePriority(tt.state); got != tt.expected {
			t.Errorf("StatePriority(%q) = %d, want %d", tt.state, got, tt.expected)
		}
	}

	// approval_waiting must have strictly higher priority than all others
	if StatePriority(StateApprovalWaiting) >= StatePriority(StateWaitingInput) {
		t.Error("approval_waiting should be higher priority than waiting_input")
	}
	if StatePriority(StateWaitingInput) >= StatePriority(StateRunning) {
		t.Error("waiting_input should be higher priority than running")
	}
}

func TestSanitizePaneID(t *testing.T) {
	tests := []struct {
		input, expected string
	}{
		{"%12", "12"},
		{"%0", "0"},
		{"12", "12"},
		{"", ""},
	}
	for _, tt := range tests {
		if got := sanitizePaneID(tt.input); got != tt.expected {
			t.Errorf("sanitizePaneID(%q) = %q, want %q", tt.input, got, tt.expected)
		}
	}
}

func TestWriteAndReadState(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CLAUDE_PANE_STATE_DIR", dir)

	ps := &PaneState{
		Session:     "main",
		WindowIndex: "0",
		WindowName:  "dev",
		PaneID:      "%12",
		PaneTitle:   "claude-code",
		State:       StateRunning,
		Cwd:         "/tmp/test",
	}

	if err := writeState(ps); err != nil {
		t.Fatalf("writeState: %v", err)
	}

	path := stateFilePath("main", "0", "%12")
	got, err := readState(path)
	if err != nil {
		t.Fatalf("readState: %v", err)
	}

	if got.Session != "main" {
		t.Errorf("Session = %q, want %q", got.Session, "main")
	}
	if got.PaneID != "%12" {
		t.Errorf("PaneID = %q, want %q", got.PaneID, "%12")
	}
	if got.State != StateRunning {
		t.Errorf("State = %q, want %q", got.State, StateRunning)
	}
	if got.LastUpdatedAt == "" {
		t.Error("LastUpdatedAt should be set")
	}
}

func TestStateFilePathFormat(t *testing.T) {
	t.Setenv("CLAUDE_PANE_STATE_DIR", "/tmp/test-state")

	path := stateFilePath("mysession", "2", "%42")
	expected := "/tmp/test-state/mysession__2__42.json"
	if path != expected {
		t.Errorf("stateFilePath = %q, want %q", path, expected)
	}
}

func TestListStates_SortedByPriority(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CLAUDE_PANE_STATE_DIR", dir)

	// Write states in non-priority order
	states := []*PaneState{
		{Session: "s", WindowIndex: "0", PaneID: "%1", State: StateRunning, Cwd: "/tmp"},
		{Session: "s", WindowIndex: "1", PaneID: "%2", State: StateApprovalWaiting, Cwd: "/tmp"},
		{Session: "s", WindowIndex: "2", PaneID: "%3", State: StateWaitingInput, Cwd: "/tmp"},
		{Session: "s", WindowIndex: "3", PaneID: "%4", State: StateDone, Cwd: "/tmp"},
	}

	for _, ps := range states {
		if err := writeState(ps); err != nil {
			t.Fatalf("writeState: %v", err)
		}
	}

	got, err := listStates()
	if err != nil {
		t.Fatalf("listStates: %v", err)
	}
	if len(got) != 4 {
		t.Fatalf("expected 4 states, got %d", len(got))
	}

	expectedOrder := []string{StateApprovalWaiting, StateWaitingInput, StateRunning, StateDone}
	for i, want := range expectedOrder {
		if got[i].State != want {
			t.Errorf("position %d: state = %q, want %q", i, got[i].State, want)
		}
	}
}

func TestListStates_EmptyDir(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CLAUDE_PANE_STATE_DIR", dir)

	got, err := listStates()
	if err != nil {
		t.Fatalf("listStates: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("expected 0 states, got %d", len(got))
	}
}

func TestListStates_NonexistentDir(t *testing.T) {
	t.Setenv("CLAUDE_PANE_STATE_DIR", "/tmp/nonexistent-claude-pane-test-"+t.Name())

	got, err := listStates()
	if err != nil {
		t.Fatalf("listStates: %v", err)
	}
	if got != nil {
		t.Errorf("expected nil, got %v", got)
	}
}

func TestCleanStaleStates(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CLAUDE_PANE_STATE_DIR", dir)

	states := []*PaneState{
		{Session: "s", WindowIndex: "0", PaneID: "%1", State: StateRunning, Cwd: "/tmp"},
		{Session: "s", WindowIndex: "1", PaneID: "%2", State: StateIdle, Cwd: "/tmp"},
		{Session: "s", WindowIndex: "2", PaneID: "%3", State: StateDone, Cwd: "/tmp"},
	}
	for _, ps := range states {
		writeState(ps)
	}

	// Only %1 is still active
	active := map[string]bool{"%1": true}
	removed, err := cleanStaleStates(active)
	if err != nil {
		t.Fatalf("cleanStaleStates: %v", err)
	}
	if removed != 2 {
		t.Errorf("expected 2 removed, got %d", removed)
	}

	remaining, _ := listStates()
	if len(remaining) != 1 {
		t.Fatalf("expected 1 remaining, got %d", len(remaining))
	}
	if remaining[0].PaneID != "%1" {
		t.Errorf("remaining pane should be %%1, got %s", remaining[0].PaneID)
	}
}

func TestCleanStaleStates_RemovesCorruptFiles(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CLAUDE_PANE_STATE_DIR", dir)

	// Write a corrupt JSON file
	corruptPath := filepath.Join(dir, "corrupt__0__99.json")
	os.WriteFile(corruptPath, []byte("not json"), 0o644)

	removed, err := cleanStaleStates(map[string]bool{})
	if err != nil {
		t.Fatalf("cleanStaleStates: %v", err)
	}
	if removed != 1 {
		t.Errorf("expected 1 removed (corrupt), got %d", removed)
	}
}

func TestFindStateByPaneID(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CLAUDE_PANE_STATE_DIR", dir)

	ps := &PaneState{
		Session:     "main",
		WindowIndex: "0",
		PaneID:      "%42",
		State:       StateRunning,
		Cwd:         "/home/test",
		Preview:     "doing stuff",
	}
	writeState(ps)

	got := findStateByPaneID("%42")
	if got == nil {
		t.Fatal("expected to find state, got nil")
	}
	if got.Preview != "doing stuff" {
		t.Errorf("Preview = %q, want %q", got.Preview, "doing stuff")
	}

	// Non-existent pane
	if findStateByPaneID("%999") != nil {
		t.Error("expected nil for non-existent pane")
	}
}

func TestDetermineState(t *testing.T) {
	tests := []struct {
		name     string
		event    string
		data     map[string]any
		expected string
	}{
		{
			name:     "SessionStart -> waiting_input",
			event:    "SessionStart",
			data:     nil,
			expected: StateWaitingInput,
		},
		{
			name:     "UserPromptSubmit -> running",
			event:    "UserPromptSubmit",
			data:     nil,
			expected: StateRunning,
		},
		{
			name:     "PreToolUse Bash -> running",
			event:    "PreToolUse",
			data:     map[string]any{"tool_name": "Bash"},
			expected: StateRunning,
		},
		{
			name:     "PreToolUse Read -> running",
			event:    "PreToolUse",
			data:     map[string]any{"tool_name": "Read"},
			expected: StateRunning,
		},
		{
			name:     "PostToolUse -> running",
			event:    "PostToolUse",
			data:     map[string]any{"tool_name": "Bash"},
			expected: StateRunning,
		},
		{
			name:     "PermissionRequest -> approval_waiting",
			event:    "PermissionRequest",
			data:     map[string]any{"tool_name": "Bash"},
			expected: StateApprovalWaiting,
		},
		{
			name:     "Stop -> done",
			event:    "Stop",
			data:     nil,
			expected: StateDone,
		},
		{
			name:     "SessionEnd -> no state change (handled specially)",
			event:    "SessionEnd",
			data:     nil,
			expected: "",
		},
		{
			name:     "Notification permission_prompt -> approval_waiting",
			event:    "Notification",
			data:     map[string]any{"type": "permission_prompt"},
			expected: StateApprovalWaiting,
		},
		{
			name:     "Notification idle_prompt -> waiting_input",
			event:    "Notification",
			data:     map[string]any{"type": "idle_prompt"},
			expected: StateWaitingInput,
		},
		{
			name:     "Notification generic -> no change",
			event:    "Notification",
			data:     map[string]any{"type": "info", "message": "Task completed"},
			expected: "",
		},
		{
			name:     "Unknown event -> unknown",
			event:    "SomeNewEvent",
			data:     nil,
			expected: StateUnknown,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := determineState(tt.event, tt.data)
			if got != tt.expected {
				t.Errorf("determineState(%q, %v) = %q, want %q", tt.event, tt.data, got, tt.expected)
			}
		})
	}
}

func TestLooksLikeQuestion(t *testing.T) {
	tests := []struct {
		name     string
		content  string
		expected bool
	}{
		{"question mark at end", "Would you like to proceed?", true},
		{"question in multiline", "Here is the result.\nShould I continue?", true},
		{"question with trailing spaces", "Is this ok?   ", true},
		{"no question", "Done! All tests pass.", false},
		{"empty content", "", false},
		{"question mark in middle", "file?.go is found", false},
		{"just whitespace lines", "  \n\t\n  ", false},
		{"mixed lines with question", "Running tests...\nAll passed.\nAnything else you need?", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := looksLikeQuestion(tt.content); got != tt.expected {
				t.Errorf("looksLikeQuestion(%q) = %v, want %v", tt.content, got, tt.expected)
			}
		})
	}
}

func TestBuildPreview(t *testing.T) {
	tests := []struct {
		name     string
		event    string
		data     map[string]any
		expected string
	}{
		{
			name:     "UserPromptSubmit with prompt",
			event:    "UserPromptSubmit",
			data:     map[string]any{"prompt": "fix the bug"},
			expected: "fix the bug",
		},
		{
			name:     "UserPromptSubmit with message key",
			event:    "UserPromptSubmit",
			data:     map[string]any{"message": "hello world"},
			expected: "hello world",
		},
		{
			name:  "UserPromptSubmit long prompt truncated",
			event: "UserPromptSubmit",
			data: map[string]any{
				"prompt": "this is a very long prompt that should be truncated because it exceeds the maximum length allowed for preview text display",
			},
		},
		{
			name:     "PreToolUse shows tool name",
			event:    "PreToolUse",
			data:     map[string]any{"tool_name": "Bash"},
			expected: "tool: Bash",
		},
		{
			name:     "Stop with reason",
			event:    "Stop",
			data:     map[string]any{"stop_reason": "end_turn"},
			expected: "stopped: end_turn",
		},
		{
			name:     "Stop without reason",
			event:    "Stop",
			data:     map[string]any{},
			expected: "waiting for input",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := buildPreview(tt.event, tt.data)
			if tt.expected != "" && got != tt.expected {
				t.Errorf("buildPreview(%q) = %q, want %q", tt.event, got, tt.expected)
			}
			if tt.name == "UserPromptSubmit long prompt truncated" {
				if len(got) > previewMaxLen+3 { // previewMaxLen + "..."
					t.Errorf("preview too long: %d chars (max %d)", len(got), previewMaxLen+3)
				}
			}
		})
	}
}

func TestWriteState_JSONRoundtrip(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CLAUDE_PANE_STATE_DIR", dir)

	ps := &PaneState{
		Session:     "work",
		WindowIndex: "1",
		WindowName:  "editor",
		PaneID:      "%5",
		PaneTitle:   "claude session",
		State:       StateApprovalWaiting,
		Cwd:         "/home/user/project",
		Branch:      "feature/auth",
		Preview:     "tool: Bash",
	}

	if err := writeState(ps); err != nil {
		t.Fatalf("writeState: %v", err)
	}

	// Read raw JSON and verify format
	path := stateFilePath("work", "1", "%5")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}

	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("JSON parse: %v", err)
	}

	if raw["session"] != "work" {
		t.Errorf("JSON session = %v", raw["session"])
	}
	if raw["state"] != StateApprovalWaiting {
		t.Errorf("JSON state = %v", raw["state"])
	}
	if raw["branch"] != "feature/auth" {
		t.Errorf("JSON branch = %v", raw["branch"])
	}
}

func TestParseTmuxPaneLine(t *testing.T) {
	line := "main\t0\tdev\t%12\tclaude-code\t/home/user/project"
	pane, err := parseTmuxPaneLine(line)
	if err != nil {
		t.Fatalf("parseTmuxPaneLine: %v", err)
	}

	if pane.Session != "main" {
		t.Errorf("Session = %q, want %q", pane.Session, "main")
	}
	if pane.PaneID != "%12" {
		t.Errorf("PaneID = %q, want %q", pane.PaneID, "%12")
	}
	if pane.Cwd != "/home/user/project" {
		t.Errorf("Cwd = %q, want %q", pane.Cwd, "/home/user/project")
	}
}

func TestParseTmuxPaneLine_TooFewFields(t *testing.T) {
	line := "main\t0\tdev\t%12\tclaude-code"
	_, err := parseTmuxPaneLine(line)
	if err == nil {
		t.Error("expected error for 5-field line, got nil")
	}
}
