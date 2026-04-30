package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestNormalizeAgent(t *testing.T) {
	tests := []struct {
		name        string
		raw         string
		flagPresent bool
		want        string
		wantErr     bool
	}{
		{"flag absent → claude fallback", "", false, AgentClaude, false},
		{"explicit claude", "claude", true, AgentClaude, false},
		{"explicit codex", "codex", true, AgentCodex, false},
		{"unknown value → unknown literal", "gemini", true, AgentUnknown, false},
		{"uppercase Claude → unknown", "Claude", true, AgentUnknown, false},
		{"empty value with flag present → error", "", true, "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := normalizeAgent(tt.raw, tt.flagPresent)
			if (err != nil) != tt.wantErr {
				t.Fatalf("normalizeAgent(%q, %v) err=%v wantErr=%v", tt.raw, tt.flagPresent, err, tt.wantErr)
			}
			if !tt.wantErr && got != tt.want {
				t.Errorf("normalizeAgent(%q, %v) = %q, want %q", tt.raw, tt.flagPresent, got, tt.want)
			}
		})
	}
}

func TestPaneStateAgentRoundTrip(t *testing.T) {
	t.Setenv("CLAUDE_PANE_STATE_DIR", t.TempDir())
	ps := &PaneState{Agent: AgentCodex, Session: "s", WindowIndex: "0", PaneID: "%1", State: StateRunning}
	if err := writeState(ps); err != nil {
		t.Fatal(err)
	}
	got := findStateByPaneID("%1")
	if got == nil || got.Agent != AgentCodex {
		t.Errorf("agent round-trip failed: %+v", got)
	}
}

func TestReadStateLegacyAgentFallback(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CLAUDE_PANE_STATE_DIR", dir)
	// 旧形式: agent フィールドなし
	legacy := `{"session":"s","window_index":"0","pane_id":"%1","state":"running","last_updated_at":"2026-04-30T00:00:00Z"}`
	if err := os.WriteFile(filepath.Join(dir, "s__0__1.json"), []byte(legacy), 0o644); err != nil {
		t.Fatal(err)
	}
	got := findStateByPaneID("%1")
	if got == nil || got.Agent != AgentClaude {
		t.Errorf("legacy fallback failed: %+v", got)
	}
}

func TestWriteStateRejectsEmptyAgent(t *testing.T) {
	t.Setenv("CLAUDE_PANE_STATE_DIR", t.TempDir())
	ps := &PaneState{Session: "s", WindowIndex: "0", PaneID: "%1", State: StateRunning}
	if err := writeState(ps); err == nil {
		t.Error("expected error when Agent is empty")
	}
}

