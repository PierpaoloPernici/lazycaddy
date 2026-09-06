# lazycaddy 🦥

<p align="center"><img src="docs/assets/lazycaddy-logo.png" alt="lazycaddy logo with sloth mascot" width="420"></p>

<p align="center"><em>The lazier way to manage your Caddyfile.</em></p>

<p align="center">
  <a href="https://github.com/PierpaoloPernici/lazycaddy/actions/workflows/ci.yml"><img src="https://github.com/PierpaoloPernici/lazycaddy/actions/workflows/ci.yml/badge.svg?branch=main" alt="CI"></a>
  <a href="https://github.com/PierpaoloPernici/lazycaddy/releases/latest"><img src="https://img.shields.io/github/v/release/PierpaoloPernici/lazycaddy?display_name=tag" alt="Latest release"></a>
  <a href="https://codecov.io/gh/PierpaoloPernici/lazycaddy"><img src="https://codecov.io/gh/PierpaoloPernici/lazycaddy/branch/main/graph/badge.svg" alt="Coverage"></a>
  <a href="https://github.com/PierpaoloPernici/lazycaddy/blob/main/LICENSE"><img src="https://img.shields.io/github/license/PierpaoloPernici/lazycaddy" alt="License"></a>
  <a href="https://github.com/PierpaoloPernici/lazycaddy/blob/main/go.mod"><img src="https://img.shields.io/github/go-mod/go-version/PierpaoloPernici/lazycaddy" alt="Go version"></a>
</p>

**Inspect and edit your Caddyfile from the terminal, with validation, diff review and a backup before every save.**

lazycaddy is a keyboard-first companion for people who manage their own Caddy
configuration on a local machine, VPS or homelab. Find a site, follow its imports,
make a focused change and review it before saving — without handing ownership of
your configuration to another tool.

- **Your Caddyfile stays yours.** Source-range edits preserve unrelated bytes,
  comments and unknown directives. Imported files remain separate documents.
- **Read-only by default.** Saving requires `--write`; browsing does not require
  Caddy to be installed or running.
- **No implicit reloads.** Saving and reloading are separate, confirmed actions.
  A saved file is not presented as proof that Caddy has loaded it.

<p align="center"><img src="docs/assets/lazycaddy-demo.gif" alt="lazycaddy document tree, source inspection and editing workflow" width="1200"></p>

## Install

### Download a binary

Get the archive for your platform from the
[latest release](https://github.com/PierpaoloPernici/lazycaddy/releases/latest).
Builds are available for **Linux and macOS**, on **amd64 and arm64**.

1. Download the archive and `checksums.txt`.
2. Verify the archive's SHA-256 hash against the manifest (`sha256sum` on Linux
   or `shasum -a 256` on macOS).
3. Extract it and place `lazycaddy` in a directory on your `PATH`, such as
   `~/.local/bin`.

```sh
lazycaddy --version
```

Release binaries do not require Go. Windows is not currently supported.

### Install with Go

With Go 1.26 or newer:

```sh
go install github.com/PierpaoloPernici/lazycaddy/cmd/lazycaddy@latest
```

Make sure your Go binary directory (`$(go env GOPATH)/bin` by default) is on
`PATH`.

## Try it without changing your configuration

```sh
lazycaddy --config /path/to/Caddyfile
```

Without `--config`, lazycaddy looks for `./Caddyfile`, then
`/etc/caddy/Caddyfile`. It never enables writes just because a file is writable.

| Key | Action |
| --- | --- |
| `↑` / `↓` | Select a document or structural block |
| `Enter` / `Space` | Expand or collapse the selected branch |
| `/` or `Ctrl-F` | Search sites, files and loaded log history |
| `Ctrl-E` | Toggle raw source view |
| `y` | Copy selected text, or the selected node/document source |
| `?` | Open the searchable command palette |
| `q` | Quit; pending edits require a decision |

The source view includes highlighting, tree-driven folding and matcher
navigation. Unsupported syntax stays visible; a parse error still leaves raw
source available for inspection.

To use lazycaddy on a server today, install it there and run it in your SSH
session. Native remote profiles and multi-server management are not implemented.

## Make your first change

Start with a disposable configuration or a non-production setup while learning
the workflow. Enable writable mode explicitly:

```sh
lazycaddy --config /path/to/Caddyfile --write
```

lazycaddy discovers `caddy` through `PATH`. If needed, select the binary explicitly
with `--caddy-path /usr/bin/caddy`. Formatting and validation need the binary,
not a running daemon.

1. Select the site or structural block you want to change.
2. Press `e` to edit its exact source range in `$VISUAL` / `$EDITOR`, or `E`
   to edit the entire selected document. Imported files are edited in their own
   document, never silently redirected to the root Caddyfile.
3. On return, lazycaddy validates the candidate. A failed validation cannot
   write the source or trigger a reload.
4. Review the diff. In the editor workflow, **Enter confirms and saves**;
   **Esc discards the candidate**. The save checks for external changes,
   creates a backup and atomically replaces the target file.
5. If you want Caddy to load the saved configuration, press `r` and review the
   separate confirmation naming the target and Admin API endpoint.

```text
edit working copy → format / validate → review diff → confirm
  → backup → atomic save → separate, explicitly confirmed reload
```

You can also use `v` to format and validate, `D` to inspect the diff and `s` to
save a validated working copy without reloading. Inside a diff, `n` / `N` jump
between hunks and `h` / `l` scroll long lines horizontally.

Common directives have structured forms through `m` where the selected node is
supported; `a` inserts a directive and `n` creates a structural node. Use `?`
for contextual commands and explanations of unavailable actions.

