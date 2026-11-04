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
| `v2.11.8` | Reviewed baseline (structured editing and recommended minimum) | 2026-11-04 | Official release notes, the `2.11` maintenance branch `admin.go` (`APIError` with `Warnings`), the `caddyconfig/warning` JSON shape, and the lexer behaviors for quoted braces, `import` in named routes and long heredoc markers. Security advisories assessed: none require a lazycaddy change. |
| `v2.11.7` | Reviewed | 2026-11-04 | Regression fixes for the `v2.11.6` idle timeouts, the `caddy fmt` end-of-input brace fix, placeholder behavior and the `Incremental` header. No parser, directive or Admin API contract change that affects lazycaddy. |
| `v2.11.6` | Reviewed | 2026-11-04 | Admin API `/load` error contract change, `url_pattern` matcher, `timeouts` directive, new global options, stricter validation, header hardening. No structured-edit grammar change. |
| `v2.11.5` | Tag without a published release | 2026-11-04 | Tag `v2.11.5` exists (2026-09-30) but its GitHub release was removed; `v2.11.6` followed the next day. Do not pin it and do not claim support for it. |
| `v2.11.4` | Reviewed baseline for v0.3 | 2026-08-10 | Official release notes and current Caddy documentation; command, Caddyfile and import semantics reviewed. Repository tests use fakes and do not claim a live-daemon validation. |
| Other `v2.11.x` releases | Unverified | — | No release-specific review has been recorded. Do not make version-specific structured-edit assumptions. |
| Versions older than `v2.11.4` | Unverified | — | Generic browsing may still work, but no compatibility claim is made for structured editing or version-specific behavior. |
| Versions newer than `v2.11.8` | Unverified until reviewed | — | Preserve unknown syntax and defer version-specific behavior until the release review is complete. |

## v0.4.1 compatibility review (2026-08-21)

The official Caddy release page still identifies `v2.11.4` as the latest
release at this review. The current Caddyfile documentation continues to
document heredocs, and the `v2.11.4` lexer explicitly ignores carriage returns
while recognizing heredoc openings and while finalizing heredoc content.

The `v0.4.1` CRLF heredoc fix aligns lazycaddy with that behavior. No newer
Caddy release or parser, directive, module, Admin API or capability change was
identified that requires a compatibility update. The reviewed baseline remains
`v2.11.4`; unknown syntax and plugin directives remain preserved and raw-editable.

## Compatibility review (2026-09-06)

The official Caddy release page still identifies `v2.11.4` as the latest
release at this review; no newer Caddy version has been published since the
2026-08-21 review, so parser, directive, formatting and documentation
boundaries reviewed earlier remain current.

Signal reviewed: CVE-2026-27589 (GHSA-879p-475x-rqh2), a CSRF vulnerability in
the local Admin API that lets browser-delivered cross-origin requests apply an
attacker-supplied configuration via `POST /load`. It is fixed in Caddy
`v2.11.1` by enforcing origin checks on unsafe admin API methods. Impact
assessment for lazycaddy:

- The reviewed baseline `v2.11.4` already includes the fix, so the Admin API
  behavior lazycaddy expects is the patched one.
- lazycaddy's Admin API client (`internal/runtime/admin.go`) is a local
  non-browser client: it POSTs to `/load` with only `Content-Type` and
  `Cache-Control` headers and never sends an `Origin` header, so origin
  enforcement does not block or alter its reloads.
- No code, fixture or capability-detection change is required.

The reviewed baseline remains `v2.11.4`; unknown syntax and plugin directives
remain preserved and raw-editable.

## Compatibility review (2026-11-04) — v2.11.6, v2.11.7 and v2.11.8

Three releases landed after the 2026-09-06 review: `v2.11.6` (2026-10-01),
`v2.11.7` (2026-10-03) and `v2.11.8` (2026-11-04). `v2.11.5` was tagged and
released on 2026-09-30, then its release was removed; only the tag remains, so
it is recorded as unsupported. `v2.11.8` becomes the recommended minimum because
of its security fixes, and the structured-edit baseline moves with it.

**Admin API `/load` error contract (the only behavior change that reaches
lazycaddy code).** `v2.11.6` changed a rejected `/load` from `200` with two
concatenated JSON objects to `400` with a single JSON body shaped like
`{"error":"...","warnings":[{"file","line","directive","message"}]}`
(`adminHandler.handleError` writing `APIError`, `2.11` branch).

- `internal/runtime/admin.go` already maps every non-2xx response to
  `ErrAdminRejected` and reads the `error` field, so the new contract is
  handled and the old ambiguity (a rejected config answered `200`) is gone.
- The `warnings` array used to be discarded. It is now rendered into the
  rejection message as `file:line: message (directive)`, joined by `; `, so a
  refusal explains which directives Caddy objected to. Covered by
  `TestLoadJSON_RejectedWithWarnings`, `TestAdminErrorBody_WarningsOnly` and
  the `TestFormatAdminWarnings` table.