func TestStatePriority(t *testing.T) {
	tests := []struct {
		state    string
		expected int
	}{
		{StateApprovalWaiting, 0},
		{StateWaitingInput, 1},
		{StateRunning, 2},
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

func TestSortPriority(t *testing.T) {
	now := time.Now().Format(time.RFC3339)
	stale := time.Now().Add(-11 * time.Minute).Format(time.RFC3339)

	tests := []struct {
		name     string
		ps       *PaneState
		expected int
	}{
		{"approval_waiting", &PaneState{State: StateApprovalWaiting, LastUpdatedAt: now}, 0},
		{"recent waiting_input", &PaneState{State: StateWaitingInput, LastUpdatedAt: now}, 1},
		{"running", &PaneState{State: StateRunning, LastUpdatedAt: now}, 2},
		{"stale waiting_input", &PaneState{State: StateWaitingInput, LastUpdatedAt: stale}, 3},
		{"invalid timestamp waiting_input", &PaneState{State: StateWaitingInput, LastUpdatedAt: "invalid"}, 3},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := sortPriority(tt.ps)
			if got != tt.expected {
				t.Errorf("sortPriority() = %d, want %d", got, tt.expected)
			}
		})
	}

	// Verify ordering: approval > recent_waiting > running > stale_waiting
	approval := sortPriority(&PaneState{State: StateApprovalWaiting, LastUpdatedAt: now})
	recentWait := sortPriority(&PaneState{State: StateWaitingInput, LastUpdatedAt: now})
	running := sortPriority(&PaneState{State: StateRunning, LastUpdatedAt: now})
	staleWait := sortPriority(&PaneState{State: StateWaitingInput, LastUpdatedAt: stale})

	if !(approval < recentWait && recentWait < running && running < staleWait) {
		t.Errorf("expected approval(%d) < recentWait(%d) < running(%d) < staleWait(%d)",
			approval, recentWait, running, staleWait)
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
		Agent:       AgentClaude,
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
		{Agent: AgentClaude, Session: "s", WindowIndex: "0", PaneID: "%1", State: StateRunning, Cwd: "/tmp"},
		{Agent: AgentClaude, Session: "s", WindowIndex: "1", PaneID: "%2", State: StateApprovalWaiting, Cwd: "/tmp"},
		{Agent: AgentClaude, Session: "s", WindowIndex: "2", PaneID: "%3", State: StateWaitingInput, Cwd: "/tmp"},
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
	if len(got) != 3 {
		t.Fatalf("expected 3 states, got %d", len(got))
	}

	expectedOrder := []string{StateApprovalWaiting, StateWaitingInput, StateRunning}
	for i, want := range expectedOrder {
		if got[i].State != want {
			t.Errorf("position %d: state = %q, want %q", i, got[i].State, want)
		}
	}
}

func TestOverlayLiveCodexPanesAddsMissingCodexState(t *testing.T) {
	now := time.Date(2026, 4, 30, 18, 30, 0, 0, time.Local)
	panes := []TmuxPane{
		{
			Session:        "main",
			WindowIndex:    "0",
			WindowName:     "dev",
			PaneID:         "%10",
			PaneTitle:      "Codex",
			Cwd:            "/repo",
			CurrentCommand: "codex",
		},
	}

	got := overlayLiveCodexPanes(nil, panes, now)
	if len(got) != 1 {
		t.Fatalf("expected one inferred Codex state, got %d", len(got))
	}
	if got[0].Agent != AgentCodex {
		t.Errorf("Agent = %q, want %q", got[0].Agent, AgentCodex)
	}
	if got[0].State != StateWaitingInput {
		t.Errorf("State = %q, want %q", got[0].State, StateWaitingInput)
	}
}

func TestOverlayLiveCodexPanesDetectsCodexChildProcess(t *testing.T) {
	orig := paneHasCodexProcess
	paneHasCodexProcess = func(tty string) bool {
		return tty == "/dev/pts/8"
	}
	defer func() { paneHasCodexProcess = orig }()

	now := time.Date(2026, 4, 30, 18, 30, 0, 0, time.Local)
	panes := []TmuxPane{
		{
			Session:        "main",
			WindowIndex:    "0",
			WindowName:     "dev",
			PaneID:         "%10",
			Cwd:            "/repo",
			Tty:            "/dev/pts/8",
			CurrentCommand: "node",
		},
	}

	got := overlayLiveCodexPanes(nil, panes, now)
	if len(got) != 1 {
		t.Fatalf("expected one inferred Codex state, got %d", len(got))
	}
	if got[0].Agent != AgentCodex {
		t.Errorf("Agent = %q, want %q", got[0].Agent, AgentCodex)
	}
}

func TestOverlayLiveCodexPanesMarksSpinnerTitleRunning(t *testing.T) {
	now := time.Date(2026, 4, 30, 18, 30, 0, 0, time.Local)
	panes := []TmuxPane{
		{
			Session:        "main",
			WindowIndex:    "0",
			WindowName:     "dev",
			PaneID:         "%10",
			PaneTitle:      "⠹ codex-support",
			Cwd:            "/repo",
			CurrentCommand: "codex",
		},
	}

	got := overlayLiveCodexPanes(nil, panes, now)
	if len(got) != 1 {
		t.Fatalf("expected one inferred Codex state, got %d", len(got))
	}
	if got[0].State != StateRunning {
		t.Errorf("State = %q, want %q", got[0].State, StateRunning)
	}
}

func TestOverlayLiveCodexPanesClearsStaleRunningWhenSpinnerStops(t *testing.T) {
	now := time.Date(2026, 4, 30, 18, 30, 0, 0, time.Local)
	states := []*PaneState{
		{
			Agent:         AgentCodex,
			Session:       "main",
			WindowIndex:   "0",
			WindowName:    "dev",
			PaneID:        "%10",
			PaneTitle:     "⠹ codex-support",
			State:         StateRunning,
			LastUpdatedAt: "2026-04-30T18:00:00+09:00",
			Cwd:           "/repo",
		},
	}
	panes := []TmuxPane{
		{
			Session:        "main",
			WindowIndex:    "0",
			WindowName:     "dev",
			PaneID:         "%10",
			PaneTitle:      "codex-support",
			Cwd:            "/repo",
			CurrentCommand: "codex",
		},
	}

	got := overlayLiveCodexPanes(states, panes, now)
	if len(got) != 1 {
		t.Fatalf("expected one state, got %d", len(got))
	}
	if got[0].State != StateWaitingInput {
		t.Errorf("State = %q, want %q", got[0].State, StateWaitingInput)
	}
}

func TestOverlayLiveCodexPanesReplacesStaleClaudeStateForSamePane(t *testing.T) {
	now := time.Date(2026, 4, 30, 18, 30, 0, 0, time.Local)
	states := []*PaneState{
		{
			Agent:         AgentClaude,
			Session:       "main",
			WindowIndex:   "0",
			WindowName:    "dev",
			PaneID:        "%10",
			PaneTitle:     "Claude",
			State:         StateWaitingInput,
			LastUpdatedAt: "2026-04-30T18:00:00+09:00",
			Cwd:           "/repo",
		},
	}
	panes := []TmuxPane{
		{
			Session:        "main",
			WindowIndex:    "0",
			WindowName:     "dev",
			PaneID:         "%10",
			PaneTitle:      "Codex",
			Cwd:            "/repo",
			CurrentCommand: "codex",
		},
	}

	got := overlayLiveCodexPanes(states, panes, now)
	if len(got) != 1 {
		t.Fatalf("expected one state after replacement, got %d", len(got))
	}
	if got[0].Agent != AgentCodex {
		t.Errorf("Agent = %q, want %q", got[0].Agent, AgentCodex)
	}
	if got[0].PaneTitle != "Codex" {
		t.Errorf("PaneTitle = %q, want Codex", got[0].PaneTitle)
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
		{Agent: AgentClaude, Session: "s", WindowIndex: "0", PaneID: "%1", State: StateRunning, Cwd: "/tmp"},
		{Agent: AgentClaude, Session: "s", WindowIndex: "1", PaneID: "%2", State: StateWaitingInput, Cwd: "/tmp"},
		{Agent: AgentClaude, Session: "s", WindowIndex: "2", PaneID: "%3", State: StateApprovalWaiting, Cwd: "/tmp"},
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
		Agent:       AgentClaude,
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

func TestFindStateByPaneIDMultiHit(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CLAUDE_PANE_STATE_DIR", dir)

	older := `{"agent":"claude","session":"old","window_index":"0","pane_id":"%1","state":"running","last_updated_at":"2025-01-01T00:00:00Z"}`
	newer := `{"agent":"codex","session":"new","window_index":"1","pane_id":"%1","state":"waiting_input","last_updated_at":"2026-04-30T00:00:00Z"}`
	if err := os.WriteFile(filepath.Join(dir, "old__0__1.json"), []byte(older), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "new__1__1.json"), []byte(newer), 0o644); err != nil {
		t.Fatal(err)
	}

	got := findStateByPaneID("%1")
	if got == nil {
		t.Fatal("expected hit, got nil")
	}
	if got.Session != "new" {
		t.Errorf("expected newest LastUpdatedAt 'new', got %q", got.Session)
	}
}

func TestFindStateByPaneIDForCurrentTmuxPrefersSessionWindow(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CLAUDE_PANE_STATE_DIR", dir)

	stale := `{"agent":"claude","session":"old","window_index":"0","pane_id":"%1","state":"running","last_updated_at":"2026-04-30T10:00:00Z"}`
	current := `{"agent":"codex","session":"main","window_index":"2","pane_id":"%1","state":"waiting_input","last_updated_at":"2025-01-01T00:00:00Z"}`
	if err := os.WriteFile(filepath.Join(dir, "old__0__1.json"), []byte(stale), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "main__2__1.json"), []byte(current), 0o644); err != nil {
		t.Fatal(err)
	}

	pane := &TmuxPane{Session: "main", WindowIndex: "2", PaneID: "%1"}
	got := findStateByPaneIDForCurrentTmux(pane)
	if got == nil {
		t.Fatal("expected hit, got nil")
	}
	if got.Session != "main" {
		t.Errorf("session/window match should win: got session=%q want main", got.Session)
	}
}

func TestFindStateByPaneIDForCurrentTmuxFallsBackToNewest(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CLAUDE_PANE_STATE_DIR", dir)

	older := `{"agent":"claude","session":"a","window_index":"0","pane_id":"%1","state":"running","last_updated_at":"2025-01-01T00:00:00Z"}`
	newer := `{"agent":"codex","session":"b","window_index":"1","pane_id":"%1","state":"waiting_input","last_updated_at":"2026-04-30T00:00:00Z"}`
	if err := os.WriteFile(filepath.Join(dir, "a__0__1.json"), []byte(older), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "b__1__1.json"), []byte(newer), 0o644); err != nil {
		t.Fatal(err)
	}

	pane := &TmuxPane{Session: "main", WindowIndex: "0", PaneID: "%1"}
	got := findStateByPaneIDForCurrentTmux(pane)
	if got == nil || got.Session != "b" {
		t.Errorf("expected fallback to newest 'b', got %+v", got)
	}
}

