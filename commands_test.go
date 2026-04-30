package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func mustMkdir(t *testing.T, p string) {
	t.Helper()
	if err := os.MkdirAll(p, 0o755); err != nil {
		t.Fatal(err)
	}
}

func mustWrite(t *testing.T, p, content string) {
	t.Helper()
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestSetupAutoDetectClaudeOnly(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("PATH", "")
	mustMkdir(t, filepath.Join(tmp, ".claude"))
	mustWrite(t, filepath.Join(tmp, ".claude", "settings.json"), "{}")

	if err := cmdSetup([]string{"--dry-run"}); err != nil {
		t.Fatal(err)
	}
}

func TestSetupAgentMismatchExits2(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("PATH", "")
	if err := cmdSetup([]string{"--agent", "claude", "--no-claude"}); err == nil {
		t.Error("expected error (usage)")
	}
}

func TestSetupInvalidAgentValueExits2(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("PATH", "")
	if err := cmdSetup([]string{"--agent", "gemini"}); err == nil {
		t.Error("expected error for unknown agent value")
	}
}

func TestSetupAgentForcedButNotDetectedExits1(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("PATH", "")
	if err := cmdSetup([]string{"--agent", "codex"}); err == nil {
		t.Error("expected error when --agent codex but Codex not detected")
	}
}

func TestSetupBakFileNamingChanged(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("PATH", "")
	mustMkdir(t, filepath.Join(tmp, ".claude"))
	settings := filepath.Join(tmp, ".claude", "settings.json")
	mustWrite(t, settings, "{}")

	if err := cmdSetup([]string{"--no-codex"}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(settings + ".cc-pane.bak"); err != nil {
		t.Errorf(".cc-pane.bak missing: %v", err)
	}
	if _, err := os.Stat(settings + ".bak"); err == nil {
		t.Errorf("old .bak naming should not be created")
	}
}

func TestUninstallRemovesCodexBlock(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("PATH", "")
	codexDir := filepath.Join(tmp, ".codex")
	mustMkdir(t, codexDir)
	cfg := filepath.Join(codexDir, "config.toml")
	mustWrite(t, cfg, "[other]\n"+codexBlockText())

	if err := cmdUninstall([]string{}); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(cfg)
	if strings.Contains(string(got), codexBeginMarker) {
		t.Errorf("Codex block not removed: %s", got)
	}
}

func TestUninstallSurvivesPartialFailure(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("PATH", "")
	mustMkdir(t, filepath.Join(tmp, ".claude"))
	mustWrite(t, filepath.Join(tmp, ".claude", "settings.json"), `{"hooks":{}}`)
	if err := cmdUninstall([]string{}); err != nil {
		t.Fatal(err)
	}
}

func TestAgentFlagSetTwiceErrors(t *testing.T) {
	var af agentFlag
	if err := af.Set("claude"); err != nil {
		t.Fatal(err)
	}
	if err := af.Set("codex"); err == nil {
		t.Error("expected error on duplicate Set")
	}
}

func TestApplyAgentSwitchReset(t *testing.T) {
	prior := &PaneState{
		Agent: AgentClaude, Session: "s", WindowIndex: "0", PaneID: "%99",
		State: StateRunning, BackgroundAgents: 5, Preview: "old preview",
	}
	pane := &TmuxPane{Session: "s", WindowIndex: "0", PaneID: "%99", Cwd: "/tmp", WindowName: "w"}
	got := applyAgentSwitchReset(prior, AgentCodex, pane)
	if got.BackgroundAgents != 0 {
		t.Errorf("BG counter not reset: %d", got.BackgroundAgents)
	}
	if got.Agent != AgentCodex {
		t.Errorf("Agent not switched: %s", got.Agent)
	}
	if got.Preview != "" {
		t.Errorf("Preview not cleared: %q", got.Preview)
	}
}

func TestMergeHooksIncludesAgentFlag(t *testing.T) {
	settings := map[string]any{}
	if !mergeHooks(settings) {
		t.Fatal("mergeHooks should report changes")
	}
	hooks := settings["hooks"].(map[string]any)
	for _, event := range requiredHookEvents {
		entries := toSlice(hooks[event])
		if len(entries) == 0 {
			t.Fatalf("no entries for %s", event)
		}
		entry := entries[0].(map[string]any)
		inner := entry["hooks"].([]any)[0].(map[string]any)
		cmd, _ := inner["command"].(string)
		if !strings.Contains(cmd, "--agent claude") {
			t.Errorf("event %s: command missing --agent claude: %q", event, cmd)
		}
	}
}

func TestMergeHooks_CleansNullValues(t *testing.T) {
	settings := map[string]any{
		"hooks": map[string]any{
			"SessionEnd":   nil,
			"SessionStart": nil,
			"SomeUnknown":  nil,
			"Stop": []any{
				map[string]any{
					"hooks": []any{
						map[string]any{"command": "other-tool", "type": "command"},
					},
				},
			},
		},
	}

	changed := mergeHooks(settings)
	if !changed {
		t.Error("mergeHooks should report changes when cleaning null values")
	}

	hooks := settings["hooks"].(map[string]any)

	// Null values were cleaned, then re-added as required hooks
	for _, event := range []string{"SessionEnd", "SessionStart"} {
		if val := hooks[event]; val == nil {
			t.Errorf("%s should exist as a valid hook (was null, now re-added)", event)
		}
	}

	// Existing valid entries should be preserved
	if hooks["Stop"] == nil {
		t.Error("Stop should still exist")
	}

	// Verify output serializes without null
	out, err := json.Marshal(settings)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	if s := string(out); strings.Contains(s, `"SomeUnknown":null`) {
		t.Errorf("JSON still contains null: %s", s)
	}
}

func TestMergeHooks_AddsRequiredHooks(t *testing.T) {
	settings := map[string]any{}

	changed := mergeHooks(settings)
	if !changed {
		t.Error("mergeHooks should report changes when adding hooks")
	}

	hooks := settings["hooks"].(map[string]any)
	for _, event := range requiredHookEvents {
		if hooks[event] == nil {
			t.Errorf("required hook %q was not added", event)
		}
	}
}

func TestRemoveHookEntries_DeletesEmptyKeys(t *testing.T) {
	hooks := map[string]any{
		"UserPromptSubmit": []any{
			map[string]any{
				"hooks": []any{
					map[string]any{
						"command": "cc-pane update-state --event UserPromptSubmit",
						"type":    "command",
					},
				},
			},
		},
	}

	changed := removeHookEntries(hooks)
	if !changed {
		t.Error("removeHookEntries should report changes")
	}

	if _, exists := hooks["UserPromptSubmit"]; exists {
		t.Error("UserPromptSubmit should be deleted when all entries are cc-pane")
	}
}

func TestRemoveHookEntries_KeepsNonCCPaneEntries(t *testing.T) {
	hooks := map[string]any{
		"Stop": []any{
			map[string]any{
				"hooks": []any{
					map[string]any{"command": "other-tool notify", "type": "command"},
				},
			},
			map[string]any{
				"hooks": []any{
					map[string]any{"command": "cc-pane update-state --event Stop", "type": "command"},
				},
			},
		},
	}

	changed := removeHookEntries(hooks)
	if !changed {
		t.Error("removeHookEntries should report changes")
	}

	entries, ok := hooks["Stop"].([]any)
	if !ok || len(entries) != 1 {
		t.Fatalf("expected 1 remaining entry, got %v", hooks["Stop"])
	}
}

func TestRemoveHookEntries_CleansNullValues(t *testing.T) {
	hooks := map[string]any{
		"SessionEnd":   nil,
		"SessionStart": nil,
	}

	changed := removeHookEntries(hooks)
	if !changed {
		t.Error("removeHookEntries should report changes for null cleanup")
	}

	if len(hooks) != 0 {
		t.Errorf("expected empty hooks map, got %v", hooks)
	}
}

func TestMergeHooks_CompactEventsHaveCatchAllMatcher(t *testing.T) {
	settings := map[string]any{}
	mergeHooks(settings)

	hooks := settings["hooks"].(map[string]any)

	for _, event := range []string{"Notification", "PreCompact", "PostCompact"} {
		entries := toSlice(hooks[event])
		if len(entries) == 0 {
			t.Fatalf("no entries for %s", event)
		}
		entry, ok := entries[0].(map[string]any)
		if !ok {
			t.Fatalf("entry for %s is not a map", event)
		}
		matcher, exists := entry["matcher"]
		if !exists {
			t.Errorf("%s: matcher key not present", event)
			continue
		}
		if matcher != "" {
			t.Errorf("%s: matcher = %q, want empty string (catch-all)", event, matcher)
		}
	}
}

func TestRemoveHookEntries_NoChangeWhenEmpty(t *testing.T) {
	hooks := map[string]any{}

	changed := removeHookEntries(hooks)
	if changed {
		t.Error("removeHookEntries should report no changes for empty map")
	}
}
