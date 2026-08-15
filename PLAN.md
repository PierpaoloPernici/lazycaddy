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

The repository has completed the configuration-engine spike, the v0.1/v0.2
read-only-by-default TUI milestones and the v0.3 structured-editing foundation.
The v0.3 foundation and its first UI workflows are merged into `main`; the
remaining v0.3 work is explicitly listed in the milestone section below.

Completed:

- Go module, repository scaffold, development commands and CI checks;
- representative sanitized Caddyfile fixtures, including homelab, imports,
  snippets, nested blocks and unknown directives;
- lossless lexer with comments, quotes, backticks, heredocs, escaped newlines,
  CRLF, BOM and exact token offsets;
- token-driven parser and source ranges for global options, sites, snippets,
  named routes, nested directives and brace-less single-site files; a block
  opener on its own line after a top-level site/snippet/named-route header is
  attached to that header (Caddy-compatible), while a header token ending in
  `{` remains an error;
- byte-preserving range patching with invalid-range guards;
- import resolver with separate documents, relative paths, sorted globs,
  snippet precedence, warnings, duplicate detection and cycle detection;
- propagation of root and imported-document parse errors;
- formatter, test and vet commands plus GitHub Actions configuration.
- testable application state and a read-only Bubble Tea inspector with
  document/site navigation, raw source viewing and parse-error fallback;
- lexical syntax highlighting in the raw source view, including comments,
  strings, heredocs, braces and placeholders. This remains an intentionally
  early, conservative presentation layer; semantic roles are now available
  to structured features, while richer inline validation and highlighting
  remain future work;
- CLI configuration loading with an explicit read-only mode and no file writes
  or Caddy daemon dependency.
- `caddy fmt` and `caddy validate` engine (`internal/validator`):
  temporary-file execution, command-runner abstraction, secret redactor,
  structured diagnostic parser, timeout/cancellation handling, and
  diagnostics that surface the real Caddyfile path instead of the
  temporary working file.
- validator integration in the TUI: `config.Settings.BinaryPath` and
  `ValidatorTimeout`, an `app.Formatter` boundary, the `v` keybinding
  that formats and validates the root document, a diagnostics modal
  with a compact list and a full detail view, and context-aware
  footers.
- a unified diff workflow (`internal/diff` backed by gotextdiff): the
  `D` keybinding compares the original root document with the
  formatted/validated working copy and renders a scrollable, colored
  unified diff modal with context-aware keys.
- backup and atomic single-file save: `internal/caddyfile` atomic
  write primitives (preflight that rejects symlinks and non-regular
  targets and probes directory writability, then a temp file in the
  same directory with fsync and atomic rename, preserving permission
  bits) and `internal/backup` (timestamped, collision-safe backups in
  `<config-dir>/.lazycaddy/backups/` with a rebuildable index and an
  injected clock). The `app.Saver` boundary orchestrates preflight →
  external-change conflict check → backup → atomic write, returning
  `ErrConflict` when the file changed on disk since load and
  `*SaveError` with the recovery backup path when a write fails after
  the backup. The TUI gates saving on a validated working copy, a
  confirmation modal that names the target and backup directory, and
  the opt-in `--write` mode (read-only by default).
- A broad test suite covering the lossless-editing contract, import
  resolution, validation, diagnostics, unified diffs, atomic writes,
  backups, the save workflow, the reload workflow and the TUI. Tests use
  fakes and require no installed caddy or network access.
- explicit Admin API reload and loaded-state verification: the `r`
  keybinding adapts the saved configuration locally with the caddy
  binary and posts it to the Admin API `/load` endpoint after a
  validated, saved configuration and a confirmation that names the
  target and endpoint. The header distinguishes saved, validated and
  loaded states (LOADED, STALE, UNREACHABLE); a failed reload leaves
  the saved file and backup intact and no reload ever happens
  implicitly.
- `$EDITOR` round-trip on the selected node range: the `e` keybinding
  hands the exact range bytes (header and braces included) of the
  selected node in its real document — imported files included, never
  the root by accident — to the configured editor. Before launch the
  document and the range are snapshotted (plain-text sidecar, no JSON),
  the file is preflighted against external changes, and the editor is
  launched without a shell from a quote-aware split of $VISUAL /
  $EDITOR. A non-zero exit, an empty result or a file changed on disk
  cancel the edit without applying anything; the recomposed document is
  validated before it can be saved, and the change is applied only to
  the original range via `caddyfile.Patch` (every byte outside the range
  is preserved). The diff is the single confirmation for the edit:
  Enter saves directly and Esc discards; the write then flows through
  the existing backup → conflict detection → atomic save pipeline,
  targeting the document that actually changed. The separate
  save-confirmation modal remains for the normal `s` flow. Explicit
  decision: with a document row selected (no node) the command is
  disabled — there is no fallback to opening the whole file.
