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

The lossless Caddyfile parser and patcher are complete. The current milestone is a read-only inspector with an opt-in format and validate workflow: the TUI loads a Caddyfile, resolves imports, lists sites and raw source, and runs `caddy fmt` and `caddy validate` (`v`) against a temporary working copy, surfacing structured diagnostics in a compact list with a full detail view.

## License

See [LICENSE](LICENSE).
