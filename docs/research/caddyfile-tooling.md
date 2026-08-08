# Caddyfile Tooling Research

This document records useful practices found in external Caddyfile tooling
projects. It is a reference for design and compatibility work, not a second
product specification. Product decisions belong in `PLAN.md`; durable product
principles belong in `VISION.md`.

## Review record

Reviewed on 2026-08-08. The repository revisions below were inspected locally:

| Project | Revision | Purpose |
| --- | --- | --- |
| [vim-caddyfile](https://github.com/isobit/vim-caddyfile) | `6d60d5af0d73f20b88ec388a9d70188d55ed8223` | Vim syntax, indentation and comment support |
| [vscode-caddyfile](https://github.com/caddyserver/vscode-caddyfile) | `9ba30fcec02e257776188ca66ed47fc210e86b26` | VS Code syntax support, formatting and a basic language server |
| [caddyfile-zed](https://github.com/nusnewob/caddyfile-zed) | `6631b10fe3c1b6c92173ce3d80a48f739d57cd89` | Zed integration using Tree-sitter queries |
| [tree-sitter-caddyfile](https://github.com/caddyserver/tree-sitter-caddyfile) | `b00e432eed3628f0f417865da9e9f601cb9f7fb85f` | Shared Tree-sitter grammar and parser corpus |
| [caddy](https://github.com/caddyserver/caddy) | `64b64c61ebc40ea37280b2baa1a7a492cc1156c5` | Official parser, formatter, import graph, Admin API commands and compatibility tests |
| [ember](https://github.com/alexandre-daubois/ember) | `b6e82fcce42f7bfd420924debad0ee2ad3de8aff` | Read-only Admin API dashboard, logs, certificates and runtime observability |
| [caddy-docker-proxy](https://github.com/lucaslorentz/caddy-docker-proxy) | `d246679c72e1c3d2ef0e610503e1c2f74581978b` | Label-to-Caddyfile generation, autosave and reload coordination |
| [clog](https://github.com/hellotimking/clog) | `c0d05846665a354aa7c664a14d42da241de660b8` | Lightweight Caddy JSON access-log viewer and dashboard |
| [caddy-analyzer](https://github.com/L9Lenny/caddy-analyzer) | `457977f36f60c95e15417f1fa3a3563fd68bc4b2` | Caddy JSON log parsing, metrics, threat analysis and TUI workflows |
| [certmagic](https://github.com/caddyserver/certmagic) | `d93662a04b9232e986ce76d657b016e58a8e69b3` | TLS automation, storage abstraction, locking and atomic file persistence |
| [caddyfile-rs](https://github.com/LeakIX/caddyfile-rs) | `61fed68e691ddf36330c89c889e4ad65ad2958a9` | Typed Caddyfile AST, spans, formatter and CLI validation workflow |

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

## Official Caddy implementation

Sources: [Caddy repository](https://github.com/caddyserver/caddy),
[Caddyfile parser](https://github.com/caddyserver/caddy/blob/master/caddyconfig/caddyfile/parse.go),
[formatter](https://github.com/caddyserver/caddy/blob/master/caddyconfig/caddyfile/formatter.go),
[import graph](https://github.com/caddyserver/caddy/blob/master/caddyconfig/caddyfile/importgraph.go),
[commands](https://github.com/caddyserver/caddy/blob/master/cmd/commands.go),
[parser tests](https://github.com/caddyserver/caddy/blob/master/caddyconfig/caddyfile/parse_test.go),
and [formatter tests](https://github.com/caddyserver/caddy/blob/master/caddyconfig/caddyfile/formatter_test.go).

### Confirmed practices

- Caddy's parser is the compatibility reference for tokenization, server
  blocks, imports, snippets, named routes, environment substitution, heredocs
  and brace-related edge cases.
- The official formatter is a separate rune-oriented pass. It is useful as the
  delegated formatting implementation, but it is not a lossless editor model.
- Import resolution has explicit graph and cycle-detection behavior that
  should remain represented in lazycaddy's document model rather than hidden
  inside a flat buffer.
- The parser and formatter tests provide a high-value compatibility corpus,
  including imports, globs, cycles, quoted braces, brace-less sites, heredocs
  and escaped input.
- `caddy reload` adapts a Caddyfile and loads it through the Admin API, while
  `list-modules` exposes runtime capability information. These are useful
  boundaries for validation, reload and capability detection.

### Lazycaddy decision

Track the official parser, formatter and tests during compatibility reviews.
Reuse Caddy for formatting, adaptation and reload where the workflow requires
semantic validation. Keep lazycaddy's lossless source model and exact-file
patching because the official parser is not a replacement for source edits.

## Runtime dashboard and observability projects

Sources: [Ember README](https://github.com/alexandre-daubois/ember),
[dashboard documentation](https://github.com/alexandre-daubois/ember/blob/main/docs/caddy-dashboard.md),
[logs documentation](https://github.com/alexandre-daubois/ember/blob/main/docs/logs.md),
[plugins documentation](https://github.com/alexandre-daubois/ember/blob/main/docs/plugins.md),
[clog](https://github.com/hellotimking/clog), and
[caddy-analyzer](https://github.com/L9Lenny/caddy-analyzer).

### Useful ideas

- Ember separates read-only Admin API fetchers from TUI models and presents
  live configuration, certificates, logs, route traffic, latency percentiles
  and status-code summaries as separate views.
- Ember's restart detection, multi-instance support and transient log sinks
  are useful references for a future runtime/operations surface.
- Clog demonstrates a deliberately small access-log workflow: tail structured
  JSON, keep a bounded history, filter by host/status/text, and aggregate
  status classes in a compact dashboard.
- Caddy-analyzer demonstrates reusable boundaries for Caddy's nested JSON log
  schema, multiple sources, top-N metrics, comparisons, bounded state and
  security-oriented analysis. Its dual-pass URI handling is a useful warning
  that decoding decisions affect detection semantics.

### Compatibility and safety limits

These projects assume a log-analysis or operations product, not a source-first
Caddyfile editor. Their firewall blocking, threat detection, HTML reporting,
Docker/Kubernetes ingestion and runtime log streaming must not silently become
lazycaddy features. Any future log view should use explicit source adapters,
bounded buffers and read-only defaults.

### Lazycaddy decision

Use Ember's separation of fetchers, models and views as an architectural
reference for a future optional runtime tab. Use clog and caddy-analyzer as
fixtures and UX references for structured access-log summaries. Do not add a
runtime or security-analysis dependency until the configuration workflow is
stable and the required interfaces are defined.

## Generated configuration and reload coordination

Source: [caddy-docker-proxy README](https://github.com/lucaslorentz/caddy-docker-proxy),
[generator](https://github.com/lucaslorentz/caddy-docker-proxy/blob/master/generator/generator.go),
[loader](https://github.com/lucaslorentz/caddy-docker-proxy/blob/master/loader.go),
and [label conversion](https://github.com/lucaslorentz/caddy-docker-proxy/blob/master/caddyfile/fromlabels.go).

### Useful ideas

- Generated configuration should have an explicit ownership boundary and a
  visible generated artifact for troubleshooting.
- Regeneration can be throttled and compared byte-for-byte before triggering
  a reload, avoiding unnecessary runtime changes.
- Autosave through a same-directory temporary file and rename provides a
  practical baseline for generated artifacts.
- Label conversion needs deterministic ordering and isolated handling of
  global blocks, server blocks, matchers and raw configuration fragments.

### Lazycaddy decision

Adopt the generated-versus-user-authored distinction, change detection and
reload coordination as future integration guidance. Lazycaddy remains the
owner of user-authored Caddyfiles; generator ownership, label discovery and
automatic reload loops are out of scope.

## TLS storage and certificate lifecycle

Sources: [CertMagic README](https://github.com/caddyserver/certmagic),
[storage interfaces](https://github.com/caddyserver/certmagic/blob/master/storage.go),
[file storage](https://github.com/caddyserver/certmagic/blob/master/filestorage.go),
and [atomic file helper](https://github.com/caddyserver/certmagic/tree/master/internal/atomicfile).

### Useful ideas

- TLS state is best exposed through an abstraction that can list, inspect and
  coordinate certificate resources without coupling the UI to one storage
  layout.
- Concurrent certificate operations need locking, stale-lock handling and
  cancellation-aware storage interfaces.
- File persistence should use restrictive permissions and atomic replacement.
- Certificate metadata, renewal state and OCSP state are distinct concerns and
  should not be inferred from a single file read.

### Lazycaddy decision

Use CertMagic as a reference for future certificate inspection interfaces and
storage safety. Do not treat CertMagic's file paths as Caddy's public layout
contract, and do not make the TUI read private TLS storage directly.

## Cross-language AST and CI reference

Sources: [caddyfile-rs](https://github.com/LeakIX/caddyfile-rs),
[AST](https://github.com/LeakIX/caddyfile-rs/blob/main/src/ast.rs),
[lexer](https://github.com/LeakIX/caddyfile-rs/blob/main/src/lexer.rs),
[parser](https://github.com/LeakIX/caddyfile-rs/blob/main/src/parser.rs),
[formatter](https://github.com/LeakIX/caddyfile-rs/blob/main/src/formatter.rs),
and [validation workflow](https://github.com/LeakIX/caddyfile-rs/blob/main/.github/workflows/validate-caddyfile.yaml).

### Useful ideas

- A typed vocabulary for global options, snippets, named routes, site blocks,
  addresses, directives, matchers and argument forms makes editor features
  easier to reason about.
- Lexer spans with line and column information are useful for diagnostics and
  test output, even when a project also needs byte offsets for patching.
- A CLI with `validate`, `fmt` and `check`, backed by CI fixtures, is a useful
  project hygiene pattern.

### Compatibility limits

The project is round-trip-oriented, but its canonical AST and formatter are
not a guarantee that every original byte, comment position or whitespace
choice survives. It is therefore a reference for taxonomy and tests, not a
reason to introduce a second parser into lazycaddy.

### Lazycaddy decision

Borrow the typed terminology, span assertions and validation/check workflow
where they improve Go tests and documentation. Keep byte offsets, raw tokens
and source ranges in the existing Go parser as the editing authority.

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
9. **Compatibility from the source project:** official Caddy parser and
   formatter tests should anchor behavior reviews for supported versions.
10. **Explicit integration boundaries:** Admin API, logs, TLS storage and
    generated configuration need separate adapters and capability checks.
11. **Bounded operational state:** runtime views and log summaries should use
    bounded buffers, cancellation and read-only defaults.

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
- track official Caddy parser/formatter fixtures when compatibility work
  changes;
- define optional Admin API, log and certificate adapters before adding runtime
  views;
- add generated-configuration ownership and reload coordination only for an
  explicitly scoped integration;
- consider a small `validate`/`check` developer workflow backed by fixtures.
