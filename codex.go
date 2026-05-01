package main

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

const (
	codexBeginMarker = "##### cc-pane:begin #####"
	codexEndMarker   = "##### cc-pane:end #####"
)

// codexConfigPath returns the path to ~/.codex/config.toml.
func codexConfigPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".codex", "config.toml")
}

// codexHooksJSONPath returns the path to ~/.codex/hooks.json. cc-pane does not
// write to this file; it is only inspected by `cc-pane doctor` for warnings.
func codexHooksJSONPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".codex", "hooks.json")
}

// agentInstalled reports whether an agent CLI appears to be installed.
// Either the user-config file or the binary on PATH counts; an empty config
// directory alone does NOT.
func agentInstalled(configPath, binary string) bool {
	if _, err := os.Stat(configPath); err == nil {
		return true
	}
	if _, err := exec.LookPath(binary); err == nil {
		return true
	}
	return false
}

func codexInstalled() bool {
	return agentInstalled(codexConfigPath(), "codex")
}

// codexHooksConfigured reports whether cc-pane has written its managed
// [notify] block in the current canonical form. Legacy [[hooks.X]] blocks
// from the broken pre-fix builds explicitly do NOT count — doctor must show
// "not configured" for them so users re-run setup and get the migration.
func codexHooksConfigured() bool {
	data, err := os.ReadFile(codexConfigPath())
	if err != nil {
		return false
	}
	beginIdx, endIdx, ok := findCodexBlock(string(data))
	if !ok {
		return false
	}
	return isCurrentCodexBlock(string(data)[beginIdx:endIdx])
}

// findCodexBlock locates the begin/end marker positions (line-anchored, exact
// match after right-trim of whitespace). Returns
// (beginByteIdx, endByteIdxExclusive, found).
func findCodexBlock(content string) (int, int, bool) {
	scanner := bufio.NewScanner(strings.NewReader(content))
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	pos := 0
	begin := -1
	end := -1
	for scanner.Scan() {
		line := scanner.Text()
		trimmed := strings.TrimRight(line, " \t\r\n")
		if begin < 0 && trimmed == codexBeginMarker {
			begin = pos
		} else if begin >= 0 && trimmed == codexEndMarker {
			end = pos + len(line) + 1 // include the newline
			break
		}
		pos += len(line) + 1 // +1 for the newline that scanner stripped
	}
	if begin < 0 || end < 0 {
		return 0, 0, false
	}
	return begin, end, true
}

// codexBlockText returns the canonical block written to config.toml.
//
// Codex CLI v0.x's interactive mode does NOT support Claude-style per-event
// hooks ([[hooks.X]] arrays are only honored by the experimental app-server
// subcommand). The only hook the interactive CLI fires is `notify`, an argv
// array (NOT a [notify] table — that yields "invalid type: map, expected a
// sequence in `notify`") that runs once at turn completion. We map it to
// cc-pane's Stop event so Codex panes at minimum show waiting_input.
func codexBlockText() string {
	var sb strings.Builder
	sb.WriteString("\n")
	sb.WriteString(codexBeginMarker + "\n")
	sb.WriteString("# cc-pane managed config. Do not edit between begin/end markers.\n")
	sb.WriteString("# Codex CLI only invokes notify on turn completion — cc-pane maps that\n")
	sb.WriteString("# to a Stop event. See README \"Known Limitations\".\n")
	sb.WriteString(`notify = ["cc-pane", "update-state", "--event", "Stop", "--agent", "codex"]` + "\n")
	sb.WriteString(codexEndMarker + "\n")
	return sb.String()
}

// bakSuffix is the suffix used for backup files created by setup/uninstall.
// The previous .bak naming risked clobbering user-managed backups, so 0.2.0
// switched to a cc-pane-specific suffix.
const bakSuffix = ".cc-pane.bak"

