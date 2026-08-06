# lazycaddy

> A keyboard-first terminal UI for inspecting and managing Caddy without taking ownership of the Caddyfile.

## Product promise

The Caddyfile remains the only source of truth. `lazycaddy` wraps Caddy's formatter, validator, CLI and Admin API rather than reimplementing Caddy. It reads the file, presents useful structure, and applies the smallest possible edit to the original source. Unsupported syntax, comments, whitespace and directive ordering must survive a read/edit/write cycle.

Every operation that can change configuration is explicit, reviewable and recoverable:

1. edit a working copy
2. format and validate
3. show the diff
4. create a backup
5. ask for confirmation
6. write the file and optionally reload Caddy

The UI must never reload Caddy implicitly after an edit.

`lazycaddy` is not a web GUI or a greenfield configuration generator. The raw Caddyfile must remain accessible, and every structured view is a projection over the user's existing source rather than a replacement for it.

## Current implementation status

The repository has completed the configuration-engine spike and the first
read-only TUI vertical slice.

Completed:

- Go module, repository scaffold, development commands and CI checks;
- representative sanitized Caddyfile fixtures, including homelab, imports,
  snippets, nested blocks and unknown directives;
- lossless lexer with comments, quotes, backticks, heredocs, escaped newlines,
  CRLF, BOM and exact token offsets;
- token-driven parser and source ranges for global options, sites, snippets,
  named routes, nested directives and brace-less single-site files;
- byte-preserving range patching with invalid-range guards;
- import resolver with separate documents, relative paths, sorted globs,
  snippet precedence, warnings, duplicate detection and cycle detection;
- propagation of root and imported-document parse errors;
- formatter, test and vet commands plus GitHub Actions configuration.
- testable application state and a read-only Bubble Tea inspector with
  document/site navigation, raw source viewing and parse-error fallback;
- CLI configuration loading with an explicit read-only mode and no file writes
  or Caddy daemon dependency.

Next milestone:

- add formatting, validation and diff workflows around a temporary working
  copy.

The resolver intentionally records snippet arguments and `{block}` data without
substituting them yet. That expansion belongs to a later milestone and must not
change the original source documents.

## Scope for the first release

### In scope

- Read one local Caddyfile and list its site blocks.
- Resolve imported files and show their source file boundaries.
- Show a structured summary while retaining the exact source range for each item.
- Open a read-only source view with line numbers and search.
- Edit a selected site block as raw Caddyfile text.
- Open the selected source in `$EDITOR` and safely re-import the result.
- Run `caddy fmt` and `caddy validate` before a save or reload.
- Show validation errors with file and line information where Caddy provides it.
- Show a unified diff before applying a change.
- Create timestamped backups before replacing the source file.
- Reload Caddy only after explicit confirmation.
- Show basic Caddy version, Admin API and loaded-configuration status.
- Provide a basic log view when a configured log source is available.
- Remain useful in an explicit read-only mode when the configuration is not writable or runtime capabilities are unavailable.
- Provide clear errors when the Caddy binary, configuration file, or Admin API is unavailable.

### Explicitly out of scope for v0.1

- JSON as an intermediate or persisted configuration format.
- Remote servers, SSH and multi-server state.
- Automatic health checks, metrics and certificate renewal.
- A visual form editor for every Caddy directive.
- Deleting or reordering blocks through the structured view.
- Automatic reloads, background writes or silent formatting of the user's file.
- Built-in privilege escalation or a privileged helper; v0.1 reports insufficient permissions and keeps mutating actions disabled.

## Technology decisions

Use Go with the current stable toolchain. Build the TUI with Bubble Tea, Bubbles and Lip Gloss; use Cobra for CLI flags and commands, Viper only where application configuration benefits from it, and `fsnotify` for external-change detection. Use a focused diff library rather than implementing a diff algorithm in the UI. Keep dependencies minimal and document any deviation from this stack.

Initial local platform support targets Linux and macOS. Windows is deferred until its file replacement, permissions, process and terminal semantics are designed and tested explicitly. Distribution should use versioned single-binary release artifacts with checksums; package-manager support such as Homebrew may follow after the release process is stable.

## UX and safety rules

