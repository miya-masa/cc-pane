# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/).

## [Unreleased]

### Added

- Support for OpenAI Codex CLI: `cc-pane setup` configures the `[notify]` table in `~/.codex/config.toml` so Codex panes appear in `cc-pane ls` after each turn completes (Codex CLI v0.x only exposes turn-end hooks, not per-tool events — see Known Limitations in README).
- `--agent` flag for `cc-pane update-state` (values: `claude`, `codex`).
- `AGENT` column in `cc-pane ls` / `cc-pane watch` output (shows `CC` / `CX` / `??`).
- `agent` field in `--json` output.
- `--no-claude` / `--no-codex` / `--agent <name>` flags for `cc-pane setup`.
- Codex CLI detection in `cc-pane doctor` with `~/.codex/hooks.json` precedence warning.

### Changed

- `--tsv` output now includes the agent in column 2 (BREAKING for tsv consumers — pane_id remains in column 1).
- OSC 9 approval notification now includes the agent label (e.g., `cc-pane: codex approval needed`).
- Backup file naming changed from `<path>.bak` to `<path>.cc-pane.bak` to avoid clobbering user-managed backups.

[Unreleased]: https://github.com/miya-masa/cc-pane/compare/v0.1.0...HEAD