- No origin, host or access-control change affects the client: it remains a
  local non-browser client that sends no `Origin` header.

**Caddyfile lexer and parser.** No lazycaddy change was required; the
behaviors that could have diverged were verified against the local parser:

- `v2.11.6` treats quoted braces as literal arguments (#7875). lazycaddy already
  does this (`internal/caddyfile/lexer_test.go`, fold and fixture tests).
- `v2.11.6` allows `import` inside named routes (#7986). The local parser
  already nests `import` under a named-route block.
- `v2.11.8` fixed a parsing slowdown with very long heredoc markers
  (#8203, #8249). A 200 KB marker parses in about 10 ms locally, so the same
  class of input is not a hazard here.
- `v2.11.7` fixed `caddy fmt` deleting an opening brace at the end of input
  (#8047). lazycaddy never emulates `fmt`; it delegates to the installed
  binary, so the fix arrives with the binary.

**Directives, matchers and options added.** `v2.11.6` adds the `url_pattern`
request matcher, the `timeouts` handler directive, and the
`tls_automate_names`, `expected_underscore_headers`, `expected_dot_headers` and
`max_header_size` options. `v2.11.8` adds `log_level`, `{time.now.rfc3339}`, a
`not` matcher for TLS connection policies, `tls()` in CEL expressions and
`proxy_status_name`. None of the ten reviewed structured forms
(`reverse_proxy`, `respond`, `redir`, `file_server`, `php_fastcgi`, `encode`,
`header`, `tls`, `log`, `import`) changed its positional grammar, and every new
construct falls under the existing unknown-preserving rule: it is displayed and
preserved verbatim, and the raw editor remains the only way to change it until
its own form review.

**Stricter validation.** `v2.11.6` rejects duplicate `named_routes`, duplicate
`forward_auth` `uri`, invalid `weighted_round_robin` weights, non-integer
`browse` `file_limit`, duplicate or ambiguous `map` inputs and ambiguous `@`
version separators. `v2.11.8` rejects multiple `expression` lines in one
matcher set, unknown TLS `curves` or `protocol_min`/`protocol_max` names,
repeated `sni_regexp`, out-of-range `ip_mask` sizes, duplicate `basic_auth`
usernames after placeholder expansion, and ports longer than 11 characters.
These are diagnostics produced by Caddy: lazycaddy surfaces the text and never
re-implements the rule, so no code or fixture change is needed. Tests use fake
runners and therefore do not depend on the exact wording of any release.

**Security and privilege.** `v2.11.6` and `v2.11.8` fix
GHSA-6365-7ppr-5r92 (reverseproxy connection reuse),
GHSA-r766-rf9f-588h (`{host}` in file paths, now a `400` for dots-only hosts),
GHSA-f278-mgc7-46j3 (`SCRIPT_FILENAME` normalization),
GHSA-25cw-38mq-q7m6 (`file` matcher and `/.env/./`),
GHSA-69hv-5jfc-mvmv (`{http.matchers.file.remainder}` escaping),
GHSA-jf74-f69g-hcj9 (data-directory fallback exposure) and
GHSA-8qx7-j2v2-x6q5 (hop-by-hop headers in 1xx responses). None of them changes
a lazycaddy code path, but they raise the recommended minimum to `v2.11.8`.
`v2.11.8` also lets Caddy bind privileged ports with
`cap_net_bind_service=+p` and respects `ip_unprivileged_port_start`, which is
relevant to the least-privilege guidance: lazycaddy still never needs `root`,
and running the TUI with elevated privileges remains unsupported.

The reviewed baseline is now `v2.11.8`; unknown syntax and plugin directives
remain preserved and raw-editable.

## Reviewed behavior baseline

The following behaviors are the compatibility boundaries for v0.3:

| Area | Baseline decision | Required lazycaddy behavior |
| --- | --- | --- |
| Caddyfile structure | Use Caddy's documented blocks, directives, tokens, quotes, heredocs, matchers, snippets and named routes as the semantic reference. | Keep the lossless source model and raw fallback available for partially parsed or unsupported input. |
| Imports | Imports may reference files, globs or snippets, are relative to the importing file, and may carry arguments or an optional block. | Keep imported files as separate documents; resolve paths deterministically; never silently merge them into a writable synthetic document. |
| Formatting | `caddy fmt` accepts a Caddyfile path or `-` for stdin and writes formatted output to stdout unless overwrite is requested. | Format temporary working content and show the result in the diff; never format the user's source implicitly. |
| Validation | `caddy validate` is stronger than adaptation because it loads and provisions modules without starting the server. | Validate before structured edits can be saved or reloaded, with cancellable and testable command execution. |
| Reload | `caddy reload` applies a config through the Admin API and corresponds to posting to `/load`. | Keep reload explicit, confirmed and separate from save. |
| Admin API errors | From `v2.11.6` a rejected `/load` is `400` with `{"error","warnings"}`; older releases answered `200` with two concatenated JSON objects. | Treat every non-2xx as a rejection, show the `error` field and the `warnings` entries, and never infer success from a `200` alone. |
| Modules and directives | Installed modules and version-specific semantics can vary. | Metadata is advisory only; unknown or plugin directives remain visible, preservable and raw-editable. |

## v0.3 compatibility checklist

- [x] Record the `v2.11.4` baseline and the official sources reviewed.
- [x] Confirm the documented `fmt`, `validate`, reload and import boundaries.
- [x] Review `v2.11.6`, `v2.11.7` and `v2.11.8` (2026-11-04) and record the
  `/load` error contract change, the new directives and the stricter validation.
- [x] Verify quoted braces, `import` in named routes and long heredoc markers
  against the local lexer/parser.
- [x] Cover the `400` plus `warnings` Admin API body with runtime tests.
- [x] Add focused fixtures for imports, globs, cycles, comments, quoted braces,
  brace-less sites, heredocs, placeholders, matchers, snippets, named routes
  and escaped input.
- [x] Add structured-edit fixtures for `reverse_proxy` and each common
  directive operation that v0.3 supports.
- [x] Verify that every structured edit preserves unrelated bytes, comments,
  unknown directives and exact file boundaries.
- [x] Record the structured-form compatibility decisions below (2026-08-15,
  official Caddy documentation reviewed against `v2.11.4`).

## Structured forms — compatibility decisions (2026-08-15)

The dedicated directive forms (`m`) are hand-authored over the documented
positional grammar of each directive. Every decision below is a no-change or
a conservative-refusal decision: when the form cannot interpret a construct
without guessing, it is disabled and the raw `$EDITOR` remains the only path,
so no byte is ever rewritten by a form that does not understand it.

| Directive | Form fields | Refused shapes (raw editor remains) |
| --- | --- | --- |
| `reverse_proxy` | matcher, upstreams | — (matcher detection follows Caddy's inline-matcher convention: `@name`, `/path*`, `*`) |
| `respond` | matcher, status, body | More than one non-matcher argument where the first is a status: the documented grammar is `<status>|<body> [<status>]` (a body followed by a status code), so `respond 200 "ok"` keeps the raw editor |
| `redir` | matcher, to, status | More than matcher + destination + status; status is free-form (`3xx`, `401`, `temporary`, `permanent`, `html`, placeholders) |
| `file_server` | matcher, mode (`browse`) | Any positional argument other than the documented `browse` (for example a browse template file on the header line) |
| `php_fastcgi` | matcher, gateways | — (matcher detection follows the inline-matcher convention) |
| `encode` | matcher, formats | — (an empty format list is valid: Caddy defaults to zstd + gzip) |
| `header` | matcher, field, value/find, replace | More than three positional tokens (values spanning several tokens stay raw) |
| `tls` | email/internal, cert file, key file | More than one marker plus one cert/key pair; a cert without its key is rejected before planning |
| `log` | logger name | More than one positional token; the documented grammar is `log [<logger_name>]` in both site and global-options contexts |
| `import` | pattern, args | A bare `import` with no pattern; the optional `{block}` is preserved verbatim (documented since v2.9) |

Additional decisions:

- **Inline matchers.** Caddy treats a leading token starting with `@`, `/` or
  `*` as an inline matcher for directives that accept one. The forms use the
  same rule, so a path matcher is never mistaken for an upstream, destination
  or format. This also fixes `reverse_proxy` matcher detection, which
  previously treated `/api/*` as an upstream.
- **Leaf directives.** Leaf directives (no nested block) are not tree rows,
  matching the existing v0.3 navigation design, so their forms are reachable
  through `a` (insertion) and through `m` when a block form is present;
  editing an existing leaf goes through the raw `E` editor. No change to the
  tree model was made for this increment.
- **Unknown arguments are never interpreted.** Any positional shape outside
  the table above returns `ErrAmbiguous` (or `ErrUnsupported` for a different
  directive) and disables the form. The bytes are never touched.
- **`respond` status/body order.** The official documentation states that the
  first non-matcher argument is a status or a body, and that a second argument
  is a status code after a body. A status followed by another token is
  therefore treated as ambiguous and keeps the raw editor instead of guessing.

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

- [Caddy v2.11.8 release](https://github.com/caddyserver/caddy/releases/tag/v2.11.8)
- [Caddy v2.11.7 release](https://github.com/caddyserver/caddy/releases/tag/v2.11.7)
- [Caddy v2.11.6 release](https://github.com/caddyserver/caddy/releases/tag/v2.11.6)
- [Caddy v2.11.4 release](https://github.com/caddyserver/caddy/releases/tag/v2.11.4)
- [Caddy command-line documentation](https://caddyserver.com/docs/command-line)
- [Caddyfile concepts](https://caddyserver.com/docs/caddyfile/concepts)
- [`import` directive](https://caddyserver.com/docs/caddyfile/directives/import)
- [Caddyfile tooling research](research/caddyfile-tooling.md)