- Start in read-only mode. The current file path and Caddy version are visible in the header.
- Browsing must not require elevated privileges. Missing write or reload permissions disable only the affected actions and explain how to proceed safely.
- Do not require the entire TUI to run as `root`. Any future privilege boundary must be explicit, narrowly scoped to one confirmed operation and implemented behind a dedicated adapter.
- Enable actions from detected capabilities rather than assumptions about the host environment or service manager.
- Unsaved changes are clearly marked and block accidental navigation or quit until discarded or saved.
- `r` means validate and reload only after a valid, saved configuration and a confirmation prompt; `s` saves without reloading.
- A failed validation never writes the source file and never calls reload.
- A failed reload leaves the saved file and backup intact and reports the command/API error.
- Saving and reloading are separate actions; `validate and reload` is the only combined workflow.
- After a reload, verify the Admin API state and clearly distinguish saved, validated and loaded configuration states.
- Backups are created atomically before the target is replaced; backup failures abort the save.
- File writes use a temporary file in the same directory, `fsync`, then an atomic rename where supported.
- Temporary files are cleaned up on cancellation and failure.
- Symlinks, permissions and ownership must be detected and reported before replacement rather than silently changed.
- Concurrent changes are detected by comparing the file metadata/content captured at load time. The user must reload or explicitly resolve the conflict.
- Destructive actions (`delete`, `stop`, discard changes) require a confirmation that names the target.

## User interface

The interaction style should feel familiar to users of LazyGit, LazyDocker, k9s and btop: terminal-native, keyboard-first, responsive and conservative with animation. Important state must never be communicated by color alone.

```text
+--------------------------------------------------------------+
| Caddy status | version | config path | modified/valid state  |
+-----------------------+--------------------------------------+
| Navigation             | Details                              |
|                       |                                      |
| Sites                 | Structured summary                  |
| Snippets              | or source view                       |
| Imports / Files       |                                      |
| Runtime               |                                      |
| Logs                  |                                      |
| TLS                   |                                      |
+-----------------------+--------------------------------------+
| key hints | validation/error message | backup/reload status   |
+--------------------------------------------------------------+
```

Primary navigation is `Sites`, `Snippets`, `Imports / Files`, `Runtime`, `Logs` and `TLS`. Empty or unsupported sections should explain their state instead of showing a blank panel. The header/status bar must always identify the selected server, configuration path, selected site, unsaved state, validation state and whether the running Caddy matches the file on disk.

Keybindings:

```text
Enter      Open selected item
e          Edit selected source range
Ctrl-E     Toggle raw source view
a          Add (only when supported by the current release)
d          Delete (confirmation required; disabled until structured editing supports it)
v          Format and validate
r          Validate and reload (confirmation required)
s          Save without reload
D          Diff current changes
l          Logs
t          TLS
/          Search
f          Toggle log follow mode
p          Pause or resume logs
?          Help
q          Quit; prompt if changes are unsaved
Esc        Close modal / cancel operation
```

Site state must distinguish at least `active`, `disabled`, `invalid`, `modified but not loaded` and `upstream unreachable`. Themes may choose glyphs and colors for these states, but the underlying state model and accessible text labels must remain explicit.

## Configuration engine

The engine has three representations with explicit source locations:

```text
original bytes
    -> lexer/parser for navigation and summaries
    -> source nodes {kind, name, start line, end line, children}
    -> patch selected source range
working bytes
```

The parser is not allowed to discard unknown directives. A parse failure must still leave the raw file view available and must identify the failing range. The initial parser may support only site blocks, imports and common directives; all other text is represented as opaque nodes.

Imported files remain separate source documents with their own ranges and write permissions. The browser may resolve imports for navigation, but an edit must identify the exact file it changes. If lossless structured editing is not possible for a construct, fall back to raw editing and state that limitation in the UI.

Represent imports as a graph with explicit source documents. Resolve nested relative imports, glob expansion and cycles deterministically according to Caddy's behavior. In v0.1, one edit operation changes exactly one source file. Any future operation spanning multiple files must preflight every target, create every backup before the first write and either commit or roll back the complete set.

Editing rules:

- Never serialize a reconstructed global configuration.
- Patch only the selected range; preserve all untouched bytes exactly.
- Keep the original bytes, working bytes and last validated bytes separate.
- Use `caddy fmt` on a temporary working file, never directly on the user's source.
- Make the formatter's result visible in the diff before applying it.
- Treat Caddy's parser/formatter output as an implementation detail, not as the application's data model.
- Keep generic directive data (name, arguments, children and raw source) underneath specialized summaries; do not model every site as a reverse proxy.

Watch loaded files with `fsnotify`. If an external change arrives while there are pending edits, never overwrite it: offer reload, compare, or keep the in-memory version, with recoverable copies when needed.

Common structured summaries should initially cover `reverse_proxy`, `tls`, `encode`, `log`, `file_server`, `php_fastcgi`, `header`, `redir`, `respond` and `import`. Unknown directives remain visible in the source view and summary.

## Validation, diff and backup workflow

