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
make build        # Build a local binary in bin/
make dist         # Build local GoReleaser snapshot artifacts
make clean        # Remove generated local artifacts
make coverage     # Generate coverage.out and print the summary
make fmt-check    # Verify formatting without changing files
make release-check # Validate the GoReleaser configuration
make test-race    # Run tests with the race detector
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

Fuzz targets live beside the code they exercise (currently `internal/caddyfile/fuzz_test.go`: `FuzzParseNoPanic`, `FuzzPatchRoundTrip`). Plain `go test` only replays their seed corpus plus any recorded crasher; run `make fuzz` (or `go test ./internal/caddyfile/ -run '^$' -fuzz FuzzParseNoPanic`) to explore with the real fuzzer before parser- or patch-related releases. Crashers are written to `testdata/fuzz/<Name>/` and replayed by every subsequent test run, so a found regression can never hide.

## Caddy Compatibility Monitoring

When a task affects parsing, directives, modules, runtime integration, the Admin API, or supported Caddy versions, review current compatibility information from these sources:

- GitHub Releases and official release notes;
- official Caddy documentation;
- relevant GitHub Issues and pull requests;
- Caddy forum announcements and maintainer guidance.

Use releases and official documentation as authoritative. Treat Issues, pull requests and forum discussions as signals that must be verified before changing behavior. For each relevant Caddy release, check parser fixtures, source-range patching, Admin API behavior, capability detection, structured UI summaries, regression tests and supported-version documentation. Preserve unknown directives even when support for their semantics is not yet implemented.

## Git, Commit, Pull Request, and Merge Workflow

Before the 1.0 release, `main` is the maintainer's active integration branch and
direct pushes are allowed while the project is developed by a single maintainer.
Feature branches and pull requests are still recommended for substantial or
isolated changes. Before the project approaches 1.0, enable branch protection so
all changes go through a pull request and the required GitHub Actions check
named `test` passes before merging.

### Release-ready change contract

Every commit and pull request must be written as if it could be included in the
next release without renaming or rewriting it later. Release preparation must
not be used to repair placeholder commit messages, vague pull request titles or
missing changelog classification.

- Commit subjects use Conventional Commits with a supported English type such
  as `feat`, `fix`, `docs`, `test`, `refactor`, `perf`, `chore`, `ci`, `build`
  or `revert`; an optional scope should identify the affected area, for example
  `feat(logs): add journalctl source`. The scope, when present, must be a
  single identifier matching `[a-z0-9._/-]*` (no commas, spaces or multiple
  scopes like `fix(tls, runtime)`); when several areas are touched, use a
  single most relevant scope or no scope at all and detail the other areas
  in the body. The repository validates this with
  `^\(feat|fix|...)(\([a-z0-9][a-z0-9._/-]*\))?!?: .+` in
  `scripts/check-release-metadata.sh`.
- Subjects are short, imperative, specific and final. Do not use messages such
  as `updates`, `work in progress`, `fix stuff`, or release-numbered
  placeholders. Do not add a version number to a commit or pull request title
  merely to make release notes look current.
- Breaking changes use Conventional Commit breaking-change syntax (`!` in the
  subject and/or a `BREAKING CHANGE:` footer) and must carry the
  `breaking-change` pull request label with migration notes.
- Pull request titles use the same Conventional Commit form as the final
  change, in English, and must remain release-ready before merge. A title must
  describe the user-visible outcome or the concrete maintenance change, not the
  temporary implementation task.
- Before merge, every pull request receives an intentional release disposition
  matching `.github/release.yml`: `breaking-change`, `enhancement`, `bug`,
  `dependencies`, `github_actions`, `documentation`, or the explicit
  `skip-changelog` label. Do not use `skip-changelog` for user-visible changes;
  do not leave release classification to be fixed during tagging.
- The CI workflow validates the release disposition from the pull request
  event payload and re-runs on PR metadata events, including `labeled` and
  `unlabeled`: applying or changing the disposition label re-triggers the
  `test` check with the updated payload. The workflow does not run on the
  initial `opened` event, so a pull request without a disposition label has
  no `test` run yet; do not rely on closing and reopening to refresh a
  stale check.
