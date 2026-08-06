# Caddyfile fixtures

These fixtures are sanitized and safe to commit.

- realistic contains a small, valid-looking configuration with a global options block, imports, a snippet and a site file;
- homelab contains a sanitized, single-file configuration derived from a real homelab setup. It exercises wildcard TLS, environment placeholders, reusable proxy snippets, HTTP and HTTPS upstreams, nested proxy options, health checks and many site blocks;
- edge-cases contains parser-focused syntax, including an unknown directive that must remain opaque and preserved.

The homelab fixture uses documentation-only domains and IP ranges. It is a parser fixture and may require external Caddy modules, such as the Cloudflare DNS module, before it can be run as a live configuration.

Do not place real credentials, private keys, production domains or sensitive infrastructure details in this directory.