```text
load -> edit working copy -> caddy fmt (temporary file)
  -> caddy validate --config <temporary-file>
      -> errors: show locations, keep editing
      -> valid: calculate unified diff
          -> cancel: keep working copy, do not write
          -> confirm: backup original -> atomic write -> optional reload
```

Validation must be cancellable, must capture stdout/stderr and must enforce a timeout. The command path, arguments and exit status are shown in a diagnostic view. The application must not expose secrets from environment variables or command output unnecessarily.

Backups live beside the configuration in a configurable directory (default: `<config-dir>/.lazycaddy/backups/`) and use a collision-safe timestamp plus sequence number, for example:

```text
2026-08-01T20-10-00-001-Caddyfile
```

The backup index must be rebuildable from files on disk. Retention is configurable and disabled by default until a cleanup policy is implemented. A future rollback operation must itself follow the same validate, diff, backup and confirmation workflow.

## Runtime integration

The runtime adapter is isolated from the UI and supports these operations in v0.1:

- detect the configured Caddy binary and query its version;
- expose capabilities such as readable configuration, writable configuration, validation, Admin API access and reload support;
- inspect the loaded configuration through the local Admin API, read-only;
- reload through the local Admin API, with configurable endpoint and timeout;
- report whether the loaded configuration matches the validated file on disk.

Process/service-manager integration (`systemd`, Docker, launchd, etc.) is deferred. Runtime status must distinguish `running`, `stopped`, `unreachable` and `unknown`; absence of the Admin API must not prevent browsing or validating the file.

Do not claim that the loaded configuration matches the file unless that state can be proven. Record successful reload identity locally where possible and show `unknown` when comparison is ambiguous; do not infer equality by regenerating a Caddyfile from Admin API JSON. Future capability discovery may inspect `caddy list-modules` to improve version- and plugin-aware summaries, but unknown modules must never block browsing or preservation.

Later runtime features may add restart and stop, but stop must never be enabled without an explicit service adapter and a target-specific confirmation.

## Modules

Keep dependencies flowing in one direction:

```text
cmd/lazycaddy
       -> app
       -> ui
       -> caddyfile -> validation -> backup/diff
       -> runtime
       -> logs / tls
```

Suggested package boundaries:

```text
internal/app/          orchestration, state machine, commands
internal/ui/           views, keymaps, modals, terminal rendering
internal/caddyfile/    source ranges, parser, imports, patching, file I/O
internal/model/        generic sites, directives, snippets and certificates
internal/config/       CLI flags and application settings
internal/validator/    fmt/validate commands and diagnostics
internal/backup/       atomic backup creation and retention hooks
internal/diff/          unified diff generation and rendering model
internal/runtime/      binary discovery and Admin API client
internal/logs/         log source abstraction and filtering
internal/tls/          certificate source abstraction
```

The UI must depend on interfaces for filesystem, command execution, clock and runtime calls so safety-critical workflows can be tested without a running Caddy instance.

## Delivery roadmap

Implement the product in this order and do not advance a later capability before the preceding safety boundary is proven:

```text
lossless parser and patcher spike
    -> read-only browser
    -> format, validate and diff
    -> backup and atomic single-file save
    -> explicit reload and loaded-state verification
    -> logs and TLS
    -> structured editing
    -> remote servers and multi-file transactions
```

### Phase 0 — bootstrap

- [x] Choose Go as the implementation language and establish module/toolchain versions.
- [x] Complete a lossless-editing spike before building the full TUI: parse representative fixtures, identify exact source ranges and patch one directive without changing any unrelated byte.
- [x] Cover imports, comments, nested blocks, malformed input and unknown/plugin directives with golden fixtures during the spike.
- [x] Add a minimal TUI shell, configuration loading flags and a testable application state model.
- [x] Add CI for formatting, static analysis and unit tests.

Acceptance: the spike proves targeted byte-preserving patches across the fixture corpus; the binary starts, shows a helpful empty state, exits cleanly and has deterministic unit tests.

Status: Phase 0 is complete. The lossless parser, patcher, import resolver,
TUI shell and application state model are implemented and covered by local
tests and CI.

### v0.1 — read-only inspector with a safe vertical slice

- [ ] Load a configured or default Caddyfile path and resolve imports. The
  resolver engine is complete; application integration is pending.
- [ ] Parse site blocks/common directives with source ranges and opaque nodes.
  The parser engine is complete; application integration is pending.
- Show sites, raw source, search, basic runtime/version information and diagnostics.
- Detect capabilities and fall back to read-only mode without blocking inspection.
- Support `$EDITOR`, temporary-file formatting and validation.
- Implement diff, timestamped backup, explicit save and explicit Admin API reload.
- Add a basic log view when a configured source is available.

