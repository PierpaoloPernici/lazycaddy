# Repository Guidelines

VISION.md is the product compass, PLAN.md is the canonical product specification, and AGENTS.md is the canonical contributor and agent guide. Fold durable decisions into these files instead of creating parallel session summaries or handover documents as long-lived sources of truth.

## Project Structure & Module Organization

Planned Go layout:

- `cmd/lazycaddy/` — executable entry point.
- `internal/app/` and `internal/ui/` — state, orchestration, views, and keybindings.
- `internal/caddyfile/` — source ranges, parsing, import resolution, patching, and file access.
- `internal/model/` and `internal/config/` — domain models and CLI/application settings.
- `internal/validator/`, `internal/backup/`, and `internal/diff/` — validation, persistence, and diff workflows.
- `internal/runtime/`, `internal/logs/`, and `internal/tls/` — runtime and operational integrations.
- Put fixtures beside the package they exercise or under `testdata/`.

Preserve the Caddyfile as the source of truth. Do not introduce a JSON-based configuration model or rewrite unrelated source ranges. Imported files remain separate documents, and every edit must identify the exact file it changes.

## Build, Test, and Development Commands

The repository includes a minimal Makefile and GitHub Actions workflow. Use:

```sh
make fmt          # Format Go sources
make test         # Run all unit and integration tests
make vet          # Run baseline static checks
make check        # Run formatting, tests and vet
make run          # Run locally
```

The equivalent direct commands are `gofmt -w .`, `go test ./...`, `go vet ./...`, and `go run ./cmd/lazycaddy`.

Use Go with Bubble Tea, Bubbles, Lip Gloss, Cobra, and `fsnotify`; keep dependencies focused and document deviations.
Tests must not require a locally installed Caddy daemon or network access; use fake commands, filesystems, clocks, and Admin API clients.

## Coding Style & Naming Conventions

Use idiomatic Go and `gofmt`. Keep packages small. UI models should emit intents and depend on application interfaces; they must not import concrete filesystem, command-execution, or Admin API adapters directly. Bubble Tea screens should be independent models communicating through explicit messages. Use `CamelCase` for exported identifiers, `camelCase` for unexported identifiers, and names such as `SourceRange` or `ValidationDiagnostic`. Prefer interfaces at integration boundaries for deterministic tests.

## Language

The user may communicate in any language, but all repository artifacts must be written in English. This includes source code, identifiers, comments, documentation, test descriptions, user-facing CLI/TUI text, error messages, and Git commit messages. Keep conversational replies in the user's language unless they request otherwise.

## Testing Guidelines

Test import resolution, nested/globbed imports and cycles, source-range parsing, byte-preserving patches, `$EDITOR` round-trips, validation guards, atomic backups, `fsnotify` conflicts, diff confirmation, permission failures, read-only fallback, capability gating, saved/validated/loaded states, and runtime timeouts. Before multi-file editing is enabled, test transaction preflight and complete rollback. Name tests `Test<Behavior>`; use table-driven cases where useful. Every write or reload operation needs guard-condition tests.

## Caddy Compatibility Monitoring

When a task affects parsing, directives, modules, runtime integration, the Admin API, or supported Caddy versions, review current compatibility information from these sources:

- GitHub Releases and official release notes;
- official Caddy documentation;
- relevant GitHub Issues and pull requests;
- Caddy forum announcements and maintainer guidance.

Use releases and official documentation as authoritative. Treat Issues, pull requests and forum discussions as signals that must be verified before changing behavior. For each relevant Caddy release, check parser fixtures, source-range patching, Admin API behavior, capability detection, structured UI summaries, regression tests and supported-version documentation. Preserve unknown directives even when support for their semantics is not yet implemented.

## Commit & Pull Request Guidelines

Use short imperative commit subjects, preferably Conventional Commit style (for example, `feat: add source range parser`). PRs should explain the change, list tests, document safety implications, and attach terminal screenshots for UI changes.

## Security & Configuration Tips

Never log secrets or blindly expose command output. Keep UI code away from file writes and shell execution. Validate temporary files, back up before replacement, detect concurrent changes, and require confirmation for reloads or destructive actions. Use least privilege: browsing must work without elevation, missing capabilities must disable only affected actions, and the entire TUI must not require `root`. Any future elevation mechanism must be explicit, narrowly scoped and isolated behind a dedicated adapter.
