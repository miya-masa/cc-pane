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

// codexInstalled reports whether Codex CLI appears to be installed (spec §6.2).
// Either the config file or the binary on PATH counts; an empty ~/.codex/
// directory alone does NOT.
func codexInstalled() bool {
	if _, err := os.Stat(codexConfigPath()); err == nil {
		return true
	}
	if _, err := exec.LookPath("codex"); err == nil {
		return true
	}
	return false
}

// codexHooksConfigured reports whether cc-pane has written its managed hook
// block to ~/.codex/config.toml AND the block contains at least one cc-pane
// update-state command line (spec §6.2.2 / §6.4).
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
// match after right-trim of whitespace; spec §7.2 step 2). Returns
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

// codexHookEvents lists Codex hook events cc-pane registers (spec §7.3).
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