func TestShouldResetStaleAgentsOnlyForClaude(t *testing.T) {
	old := time.Now().Add(-1 * time.Hour).Format(time.RFC3339)
	cases := []struct {
		agent string
		want  bool
	}{
		{AgentClaude, true},
		{AgentCodex, false},
		{AgentUnknown, false},
	}
	for _, c := range cases {
		ps := &PaneState{Agent: c.agent, BackgroundAgents: 3, LastUpdatedAt: old}
		got := shouldResetStaleAgents(ps)
		if got != c.want {
			t.Errorf("agent=%s: got %v, want %v", c.agent, got, c.want)
		}
	}
}

func TestDetermineState(t *testing.T) {
	tests := []struct {
		name     string
		event    string
		data     map[string]any
		existing *PaneState
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
			name:     "PostToolUse ExitPlanMode -> approval_waiting",
			event:    "PostToolUse",
			data:     map[string]any{"tool_name": "ExitPlanMode"},
			expected: StateApprovalWaiting,
		},
		{
			name:     "PermissionRequest -> approval_waiting",
			event:    "PermissionRequest",
			data:     map[string]any{"tool_name": "Bash"},
			expected: StateApprovalWaiting,
		},
		{
			name:     "Stop -> waiting_input",
			event:    "Stop",
			data:     nil,
			expected: StateWaitingInput,
		},
		{
			name:     "Stop with bg agents -> running",
			event:    "Stop",
			data:     nil,
			existing: &PaneState{BackgroundAgents: 2},
			expected: StateRunning,
		},
		{
			name:     "Stop with zero bg agents -> waiting_input",
			event:    "Stop",
			data:     nil,
			existing: &PaneState{BackgroundAgents: 0},
			expected: StateWaitingInput,
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
			data:     map[string]any{"notification_type": "permission_prompt"},
			expected: StateApprovalWaiting,
		},
		{
			name:     "Notification idle_prompt -> waiting_input",
			event:    "Notification",
			data:     map[string]any{"notification_type": "idle_prompt"},
			expected: StateWaitingInput,
		},
		{
			name:     "Notification idle_prompt with bg agents -> running",
			event:    "Notification",
			data:     map[string]any{"notification_type": "idle_prompt"},
			existing: &PaneState{BackgroundAgents: 1},
			expected: StateRunning,
		},
		{
			name:     "Notification generic -> no change",
			event:    "Notification",
			data:     map[string]any{"notification_type": "info", "message": "Task completed"},
			expected: "",
		},
		{
			name:     "PreCompact -> running",
			event:    "PreCompact",
			data:     map[string]any{"matcher": "auto"},
			expected: StateRunning,
		},
		{
			name:     "PostCompact -> running",
			event:    "PostCompact",
			data:     map[string]any{"matcher": "manual"},
			expected: StateRunning,
		},
		{
			name:     "Stop user_interrupt -> waiting_input",
			event:    "Stop",
			data:     map[string]any{"stop_reason": "user_interrupt"},
			existing: nil,
			expected: StateWaitingInput,
		},
		{
			name:     "Stop user_interrupt with bg agents -> waiting_input",
			event:    "Stop",
			data:     map[string]any{"stop_reason": "user_interrupt"},
			existing: &PaneState{BackgroundAgents: 3},
			expected: StateWaitingInput,
		},
		{
			name:     "Unknown event -> no change",
			event:    "SomeNewEvent",
			data:     nil,
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := determineState(tt.event, tt.data, tt.existing)
			if got != tt.expected {
				t.Errorf("determineState(%q, %v, existing) = %q, want %q", tt.event, tt.data, got, tt.expected)
			}
		})
	}
}

