# Caddy Compatibility Record

This document records the Caddy versions and behaviors reviewed by lazycaddy.
It is the operational compatibility record; broader design guidance remains in
`docs/research/`, and product decisions remain in `PLAN.md`.

## Compatibility policy

- Caddy remains the authority for Caddyfile parsing, formatting, validation,
  adaptation and reload semantics.
- lazycaddy preserves unknown and plugin directives even when it does not
  understand their semantics.
- A version is not considered supported merely because the generic parser can
  display its source. Structured editing requires reviewed syntax assumptions,
  fixtures and guard tests.
- The v0.3 work starts with one reviewed baseline instead of claiming a broad
  version range. Versions outside the reviewed baseline remain unverified until
  they receive their own compatibility review.

## Version record

| Caddy version | Status | Review date | Evidence and scope |
| --- | --- | --- | --- |
| `v2.11.4` | Reviewed baseline for v0.3 | 2026-08-10 | Official release notes and current Caddy documentation; command, Caddyfile and import semantics reviewed. Repository tests use fakes and do not claim a live-daemon validation. |
| Other `v2.11.x` releases | Unverified | — | No release-specific review has been recorded. Do not make version-specific structured-edit assumptions. |
| Versions older than `v2.11.4` | Unverified | — | Generic browsing may still work, but no compatibility claim is made for structured editing or version-specific behavior. |
| Versions newer than `v2.11.4` | Unverified until reviewed | — | Preserve unknown syntax and defer version-specific behavior until the release review is complete. |

## Reviewed behavior baseline

The following behaviors are the compatibility boundaries for v0.3:

| Area | Baseline decision | Required lazycaddy behavior |
| --- | --- | --- |
| Caddyfile structure | Use Caddy's documented blocks, directives, tokens, quotes, heredocs, matchers, snippets and named routes as the semantic reference. | Keep the lossless source model and raw fallback available for partially parsed or unsupported input. |
| Imports | Imports may reference files, globs or snippets, are relative to the importing file, and may carry arguments or an optional block. | Keep imported files as separate documents; resolve paths deterministically; never silently merge them into a writable synthetic document. |
| Formatting | `caddy fmt` accepts a Caddyfile path or `-` for stdin and writes formatted output to stdout unless overwrite is requested. | Format temporary working content and show the result in the diff; never format the user's source implicitly. |
| Validation | `caddy validate` is stronger than adaptation because it loads and provisions modules without starting the server. | Validate before structured edits can be saved or reloaded, with cancellable and testable command execution. |
| Reload | `caddy reload` applies a config through the Admin API and corresponds to posting to `/load`. | Keep reload explicit, confirmed and separate from save. |
| Modules and directives | Installed modules and version-specific semantics can vary. | Metadata is advisory only; unknown or plugin directives remain visible, preservable and raw-editable. |

## v0.3 compatibility checklist

- [x] Record the `v2.11.4` baseline and the official sources reviewed.
- [x] Confirm the documented `fmt`, `validate`, reload and import boundaries.
- [ ] Add focused fixtures for imports, globs, cycles, comments, quoted braces,
  brace-less sites, heredocs, placeholders, matchers, snippets, named routes
  and escaped input.
- [ ] Add structured-edit fixtures for `reverse_proxy` and each common
  directive operation that v0.3 supports.
- [ ] Verify that every structured edit preserves unrelated bytes, comments,
  unknown directives and exact file boundaries.
- [ ] Record any Caddy release-specific change as a fixture, code, UI,
  documentation or explicit no-change decision.

## Release review procedure

For every relevant Caddy release:

1. Review the official release notes and the current official documentation.
2. Inspect parser, formatter, import and relevant Admin API behavior in the
   released source or tests.
3. Identify changes affecting grammar, directives, modules, formatting,
   validation, reloads or capability detection.
4. Update the version table with the review date and decision.
5. Add or update sanitized fixtures and regression tests for every behavior
   that affects lazycaddy.
6. Document why no change is required when the review finds no impact.
7. Run `make check` and include the result in the release or pull request
   verification notes.

## Authoritative sources

- [Caddy v2.11.4 release](https://github.com/caddyserver/caddy/releases/tag/v2.11.4)
- [Caddy command-line documentation](https://caddyserver.com/docs/command-line)
- [Caddyfile concepts](https://caddyserver.com/docs/caddyfile/concepts)
- [`import` directive](https://caddyserver.com/docs/caddyfile/directives/import)
- [Caddyfile tooling research](research/caddyfile-tooling.md)
