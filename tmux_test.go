package main

import (
	"strings"
	"testing"
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