func TestIsBackgroundAgentLaunch(t *testing.T) {
	tests := []struct {
		name     string
		event    string
		data     map[string]any
		expected bool
	}{
		{
			name:  "PostToolUse Agent with run_in_background true",
			event: "PostToolUse",
			data: map[string]any{
				"tool_name":  "Agent",
				"tool_input": map[string]any{"run_in_background": true},
			},
			expected: true,
		},
		{
			name:  "PostToolUse Agent without run_in_background",
			event: "PostToolUse",
			data: map[string]any{
				"tool_name":  "Agent",
				"tool_input": map[string]any{"prompt": "do something"},
			},
			expected: false,
		},
		{
			name:  "PostToolUse Agent with run_in_background false",
			event: "PostToolUse",
			data: map[string]any{
				"tool_name":  "Agent",
				"tool_input": map[string]any{"run_in_background": false},
			},
			expected: false,
		},
		{
			name:  "PostToolUse non-Agent tool",
			event: "PostToolUse",
			data: map[string]any{
				"tool_name":  "Bash",
				"tool_input": map[string]any{"command": "ls"},
			},
			expected: false,
		},
		{
			name:  "PreToolUse Agent (not PostToolUse)",
			event: "PreToolUse",
			data: map[string]any{
				"tool_name":  "Agent",
				"tool_input": map[string]any{"run_in_background": true},
			},
			expected: false,
		},
		{
			name:     "PostToolUse with no tool_input",
			event:    "PostToolUse",
			data:     map[string]any{"tool_name": "Agent"},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isBackgroundAgentLaunch(tt.event, tt.data)
			if got != tt.expected {
				t.Errorf("isBackgroundAgentLaunch(%q, data) = %v, want %v", tt.event, got, tt.expected)
			}
		})
	}
}

