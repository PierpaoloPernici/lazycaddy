# Caddyfile Tooling Research

This document records useful practices found in external Caddyfile tooling
projects. It is a reference for design and compatibility work, not a second
product specification. Product decisions belong in `PLAN.md`; durable product
principles belong in `VISION.md`.

## Review record

Reviewed on 2026-08-07. The repository revisions below were inspected locally:

| Project | Revision | Purpose |
| --- | --- | --- |
| [vim-caddyfile](https://github.com/isobit/vim-caddyfile) | `6d60d5af0d73f20b88ec388a9d70188d55ed8223` | Vim syntax, indentation and comment support |
| [vscode-caddyfile](https://github.com/caddyserver/vscode-caddyfile) | `9ba30fcec02e257776188ca66ed47fc210e86b26` | VS Code syntax support, formatting and a basic language server |
| [caddyfile-zed](https://github.com/nusnewob/caddyfile-zed) | `6631b10fe3c1b6c92173ce3d80a48f739d57cd89` | Zed integration using Tree-sitter queries |
| [tree-sitter-caddyfile](https://github.com/caddyserver/tree-sitter-caddyfile) | `b00e432eed3628f0f417865da9e9f601cb9f7fb85f` | Shared Tree-sitter grammar and parser corpus |

The official [Caddyfile concepts documentation](https://caddyserver.com/docs/caddyfile/concepts),
[global options documentation](https://caddyserver.com/docs/caddyfile/options),
and [command-line documentation](https://caddyserver.com/docs/command-line)
remain authoritative for Caddy behavior. External editor projects are useful
signals and implementation references, but are not compatibility contracts.

## vim-caddyfile

Source: [README](https://github.com/isobit/vim-caddyfile/blob/master/README.md),
[syntax file](https://github.com/isobit/vim-caddyfile/blob/master/syntax/caddyfile.vim),
[indentation file](https://github.com/isobit/vim-caddyfile/blob/master/indent/caddyfile.vim).

### Useful ideas

- Keep syntax support open-ended instead of requiring a static list of every
  directive. Unknown and plugin directives should still receive useful basic
  treatment.
- Separate top-level directives, subdirectives, site addresses, imports,
  snippets, named matchers, placeholders, strings and comments when context is
  unambiguous.
- Use simple brace-aware indentation as a safe fallback. Removing comments
  before checking braces avoids common indentation mistakes.
- Treat comments as a first-class editing concern, including comment commands
  and the `#` comment string.

### Limitations to keep in mind

The Vim implementation is regex-based and editor-oriented. It is not a
lossless parser, does not resolve imports, and cannot reliably infer every
directive argument type. Its value for lazycaddy is the open-world philosophy,
not the regular expressions themselves.

### Lazycaddy decision

Adopt the open-world highlighting principle and generic fallback roles. Keep
the existing lexer and source ranges as the authority for bytes and positions.

## vscode-caddyfile

Source: [README](https://github.com/caddyserver/vscode-caddyfile/blob/master/README.md),
[formatter](https://github.com/caddyserver/vscode-caddyfile/blob/master/packages/client/src/formatter.ts),
[language server](https://github.com/caddyserver/vscode-caddyfile/blob/master/packages/server/src/index.ts),
[syntax grammar](https://github.com/caddyserver/vscode-caddyfile/blob/master/syntaxes/caddyfile.tmLanguage.json),
[changelog](https://github.com/caddyserver/vscode-caddyfile/blob/master/CHANGELOG.md).

### Useful ideas

- Delegate formatting to `caddy fmt` rather than reimplementing Caddy's
  formatting rules.
- Provide descriptions and suggestions for global options and common
  directives through a separate metadata catalog.
- Highlight domains, paths, ports, matchers, status codes, content types,
  placeholders and heredoc bodies as semantic roles.
- Treat heredocs with known content markers as embedded CSS, HTML, JavaScript,
  JSON or XML where the host editor supports language injection.
- Make the Caddy executable configurable and support cancellation when an
  external formatter process is running.

### Limitations to keep in mind

The language server is intentionally basic and mostly uses line-oriented
heuristics. The formatter returns a whole-document edit to VS Code. That is
acceptable for an editor integration but is not a substitute for lazycaddy's
byte-preserving patch and save workflow.

The manually maintained directive catalog can become stale and should not be
used as the parser's definition of valid syntax. The changelog also shows that
some inspections, such as duplicate global options, were disabled after false
positive concerns.

### Lazycaddy decision

Adopt formatter delegation, cancellation semantics, and the idea of a
separate, advisory metadata catalog. Do not adopt whole-document replacement,
format-on-save, or a static directive list as a source of truth.

## caddyfile-zed

Source: [README](https://github.com/nusnewob/caddyfile-zed/blob/main/README.md),
[highlight queries](https://github.com/nusnewob/caddyfile-zed/blob/main/languages/caddyfile/highlights.scm),
[locals queries](https://github.com/nusnewob/caddyfile-zed/blob/main/languages/caddyfile/locals.scm),
[fold queries](https://github.com/nusnewob/caddyfile-zed/blob/main/languages/caddyfile/folds.scm),
[injection queries](https://github.com/nusnewob/caddyfile-zed/blob/main/languages/caddyfile/injections.scm).

### Useful ideas

Zed demonstrates a clean separation between the grammar and editor features:

- highlights map typed syntax nodes to semantic editor scopes;
- folds are derived from block boundaries;
- locals distinguish named matcher definitions from references;
- indentation and bracket matching are independent queries;
- injections allow CEL expressions and heredoc content to be treated as
  embedded languages;
- formatting is delegated to an external Caddy process.

The repository's test Caddyfile is also a useful compact smoke fixture because
it combines global options, multiple sites, nested handlers, matchers,
placeholders, snippets, imports and TLS configuration.

### Compatibility caution

The README configures the formatter as `caddy fmt -c -`. The current official
Caddy command documentation describes `caddy fmt [<path>]` and stdin via the
path `-`; it does not document `-c` for `fmt`. Any copied command must therefore
be verified against the supported Caddy versions before use.

### Lazycaddy decision

Adopt the separation of semantic roles, folding, local definitions/references
and embedded-language handling as design patterns. Implement them from the
existing lossless parse tree first. Defer Tree-sitter integration until a
structured editor or incremental parsing requirement justifies a second
parser/runtime.

## tree-sitter-caddyfile

Source: [README](https://github.com/caddyserver/tree-sitter-caddyfile/blob/master/README.md),
[grammar](https://github.com/caddyserver/tree-sitter-caddyfile/blob/master/grammar.js),
[highlight queries](https://github.com/caddyserver/tree-sitter-caddyfile/blob/master/queries/highlights.scm),
[injection queries](https://github.com/caddyserver/tree-sitter-caddyfile/blob/master/queries/injections.scm),
[test corpus](https://github.com/caddyserver/tree-sitter-caddyfile/tree/master/test/corpus),
[external scanner](https://github.com/caddyserver/tree-sitter-caddyfile/blob/master/src/scanner.c).

### Useful ideas

The grammar provides a valuable semantic vocabulary, including:

- global options, snippets, named routes and site blocks;
- site addresses, network addresses, IP addresses and CIDR values;
- directives, matchers, named matchers and matcher directives;
- interpreted and raw strings, escape sequences, durations and integers;
- status-code fallbacks, environment variables and placeholders;
- heredoc start, body and end nodes.

The external scanner is particularly useful as a design reference for heredocs:
it stores the delimiter, accepts indentation on the closing marker, preserves
arguments after the closing marker, and has dedicated corpus coverage.

The corpus is the strongest contribution for lazycaddy. It organizes
regressions by language feature: addresses, comments, directives, durations,
environment variables, heredocs, matchers, named matchers, named routes,
placeholders, sites, snippets, source files and strings.

The project also documents a healthy grammar maintenance practice: syntax
changes should be narrowly scoped, associated with a reproducible issue, and
accompanied by a regression test.

### Limitations to keep in mind

Tree-sitter is a structural parser for editor scenarios, not automatically a
replacement for Caddy's parser or formatter. Its grammar has to approximate
context-dependent Caddy semantics, and the generated parser adds a native
runtime and build/distribution surface to a Go application.

### Lazycaddy decision

Use the grammar and query vocabulary to guide semantic spans and fixtures. Do
not introduce Tree-sitter as a second source of truth during the lossless
editing milestones. Reconsider it when incremental parsing or structured
editing becomes a demonstrated bottleneck.

## Cross-project practices worth retaining

1. **Open-world syntax support:** unknown directives remain visible and useful.
2. **Contextual semantic roles:** names, matchers, paths, addresses, literals,
   placeholders and structural delimiters should be distinguishable when the
   parser can prove their role.
3. **Lossless raw fallback:** semantic presentation must never rewrite source
   bytes or hide unsupported syntax.
4. **Folding from source ranges:** block folding should use the existing node
   ranges rather than reparsing braces in the UI.
5. **Definition/reference awareness:** named matchers, snippets and named
   routes are good candidates for navigation and diagnostics.
6. **Corpus-driven regression testing:** every newly supported syntax edge case
   should add a fixture and preserve the untouched-byte contract.
7. **Delegation to Caddy:** formatting and validation should use the configured
   Caddy binary in a cancellable, testable boundary.
8. **Optional language injection:** embedded content is a later enhancement,
   not a reason to compromise the base parser.

## Explicit non-adoptions

- Do not use a static directive catalog to decide whether syntax is valid.
- Do not replace the lossless Go parser with a second parser without a measured
  need and a compatibility plan.
- Do not adopt format-on-save or implicit writes in lazycaddy.
- Do not treat editor-specific whole-document edits as equivalent to a
  source-range patch.
- Do not copy external formatter commands without testing them against the
  Caddy versions supported by lazycaddy.

## Follow-up candidates

These candidates are distilled into `PLAN.md` rather than treated as automatic
commitments:

- add semantic spans derived from the existing lexer/parser;
- add source-view folding based on `Node.Range`;
- add named matcher and snippet definition/reference navigation;
- expand parser and highlighting fixtures using the Tree-sitter corpus;
- add a directive metadata catalog with documentation links and optional
  version/module information;
- add heredoc and CEL injection only after the base semantic layer is stable.
