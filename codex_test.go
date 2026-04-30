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
		t.Errorf("empty ~/.codex/ alone should not trigger detection")
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
[notify]
command = "cc-pane update-state --event Stop --agent codex"
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
[notify]
`
	if err := os.WriteFile(cfg, []byte(noEnd), 0o644); err != nil {
		t.Fatal(err)
	}
	if codexHooksConfigured() {
		t.Errorf("missing end marker should be false")
	}
}

func TestMergeCodexHooksEmptyFile(t *testing.T) {
	tmp := t.TempDir()
	cfg := filepath.Join(tmp, "config.toml")

	changed, err := mergeCodexHooks(cfg, false)
	if err != nil {
		t.Fatal(err)
	}
	if !changed {
		t.Error("expected changed=true on empty file")
	}
	got, _ := os.ReadFile(cfg)
	if !strings.Contains(string(got), codexBeginMarker) {
		t.Errorf("missing begin marker: %s", got)
	}
	if !strings.Contains(string(got), "[notify]") {
		t.Errorf("missing [notify] table: %s", got)
	}
	if !strings.Contains(string(got), `cc-pane update-state --event Stop --agent codex`) {
		t.Errorf("missing notify command: %s", got)
	}
}

func TestMergeCodexHooksMigratesLegacyHooksBlock(t *testing.T) {
	// Older cc-pane (0.2.0-dev pre-fix) wrote [[hooks.X]] arrays which codex CLI
	// silently ignores. The new code must rewrite such blocks to [notify] form.
	tmp := t.TempDir()
	cfg := filepath.Join(tmp, "config.toml")
	legacy := codexBeginMarker + "\n" +
		"[[hooks.SessionStart]]\n" +
		`command = "cc-pane update-state --event SessionStart --agent codex"` + "\n" +
		"async = true\n" +
		codexEndMarker + "\n"
	if err := os.WriteFile(cfg, []byte(legacy), 0o644); err != nil {
		t.Fatal(err)
	}

	changed, err := mergeCodexHooks(cfg, false)
	if err != nil {
		t.Fatal(err)
	}
	if !changed {
		t.Error("expected legacy [[hooks.X]] block to be rewritten")
	}
	got, _ := os.ReadFile(cfg)
	if strings.Contains(string(got), "[[hooks.") {
		t.Errorf("legacy [[hooks.X]] block still present: %s", got)
	}
	if !strings.Contains(string(got), "[notify]") {
		t.Errorf("[notify] not added after migration: %s", got)
	}
}

func TestMergeCodexHooksRefusesExternalNotify(t *testing.T) {
	// User already has a custom [notify] block outside our markers — refuse to
	// overwrite, since two [notify] tables would make the TOML invalid and we
	// don't want to clobber the user's work.
	tmp := t.TempDir()
	cfg := filepath.Join(tmp, "config.toml")
	existing := "[notify]\ncommand = \"/their/own/script.sh\"\n"
	if err := os.WriteFile(cfg, []byte(existing), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := mergeCodexHooks(cfg, false); err == nil {
		t.Error("expected error when user already has a [notify] block")
	}
}

func TestMergeCodexHooksPreservesExisting(t *testing.T) {
	tmp := t.TempDir()
	cfg := filepath.Join(tmp, "config.toml")
	existing := "[other]\nkey = \"val\"\n"
	if err := os.WriteFile(cfg, []byte(existing), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := mergeCodexHooks(cfg, false); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(cfg)
	if !strings.Contains(string(got), `[other]`) {
		t.Errorf("existing content lost: %s", got)
	}
}

func TestMergeCodexHooksIdempotent(t *testing.T) {
	tmp := t.TempDir()
	cfg := filepath.Join(tmp, "config.toml")

	if _, err := mergeCodexHooks(cfg, false); err != nil {
		t.Fatal(err)
	}
	first, _ := os.ReadFile(cfg)

	changed, err := mergeCodexHooks(cfg, false)
	if err != nil {
		t.Fatal(err)
	}
	if changed {
		t.Error("second call should be idempotent (changed=false)")
	}
	second, _ := os.ReadFile(cfg)
	if string(first) != string(second) {
		t.Error("file changed on idempotent call")
	}
}

func TestMergeCodexHooksRebuildsBrokenBlock(t *testing.T) {
	tmp := t.TempDir()
	cfg := filepath.Join(tmp, "config.toml")
	broken := codexBeginMarker + "\n" + codexEndMarker + "\n"
	if err := os.WriteFile(cfg, []byte(broken), 0o644); err != nil {
		t.Fatal(err)
	}

	changed, err := mergeCodexHooks(cfg, false)
	if err != nil {
		t.Fatal(err)
	}
	if !changed {
		t.Error("broken block should be rebuilt (changed=true)")
	}
	got, _ := os.ReadFile(cfg)
	if !strings.Contains(string(got), `command = "cc-pane update-state`) {
		t.Errorf("rebuild failed: %s", got)
	}
}