func TestIsUserInterrupt(t *testing.T) {
	tests := []struct {
		name     string
		data     map[string]any
		expected bool
	}{
		{
			name:     "user_interrupt",
			data:     map[string]any{"stop_reason": "user_interrupt"},
			expected: true,
		},
		{
			name:     "end_turn",
			data:     map[string]any{"stop_reason": "end_turn"},
			expected: false,
		},
		{
			name:     "no stop_reason",
			data:     map[string]any{},
			expected: false,
		},
		{
			name:     "nil data",
			data:     nil,
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isUserInterrupt(tt.data)
			if got != tt.expected {
				t.Errorf("isUserInterrupt(%v) = %v, want %v", tt.data, got, tt.expected)
			}
		})
	}
}

func TestHasPendingWork(t *testing.T) {
	if hasPendingWork(nil) {
		t.Error("expected false for nil PaneState")
	}
	if hasPendingWork(&PaneState{BackgroundAgents: 0}) {
		t.Error("expected false for zero agents")
	}
	if !hasPendingWork(&PaneState{BackgroundAgents: 1}) {
		t.Error("expected true for 1 agent")
	}
}

func TestShouldResetStaleAgents(t *testing.T) {
	if shouldResetStaleAgents(nil) {
		t.Error("expected false for nil")
	}
	if shouldResetStaleAgents(&PaneState{BackgroundAgents: 0}) {
		t.Error("expected false for zero agents")
	}

	// Fresh timestamp should not reset
	fresh := &PaneState{
		Agent:            AgentClaude,
		BackgroundAgents: 1,
		LastUpdatedAt:    time.Now().Format(time.RFC3339),
	}
	if shouldResetStaleAgents(fresh) {
		t.Error("expected false for fresh timestamp")
	}

	// Stale timestamp should reset
	stale := &PaneState{
		Agent:            AgentClaude,
		BackgroundAgents: 1,
		LastUpdatedAt:    time.Now().Add(-31 * time.Minute).Format(time.RFC3339),
	}
	if !shouldResetStaleAgents(stale) {
		t.Error("expected true for stale timestamp")
	}

	// Invalid timestamp should reset
	invalid := &PaneState{
		Agent:            AgentClaude,
		BackgroundAgents: 1,
		LastUpdatedAt:    "invalid",
	}
	if !shouldResetStaleAgents(invalid) {
		t.Error("expected true for invalid timestamp")
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
		{
			name:     "PreCompact -> compacting context",
			event:    "PreCompact",
			data:     map[string]any{},
			expected: "compacting context",
		},
		{
			name:     "PostCompact -> compaction complete",
			event:    "PostCompact",
			data:     map[string]any{},
			expected: "compaction complete",
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
		Agent:       AgentClaude,
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
	line := "main\t0\tdev\t%12\tclaude-code\t/home/user/project\t/dev/pts/5\tclaude"
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
	if pane.Tty != "/dev/pts/5" {
		t.Errorf("Tty = %q, want %q", pane.Tty, "/dev/pts/5")
	}
	if pane.CurrentCommand != "claude" {
		t.Errorf("CurrentCommand = %q, want %q", pane.CurrentCommand, "claude")
	}
}

func TestParseTmuxPaneLine_TooFewFields(t *testing.T) {
	line := "main\t0\tdev\t%12\tclaude-code\t/home/user/project"
	_, err := parseTmuxPaneLine(line)
	if err == nil {
		t.Error("expected error for 6-field line, got nil")
	}
}