Acceptance: an existing Caddyfile containing comments, unknown directives, nested blocks and imports can be opened without data loss; parse failures still permit raw viewing; invalid or cancelled changes cannot write or reload.

### v0.2 — change safety and navigation

- Improve diff review, unsaved-state prompts and error recovery.
- Detect external changes with `fsnotify` and provide reload/compare/keep actions.
- Add backup comparison, rollback and configurable retention.
- Add search/filtering across sites, files and logs.

Acceptance: external changes are never overwritten, every successful save is recoverable, and rollback follows validation, diff and confirmation rules.

### v0.3 — structured editing

- Add source-range-preserving structured editing for `reverse_proxy` and common directives.
- Add inline validation and syntax highlighting.
- Keep raw editing available for unsupported or plugin directives.

Acceptance: a structured edit changes only the selected construct and preserves unrelated bytes, comments and unknown syntax.

### v0.4 — runtime and TLS dashboards

- Add runtime dashboard, loaded-config inspection and capability-aware restart/stop actions.
- Add upstream health/reachability and response-time information.
- Add TLS certificate expiry, issuer, SAN, storage and renewal information where available.

Acceptance: unavailable runtime data is represented as a useful error state and never blocks configuration browsing.

### v0.5 and later

- Add named remote server profiles through SSH or Tailscale SSH.
- Run remote formatting, validation, backup, write and reload operations on the selected target node, and identify that node in every confirmation and result.
- Detect the target Caddy version and installed modules before enabling module-specific summaries or actions.
- Add metrics integrations and richer plugin-aware summaries.
- Stabilize rollback, recovery and the lossless editing engine for a v1.0 release.

## Caddy compatibility monitoring

Compatibility with Caddy is an ongoing maintenance responsibility. Maintain a supported-version record and review Caddy changes through these sources, in descending order of authority:

1. GitHub Releases and official release notes for new versions, deprecations and breaking changes;
2. official Caddy documentation for Caddyfile syntax, directives, modules and Admin API behavior;
3. GitHub Issues and pull requests as early signals for proposed or upcoming changes;
4. the Caddy forum for maintainer announcements, operational guidance and best practices.

Issues, pull requests and forum discussions are signals, not compatibility contracts. Treat a change as supported only after it is confirmed in a released Caddy version or official documentation.

For every relevant Caddy release, perform a compatibility review covering:

- Caddyfile grammar, imports, matchers, directives and module behavior;
- Admin API endpoints, payloads, reload semantics and error responses;
- deprecations, defaults and breaking changes;
- parser fixtures and lossless source-range patching;
- structured summaries, UI labels and capability detection;
- supported-version documentation and regression tests.

Unknown or newly introduced directives must remain preserved even before lazycaddy understands their semantics. Compatibility monitoring must result in an explicit code, fixture, UI, documentation or support-matrix change—or a recorded decision that no change is required.

## Testing strategy

Fixtures must include comments, whitespace-sensitive layouts, snippets, imports, nested blocks, malformed input, third-party directives and files with unusual permissions.

Required tests:

- parser source ranges and opaque/unknown nodes;
- import resolution and file-boundary tracking;
- nested relative imports, glob expansion and import-cycle handling;
- patching preserves untouched bytes;
- formatting and validation use temporary files;
- external editor round-trips and `fsnotify` conflict handling;
- invalid validation cannot write or reload;
- diff approval is required before writes;
- backup creation and atomic write failure paths;
- read-only fallback, permission failures and capability-gated actions;
- multi-file transaction preflight and rollback before multi-file editing is enabled;
- concurrent modification detection;
- runtime timeout and Admin API error handling;
- separate saved/validated/loaded runtime states;
- keybindings and unsaved-change prompts at the application-state level.

Add an end-to-end test using a fake Caddy command and fake Admin API. Tests must not require Caddy or network access to be installed.

## Definition of done for a release

- All in-scope acceptance criteria pass.
- `go test ./...`, formatter and static analysis pass in CI.
- No operation writes or reloads without an automated test covering its guard.
- Documentation explains configuration discovery, Admin API permissions, backup location and recovery.
- Errors identify the failed operation and provide a safe next action.

## Repository delivery policy

Until the 1.0 release, this is a single-maintainer project and direct pushes to
`main` are allowed. Feature branches and pull requests remain recommended for
substantial, isolated or review-sensitive changes, and CI must remain green on
every integrated change.

Before the project approaches 1.0, enable branch protection and require all
changes to go through pull requests targeting `main`, with the GitHub Actions
check named `test` passing before merge. Local `main` must be synchronized with
`origin/main` after remote merges. Detailed operating instructions live in
`AGENTS.md`.
