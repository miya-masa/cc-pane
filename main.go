package main

import (
	"fmt"
	"os"
)

const version = "0.1.0"

func main() {
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(0)
	}

	var err error
	switch os.Args[1] {
	case "ls":
		err = cmdLs(os.Args[2:])
	case "pick":
		err = cmdPick(os.Args[2:])
	case "show":
		err = cmdShow(os.Args[2:])
	case "jump":
		err = cmdJump(os.Args[2:])
	case "refresh":
		err = cmdRefresh()
	case "doctor":
		err = cmdDoctor()
	case "update-state":
		err = cmdUpdateState(os.Args[2:])
	case "version", "--version", "-v":
		fmt.Printf("cc-pane v%s\n", version)
	case "help", "--help", "-h":
		printUsage()
	default:
		fmt.Fprintf(os.Stderr, "unknown command: %s\n\n", os.Args[1])
		printUsage()
		os.Exit(1)
	}

	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

func printUsage() {
	fmt.Print(`cc-pane - lightweight Claude Code session monitor for tmux

Usage:
  cc-pane <command> [options]

Commands:
  ls             List all Claude Code sessions
  pick           Select a session with fzf and jump to it
  show           Show state and pane output for a specific pane
  jump           Jump to a specific pane
  refresh        Clean up stale state files
  doctor         Check dependencies and configuration
  update-state   Update pane state (called by Claude Code hooks)
  version        Show version
  help           Show this help

Options:
  ls --json      Output as JSON
  jump --pane ID Pane ID to jump to (e.g., %12)
  update-state --event TYPE
                 Event type: UserPromptSubmit, PreToolUse, PostToolUse, Stop, Notification
`)
}
