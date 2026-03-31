package main

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestMergeHooks_CleansNullValues(t *testing.T) {
	settings := map[string]any{
		"hooks": map[string]any{
			"SessionEnd":   nil,
			"SessionStart": nil,
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

	// SessionEnd null was cleaned, then re-added as a required hook
	if val := hooks["SessionEnd"]; val == nil {
		t.Error("SessionEnd should exist as a valid hook (was null, now re-added)")
	}
	// SessionStart null should be removed (not a required hook)
	if _, exists := hooks["SessionStart"]; exists {
		t.Error("SessionStart should have been removed")
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
	if s := string(out); strings.Contains(s, `"SessionStart":null`) {
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

func TestRemoveHookEntries_NoChangeWhenEmpty(t *testing.T) {
	hooks := map[string]any{}

	changed := removeHookEntries(hooks)
	if changed {
		t.Error("removeHookEntries should report no changes for empty map")
	}
}
