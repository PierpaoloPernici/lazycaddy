# lazycaddy 🦥

<p align="center"><img src="docs/assets/lazycaddy-logo.png" alt="lazycaddy logo with sloth mascot" width="420"></p>

<p align="center"><em>🦥 The lazier way to manage your Caddyfile.</em></p>

<p align="center">
  <a href="https://github.com/PierpaoloPernici/lazycaddy/actions/workflows/ci.yml"><img src="https://github.com/PierpaoloPernici/lazycaddy/actions/workflows/ci.yml/badge.svg?branch=main" alt="CI"></a>
  <a href="https://github.com/PierpaoloPernici/lazycaddy/releases/latest"><img src="https://img.shields.io/github/v/release/PierpaoloPernici/lazycaddy?display_name=tag" alt="Latest release"></a>
  <a href="https://codecov.io/gh/PierpaoloPernici/lazycaddy"><img src="https://codecov.io/gh/PierpaoloPernici/lazycaddy/branch/main/graph/badge.svg" alt="Coverage"></a>
  <a href="https://github.com/PierpaoloPernici/lazycaddy/blob/main/LICENSE"><img src="https://img.shields.io/github/license/PierpaoloPernici/lazycaddy" alt="License"></a>
  <a href="https://github.com/PierpaoloPernici/lazycaddy/blob/main/go.mod"><img src="https://img.shields.io/github/go-mod/go-version/PierpaoloPernici/lazycaddy" alt="Go version"></a>
</p>

<p align="center">lazycaddy is a keyboard-first terminal user interface for inspecting and managing Caddy while preserving the Caddyfile as the source of truth.</p>

<p align="center">
  <sub>The 🦥 is lazycaddy's mascot: deliberately unhurried, careful with your configuration.</sub>
</p>

## Screenshot

The TUI combines a navigable document tree, source inspection,
validation status and explicit editing actions in one terminal workspace.

<p align="center">
  <img src="docs/assets/lazycaddy-demo.gif" alt="lazycaddy terminal user interface demo" width="1200">
</p>

The project is under active development. Read the project direction and implementation constraints first:

- [VISION.md](VISION.md) — product vision and design principles;
- [PLAN.md](PLAN.md) — scope, architecture, safety workflow and roadmap;
- [AGENTS.md](AGENTS.md) — repository and contributor guidelines;
- [CONTRIBUTING.md](CONTRIBUTING.md) — development and contribution workflow;
- [SECURITY.md](SECURITY.md) — vulnerability reporting and security model;
- [CODE_OF_CONDUCT.md](CODE_OF_CONDUCT.md) — participation and conduct standards.

## Development

Requirements:

- Go 1.26 or newer;
- Caddy is not required for unit tests;
- network access is not required by tests.

Run the current TUI:

~~~sh
go run ./cmd/lazycaddy
~~~

Run checks:

~~~sh
make check
~~~

Run `make` without a target to list the available development commands.

Build a local binary, check the release configuration or run additional
quality checks with `make build`, `make release-check`, `make fmt-check`,
`make test-race` and `make coverage`.

Build local release artifacts for installation and testing on another machine:

~~~sh
make dist
~~~

Remove generated local artifacts (`bin/`, `dist/` and coverage files):

~~~sh
make clean
~~~

Show build information:

~~~sh
go run ./cmd/lazycaddy --version
~~~

The release process is documented in [docs/releasing.md](docs/releasing.md).

## Current status

The lossless Caddyfile parser and patcher are complete. The current application
is a read-only-by-default inspector with an opt-in format, validate, diff,
edit, save and reload workflow, and the complete v0.3 structured-editing
milestone (released as v0.3.0):

- load a Caddyfile and resolve nested imports while keeping imported files as
  separate documents;
- browse sites, directives, parse errors and raw source without discarding
  comments, whitespace or unknown directives;
- run `caddy fmt` and `caddy validate` (`v`) against a temporary working copy,
  with structured diagnostics and a detailed view;
- review a colored unified diff (`D`) before applying formatting changes;
- save only after successful validation (`s` in writable mode), creating a
  timestamped backup and replacing the source through a same-directory atomic
  write;
- reload through the local Admin API (`r`) only after a validated, saved
  configuration and a confirmation that names the target, with saved,
  validated and loaded states shown in the header;
- watch the loaded root and imported files for external changes, offering
  explicit reload, compare and keep actions without overwriting changes;
- detect external changes before saving and report a recovery backup if a
  write fails after backup creation;
