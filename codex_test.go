package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCodexInstalledByConfigFile(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("PATH", "") // 開発者マシンに codex が入っていても干渉しない
	if codexInstalled() {
		t.Errorf("expected false with empty HOME")
	}
	codexDir := filepath.Join(tmp, ".codex")
	if err := os.MkdirAll(codexDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(codexDir, "config.toml"), []byte(""), 0o644); err != nil {
		t.Fatal(err)
	}
	if !codexInstalled() {
		t.Errorf("expected true when ~/.codex/config.toml exists")
	}
}

func TestCodexInstalledEmptyDirIsNotEnough(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("PATH", "") // no codex on PATH
	codexDir := filepath.Join(tmp, ".codex")
	if err := os.MkdirAll(codexDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if codexInstalled() {
		t.Errorf("empty ~/.codex/ alone should not trigger detection (spec §6.2)")
	}
}

func TestCodexHooksConfigured(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	codexDir := filepath.Join(tmp, ".codex")
	if err := os.MkdirAll(codexDir, 0o700); err != nil {
		t.Fatal(err)
	}
	cfg := filepath.Join(codexDir, "config.toml")

	// no markers → false
	if err := os.WriteFile(cfg, []byte("[other]\nkey=\"val\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if codexHooksConfigured() {
		t.Errorf("no markers should be false")
	}

	// markers + command → true
	content := `[other]
key = "val"

##### cc-pane:begin #####
[[hooks.SessionStart]]
command = "cc-pane update-state --event SessionStart --agent codex"
##### cc-pane:end #####
`
	if err := os.WriteFile(cfg, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	if !codexHooksConfigured() {
		t.Errorf("markers + command should be true")
	}

	// markers but empty block → false (broken)
	empty := `##### cc-pane:begin #####
##### cc-pane:end #####
`
	if err := os.WriteFile(cfg, []byte(empty), 0o644); err != nil {
		t.Fatal(err)
	}
	if codexHooksConfigured() {
		t.Errorf("empty marker block should be false (broken)")
	}

	// only begin (no end) → false
	noEnd := `##### cc-pane:begin #####
[[hooks.SessionStart]]
`
	if err := os.WriteFile(cfg, []byte(noEnd), 0o644); err != nil {
		t.Fatal(err)
	}
	if codexHooksConfigured() {
		t.Errorf("missing end marker should be false")
	}
}

func TestFindCodexBlockEdgeCases(t *testing.T) {
	// 末尾改行なし
	c := codexBeginMarker + "\nfoo\n" + codexEndMarker
	begin, end, ok := findCodexBlock(c)
	if !ok {
		t.Fatal("expected found")
	}
	if begin != 0 {
		t.Errorf("begin = %d, want 0", begin)
	}
	if end <= begin {
		t.Errorf("end %d should be > begin %d", end, begin)
	}

	// 前後にコンテンツ
	c2 := "[a]\n" + codexBeginMarker + "\nx = 1\n" + codexEndMarker + "\n[b]\n"
	begin, end, ok = findCodexBlock(c2)
	if !ok {
		t.Fatal("expected found")
	}
	if !strings.Contains(c2[begin:end], "x = 1") {
		t.Errorf("block content wrong: %q", c2[begin:end])
	}
}
