# Security Policy

Security is an important part of lazycaddy's design.

lazycaddy interacts with Caddy configuration files and may perform privileged or
runtime-sensitive operations such as validating configuration, writing files,
creating backups, and requesting a Caddy reload. A security issue could
therefore affect not only lazycaddy itself, but also the Caddy instance it
manages.

Please report suspected vulnerabilities privately and responsibly.

## Supported versions

lazycaddy is currently under active development.

Security fixes are provided for the latest released version. Users are
encouraged to upgrade to the most recent release before reporting an issue that
may already have been fixed.

| Version | Supported |
| ------- | --------- |
| Latest release | ✅ |
| Older releases | ❌ |
| Development builds | Best effort |

## Reporting a vulnerability

**Do not open a public GitHub issue, Discussion, or pull request for a suspected
security vulnerability.**

Please use GitHub's private vulnerability reporting feature:

1. Open the lazycaddy repository on GitHub.
2. Go to **Security**.
3. Select **Advisories**.
4. Select **Report a vulnerability**.

Repository:

https://github.com/PierpaoloPernici/lazycaddy

If private vulnerability reporting is not available, please contact the
maintainer privately rather than publishing technical details.

When possible, include:

- the affected lazycaddy version or commit;
- operating system and architecture;
- Caddy version;
- a description of the vulnerability and its potential impact;
- steps required to reproduce it;
- a minimal proof of concept, if appropriate;
- relevant configuration fragments;
- whether exploitation requires local access, elevated privileges, or a
  particular Caddy configuration;
- any suggested mitigation or fix.

Please remove unrelated credentials, tokens, certificates, private keys,
personal data, and other secrets from reports.

## What should be reported

Examples of issues that may have security implications include:

- arbitrary command execution;
- command or argument injection;
- unintended file reads or writes;
- path traversal;
- unsafe handling of Caddyfile `import` paths;
- symlink or filesystem race vulnerabilities;
- TOCTOU issues between validation and writing;
- bypasses of validation or write-safety checks;
- unintended Caddy reloads;
- privilege escalation or unsafe privilege handling;
- unsafe temporary-file or backup handling;
- insecure file permissions;
- leakage of credentials, tokens, environment variables, configuration
  secrets, or other sensitive data;
- terminal escape-sequence injection from untrusted content;
- unsafe clipboard or OSC52 behavior;
- vulnerabilities caused by maliciously crafted Caddyfiles, logs, filenames,
  runtime responses, or imported files;
- unexpected writes outside the intended configuration scope;
- security-sensitive race conditions;
- vulnerabilities in lazycaddy's interaction with the Caddy Admin API.

This list is not exhaustive. If you are unsure whether something is a security
issue, reporting it privately is preferred.

## Usually not considered a vulnerability

The following normally do not qualify as security vulnerabilities by
themselves:

- bugs that only cause the TUI to display incorrect information;
- crashes that require deliberately malformed local input and have no security
  impact;
- failures caused by running lazycaddy with permissions that are insufficient
  for the requested operation;
- Caddy configuration mistakes that lazycaddy did not create or modify;
- vulnerabilities in Caddy itself that are unrelated to lazycaddy;
- vulnerabilities that require an already fully compromised host and do not
  provide additional capability.

These may still be valid bugs and can be reported through the normal issue
tracker when they do not expose sensitive information.

## Security model

lazycaddy follows several security principles.

### Least privilege

Browsing and inspecting configuration should work without elevated privileges
whenever possible.

Users should not need to run lazycaddy as `root` merely to inspect a Caddy
configuration.

Operations requiring additional permissions should fail explicitly rather than
silently escalating privileges.

### Explicit writes

Reading a configuration must never modify it.

Configuration changes should be explicit, reviewable, and limited to the
intended source ranges.

lazycaddy should preserve unrelated Caddyfile content, including formatting,
comments, imports, and unsupported directives.

### Validate before writing

Changes should be validated before they are committed to the active
configuration whenever the workflow permits it.

Validation failure must not silently result in an active invalid
configuration.

### No implicit reloads

Saving configuration and reloading Caddy are distinct operations.

lazycaddy must not reload Caddy merely because a configuration file was opened,
viewed, or edited. Reload operations must be explicit and visible to the user.

### Safe filesystem operations

Security-sensitive filesystem operations should account for:

- file ownership and permissions;
- symbolic links;
- atomic replacement;
- external modifications;
- backups and rollback;
- imported configuration files;
- failures during partial operations.

Changes should not unexpectedly escape the configuration files the user
intended lazycaddy to manage.

### Treat external data as untrusted

Caddyfiles, imported files, filenames, log entries, process output, Admin API
responses, and other externally supplied text may contain malicious or unusual
content.

Such data must not become shell commands or unsafe terminal control sequences
without appropriate handling.

### Protect secrets

lazycaddy should avoid exposing secrets through:

- application logs;
- error messages;
- diagnostics;
- crash output;
- clipboard operations;
- temporary files;
- generated artifacts.

Backups are deliberate copies of the user's source files and may therefore
contain credentials or other secrets already present in the Caddyfile. Protect
the backup directory with appropriate filesystem permissions, do not place it
under version control, and never commit real configuration backups or their
sidecars.

Contributors should never commit real credentials, private keys, API tokens, or
other secrets to the repository or test fixtures.

## Caddy Admin API

The Caddy Admin API is a powerful local management interface.

lazycaddy assumes that access to the Admin API is protected according to
Caddy's security model and the administrator's deployment configuration.

lazycaddy should not weaken the security of the Admin API, expose it to
additional networks, or attempt to bypass its access controls.

A deployment that exposes the Caddy Admin API insecurely is primarily a Caddy
deployment issue, unless lazycaddy caused or materially worsened that exposure.

## Responsible disclosure

Please allow reasonable time for a vulnerability to be investigated and fixed
before publishing technical details.

After a report is received, the maintainer will aim to:

1. acknowledge the report;
2. reproduce and assess the issue;
3. determine affected versions and severity;
4. develop and test a fix;
5. prepare a release when necessary;
6. coordinate disclosure with the reporter when practical.

Response and remediation times may vary because lazycaddy is an independent
open-source project maintained on a best-effort basis.

## Security fixes

Security fixes may be released without disclosing full exploitation details
until users have had a reasonable opportunity to update.

Where appropriate, a GitHub Security Advisory may be published describing:

- affected versions;
- fixed versions;
- severity;
- impact;
- mitigations;
- acknowledgements.

## Scope

This policy covers security issues in lazycaddy itself.

For vulnerabilities in Caddy, please follow the Caddy project's security
reporting process:

https://github.com/caddyserver/caddy/security

For vulnerabilities in third-party dependencies, report them to the relevant
upstream project. If a dependency vulnerability has a concrete impact on
lazycaddy, you may also report that impact privately to this project.

## Safe harbor

Security research conducted in good faith and limited to systems and data you
own or are explicitly authorized to test is welcome.

Please avoid:

- accessing or modifying data belonging to others;
- privacy violations;
- service disruption;
- destructive testing;
- social engineering;
- publishing vulnerability details before a fix can reasonably be made
  available.

Thank you for helping keep lazycaddy and its users safe. 🦥