- inspect the backup history of any document (`B`), diff a selected backup
  against the current file, and roll back to it in writable mode — following
  the same validate, diff, backup and confirmation workflow as a save, never
  reloading Caddy implicitly and always creating a fresh backup of the current
  file before the restore;
- prune old backups with `--backup-retention N` (maximum backups kept per
  source file; disabled by default).

The inspector also provides:

- Caddy version, Admin API reachability and capability status in the header;
- an opt-in, rotation-aware log view with bounded history and JSON
  highlighting (`--log-file` or `--log-journal-unit`, `l`);
- `$EDITOR` editing for a selected node (`e`) or an entire document (`E`),
  including imported files, with validation, diff review and the same backup
  and atomic-save pipeline;
- read-only global search across nodes, files, source lines and loaded logs
  (`/` or `Ctrl-F`);
- exact source copying with `y`: with mouse tracking enabled, drag to
  select text in the source, log or diff panes (or use `Shift`+arrows as a
  keyboard fallback); right-clicking inside a pane with an active selection
  also copies it. `y` copies exactly the selected visible text —
  source bytes, plain log text or the diff body without decorations; without
  a text selection `y` copies the selected document or node range as before.
  Copying uses OSC 52 in the terminal and falls back to an available local
  clipboard command (`pbcopy`, `wl-copy`, `xclip`, `xsel` or `clip`);
- exact-range node deletion (`d`) with diff confirmation and post-save tree
  rebuilding;
- moving a structural block after a same-document sibling (`o`) through a
  picker, with validation, diff confirmation, backup, atomic save and post-save
  tree re-anchoring;
- per-document diff review (`D`): the root diffs the validated working
  copy, while imported documents (and the root without a working copy) are
  diffed against their current on-disk bytes; the diff modal supports hunk
  navigation (`n`/`N`), horizontal scrolling for long lines (`h`/`l`) and
  a change summary in the title;
- an unsaved-changes guard: the header shows an explicit `UNSAVED` badge
  while edits are pending, and quitting with unsaved edits opens a prompt
  (`s` save and stay, `d` discard and quit, `Esc` cancel) — navigation
  never prompts;
- a bounded error history (`H`) that records every failure with a safe
  next action, including save/rollback/reload failures, monitor failures
  and retention failures, and recovery hints that point you at the
  recovery backup (`B`) or the editor's pre-edit snapshot.

The merged v0.3 foundation also provides:

- context-aware directive insertion with `a`;
- dedicated structured forms with `m` for `reverse_proxy`, `respond`,
  `redir`, `file_server`, `php_fastcgi`, `encode`, `header`, `tls`, `log`
  and `import` (ambiguous constructs keep the raw editor);
- structural-node creation with `n` for sites, snippets, named routes,
  global options and nested handler blocks;
- exact-range deletion with `d`, all using byte-preserving patches,
  validation, diff confirmation and the existing save/reload safety pipeline;
- line/column token spans, semantic roles, an advisory directive catalog,
  compatibility fixtures and structural-navigation primitives;
- official Caddyfile help from the command palette and structured forms with
  `Ctrl-H`.

The v0.3 structured-editing milestone is complete and released as v0.3.0.
Richer inline validation, semantic highlighting and full
folding/matcher-reference navigation are now planned for v0.4.

The v0.1 vertical slice and the v0.2 milestone are complete. The current UI
also provides a searchable command palette (`?`) alongside the direct hotkeys,
keeps the normal footer navigation-only, and uses compact `RW`/`RO` header
badges with a prominent status strip. Terminal directives remain available in
the source and search views without expanding the tree into one row per
directive. The v0.2 release also landed journal-backed logs and sensible path
defaults: lazycaddy now discovers
`./Caddyfile` (falling back to `/etc/caddy/Caddyfile`) and the `caddy`
binary through `PATH` when they are not given explicitly, and keeps format,
validate and reload disabled when `caddy` is unavailable. The interface also
provides a persistent state-aware header, semantic status strip, responsive
pane layout, adaptive theme colors, a compact navigation footer and exact
source clipboard copying with OSC 52 and local fallbacks. The current roadmap
is v0.4: richer inline validation, semantic highlighting, full folding and
matcher-reference navigation, and a runtime/TLS dashboard — without weakening
the raw-source fallback or byte-preservation guarantees.

The application is read-only by default and never reloads Caddy implicitly.
Unavailable capabilities disable only the affected actions, while browsing
and raw source inspection remain available.

### Safe change workflow

