# Contributing to lazycaddy 🦥

Thanks for your interest in contributing to lazycaddy!

lazycaddy aims to make managing Caddy from the terminal clear, fast and safe,
while keeping the Caddyfile as the source of truth.

Contributions are welcome: bug reports, documentation improvements, ideas,
tests and code are all useful.

Before making a substantial change, please read:

- [VISION.md](VISION.md) — product vision and design principles
- [PLAN.md](PLAN.md) — architecture, scope and roadmap
- [AGENTS.md](AGENTS.md) — detailed repository and contributor guidelines
- [CODE_OF_CONDUCT.md](CODE_OF_CONDUCT.md) — participation and conduct standards

## Before you start

For bug fixes and small improvements, feel free to open a pull request directly.

For larger features, architectural changes or changes to the user experience,
please open an issue or start a GitHub Discussion first. This helps make sure
the proposal fits the project's direction before significant work is done.

In particular, changes should preserve lazycaddy's core principles:

- the Caddyfile remains the source of truth;
- comments, formatting, imports and unsupported directives must be preserved;
- configuration changes must be explicit and reviewable;
- validation happens before writing or reloading;
- Caddy must never be reloaded implicitly;
- browsing should remain useful without elevated privileges.

## Development setup

Requirements:

- Go 1.26 or newer
- Git
- Make

Caddy itself is not required to run the unit tests, and tests must not depend
on network access.

Clone the repository:

```sh
git clone https://github.com/PierpaoloPernici/lazycaddy.git
cd lazycaddy
```

Run the application:

```sh
make run
```

Run the complete development checks:

```sh
make check
```

Useful additional commands include:

```sh
make test
make test-race
make fmt
make fmt-check
make vet
make coverage
make build
make dist
```

Run `make` without a target to see the available commands.

## Making changes

Start from an up-to-date `main` branch and preferably create a focused branch:

```sh
git switch main
git pull --ff-only
git switch -c feat/my-change
```

Before v1.0, direct work on `main` is allowed for small, local changes. Use a
focused branch and pull request for substantial features, experiments, UI work
or changes that benefit from review. Every integrated commit should be
release-ready; do not rely on a later release edit to repair a vague message,
pull request title or missing changelog classification.

Useful branch prefixes include:

- `feat/`
- `fix/`
- `docs/`
- `test/`
- `chore/`

Keep each change focused. Unrelated changes should normally be submitted
separately.

## Code style

lazycaddy follows idiomatic Go conventions.

Run `gofmt` on Go code and keep packages and interfaces focused.

Some particularly important architectural rules:

- UI models should express intents rather than performing filesystem or
  runtime operations directly.
- Filesystem access, command execution and Caddy Admin API operations should
  remain behind explicit interfaces.
- Tests should use fakes where possible and remain deterministic.
- Do not introduce JSON as an alternative configuration source of truth.
- Do not rewrite unrelated portions of a Caddyfile when applying a change.

See [AGENTS.md](AGENTS.md) for the detailed engineering guidelines.

## Tests

New behavior should normally include tests.

Safety-sensitive changes require particular care. Any operation that can write
configuration or reload Caddy should have tests for its guard conditions and
failure paths.

Useful areas for regression coverage include:

- Caddyfile parsing and source ranges
- imports and nested imports
- byte-preserving edits
- validation failures
- external-change conflicts
- backups and rollback
- atomic writes
- permission failures
- runtime/Admin API failures
- read-only behavior

Before submitting a pull request, run:

```sh
make check
git diff --check
```

For changes touching concurrency, also consider:

```sh
make test-race
```

## Commit messages

Commit messages use
[Conventional Commits](https://www.conventionalcommits.org/).

Examples:

```text
feat(logs): add journalctl source
fix(parser): preserve escaped newlines
docs: clarify backup behavior
test(validator): cover timeout handling
```

Use short, imperative English subjects.

Avoid placeholder messages such as:

```text
updates
fix stuff
WIP
```

Repository artifacts, source code, documentation, commit messages and pull
request titles should be written in English.

## Pull requests

Pull request titles should also follow Conventional Commit style.

A pull request should explain:

- why the change is needed;
- what changed;
- any user-visible impact;
- safety implications;
- how the change was tested;
- any migration or release-note considerations.

Use the repository's release disposition labels intentionally. The required CI
check is named `test`; wait for it to pass before merging. If the scope or user
impact changes during review, update the pull request title, labels and body
before merge.

UI changes should include a screenshot or terminal recording when that helps
review the result.

Before merge, the pull request must receive the appropriate release
classification label used by the repository, such as:

- `breaking-change`
- `enhancement`
- `bug`
- `dependencies`
- `github_actions`
- `documentation`
- `skip-changelog`

Do not use `skip-changelog` for user-visible changes.

## Reporting bugs

When reporting a bug, please include enough information to reproduce it:

- lazycaddy version
- Caddy version
- operating system
- terminal
- command-line arguments
- relevant Caddyfile fragment
- expected behavior
- actual behavior

Please remove passwords, tokens, domain secrets, private IP addresses or other
sensitive information before posting logs or configuration.

A minimal reproduction is especially helpful.

## Feature requests

Feature ideas are welcome.

Good proposals describe the problem first rather than only the desired
implementation.

Because lazycaddy deliberately keeps a narrow scope, new features should fit
the project's principles in [VISION.md](VISION.md).

In particular, lazycaddy is not intended to become:

- a web GUI;
- a replacement for the Caddy CLI;
- a JSON-first configuration manager;
- an application that takes ownership of the user's configuration.

## Security issues

Please do not report security vulnerabilities in a public issue.

See [SECURITY.md](SECURITY.md) for the appropriate reporting process.

## AI-assisted contributions

Using AI-assisted development tools is welcome.

AI-generated changes are held to exactly the same standards as any other
contribution: the contributor remains responsible for understanding the
change, reviewing the resulting code, testing it and ensuring that it follows
the project's safety and design principles.

## Questions

For general questions, ideas and design discussions, use
[GitHub Discussions](https://github.com/PierpaoloPernici/lazycaddy/discussions).

For concrete bugs or actionable work, use
[GitHub Issues](https://github.com/PierpaoloPernici/lazycaddy/issues).

Thanks for helping make lazycaddy better. 🦥
