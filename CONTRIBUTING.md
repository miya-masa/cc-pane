# Contributing to cc-pane

Contributions to cc-pane are welcome!

## Development Environment

- Go 1.22 or later
- tmux (for manual testing)

## Build & Test

```bash
make build    # Build
make test     # Run tests
make lint     # Run go vet
```

## Coding Guidelines

- Format code with `gofmt`
- Pass `go vet`
- Do not add external dependencies (standard library only)

## Commit Messages

Use [Conventional Commits](https://www.conventionalcommits.org/) format:

| Prefix | Purpose |
|--------|---------|
| `feat:` | New feature |
| `fix:` | Bug fix |
| `docs:` | Documentation changes |
| `refactor:` | Refactoring |
| `test:` | Add or update tests |
| `chore:` | Build, CI, and other housekeeping |

## Issues

- **Bug reports**: Include steps to reproduce, expected behavior, and actual behavior
- **Feature requests**: Describe the use case and expected behavior

## Pull Requests

1. Create a feature branch from `main`
2. Implement changes and add tests
3. Ensure `make test` and `make lint` pass
4. Open a PR against `main`

Keep each PR focused on a single change.
