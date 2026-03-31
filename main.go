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
	case "show":
		err = cmdShow(os.Args[2:])
	case "jump":
		err = cmdJump(os.Args[2:])
	case "rm":
		err = cmdRm(os.Args[2:])
	case "refresh":
		err = cmdRefresh()
	case "doctor":
		err = cmdDoctor()
	case "setup":
		err = cmdSetup(os.Args[2:])
	case "uninstall":
		err = cmdUninstall(os.Args[2:])
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
  setup          Configure Claude Code hooks and tmux keybinding
  ls             List all Claude Code sessions
  show           Show state and pane output for a specific pane
  jump           Jump to a specific pane
  rm             Remove a state entry (fzf picker or --pane %ID)
  refresh        Clean up stale state files
  doctor         Check dependencies and configuration
  uninstall      Remove cc-pane hooks and tmux keybinding
  update-state   Update pane state (called by Claude Code hooks)
  version        Show version
  help           Show this help

Options:
  setup --dry-run   Show what would be changed without writing
  uninstall --purge Also remove state directory
`)
}