- The pull request body must include motivation, user-visible impact, safety
  implications, verification commands/results and any release-note or
  migration detail needed by a maintainer. If the change is intentionally
  internal, say why it should be excluded from release notes. Use this
  template (as in PR #49 and #52):

  ```markdown
  ## Motivation
  ## User-visible impact
  ## Safety
  ## Verification
  ## Release note
  ```
  Missing sections or a short body like "Implements the handover..."
  will fail review even if `make check` is green — the body is part of
  the release-ready contract and is validated by the `enhancement`/`bug`
  label check.
- If a pull request's scope or user impact changes during review, update its
  title, labels and description before merge. The same rule applies to direct
  commits on `main`; direct push permission does not relax release hygiene.
- Temporary `fixup!` or `squash!` commits may be used during review only when
  they are squashed or rewritten before merge. The merged history must contain
  only release-ready commit subjects; release preparation must not rewrite
  published history.
- Prefer one coherent release concern per pull request. Split unrelated work
  instead of relying on a later release edit to explain a mixed change.

### Branches

- Start from an up-to-date `main` branch.
- Use a focused branch for each change, with a descriptive prefix such as `feat/`, `fix/`, `docs/`, `test/`, or `chore/`.
- Before 1.0, direct work on `main` is allowed for small, local changes. Use a
  focused branch for substantial changes, experiments, UI work or changes that
  benefit from an isolated review.
- Keep unrelated work out of the branch. If the scope changes materially, create a separate branch or pull request.

### Commits

- Use short, imperative English subjects in Conventional Commit style, for example `feat: add source range parser`.
- Keep commits focused and logically reviewable. Do not mix formatting-only changes with feature work unless required.
- Before committing, inspect the diff and run `make check` plus `git diff --check`.
- Confirm the final subject, body, scope and intended release disposition before creating the commit; do not defer commit cleanup until release preparation.
- Never commit secrets, real credentials, private homelab data, generated binaries, or temporary files.
- Do not create a local merge commit on `main` for work that is intended to be delivered through a pull request.

### Pull requests

- Do not create a pull request, push a PR branch to `origin`, or merge a
  pull request without explicit authorization from the repository
  maintainer. Prepare the branch and commits (as documented above) and stop
  before opening or merging a PR until the maintainer asks for it. This
  applies to direct pushes on `main` as well: do not merge or open a PR
  until authorization is given.
- When using a feature branch, push it to `origin` and open a pull request
  targeting `main`.
- Write the title and description in English and follow the release-ready change contract above. The description must explain the motivation, summarize the changes, list verification commands and results, and document safety implications.
- UI changes should include a terminal screenshot or recording when it helps reviewers evaluate the result.
- Wait for the `test` status check to complete successfully when using a pull
  request. A local test run does not replace CI verification.
- If a check remains `Expected` or `Waiting for status to be reported`, inspect the Actions run and repository status before merging. Do not bypass the protection or invent a successful status; retry the workflow or push a harmless new commit only when appropriate.
- Resolve review comments and merge the pull request through GitHub when a PR is
  being used. Before 1.0, do not create unnecessary local merge commits merely
  to simulate a pull request workflow.

### After merging

- Update the local repository with `git fetch origin` after a remote merge or
  when switching back from a feature branch.
- Ensure the working tree is clean, then align local `main` with `origin/main`.
- Delete the local and remote feature branches only after confirming that the pull request is merged and no unique work remains.
- Keep active Dependabot branches until their pull requests are merged or closed.

## Security & Configuration Tips

Never log secrets or blindly expose command output. Keep UI code away from file writes and shell execution. Validate temporary files, back up before replacement, detect concurrent changes, and require confirmation for reloads or destructive actions. Use least privilege: browsing must work without elevation, missing capabilities must disable only affected actions, and the entire TUI must not require `root`. Any future elevation mechanism must be explicit, narrowly scoped and isolated behind a dedicated adapter.
