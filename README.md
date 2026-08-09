# lazycaddy

<p align="center">
  <img src="docs/assets/lazycaddy-logo.png" alt="lazycaddy logo" width="280">
</p>

> The lazier way to manage your Caddyfile.

lazycaddy is a keyboard-first terminal user interface for inspecting and managing Caddy while preserving the Caddyfile as the source of truth.

The project is under active development. Read the project direction and implementation constraints first:

- [VISION.md](VISION.md) — product vision and design principles;
- [PLAN.md](PLAN.md) — scope, architecture, safety workflow and roadmap;
- [AGENTS.md](AGENTS.md) — repository and contributor guidelines.

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

The lossless Caddyfile parser and patcher are complete. The current vertical
slice is a read-only-by-default inspector with an opt-in format, validate,
diff, edit, save and reload workflow:

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
- detect external changes before saving and report a recovery backup if a
  write fails after backup creation.

The inspector also provides:

- Caddy version, Admin API reachability and capability status in the header;
- an opt-in, rotation-aware log view with bounded history and JSON
  highlighting (`--log-file` or `--log-journal-unit`, `l`);
- `$EDITOR` editing for a selected node (`e`) or an entire document (`E`),
  including imported files, with validation, diff review and the same backup
  and atomic-save pipeline;
- read-only global search across nodes, files, source lines and loaded logs
  (`/` or `Ctrl-F`);
- exact-range node deletion (`d`) with diff confirmation and post-save tree
  rebuilding.

The v0.1 vertical slice is complete. The v0.2 milestone has landed
journal-backed logs and sensible path defaults: lazycaddy now discovers
`./Caddyfile` (falling back to `/etc/caddy/Caddyfile`) and the `caddy`
binary through `PATH` when they are not given explicitly, and keeps format,
validate and reload disabled when `caddy` is unavailable. The interface also
provides a persistent state-aware header, semantic status strip, responsive
pane layout, adaptive theme colors and a wrapped contextual footer. Remaining
v0.2 capabilities — persistent UI state and clipboard ergonomics — are not
available in the current build until implemented and released.

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

Reloads use the local Admin API at `http://localhost:2019` by default;
override the endpoint with `--admin-endpoint` and the per-request timeout
with `--admin-timeout`. A reload never happens implicitly.

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
