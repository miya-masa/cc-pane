package main

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func mustParseRFC3339(t *testing.T, value string) time.Time {
	t.Helper()
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		t.Fatalf("parse %q: %v", value, err)
	}
	return parsed
}

func captureStateStderr(t *testing.T, fn func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	original := os.Stderr
	os.Stderr = w
	defer func() {
		os.Stderr = original
		_ = r.Close()
	}()

	fn()
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	output, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	return string(output)
}

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
	// staleApproval: beyond approvalWaitingStaleThreshold but below waitingInputStaleThreshold
	staleApproval := time.Now().Add(-(approvalWaitingStaleThreshold + 1*time.Minute)).Format(time.RFC3339)

	tests := []struct {
		name     string
		ps       *PaneState
		expected int
	}{
		{"approval_waiting", &PaneState{State: StateApprovalWaiting, LastUpdatedAt: now}, 0},
		{"stale approval_waiting -> degraded to recent rank", &PaneState{State: StateApprovalWaiting, LastUpdatedAt: staleApproval}, 1},
		{"stale approval_waiting beyond waiting_input threshold -> stale rank", &PaneState{State: StateApprovalWaiting, LastUpdatedAt: stale}, 3},
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
			Tty:            "/dev/pts/8",
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
	if got[0].LastUpdatedAt != now.Format(time.RFC3339) {
		t.Errorf("LastUpdatedAt = %q, want first observation time", got[0].LastUpdatedAt)
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

func TestOverlayLiveCodexPanesMarksApprovalPrompt(t *testing.T) {
	orig := paneHasCodexApprovalPrompt
	paneHasCodexApprovalPrompt = func(paneID string) bool {
		return paneID == "%10"
	}
	defer func() { paneHasCodexApprovalPrompt = orig }()

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
	if got[0].State != StateApprovalWaiting {
		t.Errorf("State = %q, want %q", got[0].State, StateApprovalWaiting)
	}
	if got[0].LastUpdatedAt != now.Format(time.RFC3339) {
		t.Errorf("LastUpdatedAt = %q, want state transition time", got[0].LastUpdatedAt)
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
	if got[0].LastUpdatedAt != now.Format(time.RFC3339) {
		t.Errorf("LastUpdatedAt = %q, want state transition time", got[0].LastUpdatedAt)
	}
}

func TestOverlayLiveCodexPanesClearsApprovalWhenPromptDisappears(t *testing.T) {
	now := time.Date(2026, 4, 30, 18, 30, 0, 0, time.Local)
	states := []*PaneState{
		{
			Agent:         AgentCodex,
			Session:       "main",
			WindowIndex:   "0",
			WindowName:    "dev",
			PaneID:        "%10",
			PaneTitle:     "codex-support",
			State:         StateApprovalWaiting,
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
	if got[0].LastUpdatedAt != now.Format(time.RFC3339) {
		t.Errorf("LastUpdatedAt = %q, want state transition time", got[0].LastUpdatedAt)
	}
}

func TestOverlayLiveCodexPanesPreservesTimestampWhenStateUnchanged(t *testing.T) {
	now := time.Date(2026, 4, 30, 18, 30, 0, 0, time.Local)
	old := "2026-04-30T18:00:00+09:00"
	states := []*PaneState{
		{
			Agent:         AgentCodex,
			Session:       "main",
			WindowIndex:   "0",
			WindowName:    "dev",
			PaneID:        "%10",
			PaneTitle:     "codex-support",
			State:         StateWaitingInput,
			LastUpdatedAt: old,
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
	if got[0].LastUpdatedAt != old {
		t.Errorf("LastUpdatedAt = %q, want preserved timestamp %q", got[0].LastUpdatedAt, old)
	}
}

func TestOverlayLiveCodexPanesUpdatesTimestampWhenStateStartsRunning(t *testing.T) {
	now := time.Date(2026, 4, 30, 18, 30, 0, 0, time.Local)
	states := []*PaneState{
		{
			Agent:         AgentCodex,
			Session:       "main",
			WindowIndex:   "0",
			WindowName:    "dev",
			PaneID:        "%10",
			PaneTitle:     "codex-support",
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
			PaneTitle:      "⠹ codex-support",
			Cwd:            "/repo",
			CurrentCommand: "codex",
		},
	}

	got := overlayLiveCodexPanes(states, panes, now)
	if len(got) != 1 {
		t.Fatalf("expected one state, got %d", len(got))
	}
	if got[0].State != StateRunning {
		t.Errorf("State = %q, want %q", got[0].State, StateRunning)
	}
	if got[0].LastUpdatedAt != now.Format(time.RFC3339) {
		t.Errorf("LastUpdatedAt = %q, want state transition time", got[0].LastUpdatedAt)
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
	if got[0].LastUpdatedAt != now.Format(time.RFC3339) {
		t.Errorf("LastUpdatedAt = %q, want replacement time", got[0].LastUpdatedAt)
	}
}

func TestOverlayAndCleanupDeadPanesUsesPaneLocationIdentity(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CLAUDE_PANE_STATE_DIR", dir)

	old := &PaneState{
		Agent:         AgentCodex,
		Session:       "old",
		WindowIndex:   "1",
		PaneID:        "%10",
		State:         StateWaitingInput,
		LastUpdatedAt: "2026-04-30T18:00:00Z",
	}
	current := &PaneState{
		Agent:         AgentCodex,
		Session:       "main",
		WindowIndex:   "0",
		PaneID:        "%10",
		State:         StateWaitingInput,
		LastUpdatedAt: "2026-04-30T18:10:00Z",
	}
	for _, state := range []*PaneState{old, current} {
		if err := writeStateAt(state, mustParseRFC3339(t, state.LastUpdatedAt)); err != nil {
			t.Fatalf("writeStateAt: %v", err)
		}
	}

	panes := []TmuxPane{{
		Session: "main", WindowIndex: "0", PaneID: "%10", CurrentCommand: "codex",
	}}
	states := overlayLiveCodexPanes([]*PaneState{current, old}, panes, mustParseRFC3339(t, "2026-04-30T18:20:00Z"))
	foundOldLocation := false
	for _, state := range states {
		if state.Session == old.Session && state.WindowIndex == old.WindowIndex && state.PaneID == old.PaneID {
			foundOldLocation = true
			break
		}
	}
	if !foundOldLocation {
		t.Fatal("overlayLiveCodexPanes should not replace a different location with the same pane ID")
	}
	got := cleanupDeadPanes(states, panes)

	if len(got) != 1 || got[0].Session != "main" || got[0].WindowIndex != "0" {
		t.Fatalf("cleanupDeadPanes() = %#v, want only current location", got)
	}
	if _, err := os.Stat(stateFilePath(old.Session, old.WindowIndex, old.PaneID)); !os.IsNotExist(err) {
		t.Fatalf("old location state should be removed, stat err = %v", err)
	}
	if _, err := os.Stat(stateFilePath(current.Session, current.WindowIndex, current.PaneID)); err != nil {
		t.Fatalf("current location state should remain: %v", err)
	}
}

func TestCleanupDeadPanesDoesNotRemoveStateRewrittenAfterSnapshot(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CLAUDE_PANE_STATE_DIR", dir)

	old := &PaneState{
		Agent: AgentCodex, Session: "main", WindowIndex: "0", PaneID: "%10", State: StateWaitingInput,
	}
	if err := writeStateAt(old, mustParseRFC3339(t, "2026-04-30T18:00:00Z")); err != nil {
		t.Fatalf("write old state: %v", err)
	}
	newState := &PaneState{
		Agent: AgentClaude, Session: "main", WindowIndex: "0", PaneID: "%10", State: StateRunning,
	}
	if err := writeStateAt(newState, mustParseRFC3339(t, "2026-04-30T18:01:00Z")); err != nil {
		t.Fatalf("write replacement state: %v", err)
	}

	got := cleanupDeadPanes([]*PaneState{old}, []TmuxPane{})
	if len(got) != 0 {
		t.Fatalf("cleanupDeadPanes() = %#v, want stale snapshot excluded", got)
	}
	persisted, err := readState(stateFilePath(newState.Session, newState.WindowIndex, newState.PaneID))
	if err != nil {
		t.Fatalf("replacement state should remain: %v", err)
	}
	if persisted.Agent != AgentClaude || persisted.State != StateRunning {
		t.Fatalf("replacement state = %#v, want new Claude state", persisted)
	}
}

func TestRemoveStateIfUnchanged(t *testing.T) {
	t.Run("success removes matching snapshot and lock file stays hidden", func(t *testing.T) {
		t.Setenv("CLAUDE_PANE_STATE_DIR", t.TempDir())
		state := &PaneState{Agent: AgentCodex, Session: "main", WindowIndex: "0", PaneID: "%10", State: StateWaitingInput}
		if err := writeStateAt(state, mustParseRFC3339(t, "2026-04-30T18:00:00Z")); err != nil {
			t.Fatalf("writeStateAt: %v", err)
		}

		removed, err := removeStateIfUnchanged(state)
		if err != nil {
			t.Fatalf("removeStateIfUnchanged: %v", err)
		}
		if !removed {
			t.Fatal("removeStateIfUnchanged should remove a matching state")
		}
		if _, err := os.Stat(stateFilePath(state.Session, state.WindowIndex, state.PaneID)); !os.IsNotExist(err) {
			t.Fatalf("state file should be removed, stat err = %v", err)
		}
		states, err := listStates()
		if err != nil {
			t.Fatalf("listStates: %v", err)
		}
		if len(states) != 0 {
			t.Fatalf("listStates() = %#v, want lock file excluded", states)
		}
	})

	t.Run("mismatch preserves replacement state", func(t *testing.T) {
		t.Setenv("CLAUDE_PANE_STATE_DIR", t.TempDir())
		old := &PaneState{Agent: AgentCodex, Session: "main", WindowIndex: "0", PaneID: "%10", State: StateWaitingInput}
		if err := writeStateAt(old, mustParseRFC3339(t, "2026-04-30T18:00:00Z")); err != nil {
			t.Fatalf("write old state: %v", err)
		}
		replacement := &PaneState{Agent: AgentClaude, Session: "main", WindowIndex: "0", PaneID: "%10", State: StateRunning}
		if err := writeStateAt(replacement, mustParseRFC3339(t, "2026-04-30T18:01:00Z")); err != nil {
			t.Fatalf("write replacement state: %v", err)
		}

		removed, err := removeStateIfUnchanged(old)
		if err != nil {
			t.Fatalf("removeStateIfUnchanged: %v", err)
		}
		if removed {
			t.Fatal("removeStateIfUnchanged should keep a changed state")
		}
		persisted, err := readState(stateFilePath(replacement.Session, replacement.WindowIndex, replacement.PaneID))
		if err != nil {
			t.Fatalf("read replacement state: %v", err)
		}
		if persisted.Agent != AgentClaude {
			t.Fatalf("persisted Agent = %q, want %q", persisted.Agent, AgentClaude)
		}
	})

	t.Run("missing state is a no-op", func(t *testing.T) {
		t.Setenv("CLAUDE_PANE_STATE_DIR", t.TempDir())
		expected := &PaneState{Agent: AgentClaude, Session: "main", WindowIndex: "0", PaneID: "%10"}

		removed, err := removeStateIfUnchanged(expected)
		if err != nil {
			t.Fatalf("removeStateIfUnchanged: %v", err)
		}
		if removed {
			t.Fatal("removeStateIfUnchanged should not report removal for a missing state")
		}
	})

	t.Run("error returns lock open failure", func(t *testing.T) {
		t.Setenv("CLAUDE_PANE_STATE_DIR", t.TempDir())
		if err := os.Mkdir(stateLockPath(), 0o755); err != nil {
			t.Fatalf("create lock directory: %v", err)
		}

		removed, err := removeStateIfUnchanged(&PaneState{Session: "main", WindowIndex: "0", PaneID: "%10"})
		if err == nil {
			t.Fatal("removeStateIfUnchanged should return the lock open error")
		}
		if removed {
			t.Fatal("removeStateIfUnchanged should not report removal after a lock error")
		}
	})
}

func TestCleanupDeadPanesWarnsAndExcludesStaleSnapshotOnRemovalError(t *testing.T) {
	t.Setenv("CLAUDE_PANE_STATE_DIR", t.TempDir())
	if err := os.Mkdir(stateLockPath(), 0o755); err != nil {
		t.Fatalf("create lock directory: %v", err)
	}

	stale := &PaneState{Agent: AgentCodex, Session: "main", WindowIndex: "0", PaneID: "%10", State: StateWaitingInput}
	var got []*PaneState
	output := captureStateStderr(t, func() {
		got = cleanupDeadPanes([]*PaneState{stale}, []TmuxPane{})
	})
	if len(got) != 0 {
		t.Fatalf("cleanupDeadPanes() = %#v, want stale snapshot excluded", got)
	}
	if !strings.Contains(output, "cc-pane: warn: remove stale state") {
		t.Fatalf("cleanup warning = %q, want stale removal warning", output)
	}
}

func TestPersistChangedCodexLiveStatesWritesMissingState(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CLAUDE_PANE_STATE_DIR", dir)
	now := time.Date(2026, 4, 30, 18, 30, 0, 0, time.Local)
	current := []*PaneState{
		{
			Agent:         AgentCodex,
			Session:       "main",
			WindowIndex:   "0",
			WindowName:    "dev",
			PaneID:        "%10",
			PaneTitle:     "Codex",
			State:         StateWaitingInput,
			LastUpdatedAt: now.Format(time.RFC3339),
			Cwd:           "/repo",
		},
	}

	if err := persistChangedCodexLiveStates(nil, current, now); err != nil {
		t.Fatalf("persistChangedCodexLiveStates: %v", err)
	}
	got, err := readState(stateFilePath("main", "0", "%10"))
	if err != nil {
		t.Fatalf("readState: %v", err)
	}
	if got.Agent != AgentCodex {
		t.Errorf("Agent = %q, want %q", got.Agent, AgentCodex)
	}
	if got.State != StateWaitingInput {
		t.Errorf("State = %q, want %q", got.State, StateWaitingInput)
	}
	if got.LastUpdatedAt != now.Format(time.RFC3339) {
		t.Errorf("LastUpdatedAt = %q, want %q", got.LastUpdatedAt, now.Format(time.RFC3339))
	}
}

func TestPersistChangedCodexLiveStatesWritesNewLocationForReusedPaneID(t *testing.T) {
	t.Setenv("CLAUDE_PANE_STATE_DIR", t.TempDir())
	now := mustParseRFC3339(t, "2026-04-30T18:30:00Z")
	previous := []*PaneState{{
		Agent:         AgentCodex,
		Session:       "old",
		WindowIndex:   "1",
		PaneID:        "%10",
		State:         StateWaitingInput,
		LastUpdatedAt: "2026-04-30T18:00:00Z",
	}}
	current := []*PaneState{{
		Agent:         AgentCodex,
		Session:       "main",
		WindowIndex:   "0",
		PaneID:        "%10",
		State:         StateWaitingInput,
		LastUpdatedAt: now.Format(time.RFC3339),
	}}

	if err := persistChangedCodexLiveStates(previous, current, now); err != nil {
		t.Fatalf("persistChangedCodexLiveStates: %v", err)
	}
	got, err := readState(stateFilePath("main", "0", "%10"))
	if err != nil {
		t.Fatalf("new location state should be written: %v", err)
	}
	if *got != *current[0] {
		t.Fatalf("state = %#v, want %#v", got, current[0])
	}
}

func TestSnapshotPaneStatesPreservesPreviousSliceAcrossOverlay(t *testing.T) {
	now := time.Date(2026, 4, 30, 18, 30, 0, 0, time.Local)
	old := "2026-04-30T18:00:00+09:00"
	states := []*PaneState{
		{
			Agent:         AgentCodex,
			Session:       "main",
			WindowIndex:   "0",
			WindowName:    "dev",
			PaneID:        "%10",
			PaneTitle:     "Codex",
			State:         StateWaitingInput,
			LastUpdatedAt: old,
			Cwd:           "/repo",
		},
	}
	previous := snapshotPaneStates(states)
	panes := []TmuxPane{
		{
			Session:        "main",
			WindowIndex:    "0",
			WindowName:     "dev",
			PaneID:         "%10",
			PaneTitle:      "⠹ Codex",
			Cwd:            "/repo",
			CurrentCommand: "codex",
		},
	}

	current := overlayLiveCodexPanes(states, panes, now)
	if current[0].State != StateRunning {
		t.Fatalf("current state = %q, want %q", current[0].State, StateRunning)
	}
	if previous[0].State != StateWaitingInput {
		t.Fatalf("previous state = %q, want preserved %q", previous[0].State, StateWaitingInput)
	}
	if previous[0].LastUpdatedAt != old {
		t.Fatalf("previous LastUpdatedAt = %q, want preserved %q", previous[0].LastUpdatedAt, old)
	}
}

func TestPersistChangedCodexLiveStatesKeepsTimestampForUnchangedState(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CLAUDE_PANE_STATE_DIR", dir)
	old := time.Date(2026, 4, 30, 18, 0, 0, 0, time.Local)
	now := time.Date(2026, 4, 30, 18, 30, 0, 0, time.Local)
	prior := &PaneState{
		Agent:       AgentCodex,
		Session:     "main",
		WindowIndex: "0",
		WindowName:  "dev",
		PaneID:      "%10",
		PaneTitle:   "Codex",
		State:       StateWaitingInput,
		Cwd:         "/repo",
	}
	if err := writeStateAt(prior, old); err != nil {
		t.Fatalf("writeStateAt: %v", err)
	}
	current := []*PaneState{
		{
			Agent:         AgentCodex,
			Session:       "main",
			WindowIndex:   "0",
			WindowName:    "dev",
			PaneID:        "%10",
			PaneTitle:     "Codex",
			State:         StateWaitingInput,
			LastUpdatedAt: old.Format(time.RFC3339),
			Cwd:           "/repo",
		},
	}

	if err := persistChangedCodexLiveStates([]*PaneState{prior}, current, now); err != nil {
		t.Fatalf("persistChangedCodexLiveStates: %v", err)
	}
	got, err := readState(stateFilePath("main", "0", "%10"))
	if err != nil {
		t.Fatalf("readState: %v", err)
	}
	if got.LastUpdatedAt != old.Format(time.RFC3339) {
		t.Errorf("LastUpdatedAt = %q, want unchanged %q", got.LastUpdatedAt, old.Format(time.RFC3339))
	}
}

func TestPersistChangedCodexLiveStatesUpdatesTimestampForChangedState(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CLAUDE_PANE_STATE_DIR", dir)
	old := time.Date(2026, 4, 30, 18, 0, 0, 0, time.Local)
	now := time.Date(2026, 4, 30, 18, 30, 0, 0, time.Local)
	prior := &PaneState{
		Agent:       AgentCodex,
		Session:     "main",
		WindowIndex: "0",
		WindowName:  "dev",
		PaneID:      "%10",
		PaneTitle:   "Codex",
		State:       StateWaitingInput,
		Cwd:         "/repo",
	}
	if err := writeStateAt(prior, old); err != nil {
		t.Fatalf("writeStateAt: %v", err)
	}
	current := []*PaneState{
		{
			Agent:         AgentCodex,
			Session:       "main",
			WindowIndex:   "0",
			WindowName:    "dev",
			PaneID:        "%10",
			PaneTitle:     "⠹ Codex",
			State:         StateRunning,
			LastUpdatedAt: now.Format(time.RFC3339),
			Cwd:           "/repo",
		},
	}

	if err := persistChangedCodexLiveStates([]*PaneState{prior}, current, now); err != nil {
		t.Fatalf("persistChangedCodexLiveStates: %v", err)
	}
	got, err := readState(stateFilePath("main", "0", "%10"))
	if err != nil {
		t.Fatalf("readState: %v", err)
	}
	if got.State != StateRunning {
		t.Errorf("State = %q, want %q", got.State, StateRunning)
	}
	if got.LastUpdatedAt != now.Format(time.RFC3339) {
		t.Errorf("LastUpdatedAt = %q, want changed %q", got.LastUpdatedAt, now.Format(time.RFC3339))
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

func TestCleanupDeadPanes(t *testing.T) {
	tests := []struct {
		name              string
		agent             string
		pane              *TmuxPane
		childHasCodex     bool
		wantStateRetained bool
	}{
		{
			name:  "removes Codex state when its pane returns to shell",
			agent: AgentCodex,
			pane:  &TmuxPane{Session: "main", WindowIndex: "0", PaneID: "%1", CurrentCommand: "zsh"},
		},
		{
			name:              "keeps Codex state when Codex is the current command",
			agent:             AgentCodex,
			pane:              &TmuxPane{Session: "main", WindowIndex: "0", PaneID: "%1", CurrentCommand: "codex"},
			wantStateRetained: true,
		},
		{
			name:              "keeps Codex state when Codex is a child process",
			agent:             AgentCodex,
			pane:              &TmuxPane{Session: "main", WindowIndex: "0", PaneID: "%1", Tty: "/dev/pts/1", CurrentCommand: "node"},
			childHasCodex:     true,
			wantStateRetained: true,
		},
		{
			name:              "keeps Claude state while its pane exists",
			agent:             AgentClaude,
			pane:              &TmuxPane{Session: "main", WindowIndex: "0", PaneID: "%1", CurrentCommand: "zsh"},
			wantStateRetained: true,
		},
		{
			name:  "removes state when its pane no longer exists",
			agent: AgentClaude,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			t.Setenv("CLAUDE_PANE_STATE_DIR", dir)

			originalPaneHasCodexProcess := paneHasCodexProcess
			paneHasCodexProcess = func(tty string) bool {
				return tt.childHasCodex && tty == "/dev/pts/1"
			}
			t.Cleanup(func() {
				paneHasCodexProcess = originalPaneHasCodexProcess
			})

			state := &PaneState{
				Agent:       tt.agent,
				Session:     "main",
				WindowIndex: "0",
				PaneID:      "%1",
				State:       StateWaitingInput,
			}
			if err := writeState(state); err != nil {
				t.Fatalf("writeState: %v", err)
			}

			panes := make([]TmuxPane, 0, 1)
			if tt.pane != nil {
				panes = append(panes, *tt.pane)
			}
			got := cleanupDeadPanes([]*PaneState{state}, panes)

			if tt.wantStateRetained {
				if len(got) != 1 || got[0] != state {
					t.Fatalf("cleanupDeadPanes() = %#v, want the persisted state", got)
				}
			} else if len(got) != 0 {
				t.Fatalf("cleanupDeadPanes() = %#v, want no states", got)
			}

			_, err := os.Stat(stateFilePath(state.Session, state.WindowIndex, state.PaneID))
			if tt.wantStateRetained && err != nil {
				t.Fatalf("persisted state should remain: %v", err)
			}
			if !tt.wantStateRetained && !os.IsNotExist(err) {
				t.Fatalf("persisted state should be removed, stat err = %v", err)
			}
		})
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
			name:     "PostToolUseFailure interrupt -> waiting_input",
			event:    "PostToolUseFailure",
			data:     map[string]any{"is_interrupt": true},
			expected: StateWaitingInput,
		},
		{
			name:     "PostToolUseFailure non-interrupt -> running",
			event:    "PostToolUseFailure",
			data:     map[string]any{"is_interrupt": false},
			expected: StateRunning,
		},
		{
			name:     "PostToolUseFailure missing is_interrupt -> running",
			event:    "PostToolUseFailure",
			data:     map[string]any{},
			expected: StateRunning,
		},
		{
			name:     "PostToolUseFailure non-bool is_interrupt -> running",
			event:    "PostToolUseFailure",
			data:     map[string]any{"is_interrupt": "true"},
			expected: StateRunning,
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
			name:     "Stop user_interrupted (past tense, actual binary value) -> waiting_input",
			event:    "Stop",
			data:     map[string]any{"stop_reason": "user_interrupted"},
			existing: nil,
			expected: StateWaitingInput,
		},
		{
			name:     "Stop user_interrupted with bg agents -> waiting_input",
			event:    "Stop",
			data:     map[string]any{"stop_reason": "user_interrupted"},
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
			// Claude binary 2.1.193 uses past tense "user_interrupted"
			name:     "user_interrupted (actual binary value)",
			data:     map[string]any{"stop_reason": "user_interrupted"},
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