- read-only global search across sites, files and logs: the `/` and
  Ctrl-F keybindings open a case-insensitive substring search over node
  labels, document paths and document content lines (imported files
  included, each hit carrying its exact 1-based line) and the bounded
  loaded log history. Enter jumps to the hit — a node row re-anchors the
  tree cursor and reveals the block, a document line reveals the exact
  source line, and a log hit opens the log view with the entry's detail
  — while Esc closes without touching the selection, the log view or any
  workflow. Results are capped at 200 and the whole feature is read-only
  and available even in read-only mode.
- full-document editing and node deletion: `E` edits the entire selected
  document (root or imported) in $EDITOR, reusing the node-edit safety
  pipeline (snapshot, external-change preflight, validation, diff,
  backup, conflict detection, atomic save); comments outside any block
  are editable and an empty full-edit result goes through validation and
  the diff instead of being treated as a cancellation. `d` removes the
  selected node's exact source range (never document rows or import
  directives), preserving every byte outside the range, with the diff as
  the single confirmation before the save. Both flows reload and
  re-parse the graph after a successful save so the tree, the selection,
  the line ranges and the search scope reflect the new structure, and
  the runtime stays "saved but not reloaded" until an explicit `r`.
- pane-aware mouse selection and clipboard integration: left-click/drag
  selects text in the source pane, the log pane and the diff modal body
  (right-click copies, Shift+arrows select from the keyboard), reusing one
  selection model across text-bearing views while tree, header, footer and
  modals stay navigational; copied bytes are the exact unstyled visible
  content of the range and `y` remains the precise keyboard copy action.

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
- Reordering blocks and structured editing through the structured view. Node
  deletion via the exact source range is already in scope and covered by `d`;
  reordering and structured directive editing remain future work.
- Automatic reloads, background writes or silent formatting of the user's file.
- Built-in privilege escalation or a privileged helper; v0.1 reports insufficient permissions and keeps mutating actions disabled.

## Technology decisions

Use Go with the current stable toolchain. Build the TUI with Bubble Tea, Bubbles and Lip Gloss; use Cobra for CLI flags and commands, Viper only where application configuration benefits from it, and `fsnotify` for external-change detection. Use a focused diff library rather than implementing a diff algorithm in the UI. Keep dependencies minimal and document any deviation from this stack.

## External Caddyfile tooling research

The repository maintains a comparison of existing Caddyfile editor and parser
projects in [docs/research/caddyfile-tooling.md](docs/research/caddyfile-tooling.md).
That document records external evidence, URLs, reviewed revisions, useful
patterns and explicit non-adoptions. This section records only the resulting
product decisions:

- derive semantic highlighting from the existing lossless lexer and parse tree;
- use source ranges for source-view folding;
- treat named matchers, snippets and named routes as future definition/reference
  navigation candidates;
- expand parser and highlighting fixtures using the Tree-sitter corpus;
- keep directive metadata advisory and separate from parser validity;
- defer Tree-sitter integration until structured editing or incremental parsing
  demonstrates a concrete need;
- keep formatting and validation delegated to Caddy through the existing
  cancellable application boundary;
- use the official Caddy parser, formatter and compatibility tests as the
  reference for supported-version reviews, imports, heredocs and reload
  behavior;
- keep future Admin API, log, TLS and generated-configuration integrations
  behind explicit adapters with read-only defaults and capability checks;
- use bounded buffers and cancellation for any future runtime or log view;
- preserve user-authored Caddyfiles as lazycaddy's source of truth; generated
  configuration ownership remains a separate, explicitly scoped integration.

Initial local platform support targets Linux and macOS. Windows is deferred until its file replacement, permissions, process and terminal semantics are designed and tested explicitly.

## Release and distribution

The first public release was `v0.1.0`. Release artifacts are produced by
GoReleaser from a tag-triggered GitHub Actions workflow:

- Linux and macOS `amd64` and `arm64` builds are published as versioned
  `.tar.gz` archives;
- every release includes a `checksums.txt` SHA-256 manifest;
- `--version` reports the release version, source commit and build date;
- releases run `make check` before publishing;
- GitHub generates categorized release notes from merged pull requests using
  `.github/release.yml`;
- the binary remains read-only by default in release builds;
- Windows and package-manager publishing are deferred until the release
  process and platform behavior are stable.

The operational procedure lives in
[docs/releasing.md](docs/releasing.md). Do not reuse or move a published tag;
release corrections use a new patch version.

## UX and safety rules