### Requirements and limits

- **Permissions:** the current user needs access to the configuration and the
  directories required for backups, editor snapshots and atomic replacement.
  `--write` does not grant permissions or elevate privileges. Browsing does not
  require running the entire TUI as root.
- **Editor snapshots:** pre-edit crash-recovery snapshots currently live under
  `<root-config-dir>/.lazycaddy/snapshots/`, with one slot per document. Creating
  or updating them requires access there, even when backups use a different
  directory.
- **Structured editing:** ambiguous or unsupported constructs fall back to raw
  editing. Leaf directives without nested blocks are not tree rows; use `E` to
  edit existing leaf directives. Forms do not replace Caddy's syntax authority.
- **Single-file changes:** each edit changes one source document. Multi-file
  transactions are not implemented.
- **Runtime access:** reload requires a running Caddy instance with its Admin
  API enabled and reachable. The default endpoint is `http://localhost:2019`;
  use `--admin-endpoint` and `--admin-timeout` to configure it. Missing runtime
  access does not prevent source inspection.

See the [Caddy compatibility record](docs/caddy-compatibility.md) for reviewed
versions, supported constructs and known limitations.

## Backups and recovery

Before replacing a source file, lazycaddy creates a timestamped backup. The
default directory is `$XDG_STATE_HOME/lazycaddy/backups`, falling back to
`~/.local/state/lazycaddy/backups`. Override it with `--backup-dir`. This default
avoids placing backups beside system configurations, but does not guarantee
that the resolved location is writable; a backup failure aborts the save.

- Press **`B`** to browse backups for the selected document and compare one with
  the current file. Read-only comparison does not require `--write`.
- Rollback requires writable mode, a validation binary, diff review and explicit
  confirmation. It validates the restored document in the full import graph,
  checks for external changes and backs up the current file before replacement.
- A rollback **never reloads Caddy implicitly**. Reload explicitly when ready.
- Press **`H`** for the bounded error history and safe next actions. Failed save
  or rollback operations report a recovery backup path when one is available;
  cancelled editor edits report their pre-edit snapshot path.

Backups include a `.src` identity sidecar so imported files with identical
basenames are not mixed up. Retention is disabled by default.
`--backup-retention N` enables per-source cleanup after successful saves or
rollbacks, preserving the newest/current-operation backup, legacy backups
without identity and unrelated files. Cleanup failures do not undo a completed
save or rollback.

## Inspect runtime, logs and TLS

These views are read-only and depend on independently available data sources.
Unavailable data is reported explicitly rather than treated as a healthy state.

- **`I` — Runtime:** inspect loaded configuration and observed upstream health
  through the configured Admin API, where the Caddy build exposes the data.
- **`l` — Logs:** browse bounded history, search and filter by host, status,
  level or text. Use `F` for filters, `c` to clear them, `f` for follow mode and
  `p` to pause/resume.
- **`T` — TLS:** inspect certificate metadata from an explicitly configured
  storage directory; unavailable storage, renewal or OCSP information remains
  distinct from verified data.

Choose one log source:

```sh
# Follow a log file, including rotation.
lazycaddy --log-file /var/log/caddy/access.log

# Or read a systemd journal unit.
lazycaddy --log-journal-unit caddy.service
```

`--log-file` and `--log-journal-unit` are mutually exclusive. Without either,
the log view is disabled. Journal access requires `journalctl` and permission
to read the selected unit; it does not enable service start, stop or restart.

To configure the TLS view:

```sh
lazycaddy --tls-storage-dir /path/to/caddy/storage
```

Use `lazycaddy --help` for all flags and
[the UI guide](docs/designsystem.md) for detailed interactions and keybindings.

## Status and feedback

The v0.4 milestone is complete, including source-preserving editing, rollback,
source diagnostics and runtime/log/TLS inspection. The project is still pre-1.0
and under active development. Native remote operations remain future work; see
[PLAN.md](PLAN.md) for the canonical roadmap.

Feedback from real Caddy installations is especially useful. If you try it,
[open an issue](https://github.com/PierpaoloPernici/lazycaddy/issues) describing
what you wanted to do, where you got stuck, your OS and the lazycaddy/Caddy
versions. Sanitized reproduction fixtures are welcome — do not post credentials,
private configuration or unredacted logs. Report security concerns through
[SECURITY.md](SECURITY.md), not a public issue.

## Development and project direction

Requirements: Go 1.26 or newer. Tests use fakes and do not require an installed
Caddy daemon or network access.

```sh
go run ./cmd/lazycaddy   # Run from source
make check              # Formatting, tests and vet
make build              # Build bin/lazycaddy
make test-race          # Run tests with the race detector
make coverage           # Generate coverage and print the summary
```

Run `make` to list all targets. `make dist` builds local release artifacts;
`make release-check` validates release configuration; `make clean` removes
build and coverage artifacts. See [the release guide](docs/releasing.md) for
the publishing procedure.

- [VISION.md](VISION.md) — product vision and design principles.
- [PLAN.md](PLAN.md) — specification, safety boundaries and roadmap.
- [CONTRIBUTING.md](CONTRIBUTING.md) — contribution workflow.
- [AGENTS.md](AGENTS.md) — contributor and coding-agent guidelines.
- [CODE_OF_CONDUCT.md](CODE_OF_CONDUCT.md) — participation standards.

### AI-assisted development

This project is almost entirely vibe coded and is a personal testbed for
AI-assisted software development guided by explicit engineering practices.
The maintainer remains responsible for the code, design decisions, tests and
documentation. AI assistance does not replace human review, security analysis,
testing or maintenance.

## License

See [LICENSE](LICENSE).