```text
load -> edit working copy -> format and validate -> review diff
  -> confirm -> create backup -> atomic save -> optional confirmed reload
```

Without `--config`, lazycaddy uses `./Caddyfile` when present and falls back
to `/etc/caddy/Caddyfile`. Without `--caddy-path`, it discovers `caddy`
through `PATH`; if the binary is unavailable, formatting, validation and
reload stay disabled. To pin both explicitly:

~~~sh
go run ./cmd/lazycaddy --config ./Caddyfile --caddy-path /usr/bin/caddy
~~~

To enable saving, add `--write`. The default backup directory is
`~/.local/state/lazycaddy/backups` (honoring `$XDG_STATE_HOME` when set) —
a user-state location chosen so system Caddyfiles never force backups next
to a root-owned config; it can be overridden with `--backup-dir`. The
default is a location, not a writability guarantee: if the resolved backup
directory is not writable, the save pipeline reports the failure. Formatting,
validation and saving use temporary or atomic file operations and do not
require a running Caddy daemon. Reloading does require Caddy to be running
with its Admin API enabled and reachable at the configured endpoint.

Backups keep the `<timestamp>-<seq>-<basename>` naming contract and each
also carries a plain-text `.src` identity sidecar holding its exact source
path, so backups of imported files that share a basename are never mixed
up. `B` opens the backup history of the selected document (read-only
comparison works without `--write`); in writable mode with a caddy binary
you can review the diff and roll back to a selected backup with an explicit
confirmation. Rollback validates the restored content in the context of
the full document graph — every document is mirrored into a temporary tree
that preserves its real directory layout, so relative imports and imported
snippet/fragment files resolve exactly as they do on disk — then backs up
the current file before the restore, never reloads Caddy implicitly, and
marks the loaded state unknown until you reload explicitly.

Backup retention is disabled by default. `--backup-retention N` keeps at
most `N` backups per source file, applied only after a successful save or
rollback; the newest backup and the backup created for the current
operation are always preserved, identity-less legacy backups and unrelated
files are never deleted, and any cleanup failure is reported without
undoing the completed operation.

Reloads use the local Admin API at `http://localhost:2019` by default;
override the endpoint with `--admin-endpoint` and the per-request timeout
with `--admin-timeout`. A reload never happens implicitly.

`D` diffs the currently selected document: the root compares the
validated working copy against the original after `v`, and any document
(imported files included, plus the root before `v`) is compared against
its current on-disk bytes. Inside the diff modal, `n`/`N` jump between
`@@` hunks, `h`/`l` scroll long lines horizontally, and the title shows
the change summary (`N hunks · +A −R`).

Quitting with unsaved edits opens a confirmation instead of exiting:
`s` saves (asynchronously, staying in the app), `d` discards and quits,
`Esc` cancels. The header shows an `UNSAVED` badge while edits are
pending; moving the cursor, searching, opening the log view or switching
documents never prompts.

Failures are recorded in a bounded error history opened with `H`, each
entry naming the failed operation and a safe next action. After a failed
save or rollback the status line points you at the recovery backup
(`B` on the affected document), and a cancelled editor edit surfaces its
pre-edit recovery snapshot path.

### Log sources

The log view (`l`) is opt-in and strictly read-only. Choose exactly one
source:

- `--log-file <path>` follows a Caddy log file (polling, rotation-aware),
  for example:

  ~~~sh
  go run ./cmd/lazycaddy --log-file /var/log/caddy/access.log
  ~~~

- `--log-journal-unit <unit>` follows a systemd journal unit, for example:

  ~~~sh
  go run ./cmd/lazycaddy --log-journal-unit caddy.service
  ~~~

The journal source consumes `journalctl --output=json` without a shell,
keeps the journal cursor between the bounded initial history and follow
phases, and surfaces journal errors through the log view's poll status line
while the rest of the TUI stays browsable. `--log-file` and
`--log-journal-unit` are mutually exclusive; passing both is an error.
Without either option the log view is disabled.

## Project disclaimer

This project is almost entirely vibe coded. It serves as my personal testbed
for evaluating how much value AI-assisted software development can provide
when guided by clear engineering practices, deliberate human direction and
the right tools.

The project team remains responsible for the code, design decisions, tests and
documentation. AI assistance does not replace human review, security analysis,
testing or maintenance. This repository is both a working project and an
ongoing experiment in AI-assisted development. It is all great fun—and highly
instructive!

## License

See [LICENSE](LICENSE).
