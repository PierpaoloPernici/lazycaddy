# Caddyfile fixtures

These fixtures are sanitized and safe to commit.

- realistic contains a small, valid-looking configuration with a global options block, imports, a snippet and a site file;
- homelab contains a sanitized, single-file configuration derived from a real homelab setup. It exercises wildcard TLS, environment placeholders, reusable proxy snippets, HTTP and HTTPS upstreams, nested proxy options, health checks and many site blocks;
- edge-cases contains parser-focused syntax, including an unknown directive that must remain opaque and preserved;
- compat exercises the v0.3 structured-editing surface in one file: heredocs (with trailing arguments and indented closing markers), quoted braces, escaped newlines and an escaped heredoc opener, placeholders, matcher definitions and references, snippets, named routes, nested handler blocks and an unknown/plugin directive;
- compat-braceless is a single brace-less site spanning the rest of the file;
- compat-imports exercises plain imports, nested relative imports (including a sibling-directory `../` reference), glob imports and cross-document snippet references;
- compat-cycles is a three-file import cycle (root → a → b → a);
- compat-malformed-* files cover malformed input behavior: an unclosed block whose tree stays available, an unterminated string that fails lexing, and a site address ending in a curly brace.

The homelab fixture uses documentation-only domains and IP ranges. It is a parser fixture and may require external Caddy modules, such as the Cloudflare DNS module, before it can be run as a live configuration.

Do not place real credentials, private keys, production domains or sensitive infrastructure details in this directory.
