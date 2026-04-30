package main

import (
	"strings"
	"testing"
	"time"
)

// buildApprovalMessage is extracted for unit testing (the actual file write
// side of notifyApproval is exercised via integration). The OSC 9 payload
// must allow-list agent labels: claude / codex / others → "unknown agent".
func TestBuildApprovalMessageSanitizesAgent(t *testing.T) {
	cases := []struct {
		agent       string
		wantContain string
		notContain  []string
	}{
		{AgentClaude, "claude approval needed", nil},
		{AgentCodex, "codex approval needed", nil},
		{AgentUnknown, "unknown agent approval needed", nil},
		{"", "unknown agent approval needed", nil},
		{"$(rm -rf ~)", "unknown agent approval needed", []string{"rm -rf"}},
		{"\033[31m", "unknown agent approval needed", []string{"\033[31m"}},
	}
	for _, c := range cases {
		msg := buildApprovalMessage(c.agent)
		if !strings.Contains(msg, c.wantContain) {
			t.Errorf("agent=%q: message missing %q\nfull: %q", c.agent, c.wantContain, msg)
		}
		for _, bad := range c.notContain {
			if strings.Contains(msg, bad) {
				t.Errorf("agent=%q: message must not contain %q\nfull: %q", c.agent, bad, msg)
			}
		}
	}
}

func TestLooksLikeCodexProcessLine(t *testing.T) {
	cases := []struct {
		line string
		want bool
	}{
		{"codex /path/to/vendor/codex", true},
		{"node node /home/user/.local/bin/codex", true},
		{"node node /home/user/.local/lib/node_modules/@openai/codex/bin/codex.js", true},
		{"node node /home/user/app/server.js", false},
		{"zsh -zsh", false},
	}
	for _, c := range cases {
		if got := looksLikeCodexProcessLine(c.line); got != c.want {
			t.Errorf("looksLikeCodexProcessLine(%q) = %v, want %v", c.line, got, c.want)
		}
	}
}

func TestLooksLikeCodexApprovalPrompt(t *testing.T) {
	content := `Would you like to run the following command?

  $ git status

› 1. Yes, proceed (y)
  2. Yes, and don't ask again for commands that start with git status (p)
  3. No, and tell Codex what to do differently (esc)`

	if !looksLikeCodexApprovalPrompt(content) {
		t.Fatal("expected Codex approval prompt")
	}
	if looksLikeCodexApprovalPrompt("ordinary Codex output") {
		t.Fatal("ordinary output must not be treated as approval prompt")
	}
}

func TestLooksLikeCodexApprovalPromptIgnoresApprovedPrompt(t *testing.T) {
	content := `Would you like to run the following command?

  $ git status

› 1. Yes, proceed (y)
  2. Yes, and don't ask again for commands that start with git status (p)
  3. No, and tell Codex what to do differently (esc)

✔ You approved codex to run git status this time

• Ran git status
  └ ## codex-support`

	if looksLikeCodexApprovalPrompt(content) {
		t.Fatal("approved prompt must not be treated as active approval_waiting")
	}
}

func TestParseCodexProcessStartedAt(t *testing.T) {
	now := time.Date(2026, 4, 30, 18, 30, 0, 0, time.UTC)
	got, ok := parseCodexProcessStartedAt("300 codex /path/to/codex", now)
	if !ok {
		t.Fatal("expected codex process")
	}
	if want := now.Add(-5 * time.Minute); !got.Equal(want) {
		t.Fatalf("startedAt = %s, want %s", got, want)
	}

	if _, ok := parseCodexProcessStartedAt("300 zsh -zsh", now); ok {
		t.Fatal("zsh must not be treated as codex process")
	}
}