func TestMergeCodexHooksMissingEndMarkerAborts(t *testing.T) {
	tmp := t.TempDir()
	cfg := filepath.Join(tmp, "config.toml")
	bad := codexBeginMarker + "\n[notify]\n"
	if err := os.WriteFile(cfg, []byte(bad), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := mergeCodexHooks(cfg, false); err == nil {
		t.Error("expected error on missing end marker")
	}
}

func TestMergeCodexHooksAppendsNewlineWhenNeeded(t *testing.T) {
	tmp := t.TempDir()
	cfg := filepath.Join(tmp, "config.toml")
	noTrailingNL := `key = "val"`
	if err := os.WriteFile(cfg, []byte(noTrailingNL), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := mergeCodexHooks(cfg, false); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(cfg)
	if strings.Contains(string(got), `"val"##### cc-pane`) {
		t.Errorf("line concatenation: %q", got)
	}
}

func TestMergeCodexHooksDryRun(t *testing.T) {
	tmp := t.TempDir()
	cfg := filepath.Join(tmp, "config.toml")
	if _, err := mergeCodexHooks(cfg, true); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(cfg); !os.IsNotExist(err) {
		t.Error("dry-run must not create the file")
	}
}

func TestMergeCodexHooksRefusesSymlink(t *testing.T) {
	tmp := t.TempDir()
	target := filepath.Join(tmp, "real.toml")
	link := filepath.Join(tmp, "config.toml")
	if err := os.WriteFile(target, []byte(""), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if _, err := mergeCodexHooks(link, false); err == nil {
		t.Error("expected error when target is a symlink")
	}
}

func TestMergeCodexHooksCreatesParentDir(t *testing.T) {
	tmp := t.TempDir()
	cfg := filepath.Join(tmp, "subdir", "config.toml")
	if _, err := mergeCodexHooks(cfg, false); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(filepath.Dir(cfg))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o700 {
		t.Errorf("expected 0700, got %o", info.Mode().Perm())
	}
}

func TestMergeCodexHooksWritesBak(t *testing.T) {
	tmp := t.TempDir()
	cfg := filepath.Join(tmp, "config.toml")
	if err := os.WriteFile(cfg, []byte("# original\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := mergeCodexHooks(cfg, false); err != nil {
		t.Fatal(err)
	}
	bak := cfg + ".cc-pane.bak"
	got, err := os.ReadFile(bak)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "# original\n" {
		t.Errorf("bak content: %q", got)
	}
}

func TestRemoveCodexHooksRemovesBlock(t *testing.T) {
	tmp := t.TempDir()
	cfg := filepath.Join(tmp, "config.toml")
	content := "[other]\nkey = \"val\"\n" + codexBlockText()
	if err := os.WriteFile(cfg, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	changed, err := removeCodexHooks(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if !changed {
		t.Error("expected changed=true")
	}
	got, _ := os.ReadFile(cfg)
	if strings.Contains(string(got), codexBeginMarker) {
		t.Errorf("begin marker still present: %s", got)
	}
	if !strings.Contains(string(got), `[other]`) {
		t.Errorf("non-cc-pane content lost: %s", got)
	}
}

func TestRemoveCodexHooksMissingEndMarker(t *testing.T) {
	tmp := t.TempDir()
	cfg := filepath.Join(tmp, "config.toml")
	bad := codexBeginMarker + "\n[notify]\n"
	if err := os.WriteFile(cfg, []byte(bad), 0o644); err != nil {
		t.Fatal(err)
	}
	changed, err := removeCodexHooks(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if changed {
		t.Error("must not modify when end marker missing")
	}
}

func TestRemoveCodexHooksNoBlockExits0(t *testing.T) {
	tmp := t.TempDir()
	cfg := filepath.Join(tmp, "config.toml")
	if err := os.WriteFile(cfg, []byte("[other]\nkey=\"val\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	changed, err := removeCodexHooks(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if changed {
		t.Error("no block → not changed")
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
