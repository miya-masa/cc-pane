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

// codexHooksConfigured reports whether cc-pane has written its managed hook
// block to ~/.codex/config.toml AND the block contains at least one cc-pane
// update-state command line.
func codexHooksConfigured() bool {
	data, err := os.ReadFile(codexConfigPath())
	if err != nil {
		return false
	}
	beginIdx, endIdx, ok := findCodexBlock(string(data))
	if !ok {
		return false
	}
	block := string(data)[beginIdx:endIdx]
	return strings.Contains(block, "cc-pane update-state")
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

// codexHookEvents lists Codex hook events cc-pane registers.
// SessionStart appears first so SessionEnd of a prior agent is followed by the
// new agent's SessionStart in normal lifecycles.
var codexHookEvents = []string{
	"SessionStart",
	"UserPromptSubmit",
	"PreToolUse",
	"PostToolUse",
	"PermissionRequest",
	"Notification",
	"PreCompact",
	"PostCompact",
	"Stop",
	"SessionEnd",
}

// codexBlockText returns the canonical block written to config.toml.
func codexBlockText() string {
	var sb strings.Builder
	sb.WriteString("\n")
	sb.WriteString(codexBeginMarker + "\n")
	sb.WriteString("# cc-pane managed hooks. Do not edit between begin/end markers.\n")
	for _, ev := range codexHookEvents {
		fmt.Fprintf(&sb, "[[hooks.%s]]\n", ev)
		fmt.Fprintf(&sb, "command = \"cc-pane update-state --event %s --agent codex\"\n", ev)
		sb.WriteString("async = true\n\n")
	}
	sb.WriteString(codexEndMarker + "\n")
	return sb.String()
}

// bakSuffix is the suffix used for backup files created by setup/uninstall.
// The previous .bak naming risked clobbering user-managed backups, so 0.2.0
// switched to a cc-pane-specific suffix.
const bakSuffix = ".cc-pane.bak"

// mergeCodexHooks ensures the cc-pane managed hook block exists in path.
// Returns (changed, error). If dryRun is true, no files are written but the
// proposed addition is printed as a +-prefixed unified diff.
//
// Behavior matrix:
//   - empty / missing file:           write the block, changed=true
//   - existing file without markers: append block (with newline correction)
//   - block already present + valid: changed=false (idempotent)
//   - block present but empty/broken: rewrite, changed=true (warn to stderr)
//   - begin marker but no end marker: error (refuse to modify)
//   - target is symlink:              error (refuseSymlink)
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
		if strings.Contains(block, "cc-pane update-state") {
			return false, nil
		}
		fmt.Fprintf(os.Stderr, "cc-pane: rewriting empty/broken cc-pane block in %s\n", path)
		content = content[:beginIdx] + content[endIdx:]
	} else if hasOnlyBeginMarker(content) {
		return false, fmt.Errorf("%s contains begin marker but no end marker; refusing to modify (run cc-pane uninstall or fix manually)", path)
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