// mergeCodexHooks ensures the cc-pane managed config block exists in path.
// Returns (changed, error). If dryRun is true, no files are written but the
// proposed addition is printed as a +-prefixed unified diff.
//
// Behavior matrix:
//   - empty / missing file:                        write the block, changed=true
//   - existing file without markers, no [notify]: append block
//   - existing file without markers, [notify] set: error (would clash)
//   - block already present + valid:               changed=false (idempotent)
//   - block present but legacy [[hooks.X]] form:   rewrite to [notify], changed=true
//   - block present but empty/broken:              rewrite, changed=true (warn)
//   - begin marker but no end marker:              error (refuse to modify)
//   - target is symlink:                           error (refuseSymlink)
func mergeCodexHooks(path string, dryRun bool) (bool, error) {
	if err := refuseSymlink(path); err != nil {
		return false, err
	}

	data, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return false, fmt.Errorf("read %s: %w", path, err)
	}
	content := string(data)

	beginIdx, endIdx, found := findCodexBlock(content)
	if found {
		block := content[beginIdx:endIdx]
		if isCurrentCodexBlock(block) {
			return false, nil
		}
		// Either the legacy [[hooks.X]] form (which codex CLI ignored) or an
		// empty/broken block — both need a rewrite to the working [notify] form.
		fmt.Fprintf(os.Stderr, "cc-pane: rewriting cc-pane block in %s (migrating to [notify] form)\n", path)
		content = content[:beginIdx] + content[endIdx:]
	} else if hasOnlyBeginMarker(content) {
		return false, fmt.Errorf("%s contains begin marker but no end marker; refusing to modify (run cc-pane uninstall or fix manually)", path)
	}

	// Refuse if the user already has their own notify entry outside our block —
	// TOML disallows duplicate keys and we don't want to clobber their script.
	if hasUnmanagedNotify(content) {
		return false, fmt.Errorf("%s already defines a notify entry outside the cc-pane block; refusing to add a second one. Remove or merge it manually, then re-run setup", path)
	}

	if len(content) > 0 && !strings.HasSuffix(content, "\n") {
		content += "\n"
	}
	newContent := content + codexBlockText()

	if dryRun {
		printDryRunDiff(path, codexBlockText())
		return true, nil
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return false, fmt.Errorf("mkdir %s: %w", filepath.Dir(path), err)
	}

	if data != nil {
		if err := os.WriteFile(path+bakSuffix, data, 0o644); err != nil {
			return false, fmt.Errorf("write bak: %w", err)
		}
	}

	if err := os.WriteFile(path, []byte(newContent), 0o644); err != nil {
		return false, fmt.Errorf("write %s: %w", path, err)
	}
	return true, nil
}

// isCurrentCodexBlock reports whether a marker-bounded block matches the
// current canonical form (notify argv array pointing at cc-pane). Older
// forms — [[hooks.X]] arrays from spec-time copy-paste, or [notify] tables
// from the first pivot attempt — return false so they're rewritten on next
// setup. The `[[hooks.` and `[notify]` exclusions are explicit so a future
// drift toward either is also caught.
func isCurrentCodexBlock(block string) bool {
	if strings.Contains(block, "[[hooks.") {
		return false
	}
	if strings.Contains(block, "[notify]") {
		return false
	}
	if !strings.Contains(block, "notify = [") {
		return false
	}
	if !strings.Contains(block, "cc-pane") {
		return false
	}
	return true
}

// hasUnmanagedNotify reports whether content (with the cc-pane block
// already excised) still contains a notify entry — meaning the user has
// their own one we'd clash with.
func hasUnmanagedNotify(content string) bool {
	scanner := bufio.NewScanner(strings.NewReader(content))
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "[notify]" {
			return true
		}
		if strings.HasPrefix(line, "notify ") || strings.HasPrefix(line, "notify=") {
			return true
		}
	}
	return false
}

// hasOnlyBeginMarker returns true when the content has a begin marker but no
// matching end marker — a corrupt state we refuse to auto-repair.
func hasOnlyBeginMarker(content string) bool {
	scanner := bufio.NewScanner(strings.NewReader(content))
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	hasBegin := false
	hasEnd := false
	for scanner.Scan() {
		trimmed := strings.TrimRight(scanner.Text(), " \t\r\n")
		if trimmed == codexBeginMarker {
			hasBegin = true
		}
		if trimmed == codexEndMarker {
			hasEnd = true
		}
	}
	return hasBegin && !hasEnd
}

// refuseSymlink returns an error when path is a symlink.
// A missing path is OK (we will create the file).
func refuseSymlink(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("lstat %s: %w", path, err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("%s is a symlink; refuse to overwrite. Remove the symlink or run cc-pane uninstall first", path)
	}
	return nil
}

// printDryRunDiff outputs the proposed block as a +-prefixed unified diff style.
func printDryRunDiff(path, block string) {
	fmt.Println(path)
	for _, line := range strings.Split(strings.TrimSuffix(block, "\n"), "\n") {
		fmt.Println("+ " + line)
	}
}

// removeCodexHooks removes the cc-pane managed block from path. Returns
// (changed, error). If only the begin marker is present (corrupt state),
// emit a warning and return (false, nil) — uninstall must not auto-fix
// suspicious config edits.
func removeCodexHooks(path string) (bool, error) {
	if err := refuseSymlink(path); err != nil {
		return false, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, fmt.Errorf("read %s: %w", path, err)
	}
	content := string(data)
	beginIdx, endIdx, found := findCodexBlock(content)
	if !found {
		if hasOnlyBeginMarker(content) {
			fmt.Fprintf(os.Stderr, "cc-pane: %s has begin marker but no end marker; refuse to auto-fix.\n", path)
		}
		return false, nil
	}

	if err := os.WriteFile(path+bakSuffix, data, 0o644); err != nil {
		return false, fmt.Errorf("write bak: %w", err)
	}

	// Trim a single leading newline that might precede the begin marker
	// (mergeCodexHooks writes "\n##### cc-pane:begin #####").
	if beginIdx > 0 && content[beginIdx-1] == '\n' {
		beginIdx--
	}
	newContent := content[:beginIdx] + content[endIdx:]
	if err := os.WriteFile(path, []byte(newContent), 0o644); err != nil {
		return false, fmt.Errorf("write %s: %w", path, err)
	}
	return true, nil
}