- Start in read-only mode. The current file path and Caddy version are visible in the header.
- Browsing must not require elevated privileges. Missing write or reload permissions disable only the affected actions and explain how to proceed safely.
- Do not require the entire TUI to run as `root`. Any future privilege boundary must be explicit, narrowly scoped to one confirmed operation and implemented behind a dedicated adapter.
- Enable actions from detected capabilities rather than assumptions about the host environment or service manager.
- Unsaved changes are clearly marked by an explicit UNSAVED header badge and block only genuine application exit (a quit prompt with `s` save, `d` discard & quit, `Esc` cancel); navigation — cursor movement, search, log view, raw source, document switching — never prompts because it never abandons the document. `pendingEdit`/`pendingDelete` survive navigation and are cleared only by a reload-from-disk, an explicit discard, a save-cancel or a successful save.
- `r` means validate and reload only after a valid, saved configuration and a confirmation prompt; `s` saves without reloading.
- A failed validation never writes the source file and never calls reload.
- A failed reload leaves the saved file and backup intact and reports the command/API error.
- Saving and reloading are separate actions; `validate and reload` is the only combined workflow.
- After a reload, verify the Admin API state and clearly distinguish saved, validated and loaded configuration states.
- Backups are created atomically before the target is replaced; backup failures abort the save.
- Rollback is available only in writable mode with a validation binary, restores exactly one source document, and requires the diff review plus an explicit confirmation that names the target and the backup.
- Rollback re-checks the target with the same external-change guard as a save, validates the restored content in the context of the full document graph (a temporary mirror that preserves every document's real directory layout, so imported fragments validate next to their siblings), and creates a new backup of the current file before the atomic restore; any failure or cancellation leaves the target and existing backups unchanged.
- A rollback never reloads Caddy implicitly; after a successful rollback the in-memory graph is reloaded, the change monitor is re-armed and the loaded state is marked unknown until an explicit reload.
- Backup retention is disabled by default and, when configured, applies only after a successful save or rollback: it never deletes the newest backup, the backup created for the current operation, identity-less legacy backups or unrelated files, and cleanup failures are reported without compromising the completed operation.
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
| Navigation            | Details                              |
|                       |                                      |
| Sites                 | Structured summary                   |
| Snippets              | or source view                       |
| Imports / Files       |                                      |
| Runtime               |                                      |
| Logs                  |                                      |
| TLS                   |                                      |
+-----------------------+--------------------------------------+
| key hints | validation/error message | backup/reload status  |
+--------------------------------------------------------------+
```

Primary navigation is `Sites`, `Snippets`, `Imports / Files`, `Runtime`, `Logs` and `TLS`. Empty or unsupported sections should explain their state instead of showing a blank panel. The header/status bar must always identify the selected server, configuration path, selected site, unsaved state, validation state and whether the running Caddy matches the file on disk.

Keybindings:

```text
Enter      Toggle the selected branch
Space      Toggle the selected branch
Left       Collapse the selected branch
Right      Expand the selected branch
+          Expand all branches
-          Collapse all descendants
e          Edit selected source range
E          Edit entire document (root or imported)
Ctrl-E     Toggle raw source view
a          Add (only when supported by the current release)
d          Delete selected node (confirmation via diff; never on document rows or import directives)
v          Format and validate
r          Validate and reload (confirmation required)
s          Save without reload
D          Diff current changes (selected document; root uses the working copy, imports and the root fallback use on-disk bytes)
m          Edit reverse_proxy fields (when a reverse_proxy directive is selected)
n          New structural node (normal view); next diff hunk (inside the diff modal)
N          Previous diff hunk (inside the diff modal)
h/l        Shift the diff horizontally for long lines (inside the diff modal)
B          Open backup history for the selected document (compare, then rollback in writable mode)
H          Open the error history (recorded failures with safe next actions)
l          Logs
t          TLS
/          Search
Ctrl-F     Search
f          Toggle log follow mode
p          Pause or resume logs
?          Open the searchable command palette
q          Quit; prompt if changes are unsaved
Esc        Close modal / cancel operation
Ctrl-H     Open the official Caddyfile documentation
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

The backup index must be rebuildable from files on disk. Because two
imported documents can share a basename, every backup also carries a
plain-text identity sidecar next to it (the backup name plus `.src`)
holding the exact canonical source path; `B` resolves each backup to
exactly one source file and never offers a legacy (sidecar-less) backup
to a document whose basename is shared by another document. Retention is
configured with `--backup-retention N` (maximum backups kept per source
file; the default 0 disables it). Cleanup runs only after a successful
save or rollback, always preserves the newest backup and the backup
created for the current operation, never removes identity-less legacy
backups or unrelated files, and reports failures without undoing the
completed operation. Rollback is implemented: it lists backups newest
first, diffs the selected backup against the current on-disk document,
then follows the same validate, diff, backup and confirmation workflow —
validating the restored content in the context of the full document
graph (a temporary mirror preserving every document's real directory
layout, so imported snippets and fragments validate next to their
siblings), restoring exactly one source document, never reloading Caddy
implicitly, and re-arming the change monitor with the loaded state
marked unknown until an explicit reload.

## Runtime integration

The runtime adapter is isolated from the UI and supports these operations in v0.1:

- detect the configured Caddy binary and query its version;
- expose capabilities such as readable configuration, writable configuration, validation, Admin API access and reload support;
- inspect the loaded configuration through the local Admin API, read-only;
- reload through the local Admin API, with configurable endpoint and timeout;
- report whether the loaded configuration matches the validated file on disk.

In v0.1, keybinding gating is adapter-based, not capability-driven: a nil
formatter/saver/reloader disables the corresponding action, and the startup
runtime probe is a read-only report (version, runtime status badge, status
message) that does not gate keys. Capability-driven gating (e.g. disabling
`r` until the Admin API is provably reachable) is deferred to the runtime
dashboard milestone, where it can react to capability changes.

Process/service-manager control integration (`systemd`, Docker, launchd, etc.)
is deferred to the remote/operations milestone. The v0.2 `journalctl`
integration is read-only log access and must not imply that lazycaddy can
start, stop or restart a service. Runtime status must distinguish `running`,
`stopped`, `unreachable` and `unknown`; absence of the Admin API must not
prevent browsing or validating the file.

Do not claim that the loaded configuration matches the file unless that state can be proven. Record successful reload identity locally where possible and show `unknown` when comparison is ambiguous; do not infer equality by regenerating a Caddyfile from Admin API JSON. Future capability discovery may inspect `caddy list-modules` to improve version- and plugin-aware summaries, but unknown modules must never block browsing or preservation.

Later runtime features may add restart and stop, but stop must never be enabled
without an explicit service adapter and a target-specific confirmation.

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

Completed within the vertical slice:

- [x] Load a configured or default Caddyfile path and resolve imports.
  The resolver engine and the TUI integration are both in place.
- [x] Parse site blocks/common directives with source ranges and opaque
  nodes. The parser engine and the TUI document tree are both in place.
- [x] Temporary-file formatting and validation. The `v` keybinding runs
  `caddy fmt` and `caddy validate` against a temporary working copy and
  surfaces structured diagnostics with the real Caddyfile path, a
  compact list and a full detail view.
- [x] Unified diff review. The `D` keybinding compares the original
  root document with the formatted/validated working copy through
  `internal/diff` (backed by gotextdiff) and renders a scrollable,
  colored unified diff modal.
- [x] Timestamped backup and explicit atomic save. `--write` opts into
  writable mode; `s` opens a confirmation that names the target and
  the backup directory, then creates a timestamped backup and
  atomically replaces the file. Writes are gated on a validated
  working copy and on an external-change conflict check; failed
  writes surface the recovery backup path.
- [x] Explicit Admin API reload with loaded-state verification. The
  `r` keybinding adapts the saved configuration locally, posts it to
  the Admin API `/load` endpoint after a confirmation that names the
  target and endpoint, and surfaces saved / validated / loaded states
  in the header. A failed reload leaves the saved file and backup
  intact.
- [x] Initial lexical syntax highlighting in the raw source view. The v1
  layer colors comments, strings, heredocs, braces and placeholders while
  preserving the original bytes; semantic site-address and directive roles
  are intentionally deferred.
- [x] Basic runtime/version information and capability detection with a
  read-only fallback. A startup probe queries the configured caddy binary
  for its version and checks the local Admin API, deriving a provable
  runtime status (unknown, running, stopped, unreachable) and a capability
  set (binary, validation, Admin API access, readable configuration,
  reload, writable mode). The header shows the Caddy version and a runtime
  status badge; probe failures degrade to explicit unknown/stopped states
  and the TUI remains fully browsable without caddy or write permissions.
- [x] Basic log view when a configured source is available. `--log-file`
  opts into a read-only log screen (`l`): a bounded 1000-line scrollback
  with follow mode (`f`, bottom-anchored, manual scroll takes control),
  pause/resume polling (`p`), and per-line JSON syntax highlighting
  (keys, values, timestamps, log levels and HTTP status classes follow
  the zap color conventions). The source is `internal/logs` — tolerant
  per-line JSON parsing, a bounded history buffer, and a rotation-aware
  file tailer (`tail -F` semantics, carries partial lines, follows Caddy's
  rename-based rotation). Caddy has no Admin API endpoint for reading
  logs, so a configured file is the only v0.1 source; polling is bounded
  (500 ms tick) and cancellable, and the TUI stays fully browsable without
  a log source.
- [x] `$EDITOR` round-trip on a selected source range. The `e`
  keybinding opens the exact bytes of the selected node (in its real
  document, imports included) in $VISUAL / $EDITOR without a shell,
  snapshots the document and range before launch, treats a non-zero
  exit / empty result / external change as a cancellation, recomposes
  the document with `caddyfile.Patch`, gates the result on validation
  and flows it through the existing diff / confirmation / backup /
  conflict / atomic-save pipeline. Editing a document row (no node) is
  explicitly disabled — there is no fallback to opening the whole file.
- [x] Search across sites, files and logs. The `/` and Ctrl-F
  keybindings open a read-only, case-insensitive substring search over
  node labels, document paths and content lines (imports included) and
  the loaded log history; Enter jumps to the hit (node reveal, exact
  source line, or log detail) and Esc closes without side effects.
- [x] Backup comparison, rollback and configurable retention. The `B`
  keybinding opens the backup history for the currently selected
  document (root or imported) through an `app.Rollbacker` boundary:
  backups are listed newest first, each resolved to exactly one source
  file through a plain-text identity sidecar (so two same-basename
  imported documents never share rollback candidates), and a selected
  backup is diffed against the current on-disk document. Rollback is
  offered only in writable mode with a caddy binary: it re-checks the
  target with the same external-change guard as a save, validates the
  restored content in the context of the full document graph (every
  document is mirrored into a temporary tree preserving its real
  directory layout, so relative imports — including imported snippet
  and fragment files — resolve exactly as on disk and the restored
  backup is checked next to its siblings), requires the diff review and
  an explicit confirmation, creates a backup of the current file before
  the atomic restore, never reloads Caddy implicitly, and after success
  reloads the in-memory graph, re-arms the change monitor and marks the
  loaded state unknown until an explicit reload. `--backup-retention N`
  caps backups per source file (disabled by default; applied only after
  a successful save or rollback; never removes the newest backup, the
  backup created for the current operation, identity-less legacy backups
  or unrelated files), and retention failures are reported without
  compromising the completed save/rollback.

Acceptance: an existing Caddyfile containing comments, unknown directives, nested blocks and imports can be opened without data loss; parse failures still permit raw viewing; invalid or cancelled changes cannot write or reload.

### v0.2 — change safety and navigation

- [x] Add a read-only `journalctl` log source for Caddy installations managed by
  systemd. The log view should support an explicit unit selection (for
  example, `caddy.service`), bounded initial history, follow mode and clean
  cancellation/restart when `journalctl` exits. Consume journal JSON without a
  shell, preserve the journal cursor between the initial history and follow
  phases to avoid duplicates or gaps, and reuse the existing Caddy JSON
  parsing/highlighting where `MESSAGE` contains a structured Caddy log. Show
  journal metadata and raw messages when the payload is not Caddy JSON. The
  file tailer remains available, and the selected source must degrade to a
  clear read-only error when `journalctl`, the unit or journal permissions are
  unavailable.
- [x] Add path discovery and sensible defaults. When `--config` is not
  supplied, prefer an existing `./Caddyfile`, then an existing
  `/etc/caddy/Caddyfile`, while preserving a clear missing-file error when
  neither exists. When `--caddy-path` is not supplied, discover `caddy` via
  `PATH` and keep formatting, validation and reload disabled if it is not
  available. Provide a user-writable default backup location for system
  configurations (for example `~/.local/state/lazycaddy/backups` or
  `~/.lazycaddy/backups`) while retaining an explicit `--backup-dir` override.
  `--write` remains opt-in; path discovery must never imply automatic
  privilege escalation or require the entire TUI to run as root. Implemented
  in `internal/discover` (injectable filesystem, PATH, home and environment
  seams) and wired through `cmd/lazycaddy` before the application is built;
  backup defaults to the user-state location
  `$XDG_STATE_HOME/lazycaddy/backups` (or `~/.local/state/lazycaddy/backups`)
  — the default location, not a writability guarantee: the save/backup
  pipeline reports any writability failure.
- [x] Improve the persistent application chrome and visual hierarchy. Keep the
  header visible independently from transient notifications, display the
  LazyCaddy version alongside the Caddy version and runtime/configuration
  badges, and reserve a separate status strip above the navigation footer for
  validation, save and error messages. Add a coherent terminal theme with a
  focused-pane accent, colored section titles and key hints, and restrained
  semantic colors for success, warning and error states; labels must remain
  explicit so state never depends on color alone. Define one primary accent
  color as part of the application identity, using it consistently for the
  focused pane, selected item, active section, brand/version label and key
  hints. Keep semantic status colors distinct from that accent and provide a
  terminal-safe fallback when true color is unavailable. Preserve responsive
  layout behavior by shortening low-priority path and metadata text on narrow
  terminals. Implemented in `internal/ui` with a persistent header, compact
  `RW`/`RO` state badges, a prominent semantic status strip, responsive layout
  accounting, a navigation-only footer and a searchable command palette. The
  palette and direct hotkeys share one command catalog, while unavailable
  capabilities remain visible with an explicit reason.

  Target layout preview:

  ```text
   lazycaddy dev · Caddy v2.11.4 · Config: Caddyfile       UNKNOWN  RUNNING  RO
  +---------------------------+ +--------------------------------------------------+
  | Documents                 | | Source: /etc/caddy/Caddyfile                     |
  | ...                       | | ...                                              |
  +---------------------------+ +--------------------------------------------------+
   ✓ validated (working copy updated, not saved)
   ↑/↓ move · Enter toggle · PgUp/PgDown · +/- all · ? commands
  ```

- [x] Add a simple keyboard-first clipboard action for source content. `y` should
  copy the exact source range represented by the current node selection, while
  a document-row selection may copy the complete current document. Use a
  clipboard adapter with OSC 52 and platform fallbacks where available, show a
  concise success/error notification, and never include tree, pane or footer
  decorations in the copied bytes.
- [x] Add deterministic coverage for the v0.2 boundaries: journal history/follow
  cursor continuity, journal and path-discovery failures, exact source bytes
  copied without panel decorations, OSC 52 unavailable/fallback clipboard
  behavior, and the related read-only permission paths.
- [x] Improve diff review, unsaved-state prompts and error recovery. `D` now
  diffs the currently selected document: the root keeps the working-copy
  diff after `v`, while imported documents (and the root without a working
  copy) are diffed against their current on-disk bytes read through an
  injected reader. The shared diff modal adds `n`/`N` hunk navigation,
  `h`/`l` horizontal scrolling for long lines (with a truncated
  indicator), and a change summary (`N hunks · +A −R`) in the title. An
  UNSAVED header badge and a dedicated quit-confirmation modal guard every
  real application exit when unsaved edits exist — `s` saves (and stays in
  the app), `d` discards and quits, `Esc` cancels — while pure navigation
  (cursor, search, log toggle, document switching) never prompts. Errors
  are now recoverable: a bounded 50-entry error history (`H`) records each
  failure with a safe next action, save retention failures are surfaced,
  monitor failures are recorded instead of silently disabling, and failed
  saves/rollbacks point the operator at the recovery backup via `B` while
  cancelled editor edits surface their recovery snapshot path.
- [x] Detect external changes with `fsnotify` and provide reload/compare/keep actions.
- [x] Add backup comparison, rollback and configurable retention. The `B`
  keybinding opens the backup history of the selected document, newest
  first, and diffs a selected backup against the current file. Rollback
  is writable-only and follows validate → diff → backup → confirmation;
  it restores exactly one source document, never reloads Caddy
  implicitly, and after success re-arms the change monitor and marks the
  loaded state unknown. `--backup-retention N` (default 0 = disabled)
  keeps at most N backups per source file after a successful save or
  rollback, always preserving the newest/current backup and never
  deleting identity-less legacy backups or unrelated files. Backup
  filenames stay `<timestamp>-<seq>-<basename>` and each backup now
  carries a plain-text `.src` identity sidecar holding its exact source
  path, so imports with identical basenames are never mixed up. Rollback
  validation runs in the context of the full document graph: every
  document is mirrored into a temporary tree that preserves its real
  directory layout, so imported snippet and fragment files are validated
  next to their siblings and relative imports resolve as on disk.

Acceptance: external changes are never overwritten, every successful save is
recoverable, and rollback follows validation, diff and confirmation rules. A
systemd-backed Caddy installation can show recent and following journal entries
without requiring a log file or blocking browsing/configuration workflows when
journald is unavailable. A typical installation should be usable with
`lazycaddy --write` when the discovered configuration and backup paths are
accessible to the current user, and keyboard copy produces only the requested
source bytes without panel decorations.

### v0.3 — structured editing

Current progress (2026-08-12): the v0.3 foundation and the initial editing
workflows are merged into `main`. Implemented are token spans, compatibility
fixtures, source-preserving planner primitives, semantic roles and advisory
catalog, structural-navigation primitives, the pane-aware selection model,
and the planner's `CreateNode` API. The UI now exposes `a` for directive
insertion, `m` for `reverse_proxy` fields, `n` for structural-node creation,
`d` for deletion, and pane-aware mouse selection with full clipboard
integration, all using validation, diff confirmation, save and post-save
graph reload where applicable. Official Caddy help is available through
`Ctrl-H`.

The next structured-editing increment adds editable top-level comment groups.
Comments remain source annotations rather than parser `Node` values: they are
selectable source ranges that must not affect structural parsing, folding,
deletion or reordering. The existing `E` full-document editor remains the
escape hatch for arbitrary comment and source changes.

- [x] Generalize tree navigation to arbitrary parent/child rows. Document rows,
  including imported documents, remain separate top-level rows, while visible
  structural branches can contain recursively nested branches without relying
  on `depth == 0`. Leaf directives such as `respond`, `header_up` and
  `import` are not tree rows, but remain present in the parser, source view
  and search scope. A row is expandable only when it has visible children,
  so an otherwise empty block is rendered as a leaf row without an expansion
  marker.

  The tree uses separate columns for selection and expansion: `›` marks the
  selected row, `-` an expanded branch, `+` a collapsed branch, and `·` a
  visible leaf row. Document roots start expanded, branches below them start
  collapsed, and the initial selection is deterministic; expansion state is
  session-local and is not persisted. `Enter`/`Space` toggle the selected
  branch, `Left`/`Right` collapse or expand it, `+` expands all branches, and
  `-` collapses all descendants while keeping document roots expanded. The
  footer advertises only navigation actions; operational commands remain
  available through direct hotkeys and the `?` command palette.

  Tree rows use stable document/node/source-range keys so selection survives
  rebuilds after saves, reloads, rollbacks and tree toggles. Search traverses
  the complete parse tree independently of expansion state: a hidden leaf hit
  selects its nearest visible structural ancestor and reveals the exact source
  line, while a source-content hit expands the required document and structural
  ancestors before selecting the deepest containing branch. Keep the current
  one-file-per-edit safety boundary; this tree behavior is a navigation concern
  and must not imply multi-file editing.
- [x] Add pane-aware mouse selection and full clipboard integration for text-bearing views.
  Mouse tracking should make the source pane the selectable region when it is
  active, keep tree/header/footer interactions navigational, and render the
  selected text range without selecting neighboring panes. Reuse the same
  selection model for source content, logs and future diff/diagnostic views;
  map screen cells through each viewport's gutters, scrolling and wrapping,
  and provide keyboard and non-mouse fallbacks when mouse tracking or
  clipboard integration is unavailable. `y` remains the precise keyboard copy
  action for the active view.

  Implemented: left-click and drag create or extend a selection inside the
  source pane, the main log pane and the diff modal body; right-click copies
  an active selection of the owning pane; and Shift+arrows extend the
  selection from the keyboard (each pane seeds its keyboard cursor at the
  visible top-left cell). The selection renders as an overlay that preserves
  the existing syntax, log and diff colors, and copied bytes are the exact
  unstyled visible content of the range (a copied diff excludes the hunk
  cursor marker). Tree, header, footer and every modal remain navigational;
  wheel events and non-text regions are inert, and a selection whose pane is
  hidden is dropped so it can never be copied against the wrong content. `y`
  keeps its keyboard-copy role, preferring an active text selection over the
  node/document range.
- Continue source-range-preserving structured editing for `reverse_proxy` and common
  directives, covering the supported operations explicitly: edit existing
  values, insert supported directives, delete selected constructs and reorder
  compatible blocks. The `a` add action must be capability- and context-aware;
  unsupported or ambiguous insertions remain unavailable rather than guessing.
  The current UI covers directive insertion, `reverse_proxy` field editing,
  structural-node creation and deletion. Dedicated forms for more common
  directives and a reorder command remain product work.
  The `n` New node action exposes the planner's structural-node creation
  API for top-level sites, snippets, named routes and global options, plus
  nested handler blocks; `a` remains the directive-insertion action. For v0.3,
  directive forms remain explicit,
  hand-authored implementations; build-time form-schema generation from Caddy
  sources or documentation is deferred to v0.4 so it does not become a second
  syntax authority.
- Add editable top-level comment groups as source annotations. Detect
  contiguous full-line comments outside structural blocks, preserve their
  exact byte ranges and keep them separate from `caddyfile.Node` values. Show
  a virtual, collapsed `comments (N)` branch under each document; each leaf
  identifies its line range, a short preview and, when available, the nearby
  block it documents. Selecting a comment group reveals its exact range in the
  source pane and enables `e` for editing only that range.
- Make `a` context-aware for comment insertion: on a document, offer file
  header/footer placement; on a top-level block, offer insertion before or
  after the block; on a comment group, append a new group after it. Open the
  new comment in the configured editor with a comment template, accept one or
  more `#` lines and reject non-comment content with a safe instruction to use
  `E` for a full document edit. Route additions and edits through validation,
  diff review, backup, conflict detection, atomic save and post-save graph
  reload. Preserve blank lines and every byte outside the targeted source
  range.
- Node deletion (`d`) is already covered by the exact-range patch plus the
  diff confirmation and post-save graph reload; the v0.3 work extends the
  same safety contract to insertion, reordering and structured directive
  editing.
- [x] Preserve token spans with line/column information alongside byte offsets
  so source selection and copy operations can identify the exact visible text
  without weakening byte-preserving patches. Inline diagnostics still need
  richer semantic validation.
- [x] Add an advisory metadata catalog for descriptions and suggestions for common
  directives and global options. The catalog must never define valid syntax or
  hide unknown/plugin directives, and its entries should be version- and
  module-aware when capability information is available.
- [x] Expand the compatibility and regression corpus with focused fixtures for
  imports, globs, cycles, comments, quoted braces, brace-less sites, heredocs,
  placeholders, matchers, snippets, named routes and escaped input. Keep the
  official Caddy parser/formatter behavior authoritative and do not introduce
  Tree-sitter as a second parser unless incremental parsing becomes a proven
  bottleneck.
- Keep raw editing available for unsupported or plugin directives.

Acceptance: a structured edit changes only the selected construct and preserves
unrelated bytes, comments and unknown syntax. Structural navigation remains
usable with partially parsed files, and advisory metadata never prevents
browsing, raw editing or preservation of unsupported syntax. Mouse selection is
confined to the active text pane and copies the correct content across source,
log and other supported views. Top-level comments can be discovered in the
document tree, edited or added without becoming structural nodes, and every
comment operation preserves unrelated bytes and follows the normal validation,
diff, backup and atomic-save safeguards.

### v0.4 — runtime and TLS dashboards

- Add inline validation and richer semantic highlighting when the parse tree
  can identify roles reliably: site addresses, domains, paths, ports, IP/CIDR
  values, matchers, placeholders, durations, status codes, strings and
  heredoc boundaries. Keep Caddy authoritative for syntax and validation.
- Add structural navigation features derived from the parsed source: folding
  for site blocks, snippets, named routes and nested handlers; navigation from
  named matcher definitions to references; and brace-aware indentation or
  movement where the source ranges make it safe.
- Add read-only runtime observability and loaded-config inspection through
  separate, cancellable Admin API fetchers. Each panel must expose explicit
  `loading`, `available`, `stale` and `unavailable` states, refresh without
  blocking the TUI, and preserve per-panel capability/error information. Use
  the configured Admin API endpoint and runtime identity; do not infer loaded
  equality by regenerating a Caddyfile from JSON.
- Add upstream health/reachability and response-time information where the
  target Caddy build exposes it. Interpret upstream status together with the
  configured passive/active health-check thresholds and label it as observed
  runtime state rather than a generic network ping.
- Extend the log dashboard with source-aware filtering by host, status, level
  and text, plus bounded status-class counts and basic latency summaries. Keep
  multiple log sources behind explicit adapters and retain read-only,
  cancellation-aware behavior.
- Add a TLS certificate dashboard behind a certificate source adapter. Keep
  certificate metadata, storage location, renewal state and OCSP state as
  distinct values; do not assume a private CertMagic filesystem layout or
  infer renewal from a single certificate file. Surface locking, permission
  and unavailable-storage states without reading private material by default.
- Keep restart and stop unavailable unless an explicit target-specific service
  adapter is present and the action has a confirmation that names the target;
  service-manager control belongs to the remote/operations milestone.

Acceptance: unavailable runtime data is represented as a useful error state and
never blocks configuration browsing. Runtime, log and TLS panels remain
responsive under refresh failures, preserve bounded state, and never enable a
mutating service action without a verified service adapter.

### v0.5 and later

- Add named remote server profiles through SSH or Tailscale SSH with explicit
  authentication, host-key policy, timeouts, cancellation and target identity.
  Never expose credentials or silently reuse a different target profile.
- Run remote formatting, validation, backup, write and reload operations on the
  selected target node, and identify that node in every confirmation and result.
  Extend the same target boundary to remote Admin API inspection, logs
  (`journalctl` or file sources) and TLS data, with independent offline/error
  states when one target is unavailable.
- Add explicit per-target service adapters for systemd, Docker, launchd or
  another supported manager before enabling restart/stop. Each action requires
  target-specific confirmation, capability verification and a recoverable
  failure report.
- Detect the target Caddy version and installed modules before enabling
  module-specific summaries or actions, and maintain an explicit supported
  version/module compatibility record with fixture and UI impact notes.
- Detect and display generated-versus-user-authored configuration ownership
  where integrations such as Docker proxy are present. Never regenerate,
  overwrite or reload a generated configuration without an explicit ownership
  boundary and confirmation.
- Add metrics integrations, multi-instance views and richer plugin-aware
  summaries while keeping bounded state, read-only defaults and per-target
  capability gating.
- Stabilize rollback, recovery and the lossless editing engine for a v1.0 release.

## Caddy compatibility monitoring

Compatibility with Caddy is an ongoing maintenance responsibility. Maintain the [supported-version record](docs/caddy-compatibility.md) and review Caddy changes through these sources, in descending order of authority:

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
- journal/file log source selection, cursor continuity, bounded filtering and
  status summaries;
- clipboard OSC 52/fallback behavior and pane-aware mouse coordinate mapping;
- runtime fetcher refresh states, upstream health interpretation and distinct
  certificate/storage/renewal/OCSP states;
- remote target isolation, authentication/host-key policy, timeout/cancel
  handling and per-target offline states;
- generated-configuration ownership detection and confirmation guards;
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
